// Copyright (c) 2017 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5520.c) by Davin.

// Package tbs5520 drives the TurboSight TBS 5520: an AVL6882 combined
// terrestrial, cable and satellite demodulator with an R848 tuner in front of
// it, behind a TBS vendor-protocol USB bridge that needs its microcontroller
// firmware uploaded on every open.
//
// Unlike the TBS 5520SE's two tuners on separate antenna inputs, this board has
// a single wideband tuner covering 40 MHz to 2.4 GHz. The AVL6882 runs one
// firmware image at a time, so changing delivery-system family means uploading
// another.
package tbs5520

import (
	"context"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/avl6882"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/r848"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/tbs5520"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "tbs5520",
	Description: "TurboSight TBS 5520 (AVL6882 + R848)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{
		Index:      0,
		Name:       "AVL6882",
		Deliveries: deliveries,
		// The demodulator's GPIO: 13V, 18V or nothing, for this tuner alone.
		Controls: device.Controls{LNB: &device.LNBControl{Off: true, Low: "13V", High: "18V"}},
	}}
}

// deliveries is what the board's single tuner reaches. The AVL6882 declares
// exactly these five.
var deliveries = []string{"DVB-T", "DVB-T2", "DVB-C", "DVB-S", "DVB-S2"}

// firmwareFile is the bridge microcontroller firmware. The AVL6882 has its own
// separate demodulator firmware, loaded by its own package.
const firmwareFile = "dvb-usb-tbs5520-01.fw"

var supportedIDs = []usbio.ID{{Vendor: 0x734c, Product: 0x5520}}

// satelliteSystems are the delivery systems whose channel files carry
// frequencies in kHz rather than Hz.
var satelliteSystems = map[string]bool{"DVBS": true, "DVBS2": true, "DVBS2X": true}

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	switch tuning.DemodDeliverySystem(deliverySystem) {
	case "DVBT", "DVBT2", "DVBC", "DVBC/ANNEXA", "DVBS", "DVBS2", "DVBS2X":
		return true
	default:
		return false
	}
}

// open claims a TBS 5520 and brings its demodulator and tuner up.
func open(options device.Options) (device.Board, error) {
	firmwareDir := options.FirmwareDirOrDefault()
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	fail := func(operation string, cause error) (device.Board, error) {
		_ = transport.Close()
		return nil, fmt.Errorf("%s: %w", operation, cause)
	}

	bridge := tbs5520.New(transport)
	// The bridge's microcontroller has no persistent storage: every open has
	// to reload it.
	firmware, err := device.ReadFirmware(firmwareDir, firmwareFile)
	if err != nil {
		return fail("read TBS5520 firmware", err)
	}
	if err := bridge.LoadFirmware(firmware); err != nil {
		return fail("load TBS5520 firmware", err)
	}

	demod := avl6882.New(bridge, firmwareDir, avl6882.Config{Address: 0x14})
	if err := demod.Initialize(); err != nil {
		return fail("initialize AVL6882", err)
	}
	// The tuner sits behind the demodulator's I2C repeater, not on the bridge
	// bus directly.
	tuner := r848.New(demod.TunerBus(), r848.Config{
		Address:   0x7a,
		XtalHz:    16_000_000,
		SetTfType: 8, // R848_TF_82N_270N, the starting tracking filter
	})
	if err := bridge.Initialize(); err != nil {
		return fail("initialize TBS5520 GPIO", err)
	}

	front := &frontend{bridge: bridge, demod: demod, tuner: tuner}
	board, err := boardkit.New(boardkit.Config{
		Model:    "TurboSight TBS 5520",
		Location: transport.Name(),
		Tuners:   []device.TunerInfo{{Index: 0, Name: "AVL6882", Deliveries: deliveries}},
		Release:  transport.Close,
		Supports: Supports,
		Open:     func(int) (boardkit.Tuner, error) { return front, nil },
	})
	if err != nil {
		return fail("open", err)
	}
	return board, nil
}

