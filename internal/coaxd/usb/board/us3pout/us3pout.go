// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// Package us3pout drives the US-3POUT: an EM287x USB bridge carrying a
// TC90522 dual demodulator, with an MxL136 on its terrestrial tuner bus and a
// TDA20142 on its satellite one. Only one of the two can be live at a time.
package us3pout

import (
	"context"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/mxl136"
	"github.com/Till0196/coax/internal/coaxd/tuner/tda20142"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/em287x"
	"github.com/Till0196/coax/internal/coaxd/usb/tsdemux"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "us3pout",
	Description: "US-3POUT (TC90522 + MxL136 + TDA20142)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement, the same on every product this
// backend claims. A listing needs it before anything is opened.
const (
	// transferSize is one isochronous transfer's worth: sixteen frames of five
	// packets, which is what em287x.ReadStream submits and the most it can
	// return.
	transferSize = 188 * 5 * 16
	// readSize is four of them, taken in one trip when several have come back at
	// once, which keeps a reader on a busy machine from falling behind. It is not
	// larger because a read holds a place in the queue whether it filled its
	// buffer or not, so a queue counted in reads covers less time the larger they
	// are.
	readSize = transferSize * 4
	// queueBytes is how much sorted stream may wait for whoever asked for it
	// before packets are thrown away. It is about the consumer and not the
	// endpoint: a player that stops taking bytes while it starts its decoder is
	// behind for a moment, not broken. This is around six seconds of a full
	// multiplex; sizing it to the endpoint's own transfers instead lost hundreds
	// of packets a minute.
	queueBytes = 16 << 20
)

func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "MxL136 / TDA20142", Deliveries: deliveries}}
}

// deliveries covers both demodulator cores. Only one of them can be live at a
// time, so the board presents a single tuner that can do either.
var deliveries = []string{"ISDB-T", "ISDB-S"}

var supportedIDs = []usbio.ID{{Vendor: 0x0511, Product: 0x0045}}

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	switch tuning.Normalize(deliverySystem) {
	case "ISDBT", "ISDBS":
		return true
	default:
		return false
	}
}

// open claims a US-3POUT and brings its demodulator and both tuners up.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	bridge, err := em287x.New(transport)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("initialize EM287x: %w", err)
	}
	fail := func(operation string, cause error) (device.Board, error) {
		_ = bridge.Close()
		return nil, fmt.Errorf("%s: %w", operation, cause)
	}
	demod, err := newDemodulator(bridge.I2CBus())
	if err != nil {
		return fail("initialize TC90522", err)
	}
	terra, err := mxl136.New(demod.terrestrial.TunerBus())
	if err != nil {
		return fail("initialize MxL136", err)
	}
	satellite, err := tda20142.New(demod.satellite.TunerBus())
	if err != nil {
		return fail("initialize TDA20142", err)
	}

	demux, err := tsdemux.New(tsdemux.Config{
		Read:     bridge.ReadStream,
		Start:    func() error { return bridge.Stream(true) },
		Stop:     func() error { return bridge.Stream(false) },
		Tag:      tsdemux.PlainSync(),
		ReadSize: readSize,
		Blocks:   queueBytes / readSize,
		Tuners:   1,
	})
	if err != nil {
		return fail("prepare the stream reader", err)
	}

	front := &frontend{bridge: bridge, demod: demod, terra: terra, satellite: satellite, demux: demux}
	inner, err := boardkit.New(boardkit.Config{
		Model:    "US-3POUT",
		Location: bridge.Name(),
		// The two tuner ICs share one demodulator pair and one endpoint, so
		// they are one switchable tuner, not two.
		Tuners:   []device.TunerInfo{{Index: 0, Name: "MxL136 / TDA20142", Deliveries: deliveries}},
		Release:  bridge.Close,
		Supports: Supports,
		Open:     func(int) (boardkit.Tuner, error) { return front, nil },
	})
	if err != nil {
		return fail("open", err)
	}
	// The on-board reader is the EM287x's own, the same interface the
	// KTV-FSUSB2 uses.
	return boardkit.CardBoard(inner, bridge, "EM287x"), nil
}

// frontend is the board's switchable tuner: whichever of the two tuner ICs the
// last tune selected.
type frontend struct {
	bridge    *em287x.Bridge
	demod     *demodulator
	terra     *mxl136.Tuner
	satellite *tda20142.Tuner
	delivery  string

	// demux owns the reading goroutine; reader is this tuner's share of it,
	// held between StartStream and StopStream.
	demux  *tsdemux.Demux
	reader *tsdemux.Reader
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	if !Supports(delivery) {
		return fmt.Errorf("US-3POUT does not support %q", properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	satellite := delivery == "ISDBS"
	var streamID uint16
	if satellite {
		streamID, err = tuning.StreamID(properties)
		if err != nil {
			return err
		}
	}
	frequency = tuning.FrequencyKHz(frequency)
	if err := front.demod.selectSatellite(satellite); err != nil {
		return fmt.Errorf("select tuner: %w", err)
	}
	if satellite {
		// The two tuners share an antenna path, so park the idle one.
		_ = front.terra.Sleep()
		if err := front.satellite.Tune(frequency); err != nil {
			return fmt.Errorf("tune TDA20142: %w", err)
		}
	} else {
		if err := front.terra.Wake(); err != nil {
			return err
		}
		if err := front.terra.Tune(frequency); err != nil {
			return fmt.Errorf("tune MxL136: %w", err)
		}
	}
	time.Sleep(30 * time.Millisecond)
	if err := front.demod.reset(); err != nil {
		return fmt.Errorf("reset TC90522: %w", err)
	}
	time.Sleep(80 * time.Millisecond)
	if satellite {
		if err := front.demod.selectStream(streamID); err != nil {
			return fmt.Errorf("select stream ID: %w", err)
		}
	}
	front.delivery = delivery
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if front.delivery == "" {
		return device.Status{}, nil
	}
	locked, names, err := front.demod.status()
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: locked, Names: names}
	// The demodulator only keeps the measurement meaningful while it is
	// locked, and a failed read is not a failed lock.
	if locked {
		if cnr, measured, cnrErr := front.demod.cnr(); cnrErr == nil && measured {
			status.CNR = device.Measured(cnr)
		}
	}
	return status, nil
}

// StartStream begins reading the endpoint, which opens it.
func (front *frontend) StartStream() error {
	if front.reader != nil {
		return nil
	}
	reader, err := front.demux.Open(0)
	if err != nil {
		return err
	}
	front.reader = reader
	return nil
}

// StopStream stops reading it, which closes it.
func (front *frontend) StopStream() error {
	if front.reader == nil {
		return nil
	}
	reader := front.reader
	front.reader = nil
	return reader.Close()
}

// Read takes the next packets the reading goroutine has queued.
func (front *frontend) Read(ctx context.Context, buffer []byte) (int, error) {
	if front.reader == nil {
		return 0, device.ErrWouldBlock
	}
	return front.reader.Read(ctx, buffer)
}

// Dropped counts the packets the reading goroutine discarded because nothing
// was taking them fast enough. It belongs to the reader, so each stream starts
// again at zero.
func (front *frontend) Dropped() uint64 {
	if front.reader == nil {
		return 0
	}
	return front.reader.Dropped()
}
