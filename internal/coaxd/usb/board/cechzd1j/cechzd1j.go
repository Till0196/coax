// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package cechzd1j drives the Sony CECH-ZD1 J: a Fujitsu MB86H47A USB bridge in front of a
// DiBcom DiB8096, the DiB8000 demodulator and DiB0090 tuner in one package.
//
// The host drives the bridge register by register (the mb86h47 package) and
// gets the stream as the demodulator delivers it.
package cechzd1j

import (
	"context"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/dib8096"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/mb86h47"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/iso7816"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "cech-zd1j",
	Aliases:     []string{"mb86h47"},
	Description: "Sony CECH-ZD1 J (MB86H47A + DiB8096)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's single tuner.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "DiB8096", Deliveries: deliveries}}
}

var deliveries = []string{"ISDB-T"}

var supportedIDs = []usbio.ID{{Vendor: 0x054c, Product: 0x04b2}}

const model = "Sony CECH-ZD1 J"

// ledPin is the DiB8096 GPIO wired to the front LED: low or left as an input, green.
const ledPin = 1

// streamTimeoutMS is how long a stream read waits before reporting that
// nothing arrived. It is generous because a read that times out loses what it
// had received, and the bridge's output comes in bursts.
const streamTimeoutMS = 300

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == "ISDBT"
}

// open claims a CECH-ZD1 J and brings it up to a tunable demodulator.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	fail := func(operation string, cause error) (device.Board, error) {
		_ = transport.Close()
		return nil, fmt.Errorf("%s: %w", operation, cause)
	}
	// The interface's only setting is selected first; the stream endpoint is
	// described there.
	if err := transport.SetAlternate(0, 0); err != nil {
		return fail("select interface", err)
	}

	bridge := mb86h47.New(transport)
	if err := bridge.Start(); err != nil {
		return fail("start MB86H47A", err)
	}
	if err := bridge.ResetDemodulator(); err != nil {
		return fail("reset DiB8096", err)
	}
	// The DiB8000 wakes up needing its I2C address confirmed and its
	// high-speed interface left alone, which is what register 1287 = 3 does
	// in DiBcom's enumeration; with one demodulator the default address
	// stays.
	if err := bridge.Write(dib8096.Address, []byte{0x05, 0x07, 0x00, 0x03}); err != nil {
		return fail("enumerate DiB8096", err)
	}
	demod := dib8096.New(bridge)
	if err := demod.Initialize(); err != nil {
		return fail("initialize DiB8096", err)
	}

	front := &frontend{bridge: bridge, demod: demod}
	board, err := boardkit.New(boardkit.Config{
		Model:    model,
		Location: transport.Name(),
		Tuners:   Layout(transport.ID()),
		Release: func() error {
			_ = demod.Sleep()
			return transport.Close()
		},
		Supports: Supports,
		Open: func(int) (boardkit.Tuner, error) {
			if err := demod.SetGPIO(ledPin, true); err != nil {
				return nil, fmt.Errorf("light LED: %w", err)
			}
			return front, nil
		},
	})
	if err != nil {
		return fail("open", err)
	}
	slot := &cardSlot{session: iso7816.New(bridge.CardLine())}
	return boardkit.CardBoard(board, slot, cardReaderName), nil
}

// cardReaderName names the slot's hardware: the smart-card interface built
// into the bridge.
const cardReaderName = "MB86H47A"

// cardSlot is the board's card slot, a T=1 session over the bridge's
// smart-card interface.
type cardSlot struct{ session *iso7816.Session }

var _ boardkit.CardHardware = (*cardSlot)(nil)

// CardPresent reports whether a card is seated in the slot.
func (slot *cardSlot) CardPresent() (bool, error) { return slot.session.Present() }

// ResetCard powers the card up and returns its answer to reset.
func (slot *cardSlot) ResetCard() ([]byte, error) { return slot.session.Reset() }

// TransmitAPDU sends one command APDU to the card and returns its answer.
func (slot *cardSlot) TransmitAPDU(apdu []byte) ([]byte, error) {
	return slot.session.Transmit(apdu)
}

// frontend is the board's single tuner.
type frontend struct {
	bridge *mb86h47.Bridge
	demod  *dib8096.Demod
	tuned  bool
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
	front.tuned = false
	// The stream setup is left to the tune's waits, which on some hosts are
	// most of the time the setup would otherwise add.
	front.bridge.PrepareStream()
	if err := front.demod.Tune(uint32(tuning.FrequencyKHz(frequency)) * 1000); err != nil {
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
	measured, err := front.demod.Status()
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: measured.Locked, Names: measured.Names}
	if measured.HasCNR {
		status.CNR = device.Measured(measured.CNR)
	}
	if measured.HasStrength {
		status.Strength = device.Measured(measured.StrengthDBm)
	}
	return status, nil
}

// StartStream turns the demodulator's transport stream output on and moves
// the bridge into streaming.
func (front *frontend) StartStream() error {
	if err := front.demod.SetOutputMode(dib8096.OutputParallelGatedClock); err != nil {
		return err
	}
	return front.bridge.StartStream()
}

// StopStream is the counterpart of StartStream. The output is parked in high
// impedance so that the bridge sees no packets between streams.
func (front *frontend) StopStream() error {
	err := front.bridge.StopStream()
	if outputErr := front.demod.SetOutputMode(dib8096.OutputHighZ); err == nil {
		err = outputErr
	}
	return err
}

// Release turns the LED back to green once the tuner is handed back.
func (front *frontend) Release() error { return front.demod.SetGPIO(ledPin, false) }

// Read takes the next packets off the stream endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.bridge.ReadStream(buffer, streamTimeoutMS))
}