// frontend is the board's single tuner.
type frontend struct {
	bridge *tbs5520.Bridge
	demod  *avl6882.Demod
	tuner  *r848.Tuner

	delivery    string
	tunerWarmed bool
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	if !Supports(delivery) {
		return fmt.Errorf("TBS 5520 does not support %q", properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", satelliteSystems[delivery])
	if err != nil {
		return err
	}
	var bandwidthHz uint64
	if !satelliteSystems[delivery] {
		if bandwidthHz, err = tuning.BandwidthHz(properties); err != nil {
			return err
		}
	}
	streamID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}

	// The demodulator has to be in the right delivery-system family before
	// either chip is programmed: the firmware upload resets the I2C repeater
	// the tuner is reached through.
	if err := front.demod.SetDeliverySystem(delivery); err != nil {
		return fmt.Errorf("select the AVL6882 delivery system: %w", err)
	}
	// The tuner's self-calibration runs once, and can only run once the
	// repeater is up, so it waits until the first tune rather than happening
	// at open time.
	if !front.tunerWarmed {
		if err := front.tuner.Initialize(); err != nil {
			return fmt.Errorf("initialize R848: %w", err)
		}
		front.tunerWarmed = true
	}

	// A satellite channel file's FREQUENCY is in kHz; everything else is Hz.
	frequencyHz := uint32(frequency)
	if satelliteSystems[delivery] {
		frequencyHz = uint32(frequency) * 1000
	}
	// The supply on the aerial lead is the demodulator's own here, through a
	// pair of its GPIOs, and it is only a satellite channel's business: a
	// terrestrial one leaves it where the chip parked it.
	if satelliteSystems[delivery] {
		supply, err := tuning.SatelliteSupply(properties)
		if err != nil {
			return err
		}
		if err := tuning.ApplySupply(supply, front.demod.SetVoltage); err != nil {
			return fmt.Errorf("set the LNB voltage: %w", err)
		}
		if err := front.demod.SetTone(tuning.ToneWanted(properties)); err != nil {
			return fmt.Errorf("set the 22 kHz tone: %w", err)
		}
	}
	if err := front.tuner.Tune(r848.Params{
		DeliverySystem: delivery,
		FrequencyHz:    frequencyHz,
		BandwidthHz:    uint32(bandwidthHz),
		SymbolRate:     uint32(symbolRate),
	}); err != nil {
		return fmt.Errorf("tune R848: %w", err)
	}
	if err := front.demod.Tune(avl6882.Params{
		DeliverySystem: delivery,
		BandwidthHz:    uint32(bandwidthHz),
		SymbolRate:     uint32(symbolRate),
		StreamID:       streamID,
	}); err != nil {
		return fmt.Errorf("tune AVL6882: %w", err)
	}
	front.delivery = delivery
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if front.delivery == "" {
		return device.Status{}, nil
	}
	status, err := front.demod.Status()
	if err != nil {
		return device.Status{}, err
	}
	result := device.Status{Locked: status.Locked}
	if status.Locked {
		result.Names = device.LockNames
	}
	if status.HasCNR {
		result.CNR = device.Measured(status.CNRdB)
	}
	// The AVL6882's strength reading is a raw AGC count with no conversion curve
	// behind it, so it is deliberately not reported as a dBm measurement.
	return result, nil
}

// StartStream only puts the reads in flight: this bridge's bulk endpoint is
// never gated, so the stream is already flowing once the frontend is tuned.
func (front *frontend) StartStream() error { return front.bridge.StartReading() }

// StopStream only withdraws the reads still outstanding, which would otherwise
// carry the last channel's packets into the next stream.
func (front *frontend) StopStream() error { return front.bridge.StopReading() }

// Read takes the next packets off the bulk endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.bridge.ReadStream(buffer, 100))
}
