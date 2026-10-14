// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (isdb2056_device.c, m1ur_device.c) by nns779.

// Package isdb2056 drives the single-tuner PLEX and DigiBest sticks built from
// an IT930x bridge, a pair of TC90522 demodulator cores and both an R850 and an
// RT710 tuner: DigiBest DTV02A-1T1S-U (ISDB2056 and ISDB2056N) and PLEX PX-M1UR.
//
// The board has one TS output shared by the two demodulator cores, so it is one
// tuner that switches between ISDB-T and ISDB-S rather than two.
package isdb2056

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/r850"
	"github.com/Till0196/coax/internal/coaxd/tuner/rt710"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/board/plex"
	"github.com/Till0196/coax/internal/coaxd/usb/tsdemux"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "isdb2056",
	Aliases:     []string{"dtv02a", "m1ur", "pxm1ur"},
	Description: "DigiBest DTV02A-1T1S-U, PLEX PX-M1UR (IT930x + TC90522 + R850/RT710)",
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
		Name:       "R850 / RT710",
		Deliveries: deliveries,
		// The supply is in or out; the gain is the RT710's trim, on the satellite
		// side only (the R850 has none to move).
		Controls: device.Controls{
			LNB: &device.LNBControl{Off: true, Shared: "board"},
			Gain: &device.GainControl{
				Decibels:   rt710.RT710FineGainStepsDB(),
				Deliveries: []string{"ISDB-S"},
			},
		},
	}}
}

// deliveries is what the board's single tuner can reach.
var deliveries = []string{"ISDB-T", "ISDB-S"}

// model identifies which of the near-identical products this is.
type model int

const (
	modelISDB2056 model = iota
	// modelISDB2056N fits a second satellite demodulator core whose registers
	// carry the board's clock setup.
	modelISDB2056N
	modelM1UR
)

// models maps each USB product ID to its variant.
var models = map[uint16]model{
	0x004b: modelISDB2056,  // DigiBest ISDB2056
	0x084b: modelISDB2056N, // DigiBest ISDB2056N
	0x0854: modelM1UR,      // PLEX PX-M1UR
}

var supportedIDs = usbio.IDs(0x0511, models)

// I2C addresses, 8-bit as the rest of the tree uses.
const (
	terrestrialDemod = 0x10 << 1
	// The satellite core moved on the ISDB2056N; the original address stays as
	// the clock core there.
	satelliteDemod  = 0x11 << 1
	satelliteDemodN = 0x13 << 1
	clockDemodN     = 0x11 << 1
	r850Address     = 0x7c << 1
	rt710Address    = 0x7a << 1
)

// initSatellite is the register sequence the satellite core needs at
// power-up. The terrestrial core takes tc90522.InitTerrestrial.
var initSatellite = []byte{0x15, 0x00, 0x1d, 0x00}

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	switch tuning.Normalize(deliverySystem) {
	case "ISDBT", "ISDBS":
		return true
	default:
		return false
	}
}

// open claims the stick and brings its demodulators and tuners up.
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

	name := "DigiBest DTV02A-1T1S-U"
	if variant == modelM1UR {
		name = "PLEX PX-M1UR"
	}
	front := &frontend{model: variant, name: name}
	frame, err := plex.Open(options, plex.Config{
		IDs: []usbio.ID{{Vendor: 0x0511, Product: product}},
		// One input, so the stream needs no tagging.
		Inputs:          plex.Inputs([]plex.InputSpec{{Port: 0, Bus: 3, Address: 0x10}}, false, []byte{1, 2, 3, 4}),
		Tuners:          1,
		Tag:             tsdemux.PlainSync(),
		PowerGPIO:       3,
		PowerEnableGPIO: 2,
		LNBGPIO:         11,
		PowerOn:         front.powerOn,
	})
	if err != nil {
		return nil, err
	}
	front.lnb = frame.NewLNBClaim()

	bus := frame.Bridge.I2C(3)
	front.terrestrial = tc90522.New(bus, terrestrialDemod, false)
	satelliteAddress := byte(satelliteDemod)
	if variant == modelISDB2056N {
		satelliteAddress = satelliteDemodN
		front.clock = tc90522.New(bus, clockDemodN, false)
	}
	front.satellite = tc90522.New(bus, satelliteAddress, false)
	front.r850 = r850.New(front.terrestrial.TunerBus(), r850Address, r850.Config{XtalKHz: 24000})
	front.rt710 = rt710.New(front.satellite.TunerBus(), rt710Address, rt710.ISDBSBoardConfig)

	inner, err := frame.Board(name,
		[]device.TunerInfo{{Index: 0, Name: "R850 / RT710", Deliveries: deliveries}},
		func(int) (plex.Tuner, error) { return front, nil },
	)
	if err != nil {
		_ = frame.Close()
		return nil, err
	}
	// Every product in this family carries a card slot.
	return frame.CardBoard(inner, true, "IT930x"), nil
}

