// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (s1ur_device.c) by nns779.

// Package s1ur drives the terrestrial-only single-tuner sticks: PLEX PX-S1UR
// and DigiBest ISDBT2071 (DTV03A-1TU).
//
// Both fit an IT930x bridge, a TC90522 terrestrial demodulator and an R850
// tuner. The PX-S1UR also carries an unused satellite demodulator core, which
// is parked so it cannot drive the shared TS output.
package s1ur

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/r850"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/board/plex"
	"github.com/Till0196/coax/internal/coaxd/usb/tsdemux"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "s1ur",
	Aliases:     []string{"pxs1ur", "isdbt2071", "dtv03a"},
	Description: "PLEX PX-S1UR, DigiBest ISDBT2071 (IT930x + TC90522 + R850)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "R850", Deliveries: deliveries}}
}

// deliveries is what the board's single tuner can reach.
var deliveries = []string{"ISDB-T"}

// model identifies which of the two products this is.
type model int

const (
	modelS1UR model = iota
	modelISDBT2071
)

// models maps each USB product ID to its variant.
var models = map[uint16]model{
	0x0855: modelS1UR,      // PLEX PX-S1UR
	0x0052: modelISDBT2071, // DigiBest ISDBT2071 (DTV03A-1TU)
}

var supportedIDs = usbio.IDs(0x0511, models)

// I2C addresses, 8-bit as the rest of the tree uses.
const (
	terrestrialDemod     = 0x10 << 1
	terrestrialDemod2071 = 0x18 << 1
	satelliteDemodParked = 0x11 << 1
	r850Address          = 0x7c << 1
)

// initISDBT2071 is the register sequence the ISDBT2071's demodulator needs at
// power-up. The PX-S1UR uses the entirely different tc90522.InitTerrestrial.
var initISDBT2071 = []byte{
	0x04, 0x00, 0x10, 0x00, 0x11, 0x2d, 0x12, 0x02,
	0x13, 0x62, 0x14, 0x60, 0x15, 0x00, 0x16, 0x00,
	0x1d, 0x05, 0x1e, 0x15, 0x1f, 0x40, 0x30, 0x20,
	0x31, 0x0b, 0x32, 0x8f, 0x34, 0x0f, 0x38, 0x01,
	0x39, 0x1c,
}

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == "ISDBT"
}

// open claims the stick and brings its demodulator and tuner up.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	product := transport.ProductID()
	_ = transport.Close()
	variant, ok := models[product]
	if !ok {
		return nil, fmt.Errorf("unsupported product ID %#04x", product)
	}

	// The ISDBT2071 puts its demodulator at a different address and feeds a
	// different bridge port.
	spec := plex.InputSpec{Port: 0, Bus: 3, Address: 0x10}
	unused := []byte{1, 2, 3, 4}
	if variant == modelISDBT2071 {
		spec = plex.InputSpec{Port: 4, Bus: 3, Address: 0x18}
		unused = []byte{0, 1, 2, 3}
	}

	name := "PLEX PX-S1UR"
	if variant == modelISDBT2071 {
		name = "DigiBest ISDBT2071"
	}
	front := &frontend{model: variant, name: name}
	frame, err := plex.Open(options, plex.Config{
		IDs: []usbio.ID{{Vendor: 0x0511, Product: product}},
		// One input, so the stream needs no tagging.
		Inputs:          plex.Inputs([]plex.InputSpec{spec}, false, unused),
		Tuners:          1,
		Tag:             tsdemux.PlainSync(),
		PowerGPIO:       3,
		PowerEnableGPIO: 2,
		PowerOn:         front.powerOn,
	})
	if err != nil {
		return nil, err
	}

	bus := frame.Bridge.I2C(3)
	address := byte(terrestrialDemod)
	if variant == modelISDBT2071 {
		address = terrestrialDemod2071
	} else {
		front.parked = tc90522.New(bus, satelliteDemodParked, false)
	}
	front.demod = tc90522.New(bus, address, false)
	front.r850 = r850.New(front.demod.TunerBus(), r850Address, r850.Config{XtalKHz: 24000})

	inner, err := frame.Board(name,
		[]device.TunerInfo{{Index: 0, Name: "R850", Deliveries: deliveries}},
		func(int) (plex.Tuner, error) { return front, nil },
	)
	if err != nil {
		_ = frame.Close()
		return nil, err
	}
	// Every product in this family carries a card slot.
	return frame.CardBoard(inner, true, "IT930x"), nil
}

