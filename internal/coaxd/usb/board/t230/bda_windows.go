// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package t230

// A stick bound to its vendor's BDA driver (cydtv.sys) rather than to WinUSB.
//
// The driver carries the transport stream and nothing else is asked of it: in
// DVB-C it does not program the Si2168's symbol rate, so ISDB-C never locks
// through BDA. The chips are tuned with the same drivers as over WinUSB, through
// the raw I2C transfer the driver puts on its capture filter.

import (
	"fmt"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/lwire"
	"github.com/Till0196/coax/internal/coaxd/demod/si2168"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/si2157"
	"github.com/Till0196/coax/pkg/i2c"
)

// BDAStick is one stick the vendor's driver offers.
type BDAStick struct {
	Path       string
	Name       string
	Deliveries []string
}

// BDASticks lists the sticks of this family among the BDA tuners. Nothing is
// opened: the device path says which board it is.
func BDASticks() ([]BDAStick, error) {
	tuners, err := bda.Devices()
	if err != nil {
		return nil, err
	}
	var found []BDAStick
	for _, tuner := range tuners {
		board, ok := productOfPath(tuner.Selector)
		if !ok {
			continue
		}
		found = append(found, BDAStick{Path: tuner.Selector, Name: board.name, Deliveries: append([]string(nil), deliveries...)})
	}
	return found, nil
}

// IsBDAPath reports whether a selector names a BDA tuner rather than a USB
// device.
func IsBDAPath(selector string) bool { return strings.HasPrefix(selector, "@device:") }

// BDADevice is one stick, driven from here.
type BDADevice struct {
	transport *bda.Device
	board     product
	// control and chips belong to the graph standing now; see carry.
	control *vendors.Control
	chips   *chips
}

// OpenBDA claims a stick.
func OpenBDA(path string) (*BDADevice, error) {
	board, ok := productOfPath(path)
	if !ok {
		return nil, fmt.Errorf("%q is not a stick this backend drives", path)
	}
	transport, err := bda.Open(path)
	if err != nil {
		return nil, err
	}
	return &BDADevice{transport: transport, board: board}, nil
}

func (stick *BDADevice) Name() string { return stick.board.name }

func (stick *BDADevice) Supports(deliverySystem string) bool { return Supports(deliverySystem) }

// Tune points the chips at a channel, bringing the graph up first when none is
// standing: the bus is a property of a filter in it.
func (stick *BDADevice) Tune(properties map[string]string) error {
	if err := stick.carry(); err != nil {
		return err
	}
	return stick.chips.Tune(properties)
}

// carry makes sure a graph is standing and the chips are reached through it.
//
// The graph is built without a channel. A tune request the driver is handed is
// worked through after the graph is up, and lands on the chips after they have
// been tuned from here.
//
// A closed stream takes its graph with it, and the filter the bus was a
// property of goes too, so this is asked on every tune.
func (stick *BDADevice) carry() error {
	if stick.chips != nil && stick.transport.TransportLive() {
		return nil
	}
	stick.release()
	if err := stick.transport.StartUntunedTransport(networkType); err != nil {
		return err
	}
	control, err := stick.transport.CaptureControl()
	if err != nil {
		return err
	}
	stick.control = control
	bus := &lwire.Bus{Control: control, Name: stick.board.name}
	// The driver has the chips running on their firmware already, so they are
	// not initialized again here.
	demod := si2168.New(bus, "", stick.board.tsClockManual)
	tuner := si2157.New(i2c.Gated(bus, demod), "", stick.board.tuner)
	stick.chips = &chips{demod: demod, tuner: tuner, model: stick.board.name}
	return nil
}

// networkType is what the graph is built against. Only the network type
// matters, and DVB-T is the one the driver accepts; the channel is never sent.
var networkType = func() bda.TuneProfile {
	profile, err := bda.BuildTuneProfile(map[string]string{
		"DELIVERY_SYSTEM": "DVBT",
		"FREQUENCY":       "474000000",
		"BANDWIDTH_HZ":    "8000000",
	})
	if err != nil {
		panic(err)
	}
	return profile
}()

func (stick *BDADevice) release() {
	if stick.control != nil {
		stick.control.Release()
		stick.control = nil
	}
	stick.chips = nil
}

// Status is what the demodulator and the tuner measure, or nothing before a
// tune.
func (stick *BDADevice) Status() (device.Status, error) {
	if stick.chips == nil || !stick.transport.TransportLive() {
		return device.Status{}, nil
	}
	return stick.chips.Status()
}

func (stick *BDADevice) OpenStream() (*bda.Stream, error) { return stick.transport.OpenStream() }

func (stick *BDADevice) Close() error {
	stick.release()
	return stick.transport.Close()
}