// frontend is the board's single tuner. It reaches both systems by switching
// the shared TS output between the two demodulator cores.
type frontend struct {
	model model
	// name is the product's display name, for messages.
	name string

	terrestrial *tc90522.Demod
	satellite   *tc90522.Demod
	// clock is the ISDB2056N's extra core, nil on the other variants.
	clock *tc90522.Demod
	r850  *r850.Tuner
	rt710 *rt710.Tuner

	// onSatellite records which core the last tune left driving the output.
	onSatellite bool
	tuned       bool
	// lnb is this tuner's claim on the shared LNB supply.
	lnb plex.LNBClaim
}

// GainLadder is the RT710's fine gain trim, reachable only on satellite: the
// R850 on the terrestrial side has no gain setting at all.
func (front *frontend) GainLadder(deliverySystem string) tuning.GainLadder {
	if !tuning.IsSatellite(deliverySystem) || front.rt710 == nil {
		return tuning.GainLadder{}
	}
	return tuning.NewGainLadder(front.rt710.FineGainStepsDB()...)
}

// SetGain moves the trim.
func (front *frontend) SetGain(step int) error {
	if front.rt710 == nil {
		return fmt.Errorf("%s has no gain setting to change", front.name)
	}
	return front.rt710.SetFineGain(step)
}