// frontend is the board's single terrestrial tuner.
type frontend struct {
	model model
	// name is the product's display name, for messages.
	name  string
	demod *tc90522.Demod
	// parked is the PX-S1UR's unused satellite core, nil on the ISDBT2071.
	parked *tc90522.Demod
	r850   *r850.Tuner
	tuned  bool
}

// powerOn initialises the tuner and demodulator once the supply is up.
func (front *frontend) powerOn() error {
	if err := front.r850.Initialize(); err != nil {
		return fmt.Errorf("initialize R850: %w", err)
	}
	sequence := tc90522.InitTerrestrial
	if front.model == modelISDBT2071 {
		sequence = initISDBT2071
	}
	if err := front.demod.WritePairs(sequence); err != nil {
		return err
	}
	if err := front.demod.EnableTSPinsT(false); err != nil {
		return err
	}
	if err := front.demod.SleepT(false); err != nil {
		return err
	}
	if err := front.r850.Wake(); err != nil {
		return err
	}
	if err := front.r850.SetSystem(r850.ISDBTSystemConfig); err != nil {
		return err
	}
	if front.parked == nil {
		return nil
	}
	// Park the unused satellite core so it cannot drive the shared TS output.
	if err := front.parked.EnableTSPinsS(false); err != nil {
		return err
	}
	return front.parked.SleepS(true)
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	if !Supports(properties["DELIVERY_SYSTEM"]) {
		return fmt.Errorf("%s does not support %q", front.name, properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	frequency = tuning.FrequencyKHz(frequency)
	if err := front.demod.WriteRegister(0x47, 0x30); err != nil {
		return err
	}
	// The gain control is held off while the synthesiser moves.
	if err := front.demod.SetAGCT(false); err != nil {
		return err
	}
	if front.model == modelISDBT2071 {
		err = front.demod.WritePairs([]byte{0x76, 0x03, 0x77, 0x01, 0x3b, 0x10, 0x3c, 0x10, 0x3d, 0x24})
	} else {
		err = front.demod.WritePairs([]byte{0x0e, 0x77, 0x0f, 0x10, 0x71, 0x20, 0x76, 0x0c, 0x1f, 0x30})
	}
	if err != nil {
		return err
	}
	if err := front.r850.Tune(uint32(frequency)); err != nil {
		return fmt.Errorf("tune R850: %w", err)
	}
	if err := plex.WaitForLock(front.r850.PLLLocked); err != nil {
		return fmt.Errorf("R850 synthesiser: %w", err)
	}
	if err := front.demod.SetAGCT(true); err != nil {
		return err
	}
	if front.model != modelISDBT2071 {
		if err := front.demod.WritePairs([]byte{0x71, 0x01, 0x72, 0x25, 0x75, 0x00}); err != nil {
			return err
		}
	}
	// The demodulator needs a moment before its lock flag means anything.
	time.Sleep(100 * time.Millisecond)
	front.tuned = true
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if !front.tuned {
		return device.Status{}, nil
	}
	locked, err := front.demod.LockedT()
	if err != nil || !locked {
		return device.Status{}, err
	}
	status := device.Status{
		Locked: true,
		Names:  device.LockNames,
	}
	if cnr, measured, cnrErr := front.demod.CNRT(); cnrErr == nil && measured {
		status.CNR = device.Measured(cnr)
	}
	return status, nil
}

// EnableTS switches the demodulator's TS output.
func (front *frontend) EnableTS(enable bool) error { return front.demod.EnableTSPinsT(enable) }

// Park powers the tuner and demodulator down.
func (front *frontend) Park() error {
	_ = front.r850.Sleep()
	return front.demod.SleepT(true)
}
