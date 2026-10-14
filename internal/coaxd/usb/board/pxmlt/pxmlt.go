// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (pxmlt_device.c) by nns779.

// Package pxmlt drives the PLEX PX-MLT5U, PX-MLT5PE, PX-MLT8PE, the DigiBest
// ISDB6014 and the e-Better DTV02A-5TS-P.
//
// An IT930x bridge feeds three to five identical tuners, each a CXD2856ER
// demodulator with a CXD2858ER tuner behind its I2C gate. Every tuner can do
// either ISDB-T or ISDB-S, switched per tune. Tuners sharing a bridge I2C bus
// take turns on it, their tuners answering at the same address.
package pxmlt

import (
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/demod/cxd2856er"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/cxd2858er"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/board/plex"
	"github.com/Till0196/coax/internal/coaxd/usb/tsdemux"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "pxmlt",
	Aliases:     []string{"pxmlt5", "pxmlt8", "isdb6014"},
	Description: "PLEX PX-MLT5U/MLT5PE/MLT8PE, DigiBest ISDB6014, e-Better DTV02A-5TS-P (IT930x + CXD2856ER + CXD2858ER)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the tuner arrangement a product carries, which follows from its USB
// product number. open builds its tuners from the same table, so the two cannot
// describe the board differently. An unrecognised product answers with nothing.
func Layout(id usbio.ID) []device.TunerInfo {
	layout, ok := layouts[models[id.Product]]
	if !ok {
		return nil
	}
	descriptors := make([]device.TunerInfo, 0, len(layout))
	for index := range layout {
		descriptors = append(descriptors, device.TunerInfo{
			Index:      index,
			Name:       "CXD2858ER",
			Deliveries: deliveries,
			// The board's one supply line, in or out, and an amplifier in or out of
			// circuit on every input.
			Controls: device.Controls{
				LNB:  &device.LNBControl{Off: true, Shared: "board"},
				Gain: &device.GainControl{},
			},
		})
	}
	return descriptors
}

// deliveries is what every tuner of these boards can reach.
//
// The cable entries are J.83 Annex A and Annex B: Japan's Annex C differs from
// Annex A only in roll-off and the CXD2856ER has no separate Annex C core, so a
// channel file naming ISDB-C is locked as Annex A. Annex B is the trellis-coded
// core. The 8 MHz Annex A channel is absent because the chip carries one J.83
// filter and it is the 6 MHz one.
var deliveries = []string{
	"ISDB-T", "ISDB-S", "DVB-T", "DVB-T2", "DVB-S", "DVB-S2",
	"ISDB-C", "DVB-C/ANNEX_A", "DVB-C/ANNEX_B",
}

// systems maps a channel file's delivery system onto the sony.System the chips
// are told to receive.
var systems = map[string]sony.System{
	"ISDBT": sony.SystemISDBT,
	"ISDBS": sony.SystemISDBS,
	"DVBT":  sony.SystemDVBT,
	"DVBT2": sony.SystemDVBT2,
	"DVBS":  sony.SystemDVBS,
	"DVBS2": sony.SystemDVBS2,

	// Every spelling of J.83 Annex A and Annex C reaches the one cable mode the
	// chips have. The list is tuning.IsJ83AnnexAC's, written out because this
	// map is what Supports answers from.
	"ISDBC":       sony.SystemDVBC,
	"DVBC":        sony.SystemDVBC,
	"DVBC/ANNEXA": sony.SystemDVBC,
	"DVBC/ANNEXC": sony.SystemDVBC,
	"DVBC/ANNEXB": sony.SystemJ83B,
}

// model identifies which arrangement of tuners a product fits.
type model int

const (
	modelMLT5U model = iota
	modelMLT5PE
	modelMLT8PE3
	modelMLT8PE5
	modelISDB6014
)

// models maps each USB product ID to its layout.
var models = map[uint16]model{
	0x084e: modelMLT5U,    // PX-MLT5U
	0x024e: modelMLT5PE,   // PX-MLT5PE
	0x0252: modelMLT8PE3,  // PX-MLT8PE, first half
	0x0253: modelMLT8PE5,  // PX-MLT8PE, second half
	0x0254: modelISDB6014, // DigiBest ISDB6014 V2.0 (4TS)
	0x924e: modelMLT5PE,   // e-Better DTV02A-5TS-P, a PX-MLT5PE under another product ID
}

var supportedIDs = usbio.IDs(0x0511, models)

// layouts gives each model's inputs in board order: the demodulator's 7-bit
// address, the bridge I2C bus it sits on, and the bridge's port number.
var layouts = map[model][]plex.InputSpec{
	modelMLT5U: {
		{Address: 0x65, Bus: 3, Port: 4}, {Address: 0x6c, Bus: 1, Port: 3},
		{Address: 0x64, Bus: 1, Port: 1}, {Address: 0x6c, Bus: 3, Port: 2},
		{Address: 0x64, Bus: 3, Port: 0},
	},
	modelMLT5PE: {
		{Address: 0x65, Bus: 3, Port: 0}, {Address: 0x6c, Bus: 1, Port: 1},
		{Address: 0x64, Bus: 1, Port: 2}, {Address: 0x6c, Bus: 3, Port: 3},
		{Address: 0x64, Bus: 3, Port: 4},
	},
	modelMLT8PE3: {
		{Address: 0x65, Bus: 3, Port: 0}, {Address: 0x6c, Bus: 3, Port: 3},
		{Address: 0x64, Bus: 3, Port: 4},
	},
	modelMLT8PE5: {
		{Address: 0x65, Bus: 1, Port: 0}, {Address: 0x64, Bus: 1, Port: 1},
		{Address: 0x6c, Bus: 1, Port: 2}, {Address: 0x6c, Bus: 3, Port: 3},
		{Address: 0x64, Bus: 3, Port: 4},
	},
	modelISDB6014: {
		{Address: 0x65, Bus: 3, Port: 0}, {Address: 0x6c, Bus: 1, Port: 1},
		{Address: 0x64, Bus: 1, Port: 2}, {Address: 0x64, Bus: 3, Port: 4},
	},
}

// unusedPorts names the ports of the inputs a model does not fit, which still
// have to be switched off.
var unusedPorts = map[model][]byte{
	modelMLT8PE3:  {1, 2},
	modelISDB6014: {3},
}

// tunerAddress is the CXD2858ER's 8-bit I2C address behind every demodulator.
const tunerAddress = 0x60 << 1

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	_, ok := systems[tuning.Normalize(deliverySystem)]
	return ok
}