// powerOn initialises both tuner ICs and parks the demodulators.
func (front *frontend) powerOn() error {
	if err := front.r850.Initialize(); err != nil {
		return fmt.Errorf("initialize R850: %w", err)
	}
	if err := front.rt710.Initialize(); err != nil {
		return fmt.Errorf("initialize RT710: %w", err)
	}
	if err := front.terrestrial.WritePairs(tc90522.InitTerrestrial); err != nil {
		return err
	}
	if err := front.terrestrial.EnableTSPinsT(false); err != nil {
		return err
	}
	if err := front.terrestrial.SleepT(true); err != nil {
		return err
	}
	if err := front.r850.SetSystem(r850.ISDBTSystemConfig); err != nil {
		return err
	}
	if err := front.satellite.WritePairs(initSatellite); err != nil {
		return err
	}
	if err := front.satellite.EnableTSPinsS(false); err != nil {
		return err
	}
	return front.satellite.SleepS(true)
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	if !Supports(delivery) {
		return fmt.Errorf("%s does not support %q", front.name, properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	frequency = tuning.FrequencyKHz(frequency)
	if delivery == "ISDBS" {
		streamID, err := tuning.StreamID(properties)
		if err != nil {
			return err
		}
		feed, err := tuning.SwitchedSupply(properties)
		if err != nil {
			return err
		}
		if err := front.tuneSatellite(uint32(frequency), streamID, feed); err != nil {
			return err
		}
		front.onSatellite = true
	} else {
		if err := front.tuneTerrestrial(uint32(frequency)); err != nil {
			return err
		}
		front.onSatellite = false
	}
	front.tuned = true
	return nil
}

// tuneTerrestrial parks the satellite side and acquires an ISDB-T channel.
func (front *frontend) tuneTerrestrial(frequency uint32) error {
	demod := front.terrestrial
	if err := demod.WriteRegister(0x47, 0x30); err != nil {
		return err
	}
	// The gain control is held off while the synthesiser moves.
	if err := demod.SetAGCT(false); err != nil {
		return err
	}
	if err := front.satellite.SleepS(true); err != nil {
		return err
	}
	// Route the shared TS output to the terrestrial core.
	if err := demod.WritePairs([]byte{0x0e, 0x77, 0x0f, 0x10, 0x71, 0x20}); err != nil {
		return err
	}
	if err := demod.SleepT(false); err != nil {
		return err
	}
	if err := demod.WritePairs([]byte{0x76, 0x0c, 0x1f, 0x30}); err != nil {
		return err
	}
	if err := front.r850.Wake(); err != nil {
		return err
	}
	if err := front.r850.Tune(frequency); err != nil {
		return fmt.Errorf("tune R850: %w", err)
	}
	if err := plex.WaitForLock(front.r850.PLLLocked); err != nil {
		return fmt.Errorf("R850 synthesiser: %w", err)
	}
	if err := demod.SetAGCT(true); err != nil {
		return err
	}
	if err := demod.WritePairs([]byte{0x71, 0x01, 0x72, 0x25, 0x75, 0x00}); err != nil {
		return err
	}
	// The demodulator needs a moment before its lock flag means anything.
	time.Sleep(100 * time.Millisecond)
	return nil
}

// tuneSatellite parks the terrestrial side and acquires an ISDB-S channel.
func (front *frontend) tuneSatellite(frequency uint32, streamID uint16, supply bool) error {
	demod := front.satellite
	if err := front.lnb.Want(supply); err != nil {
		return err
	}
	if err := demod.SetAGCS(false); err != nil {
		return err
	}
	// Route the shared TS output to the satellite core.
	if err := front.terrestrial.WritePairs([]byte{0x0e, 0x11, 0x0f, 0x70}); err != nil {
		return err
	}
	if err := front.terrestrial.SleepT(true); err != nil {
		return err
	}
	// The ISDB2056N carries the board's clock setup on a separate core.
	clock, first, second := demod, byte(0x77), byte(0x10)
	if front.model == modelISDB2056N {
		clock, first, second = front.clock, 0x77, 0x37
	}
	if err := clock.WritePairs([]byte{0x07, first, 0x08, second}); err != nil {
		return err
	}
	if err := demod.SleepS(false); err != nil {
		return err
	}
	if err := demod.WritePairs([]byte{0x04, 0x02, 0x8e, 0x02}); err != nil {
		return err
	}
	if err := front.terrestrial.WriteRegister(0x1f, 0x20); err != nil {
		return err
	}
	// 28860 kBaud with a roll-off index of 4 is the ISDB-S carrier.
	if err := front.rt710.Tune(frequency, 28860, 4); err != nil {
		return fmt.Errorf("tune RT710: %w", err)
	}
	if err := plex.WaitForLock(front.rt710.PLLLocked); err != nil {
		return fmt.Errorf("RT710 synthesiser: %w", err)
	}
	if err := demod.SetAGCS(true); err != nil {
		return err
	}
	return demod.SelectStreamS(streamID)
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if !front.tuned {
		return device.Status{}, nil
	}
	var locked bool
	var err error
	if front.onSatellite {
		locked, err = front.satellite.LockedS()
	} else {
		locked, err = front.terrestrial.LockedT()
	}
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: locked}
	if locked {
		status.Names = device.LockNames
		cnr, measured, cnrErr := front.terrestrial.CNRT()
		if front.onSatellite {
			cnr, measured, cnrErr = front.satellite.CNRS()
		}
		if cnrErr == nil && measured {
			status.CNR = device.Measured(cnr)
		}
	}
	// A tuner measurement, so it stands whether or not the demodulator locked.
	if front.onSatellite {
		if strength, strengthErr := front.rt710.SignalStrength(); strengthErr == nil {
			status.Strength = device.Measured(float64(strength) / 1000)
		}
	}
	return status, nil
}

// EnableTS starts or stops the TS output of whichever core is in use.
func (front *frontend) EnableTS(enable bool) error {
	if front.onSatellite {
		return front.satellite.EnableTSPinsS(enable)
	}
	return front.terrestrial.EnableTSPinsT(enable)
}

// Park powers both sides down and drops the claim on the LNB supply.
func (front *frontend) Park() error {
	_ = front.r850.Sleep()
	_ = front.terrestrial.SleepT(true)
	_ = front.rt710.Sleep()
	err := front.satellite.SleepS(true)
	return front.lnb.Release(err)
}
