// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// Package fsusb2i drives the KEIAN sticks built on the ITE IT9175/IT9179: the
// KTV-FSMINI, and the KTV-FSUSB2/V3 boards from serial K1212 onwards.
//
// The whole receiver is one chip running downloadable firmware, so this package
// only names the product, points the chip driver at its firmware and passes the
// card slot through.
//
// These sticks share no silicon with the earlier KTV-FSUSB2 boards, which are
// an eMPIA EM2874B with a separate Fujitsu demodulator and are driven by the
// fsusb2 package.
package fsusb2i

import (
	"context"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/it9175"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "fsusb2i",
	Aliases:     []string{"ktv-fsusb2i", "fsmini", "ktv-fsmini", "it9175", "it9179"},
	Description: "KEIAN KTV-FSMINI, KTV-FSUSB2/V3 K1212 and later (IT9175/IT9179)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement, the same on every product this
// backend claims. A listing needs it before anything is opened.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "IT9175", Deliveries: deliveries}}
}

// deliveries is what the board's single tuner can reach.
var deliveries = []string{"ISDB-T"}

// The two products enumerate identically, so which of them this is cannot be
// told apart from the USB descriptors; the name covers both.
var supportedIDs = []usbio.ID{{Vendor: 0x0511, Product: 0x0046}}

// model names the hardware in listings.
const model = "KEIAN KTV-FSUSB2i / FSMINI"

// lockTimeout bounds how long the demodulator is given to decide whether a
// channel is there.
const lockTimeout = 1500 * time.Millisecond

// streamTimeoutMS is how long a stream read waits before reporting that
// nothing arrived.
const streamTimeoutMS = 100

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == "ISDBT"
}

// open claims a stick, downloads its firmware if the chip is still on its boot
// ROM, and brings the demodulator and tuner up.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	chip := it9175.New(transport)
	// Looked for where every other blob is, not in one directory only.
	firmware, err := device.FindFirmware(options.FirmwareDirOrDefault(), it9175.FirmwareFile)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	if err := chip.Open(firmware); err != nil {
		// The chip never got far enough to need parking, so the device is
		// released directly rather than through its close.
		_ = transport.Close()
		return nil, err
	}
	inner, err := boardkit.New(boardkit.Config{
		Model:    model,
		Location: chip.Name(),
		Tuners:   []device.TunerInfo{{Index: 0, Name: "IT9175", Deliveries: deliveries}},
		Release:  chip.Close,
		Supports: Supports,
		Open: func(int) (boardkit.Tuner, error) {
			return &frontend{chip: chip}, nil
		},
	})
	if err != nil {
		_ = chip.Close()
		return nil, fmt.Errorf("open: %w", err)
	}
	// The stick carries an on-board reader, on the chip's own card interface.
	return boardkit.CardBoard(inner, chip, "IT9175"), nil
}

// frontend is the board's single terrestrial tuner.
type frontend struct {
	chip  *it9175.Device
	tuned bool
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := properties["DELIVERY_SYSTEM"]
	if !Supports(delivery) {
		return fmt.Errorf("%s does not support %q", model, delivery)
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	frequency = tuning.FrequencyKHz(frequency)
	front.tuned = false
	if err := front.chip.Tune(uint32(frequency)); err != nil {
		return err
	}
	// An empty frequency is a normal outcome for a scan rather than a failure,
	// so only a transport error stops here; Status reports what was found.
	if _, err := front.chip.WaitTuning(lockTimeout); err != nil {
		return err
	}
	front.tuned = true
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if !front.tuned {
		return device.Status{}, nil
	}
	statistics, err := front.chip.Statistics()
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: statistics.MPEGLocked}
	switch {
	case statistics.MPEGLocked:
		status.Names = device.LockNames
	case statistics.TPSDLocked:
		// The OFDM parameters decoded but the transport stream has not
		// synchronised, which is as far as a marginal signal gets.
		status.Names = []string{"SIGNAL", "CARRIER", "VITERBI"}
	}
	// The chip reports the ratio in whole dB and zero when it has none yet.
	if statistics.SNR != 0 {
		status.CNR = device.Measured(float64(statistics.SNR))
	}
	status.Strength = device.Measured(float64(statistics.StrengthDBm))
	return status, nil
}

// StartStream only puts the reads in flight: the chip delivers packets from the
// moment a channel is acquired.
func (front *frontend) StartStream() error { return front.chip.StartReading() }

// StopStream withdraws the reads still outstanding, which would otherwise carry
// the last channel's packets into the next stream.
func (front *frontend) StopStream() error { return front.chip.StopReading() }

// Read takes the next packets off the stream endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.chip.ReadStream(buffer, streamTimeoutMS))
}
