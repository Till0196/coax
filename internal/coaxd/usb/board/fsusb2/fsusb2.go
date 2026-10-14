// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package fsusb2 drives the KEIAN KTV-FSUSB2 ISDB-T stick: an EM2874 USB
// bridge carrying a Fujitsu demodulator with its tuner on the gated bus.
//
// The stick shipped in two builds that share a USB identity, so which silicon
// is fitted has to be probed rather than looked up: an MB86A20S with an NXP
// TDA18211HD tuner, or an MB86A21S with a MaxLinear MxL135RF. The MxL135RF is
// the same part the US-3POUT carries, so it reuses that driver.
package fsusb2

import (
	"context"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/mb86a20s"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/mxl136"
	"github.com/Till0196/coax/internal/coaxd/tuner/tda18211"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/em287x"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "fsusb2",
	Aliases:     []string{"ktv-fsusb2", "ktvfsusb2", "fsusb2n"},
	Description: "KEIAN KTV-FSUSB2 (EM2874 + MB86A20S/A21S)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's single tuner. Which front end is fitted cannot be told
// from the product number -- both variants enumerate alike and are only told
// apart once the demodulator answers -- so the name here covers both.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "MxL135RF / TDA18211HD", Deliveries: deliveries}}
}

// deliveries is what the board's single tuner can reach.
var deliveries = []string{"ISDB-T"}

var supportedIDs = []usbio.ID{
	{Vendor: 0x0511, Product: 0x0029},
	{Vendor: 0x0511, Product: 0x003b},
}

// I2C addresses on the bridge, already shifted. The EEPROM answers on the
// bridge's primary bus, which bit 0x1000 of the address selects.
const (
	eepromAddress = 0x1000 | 0xa0
	demodAddress  = 0x20
	tunerAddress  = 0xc0
)

// lockTimeout is how long the demodulator is given to finish acquisition.
const lockTimeout = 10 * time.Second

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == "ISDBT"
}

// tuner is whichever of the two front ends this stick was built with.
type tuner interface {
	Initialize() error
	Tune(frequencyKHz uint32) error
	Sleep() error
}

// mxlTuner adapts the shared MxL135RF driver to the interface above.
type mxlTuner struct {
	bus   i2c.Bus
	inner *mxl136.Tuner
}

func (adapter *mxlTuner) Initialize() error {
	inner, err := mxl136.New(adapter.bus)
	if err != nil {
		return err
	}
	adapter.inner = inner
	return nil
}

func (adapter *mxlTuner) Tune(frequencyKHz uint32) error {
	return adapter.inner.Tune(uint64(frequencyKHz))
}

func (adapter *mxlTuner) Sleep() error { return adapter.inner.Sleep() }

// open claims a KTV-FSUSB2 and brings its demodulator and tuner up.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	bridge, err := em287x.NewFSUSB2(transport)
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("initialize EM2874: %w", err)
	}
	variant, err := detect(bridge)
	if err != nil {
		_ = bridge.Close()
		return nil, err
	}

	demod := mb86a20s.New(bridge.I2CBus(), variant)
	tunerBus := i2c.Gated(bridge.I2CBus(), demod)
	model, tunerName := "KEIAN KTV-FSUSB2 (MB86A21S)", "MxL135RF"
	var front tuner = &mxlTuner{bus: tunerBus}
	if variant == mb86a20s.A20S {
		model, tunerName = "KEIAN KTV-FSUSB2 (MB86A20S)", "TDA18211HD"
		front = tda18211.New(tunerBus)
	}

	inner, err := boardkit.New(boardkit.Config{
		Model:    model,
		Location: bridge.Name(),
		Tuners:   []device.TunerInfo{{Index: 0, Name: tunerName, Deliveries: deliveries}},
		Release:  bridge.Close,
		Supports: Supports,
		Open: func(int) (boardkit.Tuner, error) {
			return &frontend{bridge: bridge, demod: demod, tuner: front}, nil
		},
	})
	if err != nil {
		_ = bridge.Close()
		return nil, fmt.Errorf("open: %w", err)
	}
	// The stick carries an on-board reader, on the bridge's own card interface.
	return boardkit.CardBoard(inner, bridge, "EM2874"), nil
}