// open claims a PX-MLT series device and brings its tuners up.
func open(options device.Options) (device.Board, error) {
	// The layout depends on which product this is, so the USB device has to be
	// opened before the bridge can be configured.
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	product := transport.ProductID()
	_ = transport.Close()
	layout, ok := layouts[models[product]]
	if !ok {
		return nil, fmt.Errorf("unsupported PX-MLT product ID %#04x", product)
	}

	fronts := make([]*frontend, 0, len(layout))
	frame, err := plex.Open(options, plex.Config{
		IDs:             []usbio.ID{{Vendor: 0x0511, Product: product}},
		Inputs:          plex.Inputs(layout, true, unusedPorts[models[product]]),
		Tuners:          len(layout),
		Tag:             tsdemux.TaggedSync(len(layout)),
		PowerGPIO:       7,
		PowerEnableGPIO: 2,
		LNBGPIO:         11,
		PowerOn:         func() error { return powerOn(fronts) },
	})
	if err != nil {
		return nil, err
	}

	// Tuners sharing a bridge I2C bus answer at the same tuner address, so only
	// one of them may hold its demodulator's gate open at a time.
	busLocks := map[byte]*sync.Mutex{}
	for _, spec := range layout {
		if _, ok := busLocks[spec.Bus]; !ok {
			busLocks[spec.Bus] = &sync.Mutex{}
		}
	}
	descriptors := Layout(usbio.ID{Vendor: 0x0511, Product: product})
	for index, spec := range layout {
		bus := frame.Bridge.I2C(spec.Bus)
		// The chip answers on two addresses: the demodulator's, and that plus
		// one 7-bit step for the system side.
		demod := cxd2856er.New(bus, (spec.Address+2)<<1, spec.Address<<1, sony.Config{
			XtalCode: sony.Xtal24000KHz,
			TunerI2C: true,
		})
		fronts = append(fronts, &frontend{
			index: index,
			lnb:   frame.NewLNBClaim(),
			demod: demod,
			tuner: cxd2858er.New(bus, busGate{lock: busLocks[spec.Bus], demod: demod}, tunerAddress, cxd2858er.Config{
				Xtal:           sony.TunerXtal16000KHz,
				TerrestrialLNA: true,
				SatelliteLNA:   true,
			}),
		})
	}

	inner, err := frame.Board("PLEX PX-MLT", descriptors,
		func(index int) (plex.Tuner, error) { return fronts[index], nil },
	)
	if err != nil {
		_ = frame.Close()
		return nil, err
	}
	// The eight-tuner enclosure splits into a five-tuner device and a
	// three-tuner one, and only the five-tuner half is wired to the slot.
	fitted := models[product] != modelMLT8PE3
	return frame.CardBoard(inner, fitted, "IT930x"), nil
}

