// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package px3 drives PLEX ASV5211/ASV5212 USB tuners. Each enumerated bridge
// exposes one ISDB-S and one ISDB-T tuner; a four-tuner enclosure appears as
// two USB devices.
//
// Model-specific connections and register settings are described by wiring.
//
// The two tuners are independent down to their stream endpoints, so unlike the
// US-3POUT both can be tuned and streamed at once. They contend only for the
// LNB supply, which the satellite side holds while it is tuned.
package px3

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/trace"
	"github.com/Till0196/coax/internal/coaxd/tuner/fc0012"
	"github.com/Till0196/coax/internal/coaxd/tuner/nm120"
	"github.com/Till0196/coax/internal/coaxd/tuner/tda20142"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "px3",
	Aliases:     []string{"px-w3u3", "pxw3u3v2", "px-w3u2", "pxs3u", "px-s3u", "pxs3u2", "px-s3u2"},
	Description: "PLEX PX-S3U/S3U2/W3U2/W3U3/W3U3 V2 (ASV52xx + TC90522 + NM120/TDA20142)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	ColdIDs:     coldIDs,
	Boot:        boot,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's two tuners: one satellite, one terrestrial. Every product
// this backend claims carries both.
func Layout(id usbio.ID) []device.TunerInfo {
	wired := wiringFor(id.Product)
	// The supply is the enclosure's one line, in or out. The gain is the V2's
	// external amplifiers (in or out, both tuners), or on the FC0012 boards the
	// FC0012's ladder on the terrestrial tuner (frontend.GainLadder).
	satelliteControls := device.Controls{LNB: &device.LNBControl{Off: true, Shared: "board"}}
	terrestrialControls := device.Controls{}
	if wired.externalAmplifier != nil {
		satelliteControls.Gain = &device.GainControl{}
		terrestrialControls.Gain = &device.GainControl{}
	} else {
		terrestrialControls.Gain = &device.GainControl{Decibels: fc0012.GainStepsDB()}
	}
	return []device.TunerInfo{
		{Index: satellite, Name: wired.satelliteName, Deliveries: []string{"ISDB-S"}, Controls: satelliteControls},
		{Index: terrestrial, Name: wired.terrestrialName, Deliveries: []string{"ISDB-T"}, Controls: terrestrialControls},
	}
}

// deliveries is what the two tuners reach between them.
var deliveries = []string{"ISDB-T", "ISDB-S"}

func Supports(deliverySystem string) bool {
	switch tuning.Normalize(deliverySystem) {
	case "ISDBT", "ISDBS":
		return true
	default:
		return false
	}
}

// lnbSupplyMask is the bridge GPIO line that feeds the aerial lead.
const lnbSupplyMask = 0x20

// The board's two tuners.
const (
	satellite   = 0
	terrestrial = 1
)

// wiring describes the tuner connections and board settings of each generation.
type wiring struct {
	// demodTerrestrial and demodSatellite are the bus targets the two TC90522 cores
	// answer on: 0x30 and 0x32 on the FC0012 boards, 0x20 and 0x22 on the V2.
	demodTerrestrial, demodSatellite byte
	// newTerrestrial builds the ISDB-T tuner: an FC0012 at 0xc6 with eight-bit
	// registers on the FC0012 boards, a Newport Media part at 0xce with sixteen-bit
	// ones on the V2.
	newTerrestrial func(bus i2c.Bus, demod byte) (terrestrialTuner, error)
	// newSatellite builds the ISDB-S tuner on the demodulator's tuner bus: a
	// TDA20142 on the V2, a table-driven part on the FC0012 boards (satellite.go).
	newSatellite  func(bus i2c.Bus) (satelliteTuner, error)
	terrestrialB8 byte
	// beforeRF and afterRF configure the demodulators around RF initialization.
	beforeRF, afterRF []demodSetup
	// afterTune configures each source around AGC restart after a successful tune.
	afterTune [2]demodTune
	// lnbSupplyOn is what to put on GPIO bit 0x20 to feed the aerial lead. The two
	// generations are opposite: zero on the FC0012 boards, the bit itself on the V2.
	lnbSupplyOn byte
	// externalAmplifier is the board-level amplifier lines. Only the V2 has a pair.
	externalAmplifier *amplifierLines
	// terrestrialName and satelliteName are what a listing calls the two tuners.
	terrestrialName, satelliteName string
}

