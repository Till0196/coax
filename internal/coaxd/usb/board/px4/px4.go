// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (px4_device.c) by nns779.

// Package px4 drives the PLEX PX-W3U4, PX-Q3U4, PX-W3PE4, PX-Q3PE4, PX-W3PE5
// and PX-Q3PE5.
//
// One USB device carries an IT930x bridge feeding four tuners: two ISDB-S
// (TC90522 plus RT710) at indices 0 and 1, and two ISDB-T (TC90522 plus R850)
// at indices 2 and 3. The Q3 models are two such devices sharing a serial
// number, so each half enumerates as its own four-tuner board.
package px4

import (
	"fmt"

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
	Name:        "px4",
	Aliases:     []string{"pxw3u4", "pxq3u4", "pxw3pe4", "pxq3pe4", "pxw3pe5", "pxq3pe5"},
	Description: "PLEX PX-W3U4/Q3U4/W3PE4/Q3PE4/W3PE5/Q3PE5 (IT930x + TC90522 + R850/RT710)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement, the same on every product this
// backend claims: two satellite tuners then two terrestrial ones. open builds
// its tuners from the same function, so the two cannot disagree.
func Layout(usbio.ID) []device.TunerInfo {
	descriptors := make([]device.TunerInfo, 0, tunerCount)
	for index, entry := range layout {
		descriptor := device.TunerInfo{Index: index, Name: "R850", Deliveries: []string{"ISDB-T"}}
		if entry.kind == kindSatellite {
			descriptor.Name, descriptor.Deliveries = "RT710", []string{"ISDB-S"}
			// The supply is one line for the board's satellite tuners (plex.LNBClaim),
			// in or out; the RT710's fine gain trim is the only gain there is.
			descriptor.Controls = device.Controls{
				LNB:  &device.LNBControl{Off: true, Shared: "board"},
				Gain: &device.GainControl{Decibels: rt710.RT710FineGainStepsDB()},
			}
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors
}

// deliveries is what the four tuners reach between them; each on its own
// reaches one of the two.
var deliveries = []string{"ISDB-T", "ISDB-S"}

var supportedIDs = []usbio.ID{
	{Vendor: 0x0511, Product: 0x083f}, // PX-W3U4
	{Vendor: 0x0511, Product: 0x084a}, // PX-Q3U4
	{Vendor: 0x0511, Product: 0x023f}, // PX-W3PE4
	{Vendor: 0x0511, Product: 0x024a}, // PX-Q3PE4
	{Vendor: 0x0511, Product: 0x073f}, // PX-W3PE5
	{Vendor: 0x0511, Product: 0x074a}, // PX-Q3PE5
}

// tunerCount is how many tuners one USB device carries.
const tunerCount = 4

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	switch tuning.Normalize(deliverySystem) {
	case "ISDBT", "ISDBS":
		return true
	default:
		return false
	}
}

// tunerKind is which of the two tuner types sits at an index.
type tunerKind int

const (
	kindSatellite tunerKind = iota
	kindTerrestrial
)

// layout describes the board's fixed tuner arrangement. Indices 0 and 1 are
// satellite, 2 and 3 terrestrial.
var layout = [tunerCount]struct {
	kind    tunerKind
	demodID byte // TC90522 7-bit I2C address
}{
	{kindSatellite, 0x11},
	{kindSatellite, 0x13},
	{kindTerrestrial, 0x10},
	{kindTerrestrial, 0x12},
}

// Demodulator and tuner I2C addresses, 8-bit as the rest of the tree uses.
const (
	rt710Address = 0x7a << 1
	r850Address  = 0x7c << 1
)

// Register sequences the board's demodulators need at power-up. The
// terrestrial one is the shared tc90522.InitTerrestrial plus two board pairs.
var (
	initSatellite   = []byte{0x15, 0x00, 0x1d, 0x00, 0x04, 0x02}
	initTerrestrial = append(append([]byte(nil), tc90522.InitTerrestrial...), 0x1f, 0x00, 0x75, 0x00)
	// The first satellite and first terrestrial demodulator carry the board's
	// shared clock, so they get an extra nudge once per power-up.
	initSatelliteMaster   = []byte{0x07, 0x31, 0x08, 0x77}
	initTerrestrialMaster = []byte{0x0e, 0x77, 0x0f, 0x13}
)

// open claims a PX-series device and brings its bridge and four tuners up.
func open(options device.Options) (device.Board, error) {
	specs := make([]plex.InputSpec, 0, tunerCount)
	for index, entry := range layout {
		specs = append(specs, plex.InputSpec{
			Port:    byte(index + 1),
			Bus:     2,
			Address: entry.demodID,
		})
	}
	var fronts [tunerCount]*frontend
	frame, err := plex.Open(options, plex.Config{
		IDs:    supportedIDs,
		Inputs: plex.Inputs(specs, true, []byte{0}),
		Tuners: tunerCount,
		Tag:    tsdemux.TaggedSync(tunerCount),
		// GPIO 7 and 2 gate the tuner board's supply; 11 gates the LNB supply.
		PowerGPIO:       7,
		PowerEnableGPIO: 2,
		LNBGPIO:         11,
		PowerOn:         func() error { return powerOn(fronts) },
	})
	if err != nil {
		return nil, err
	}

	demodBus := frame.Bridge.I2C(2)
	descriptors := Layout(usbio.ID{})
	for index, entry := range layout {
		demod := tc90522.New(demodBus, entry.demodID<<1, index%2 == 1)
		front := &frontend{index: index, kind: entry.kind, demod: demod, lnb: frame.NewLNBClaim()}
		switch entry.kind {
		case kindSatellite:
			front.rt710 = rt710.New(demod.TunerBus(), rt710Address, rt710.ISDBSBoardConfig)
		case kindTerrestrial:
			front.r850 = r850.New(demod.TunerBus(), r850Address, r850.Config{
				XtalKHz: 24000,
				// Only the first terrestrial tuner drives the loop-through to
				// the second one.
				LoopThrough:      index%2 == 0,
				NoIMRCalibration: true,
				NoLPFCalibration: true,
			})
		}
		fronts[index] = front
	}

	inner, err := frame.Board("PLEX PX4", descriptors, func(index int) (plex.Tuner, error) {
		front := fronts[index]
		if err := front.start(); err != nil {
			return nil, fmt.Errorf("start tuner %d: %w", index, err)
		}
		return front, nil
	})
	if err != nil {
		_ = frame.Close()
		return nil, err
	}
	return frame.CardBoard(inner, cardReaderFitted(frame), "IT930x"), nil
}

// pairedProducts are the eight-tuner enclosures, which are two four-tuner USB
// devices sharing one serial number.
var pairedProducts = map[uint16]bool{0x084a: true, 0x024a: true, 0x074a: true}

// serialDigits is how long these enclosures' serial numbers are. A string of
// any other length is not one of theirs, whatever else it may be.
const serialDigits = 15

// cardReaderFitted reports whether this USB device is the one the enclosure's
// card slot is wired to. A four-tuner enclosure is one device and has it; an
// eight-tuner one is two devices whose shared serial ends in the digit that
// numbers them, and only the first is wired to the slot.
func cardReaderFitted(frame *plex.Frame) bool {
	if !pairedProducts[frame.Bridge.ProductID()] {
		return true
	}
	number, ok := deviceNumber(frame)
	if !ok {
		// Which of the two devices this is cannot be settled. Claiming the
		// slot and finding it empty is recoverable; claiming it nowhere would
		// put a slot that is physically present out of reach.
		return true
	}
	return number == 1
}

// deviceNumber reads the digit that numbers this device within its enclosure.
func deviceNumber(frame *plex.Frame) (int, bool) {
	serial, err := frame.Bridge.Serial()
	if err != nil {
		return 0, false
	}
	return parseDeviceNumber(serial)
}

// parseDeviceNumber takes that digit out of a serial number, rejecting
// anything that is not one of these enclosures' own.
func parseDeviceNumber(serial string) (int, bool) {
	if len(serial) != serialDigits {
		return 0, false
	}
	for _, digit := range serial {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	number := int(serial[serialDigits-1] - '0')
	// Only two devices make up an enclosure, so nothing else is meaningful.
	if number != 1 && number != 2 {
		return 0, false
	}
	return number, true
}

// powerOn initialises every tuner IC once the supply is up, parking the ones
// nobody asked for.
func powerOn(fronts [tunerCount]*frontend) error {
	for _, front := range fronts {
		switch front.kind {
		case kindTerrestrial:
			if err := front.r850.Initialize(); err != nil {
				return fmt.Errorf("initialize R850 %d: %w", front.index, err)
			}
		case kindSatellite:
			if err := front.rt710.Initialize(); err != nil {
				return fmt.Errorf("initialize RT710 %d: %w", front.index, err)
			}
		}
	}
	for _, front := range fronts {
		if err := front.Park(); err != nil {
			return err
		}
	}
	// The first demodulator of each kind carries the shared clock.
	if err := fronts[0].demod.WritePairs(initSatelliteMaster); err != nil {
		return err
	}
	return fronts[2].demod.WritePairs(initTerrestrialMaster)
}

// frontend is one of the board's four tuners, each fixed to the system its
// silicon was fitted for.
type frontend struct {
	index int
	kind  tunerKind
	demod *tc90522.Demod
	r850  *r850.Tuner
	rt710 *rt710.Tuner

	tuned bool
	// lnb is this tuner's claim on the shared LNB supply.
	lnb plex.LNBClaim
}

// GainLadder is the RT710's fine gain trim, which only the satellite tuners
// have: the R850's gain comes from per-band tables chosen by frequency, with
// no way to move it.
func (front *frontend) GainLadder(string) tuning.GainLadder {
	if front.kind != kindSatellite || front.rt710 == nil {
		return tuning.GainLadder{}
	}
	return tuning.NewGainLadder(front.rt710.FineGainStepsDB()...)
}

// SetGain moves the trim. Every board in this tree comes up at the top of it,
// so turning it down is the setting's reason to exist.
func (front *frontend) SetGain(step int) error {
	if front.kind != kindSatellite || front.rt710 == nil {
		return fmt.Errorf("this board's terrestrial tuner has no gain setting to change")
	}
	return front.rt710.SetFineGain(step)
}

// start wakes one tuner and loads the register set its system needs.
func (front *frontend) start() error {
	if front.kind == kindTerrestrial {
		if err := front.demod.WritePairs(initTerrestrial); err != nil {
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
		return front.r850.SetSystem(r850.ISDBTSystemConfig)
	}
	if err := front.demod.WritePairs(initSatellite); err != nil {
		return err
	}
	if err := front.demod.EnableTSPinsS(false); err != nil {
		return err
	}
	return front.demod.SleepS(false)
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	wanted := "ISDBT"
	if front.kind == kindSatellite {
		wanted = "ISDBS"
	}
	if delivery != wanted {
		return fmt.Errorf("PX4 tuner %d does not support %q", front.index, properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	frequency = tuning.FrequencyKHz(frequency)
	if front.kind == kindSatellite {
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
	} else if err := front.tuneTerrestrial(uint32(frequency)); err != nil {
		return err
	}
	front.tuned = true
	return nil
}

// tuneTerrestrial runs the ISDB-T acquisition sequence.
func (front *frontend) tuneTerrestrial(frequency uint32) error {
	demod := front.demod
	if err := demod.WriteRegister(0x47, 0x30); err != nil {
		return err
	}
	// The gain control is held off while the synthesiser moves.
	if err := demod.SetAGCT(false); err != nil {
		return err
	}
	if err := demod.WriteRegister(0x76, 0x0c); err != nil {
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
	return demod.WritePairs([]byte{0x71, 0x21, 0x72, 0x25, 0x75, 0x08})
}

// tuneSatellite runs the ISDB-S acquisition sequence and selects the stream.
// One GPIO gates the supply for the whole enclosure, so there is no voltage to
// choose: a channel asking for none is honoured by not claiming it.
func (front *frontend) tuneSatellite(frequency uint32, streamID uint16, supply bool) error {
	demod := front.demod
	if err := front.lnb.Want(supply); err != nil {
		return err
	}
	if err := demod.SetAGCS(false); err != nil {
		return err
	}
	if err := demod.WriteRegister(0x8e, 0x06); err != nil {
		return err
	}
	if err := demod.WriteRegister(0xa3, 0xf7); err != nil {
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
	if front.kind == kindSatellite {
		locked, err = front.demod.LockedS()
	} else {
		locked, err = front.demod.LockedT()
	}
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: locked}
	if locked {
		status.Names = device.LockNames
		cnr, measured, cnrErr := front.demod.CNRT()
		if front.kind == kindSatellite {
			cnr, measured, cnrErr = front.demod.CNRS()
		}
		if cnrErr == nil && measured {
			status.CNR = device.Measured(cnr)
		}
	}
	// The level comes from the tuner rather than the demodulator, so it is
	// worth having even on a channel that never locks: it separates an empty
	// frequency from a strong signal that will not decode.
	if front.kind == kindSatellite {
		if strength, strengthErr := front.rt710.SignalStrength(); strengthErr == nil {
			status.Strength = device.Measured(float64(strength) / 1000)
		}
	}
	return status, nil
}

// EnableTS switches this tuner's demodulator TS output.
func (front *frontend) EnableTS(enable bool) error {
	if front.kind == kindSatellite {
		return front.demod.EnableTSPinsS(enable)
	}
	return front.demod.EnableTSPinsT(enable)
}

// Park powers the tuner and its demodulator core down, and drops the claim on
// the LNB supply.
func (front *frontend) Park() error {
	var err error
	if front.kind == kindTerrestrial {
		_ = front.r850.Sleep()
		err = front.demod.SleepT(true)
	} else {
		_ = front.rt710.Sleep()
		err = front.demod.SleepS(true)
	}
	return front.lnb.Release(err)
}