// busGate holds a bridge I2C bus exclusively while a tuner's gate is open, so
// two tuners at the same address never appear on it together.
type busGate struct {
	lock  *sync.Mutex
	demod *cxd2856er.Demod
}

func (gate busGate) OpenTunerGate(open bool) error {
	if open {
		gate.lock.Lock()
		if err := gate.demod.OpenTunerGate(true); err != nil {
			gate.lock.Unlock()
			return err
		}
		return nil
	}
	err := gate.demod.OpenTunerGate(false)
	gate.lock.Unlock()
	return err
}

// powerOn initialises every demodulator and tuner once the supply is up.
func powerOn(fronts []*frontend) error {
	for _, front := range fronts {
		if err := front.demod.Initialize(); err != nil {
			return fmt.Errorf("initialize CXD2856ER %d: %w", front.index, err)
		}
		if err := front.tuner.Initialize(); err != nil {
			return fmt.Errorf("initialize CXD2858ER %d: %w", front.index, err)
		}
	}
	return nil
}

// frontend is one of the board's identical tuners. Each can reach both systems.
type frontend struct {
	index int
	demod *cxd2856er.Demod
	tuner *cxd2858er.Tuner
	// system is what the last tune acquired, which decides how Status reads.
	system sony.System
	// lnb is this tuner's claim on the shared LNB supply.
	lnb plex.LNBClaim
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	system, ok := systems[delivery]
	if !ok {
		return fmt.Errorf("PX-MLT does not support %q", properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	frequency = tuning.FrequencyKHz(frequency)
	if system.IsSatellite() {
		return front.tuneSatellite(system, uint32(frequency), properties)
	}
	return front.tuneTerrestrial(system, uint32(frequency), properties)
}

// GainLadder is the board's external amplifiers: in circuit or out, with no
// figures because the chip does not say by how much. Both of the tuner's inputs
// have one, so the answer is the same for every delivery system.
func (front *frontend) GainLadder(string) tuning.GainLadder { return tuning.NewGainSwitch() }

// SetGain switches them. The board starts with both in circuit, so step 1 is
// where it came up and step 0 is the setting's reason to exist.
func (front *frontend) SetGain(step int) error {
	front.tuner.SetLNA(step > 0)
	return nil
}

// tuneTerrestrial acquires an ISDB-T, DVB-T, DVB-T2 or J.83 cable channel.
func (front *frontend) tuneTerrestrial(system sony.System, frequency uint32, properties map[string]string) error {
	// ISDB-T channels are always 6 MHz, and so is the chip's only J.83 filter; the
	// DVB terrestrial systems say what they are in the file. An Annex A/C channel
	// is not asked for its symbol rate: the demodulator searches by bandwidth and
	// finds the rate itself. Annex B is: the rate chooses between the 5.6 Msym/s
	// setting and the 5.057/5.361 pair.
	bandwidthMHz := uint32(6)
	var symbolRate uint64
	switch {
	case system == sony.SystemDVBC || system == sony.SystemJ83B:
		width, err := tuning.Number(properties, "BANDWIDTH_HZ", false)
		if err != nil {
			return err
		}
		if width != 0 && width != 6_000_000 {
			return fmt.Errorf("PX-MLT has only a 6 MHz cable filter, not %d Hz", width)
		}
		if system == sony.SystemJ83B {
			if symbolRate, err = tuning.Number(properties, "SYMBOL_RATE", true); err != nil {
				return err
			}
		}
	case system != sony.SystemISDBT:
		bandwidth, err := tuning.BandwidthHz(properties)
		if err != nil {
			return err
		}
		bandwidthMHz = uint32((bandwidth + 500_000) / 1_000_000)
	}
	// A DVB-T2 file names the physical layer pipe to decode. Its absence means
	// the demodulator chooses, which is not the same as pipe zero.
	plpID := -1
	if system == sony.SystemDVBT2 {
		streamID, err := tuning.Number(properties, "STREAM_ID", false)
		if err != nil {
			return err
		}
		if properties["STREAM_ID"] != "" {
			if streamID > 255 {
				return fmt.Errorf("STREAM_ID %d is outside the DVB-T2 range 0..255", streamID)
			}
			plpID = int(streamID)
		}
	}
	if err := front.demod.Wakeup(system, sony.WakeupParams{
		BandwidthMHz:  bandwidthMHz,
		PLPID:         plpID,
		SymbolRateSPS: uint32(symbolRate),
	}); err != nil {
		return fmt.Errorf("wake CXD2856ER for %s: %w", system, err)
	}
	if err := front.tuner.Tune(system, cxd2858er.TuneParams{
		FrequencyKHz: frequency,
		BandwidthMHz: bandwidthMHz,
	}); err != nil {
		return fmt.Errorf("tune CXD2858ER: %w", err)
	}
	if err := front.demod.PostTune(); err != nil {
		return err
	}
	front.system = system
	return nil
}

func (front *frontend) tuneSatellite(system sony.System, frequency uint32, properties map[string]string) error {
	// ISDB-S selects a stream inside the carrier; the DVB systems need the
	// symbol rate instead, to set the tuner's filter width.
	isdb := system == sony.SystemISDBS
	var streamID uint16
	var err error
	if isdb {
		if streamID, err = tuning.StreamID(properties); err != nil {
			return err
		}
	}
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", !isdb)
	if err != nil {
		return err
	}
	feed, err := tuning.SwitchedSupply(properties)
	if err != nil {
		return err
	}
	if err := front.lnb.Want(feed); err != nil {
		return err
	}
	if err := front.demod.Wakeup(system, sony.WakeupParams{
		PLPID:         -1,
		SymbolRateSPS: uint32(symbolRate),
	}); err != nil {
		return fmt.Errorf("wake CXD2856ER for %s: %w", system, err)
	}
	if err := front.tuner.Tune(system, cxd2858er.TuneParams{
		FrequencyKHz: frequency,
		SymbolRateKS: uint32(symbolRate / 1000),
	}); err != nil {
		return fmt.Errorf("tune CXD2858ER: %w", err)
	}
	if err := front.demod.PostTune(); err != nil {
		return err
	}
	if isdb {
		// Values below 8 name a relative slot rather than a TSID. Eight, not
		// the twelve a TC90522 has: the two chips hold different numbers of
		// slots, and SetSlotISDBS refuses anything above seven.
		if streamID < cxd2856er.ISDBSSlots {
			err = front.demod.SetSlotISDBS(streamID)
		} else {
			err = front.demod.SetTSIDISDBS(streamID)
		}
		if err != nil {
			return err
		}
	}
	front.system = system
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if front.system == sony.SystemUnspecified {
		return device.Status{}, nil
	}
	lock, err := front.demod.Lock()
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: lock.Locked}
	switch {
	case lock.Locked:
		status.Names = device.LockNames
	case lock.Synced:
		status.Names = []string{"SIGNAL", "CARRIER"}
	}
	// The measurements are not always ready the moment the lock is, and a
	// missing one is not a reason to withhold the lock.
	if cnr, cnrErr := front.demod.CNR(); cnrErr == nil {
		status.CNR = device.Measured(cnr)
	}
	if strength, strengthErr := front.demod.SignalStrength(); strengthErr == nil {
		status.Strength = device.Measured(strength)
	}
	return status, nil
}

// EnableTS is a no-op: the CXD2856ER drives its TS output as soon as it is
// woken, so there is no separate pin to switch.
func (front *frontend) EnableTS(bool) error { return nil }

// Park powers the tuner down and drops its claim on the LNB supply.
func (front *frontend) Park() error {
	_ = front.tuner.Stop()
	err := front.demod.Sleep()
	return front.lnb.Release(err)
}

var _ i2c.Gate = busGate{}