type demodSetup struct {
	index   int
	address byte // Zero uses the indexed demodulator's address.
	pairs   []byte
}

type demodTune struct {
	beforeAGC, afterAGC []demodSetup
}

// amplifierLines is how a generation switches its board-level amplifiers: a
// masked write to one of the bridge's two GPIO ports. The terrestrial line is
// active low and the satellite one is two bits wide, which is why neither is a
// plain boolean.
type amplifierLines struct {
	// terrestrialMask and terrestrialOn are the main GPIO port's line.
	terrestrialMask, terrestrialOn byte
	// satelliteMask and satelliteOn are the extended port's.
	satelliteMask, satelliteOn byte
}

// v2Amplifiers is the PX-W3U3 V2's. Both are meant to come up switched on.
var v2Amplifiers = amplifierLines{
	terrestrialMask: 0x01, terrestrialOn: 0x00,
	satelliteMask: 0x03, satelliteOn: 0x03,
}

type terrestrialTuner interface {
	Tune(frequencyHz uint32) error
}

type satelliteTuner interface {
	Tune(frequencyKHz uint64) error
}

// amplifier is a tuner whose gain can be moved. Only the FC0012
// has one this program can drive, so a channel asking any other tuner
// for gain is told so. What is known of the V2's gain settings is in px3.md.
type amplifier interface {
	SetLNAGain(step int) error
}

// isdbtBandwidthHz is the width of every channel this board receives. The FC0012
// takes it because the chip encodes it; ISDB-T in Japan has one width.
const isdbtBandwidthHz = 6_000_000

// fc0012Tuner fixes the channel width, leaving the one-argument shape the board
// uses.
type fc0012Tuner struct{ *fc0012.Tuner }

func (tuner fc0012Tuner) Tune(frequencyHz uint32) error {
	return tuner.Tuner.Tune(frequencyHz, isdbtBandwidthHz)
}

func newFC0012(bus i2c.Bus, demod byte) (terrestrialTuner, error) {
	tuner := fc0012.New(bus, demod, fc0012.Config{})
	if err := tuner.Initialize(); err != nil {
		return nil, err
	}
	return fc0012Tuner{tuner}, nil
}

func newNM120(bus i2c.Bus, demod byte) (terrestrialTuner, error) {
	tuner := nm120.New(bus, demod)
	if err := tuner.Initialize(); err != nil {
		return nil, err
	}
	return tuner, nil
}

// newTableSatellite is the FC0012 boards': nothing to initialize beyond the
// demodulator.
func newTableSatellite(bus i2c.Bus) (satelliteTuner, error) {
	return tableSatelliteTuner{tuner: newSatelliteTuner(bus)}, nil
}

func newTDA20142(bus i2c.Bus) (satelliteTuner, error) {
	return tda20142.New(bus)
}

// fc0012Wiring is the PX-S3U, S3U2, W3U2 and W3U3.
var fc0012Wiring = wiring{
	demodTerrestrial: 0x30,
	demodSatellite:   0x32,
	newTerrestrial:   newFC0012,
	newSatellite:     newTableSatellite,
	terrestrialB8:    0xff,
	afterRF: []demodSetup{
		{index: satellite, pairs: []byte{0x15, 0, 0x1d, 0, 0x04, 0x02, 0x07, 0x31, 0x08, 0x77}},
		{index: terrestrial, pairs: []byte{0x0f, 0x34}},
	},
	afterTune: [2]demodTune{
		satellite: {
			beforeAGC: []demodSetup{{index: terrestrial, pairs: []byte{0x0f, 0x34}}},
		},
		terrestrial: {
			beforeAGC: []demodSetup{{index: terrestrial, pairs: []byte{0x0f, 0x34}}},
			afterAGC:  []demodSetup{{index: terrestrial, pairs: []byte{0x71, 0x21, 0x72, 0x25, 0x75, 0x08}}},
		},
	},
	lnbSupplyOn:     0x00,
	terrestrialName: "FC0012",
	satelliteName:   "QM1D1B0004",
}