// detect works out which of the two builds this is: the newer one records its
// identity in the EEPROM, and the older one is told apart by the identifier
// its NXP tuner reports.
func detect(bridge *em287x.Bridge) (mb86a20s.Variant, error) {
	identity, err := bridge.I2C(eepromAddress, []byte{0x00, 0x6a}, 2)
	if err != nil {
		return 0, fmt.Errorf("read the board EEPROM: %w", err)
	}
	if len(identity) == 2 && identity[0] == 0x3b && identity[1] == 0x00 {
		return mb86a20s.A21S, nil
	}

	// Otherwise ask the tuner directly, which means opening the demodulator's
	// gate around the read.
	if _, err := bridge.I2C(demodAddress, []byte{0xfe, 0x00}, 0); err != nil {
		return 0, fmt.Errorf("open the tuner gate: %w", err)
	}
	identifier, readErr := bridge.I2C(tunerAddress, []byte{0x00}, 1)
	if _, err := bridge.I2C(demodAddress, []byte{0xfe, 0x01}, 0); err != nil {
		return 0, fmt.Errorf("close the tuner gate: %w", err)
	}
	if readErr != nil {
		return 0, fmt.Errorf("read the tuner identifier: %w", readErr)
	}
	if len(identifier) == 1 && identifier[0] == 0x84 {
		return mb86a20s.A20S, nil
	}
	return mb86a20s.A21S, nil
}

// frontend is the board's single terrestrial tuner: whichever front end the
// stick was built with, in front of the Fujitsu demodulator.
type frontend struct {
	bridge *em287x.Bridge
	demod  *mb86a20s.Demod
	tuner  tuner
	tuned  bool
}

// Tune locks onto the channel described by the given channel-file properties.
// The whole front end is reprogrammed per channel -- the tuner's calibration and
// the demodulator's register set are rerun rather than only moving the
// synthesiser -- because the parts do not acquire reliably otherwise.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := properties["DELIVERY_SYSTEM"]
	if !Supports(delivery) {
		return fmt.Errorf("the KTV-FSUSB2 does not support %q", delivery)
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	frequency = tuning.FrequencyKHz(frequency)
	if err := front.tuner.Initialize(); err != nil {
		return fmt.Errorf("initialize the tuner: %w", err)
	}
	if err := front.tuner.Tune(uint32(frequency)); err != nil {
		return fmt.Errorf("tune to %d kHz: %w", frequency, err)
	}
	if err := front.demod.Initialize(); err != nil {
		return fmt.Errorf("initialize %s: %w", front.demod.Variant(), err)
	}
	if err := front.demod.Reset(); err != nil {
		return fmt.Errorf("restart %s acquisition: %w", front.demod.Variant(), err)
	}
	// Acquisition runs on its own once reset; Status reports how it went.
	if _, err := front.demod.WaitForLock(lockTimeout); err != nil {
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
	locked, err := front.demod.Locked()
	if err != nil || !locked {
		return device.Status{}, err
	}
	status := device.Status{
		Locked: true,
		Names:  device.LockNames,
	}
	// The demodulator reports thousandths of a dB, and zero when it has no
	// reading yet; a failure to measure is not a failure of the lock. It has no
	// signal strength to offer: its readings are relative, and Status.Strength
	// is a level in dBm.
	if cnr, cnrErr := front.demod.CNR(); cnrErr == nil && cnr != 0 {
		status.CNR = device.Measured(float64(cnr) / 1000)
	}
	return status, nil
}

// StartStream opens the bridge's stream endpoint.
func (front *frontend) StartStream() error { return front.bridge.Stream(true) }

func (front *frontend) StopStream() error { return front.bridge.Stream(false) }

// Read takes the next packets off the endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.bridge.ReadStream(buffer, 100))
}

// Release parks the tuner when the board's tuner is handed back.
func (front *frontend) Release() error { return front.tuner.Sleep() }