// v2Wiring is the PX-W3U3 V2's first pair. Its demodulators sit at
// ((unit & 7) + 0x10) * 2, the NM120 behind the even unit and the TDA20142
// behind the odd one, so the terrestrial side is unit zero; the second pair is
// units two and three. See v2WiringAt.
var v2Wiring = wiring{
	demodTerrestrial: 0x20,
	demodSatellite:   0x22,
	newTerrestrial:   newNM120,
	newSatellite:     newTDA20142,
	terrestrialB8:    0xc0,
	beforeRF: []demodSetup{
		{index: terrestrial, pairs: []byte{0x03, 0, 0x1d, 0, 0x1f, 0, 0x0e, 0x77, 0x0f, 0x13, 0x75, 0x02}},
	},
	afterRF: []demodSetup{
		{index: terrestrial, pairs: []byte{0x0e, 0x77, 0x0f, 0x13}},
		{index: satellite, pairs: []byte{0x13, 0, 0x15, 0, 0x17, 0, 0x1c, 0, 0x1d, 0, 0x1f, 0, 0x07, 0x31, 0x08, 0x77, 0x04, 0x02}},
	},
	afterTune: [2]demodTune{
		terrestrial: {
			afterAGC: []demodSetup{{index: terrestrial, pairs: []byte{0x71, 0x01, 0x72, 0x24, 0x75, 0x02}}},
		},
	},
	lnbSupplyOn:       lnbSupplyMask,
	externalAmplifier: &v2Amplifiers,
	terrestrialName:   "NM120",
	satelliteName:     "TDA20142",
}

// v2Product is the PX-W3U3 V2's product ID.
const v2Product = 0x0006

// v2WiringAt is the V2's wiring for the pair a bridge at a position streams:
// units 2p and 2p+1, reached through the controlling bridge.
func v2WiringAt(at int) wiring {
	wired := v2Wiring
	unit := byte(2 * at)
	wired.demodTerrestrial = ((unit & 7) + 0x10) * 2
	wired.demodSatellite = (((unit + 1) & 7) + 0x10) * 2
	return wired
}

func wiringFor(product uint16) wiring {
	if product == v2Product {
		return v2Wiring
	}
	wired := fc0012Wiring
	if product == 0x0001 {
		wired.afterRF = wired.afterRF[:1]
		wired.afterTune[terrestrial].beforeAGC = []demodSetup{{index: terrestrial, pairs: []byte{0x0f, 0x14}}}
		wired.afterTune[satellite].beforeAGC = []demodSetup{{index: terrestrial, address: 0x34, pairs: []byte{0x0f, 0x3c}}}
	}
	return wired
}

var products = map[uint16]string{
	0x0001: "PX-S3U",
	0x0003: "PX-S3U2",
	0x0004: "PX-W3U2",
	0x0005: "PX-W3U3",
	0x0006: "PX-W3U3 V2",
}

var supportedIDs = usbio.IDs(0x0b06, products)

// open claims an ASV52xx board and brings both front ends up.
func open(options device.Options) (device.Board, error) {
	own, err := acquireBridge(options.Selector, 0)
	if err != nil {
		// Nothing is booted, so a board may still be sitting in its boot ROM under the
		// bridge vendor's own identifier.
		if bootErr := bootColdBoards(options); bootErr != nil {
			if errors.Is(bootErr, errNoColdBoard) {
				return nil, err
			}
			return nil, bootErr
		}
		if own, err = acquireBridge(options.Selector, enumerationTimeout); err != nil {
			return nil, fmt.Errorf("the ASV52xx firmware was uploaded but no board came back: %w", err)
		}
	}
	bridge := own.bridge
	releases := []func() error{own.release}
	release := func() error {
		var first error
		for index := len(releases) - 1; index >= 0; index-- {
			if err := releases[index](); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	fail := func(operation string, cause error) (device.Board, error) {
		_ = release()
		return nil, fmt.Errorf("%s: %w", operation, cause)
	}
	product := bridge.ProductID()
	trace.Printf("px3 %s: opening, product %#04x", bridge.Name(), product)
	model, known := products[product]
	if !known {
		return fail("open", fmt.Errorf("unsupported product ID %#04x", product))
	}

	shared, at, err := openEnclosure(own)
	if err != nil {
		return fail("open PLEX "+model, err)
	}
	releases = append(releases, shared.release)
	pair := shared.pairs[at%2]
	ends := pair.frontEnds
	hardware := &hardware{
		bridge: bridge, controller: pair.controller, frontEnds: ends,
		v2: product == v2Product, at: at,
	}
	if err := hardware.restoreExternalAmplifiers(terrestrial, satellite); err != nil {
		return fail("restore the amplifier settings", err)
	}
	// Leave both TS outputs off until a tuner actually streams.
	_ = hardware.demodT.EnableTSPinsT(false)
	_ = hardware.demodS.EnableTSPinsS(false)
	// The card interface ICs come up here, satellite first, rather than as a
	// stream starts. A missing one does not stop the open: the stream start
	// looks again.
	for _, index := range []int{satellite, terrestrial} {
		if err := hardware.initTSChip(index); err != nil {
			trace.Printf("px3 %s: bring up the card interface for tuner %d: %v", bridge.Name(), index, err)
		}
	}

	inner, err := boardkit.New(boardkit.Config{
		Model:    "PLEX " + model,
		Location: bridge.Name(),
		Tuners:   Layout(usbio.ID{Vendor: 0x0b06, Product: product}),
		Release:  release,
		Open: func(index int) (boardkit.Tuner, error) {
			return &frontend{hardware: hardware, index: index}, nil
		},
	})
	if err != nil {
		return fail("open", err)
	}
	return &board{Board: inner, hardware: hardware}, nil
}

// board is the boardkit handle with the enclosure's card reader alongside it.
// One slot serves however many tuners the enclosure carries, so the reader is
// board-level.
type board struct {
	device.Board
	hardware *hardware
}

// hardware is the silicon on one ASV52xx board, shared by its two tuners.
type hardware struct {
	*controller
	frontEnds
	// bridge is the one this board's streams arrive on.
	bridge *asv5211.Bridge
	// v2 marks a PX-W3U3 V2, whose channel filters are set up a little
	// differently.
	v2 bool
	// at is a V2 bridge's position in its enclosure.
	at int
}

// setExternalAmplifier switches one of the board-level amplifiers, and does
// nothing on the generation without them.
func (hardware *hardware) setExternalAmplifier(index int, on bool) error {
	lines := hardware.wired.externalAmplifier
	if lines == nil {
		return nil
	}
	hardware.amplifiers.Lock()
	defer hardware.amplifiers.Unlock()
	if err := hardware.writeExternalAmplifier(index, on); err != nil {
		return err
	}
	hardware.amplifiers.on[index] = on
	return nil
}

func (hardware *hardware) restoreExternalAmplifiers(indices ...int) error {
	if hardware.wired.externalAmplifier == nil {
		return nil
	}
	hardware.amplifiers.Lock()
	defer hardware.amplifiers.Unlock()
	for _, index := range indices {
		if err := hardware.writeExternalAmplifier(index, hardware.amplifiers.on[index]); err != nil {
			return err
		}
	}
	return nil
}

func (hardware *hardware) writeExternalAmplifier(index int, on bool) error {
	lines := hardware.wired.externalAmplifier
	extended, value, mask := lines.request(index, on)
	if extended {
		return hardware.control.SetGPIOEx(value, mask)
	}
	return hardware.control.SetGPIO(value, mask)
}

// request is the masked write one of those switches comes down to. The off
// value is derived from the on value rather than carried, so that the
// terrestrial line being active low and the satellite one not cannot be written
// down inconsistently.
func (lines *amplifierLines) request(index int, on bool) (extended bool, value, mask byte) {
	extended = index == satellite
	mask, value = lines.terrestrialMask, lines.terrestrialOn
	if extended {
		mask, value = lines.satelliteMask, lines.satelliteOn
	}
	if !on {
		value = ^value & mask
	}
	return extended, value, mask
}

// frontend is one of the board's two tuners.
type frontend struct {
	hardware *hardware
	index    int
	tuned    bool
	// lnbHeld records whether this tuner is holding the LNB supply up.
	lnbHeld bool
	// streamed and streamStarted are for the log: how much a stream carried, and
	// since when.
	streamed      int64
	streamStarted time.Time
	// sync puts back the sync bytes the bridge marks.
	sync packetSync
}

// name is how the log calls this tuner.
func (front *frontend) name() string {
	return fmt.Sprintf("px3 %s %s", front.hardware.bridge.Name(), front.what())
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	wanted := "ISDBS"
	if front.index == terrestrial {
		wanted = "ISDBT"
	}
	if delivery != wanted {
		return fmt.Errorf("ASV52xx tuner %d does not support %q", front.index, properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	// Put the amplifier back where it was asked to be: the card interface's reset
	// pulses the same GPIO line.
	if err := front.hardware.restoreExternalAmplifiers(front.index); err != nil {
		return err
	}
	if front.index == terrestrial {
		err = front.tuneTerrestrial(frequency)
	} else {
		err = front.tuneSatellite(frequency, properties)
	}
	if err != nil {
		trace.Printf("%s: tune %d: %v", front.name(), frequency, err)
		return err
	}
	trace.Printf("%s: tuned %d", front.name(), frequency)
	front.tuned = true
	return nil
}

// GainLadder is what this tuner's gain can be set to.
//
// The FC0012 boards' terrestrial FC0012 has a twenty-four step ladder with
// published figures, and their satellite side has nothing. The V2 has a
// board-level amplifier on each input with no figures for what it is worth, so
// it is a switch. Its other settings are not offered; px3.md sets out what each
// does.
func (front *frontend) GainLadder(string) tuning.GainLadder {
	if front.hardware.wired.externalAmplifier != nil {
		return tuning.NewGainSwitch()
	}
	if front.index != terrestrial {
		return tuning.GainLadder{}
	}
	if _, ok := front.hardware.tunerT.(amplifier); !ok {
		return tuning.GainLadder{}
	}
	return tuning.NewGainLadder(fc0012.GainStepsDB()...)
}

// SetGain moves whichever of those this board has. Both come up at the top, so
// the setting exists to turn one down for an aerial strong enough to overload
// the front end.
func (front *frontend) SetGain(step int) error {
	if front.hardware.wired.externalAmplifier != nil {
		return front.hardware.setExternalAmplifier(front.index, step > 0)
	}
	tuner, ok := front.hardware.tunerT.(amplifier)
	if !ok || front.index != terrestrial {
		return fmt.Errorf("this board's %s tuner has no gain setting to change", front.what())
	}
	return tuner.SetLNAGain(step)
}

// what names this tuner for an error message.
func (front *frontend) what() string {
	if front.index == terrestrial {
		return "terrestrial"
	}
	return "satellite"
}

// tuneTerrestrial moves the terrestrial tuner, holding the demodulator's AGC
// still while the synthesiser settles.
func (front *frontend) tuneTerrestrial(frequency uint64) error {
	// Channel files carry Hz for terrestrial, and so does the tuner, but the kHz
	// spelling is small enough to tell apart.
	if frequency < 10_000_000 {
		frequency *= 1000
	}
	demod := front.hardware.demodT
	if err := demod.SetAGCT(false); err != nil {
		return err
	}
	if err := front.hardware.tunerT.Tune(uint32(frequency)); err != nil {
		return fmt.Errorf("tune the terrestrial tuner: %w", err)
	}
	setup := front.hardware.wired.afterTune[terrestrial]
	if err := front.hardware.configureDemods(front.hardware.control, setup.beforeAGC); err != nil {
		return err
	}
	if err := demod.SetAGCT(true); err != nil {
		return err
	}
	return front.hardware.configureDemods(front.hardware.control, setup.afterAGC)
}

// tuneSatellite moves the satellite tuner and picks the relative stream out of
// the multiplexed carrier, raising the LNB supply first if this tuner has not
// claimed it.
func (front *frontend) tuneSatellite(frequency uint64, properties map[string]string) error {
	frequency = tuning.FrequencyKHz(frequency)
	// A channel naming no stream gets relative slot 0. Refusing one broke a client walking the transponders by
	// frequency to read their network tables, which is what channel-files.md
	// allows; leaving the demodulator alone instead would keep whichever stream
	// the last tune picked.
	streamID, err := tuning.StreamID(properties)
	if err != nil {
		return err
	}
	// The enclosure gates one supply for both satellite tuners, so there is no
	// voltage to choose: a channel asking for none is honoured by not claiming it.
	feed, err := tuning.SwitchedSupply(properties)
	if err != nil {
		return err
	}
	if err := front.wantLNB(feed); err != nil {
		return err
	}
	demod := front.hardware.demodS
	if err := demod.SetAGCS(false); err != nil {
		return err
	}
	if err := front.hardware.tunerS.Tune(frequency); err != nil {
		return fmt.Errorf("tune the satellite tuner: %w", err)
	}
	setup := front.hardware.wired.afterTune[satellite]
	if err := front.hardware.configureDemods(front.hardware.control, setup.beforeAGC); err != nil {
		return err
	}
	if err := demod.SetAGCS(true); err != nil {
		return err
	}
	if err := front.hardware.configureDemods(front.hardware.control, setup.afterAGC); err != nil {
		return err
	}
	return demod.SelectStreamS(streamID)
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if !front.tuned {
		return device.Status{}, nil
	}
	// Each branch asks only its own demodulator: polling both would spend two I2C
	// transactions across the bridge on an answer that is thrown away.
	var locked bool
	var err error
	if front.index == terrestrial {
		locked, err = front.hardware.demodT.LockedT()
	} else {
		locked, err = front.hardware.demodS.LockedS()
	}
	if err != nil || !locked {
		return device.Status{}, err
	}
	status := device.Status{Locked: true, Names: device.LockNames}
	var cnr float64
	var measured bool
	var cnrErr error
	if front.index == terrestrial {
		cnr, measured, cnrErr = front.hardware.demodT.CNRT()
	} else {
		cnr, measured, cnrErr = front.hardware.demodS.CNRS()
	}
	if cnrErr == nil && measured {
		status.CNR = device.Measured(cnr)
	}
	return status, nil
}

// StartStream switches the demodulator's TS pins on and opens this tuner's
// endpoint.
func (front *frontend) StartStream() (err error) {
	defer func() {
		if err != nil {
			_ = front.StopStream()
		}
	}()
	if err := front.enableTSPins(true); err != nil {
		return err
	}
	if err := front.hardware.prepareTSChip(front.index); err != nil {
		trace.Printf("%s: %v", front.name(), err)
		return err
	}
	// The channel is reset with mode 1 on every tune -- bit 0 of the
	// demodulator bring-up's answer, 0xe9 -- which sets bit 2 of the filter's
	// control register.
	if err := front.hardware.bridge.ResetFilter(front.index, 1); err != nil {
		trace.Printf("%s: %v", front.name(), err)
		return err
	}
	if front.hardware.v2 {
		if err := front.hardware.routeSlowdown(front.index); err != nil {
			trace.Printf("%s: route the TS through the rate converter: %v", front.name(), err)
			return err
		}
		// Registers 0x41 and 0x43 of a channel filter take 0x1fff as it starts.
		for _, register := range []byte{0x41, 0x43} {
			if err := front.hardware.bridge.SetFilterWord(front.index, register, 0x1fff); err != nil {
				return err
			}
		}
	}
	// The filter is taken in and set running before the transfers are queued.
	// Queueing first and setting the run bit last, together with a channel
	// reset, carried nothing at all, and which of the two did it is not yet
	// known; this order is the one a stream has been seen to flow in.
	if err := front.hardware.bridge.SetFilterInput(front.index, true); err != nil {
		return err
	}
	if err := front.hardware.bridge.SetFilterRun(front.index, true); err != nil {
		return err
	}
	if err := front.hardware.bridge.StartStream(front.index); err != nil {
		trace.Printf("%s: start the stream on channel %d: %v", front.name(), front.index, err)
		return err
	}
	front.sync = packetSync{}
	front.streamed, front.streamStarted = 0, time.Now()
	trace.Printf("%s: stream started on channel %d", front.name(), front.index)
	return nil
}

// slowdownAddress is where a V2 may carry a TS rate converter between the
// demodulators and the bridge.
const slowdownAddress = 0xd2

// routeSlowdown points the rate converter's output for a channel at that
// channel's demodulator, after every tune: input pin (the tuner's index) in
// the top nibble of the output pin's register, and the sync byte three
// registers on. It writes the register twice, without and then with the low
// two bits. A board without the converter, told by register 0 not reading 1,
// is left alone.
func (hardware *hardware) routeSlowdown(index int) error {
	hardware.slowdown.Lock()
	defer hardware.slowdown.Unlock()
	bus := hardware.control.I2C(slowdownAddress)
	if !hardware.slowdown.probed {
		hardware.slowdown.probed = true
		version, err := i2c.WriteRead(bus, slowdownAddress, []byte{0}, 1)
		hardware.slowdown.present = err == nil && len(version) == 1 && version[0] == 1
		trace.Printf("px3 %s: rate converter at %#02x: present %v (read % x, %v)", hardware.bridge.Name(),
			slowdownAddress, hardware.slowdown.present, version, err)
	}
	if !hardware.slowdown.present {
		return nil
	}
	input, output := byte(index), byte(index+1)
	for _, low := range []byte{0, 0x03} {
		if err := bus.Write(slowdownAddress, []byte{output, input<<4 | low}); err != nil {
			return err
		}
		if err := bus.Write(slowdownAddress, []byte{output + 3, 0x47}); err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// StopStream unwinds StartStream, reporting the first failure but always
// finishing.
func (front *frontend) StopStream() error {
	if elapsed := time.Since(front.streamStarted); !front.streamStarted.IsZero() {
		trace.Printf("%s: stream stopped after %d bytes in %.1fs", front.name(), front.streamed, elapsed.Seconds())
		front.streamStarted = time.Time{}
	}
	err := front.hardware.bridge.SetFilterRun(front.index, false)
	if stop := front.hardware.bridge.StopStream(front.index); err == nil {
		err = stop
	}
	if pins := front.enableTSPins(false); err == nil {
		err = pins
	}
	return err
}

// enableTSPins switches this tuner's demodulator TS output on or off.
func (front *frontend) enableTSPins(enable bool) error {
	if front.index == terrestrial {
		return front.hardware.demodT.EnableTSPinsT(enable)
	}
	return front.hardware.demodS.EnableTSPinsS(enable)
}

// Read takes the next packets off this tuner's endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	read, err := front.hardware.bridge.ReadStream(front.index, buffer, 200)
	front.streamed += int64(read)
	front.sync.restore(buffer[:read])
	return boardkit.WouldBlock(read, err)
}

// wantLNB takes or drops this tuner's claim to match what the channel asked
// for. The supply is shared, so a tuner that does not want it lets go rather
// than switching it off under the other one.
func (front *frontend) wantLNB(supply bool) error {
	if supply == front.lnbHeld {
		return nil
	}
	trace.Printf("%s: LNB supply claim %v", front.name(), supply)
	if supply {
		if err := front.hardware.lnb.Acquire(); err != nil {
			return err
		}
		front.lnbHeld = true
		return nil
	}
	front.lnbHeld = false
	return front.hardware.lnb.Release()
}

// Release drops this tuner's claim on the LNB supply.
func (front *frontend) Release() error { return front.wantLNB(false) }
