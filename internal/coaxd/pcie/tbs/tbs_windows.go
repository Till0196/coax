// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package tbs drives the demodulator on a TBS PCIe card directly, using the
// vendor's Windows driver only to carry the transport stream.
//
// The driver has one demodulator mode for the whole card while the hardware is
// four independent demodulators. The way out is the raw I2C property the driver
// exposes on each tuner's input pin: the chip is driven with this repository's
// own drivers and the card's mode left alone. The BDA graph is still built,
// because the card's DMA is the only way to move the stream into user memory;
// the channel it is built against is a formality.
package tbs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Till0196/coax/internal/coaxd/bda"
	tbsvendor "github.com/Till0196/coax/internal/coaxd/bda/vendors/tbs"
	"github.com/Till0196/coax/internal/coaxd/demod/cxd2857er"
	"github.com/Till0196/coax/internal/coaxd/demod/cxd2878"
	"github.com/Till0196/coax/internal/coaxd/demod/m88rs6060"
	"github.com/Till0196/coax/internal/coaxd/demod/mxl58x"
	"github.com/Till0196/coax/internal/coaxd/demod/si2183"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/demod/stid135"
	"github.com/Till0196/coax/internal/coaxd/demod/stv091x"
	"github.com/Till0196/coax/internal/coaxd/demod/tas2101"
	coaxdevice "github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/av201x"
	"github.com/Till0196/coax/internal/coaxd/tuner/rda5816"
	"github.com/Till0196/coax/internal/coaxd/tuner/si2157"
	"github.com/Till0196/coax/internal/coaxd/tuner/stv6120"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/i2c"
)

// Backend is the name these tuners are listed under.
const Backend = "tbs"

// demodulator is the part of a chip driver this backend uses.
type demodulator interface {
	Initialize() error
	Wakeup(system sony.System, params sony.WakeupParams) error
	Tune(frequencyKHz uint32) error
	Lock() (sony.Lock, error)
	CNR() (float64, error)
	SignalStrength(frequencyKHz uint32) (float64, error)
}

// vendorState is what one value of a card's demodulator-mode property means.
// Putting the demodulator back into it hands the card over again on the way out.
type vendorState struct {
	// systems are the standards this mode is the vendor's own for. The first is
	// what the demodulator is put back into on the way out.
	systems []sony.System
	// borrowed are standards this mode can be made to receive that the vendor has
	// no mode for. A tuner is never handed back in one; recovery repairs it.
	borrowed []sony.System
	// bandwidthMHz and symbolRateSPS are what waking the first system needs.
	bandwidthMHz  uint32
	symbolRateSPS uint32
	// carrier is a channel of the mode's standard for the graph to be built
	// against. Nothing is expected to lock on it: it only has to name the network
	// type the BDA driver advertises for this mode.
	carrier map[string]string
}

// model is one card this package knows how to drive.
type model struct {
	// match is looked for in the tuner's BDA name.
	match string
	// demod is the chip and how it is wired.
	demod sony.Config
	// tunerAddresses is the embedded tuner's I2C address per tuner. Empty means
	// demod.TunerAddress for every tuner.
	tunerAddresses []byte
	// demodAddresses is the same for the demodulator, telling apart two chips on
	// one bus. Indexed by tuner number modulo its length.
	demodAddresses []byte
	// drives is how many of the card's tuners this entry is for, counted from the
	// first; zero means all of them. Non-zero on a card that mixes chips.
	drives int
	// satellite is the second demodulator on cards that carry one; the delivery
	// system says which of the two a channel wants.
	satellite *m88rs6060.Config
	// satelliteFrom is the first tuner that is satellite and nothing else. Zero
	// means every tuner has both.
	satelliteFrom int
	// satelliteDeliveries is what the satellite-only tuners of a split card receive.
	satelliteDeliveries []string
	// satelliteAddresses cycles the satellite demodulator's address the way
	// demodAddresses cycles the other one's.
	satelliteAddresses []byte
	// stid135 is eight demodulators and four aerial inputs behind one address,
	// chosen by register block rather than by the bus.
	stid135 *stid135Card
	// quattroFed says the aerial inputs are fed by a quattro block, so the vendor's
	// LNB property is left alone and polarization is answered by lead choice.
	quattroFed bool
	// stv091x is two demodulators and two tuners shared by a pair of tuners.
	stv091x *stv091xCard
	// mxl58x is one chip that is all eight demodulators, told apart by index.
	mxl58x *mxl58x.Config
	// si2183 is one demodulator that receives everything, with a tuner on each
	// side of the band. A card has this or the pair above, never both.
	si2183 *si2183Card
	// satelliteTAS is a TAS2101 with a tuner beside it, on the older cards.
	satelliteTAS *tas2101Card
	// satelliteNoToneAt names satellite demodulator addresses whose 22 kHz tone
	// output is not brought out to a lead.
	satelliteNoToneAt []byte
	// deliveries is what the demodulator can be put into, per tuner.
	deliveries []string
	// newDemod builds the driver for the chip this card carries.
	newDemod func(bus i2c.Bus, config sony.Config) demodulator
	// states says what each of the card's mode values means.
	states map[bda.DemodMode]vendorState
	// modeSelectsFilter says the mode picks which network type the vendor driver's
	// BDA filter advertises, so it has to agree with the standard wanted before the
	// graph goes up. Nothing outside the demodulator depends on it: the per-tuner
	// antenna switch at GPIO offset 0x8 has nothing behind it, and the LNB supply
	// is a separate vendor property (0x15, TBSACCESS) on GPIO offset 0.
	modeSelectsFilter bool
}

// modeFor is the mode this card has to be in to receive a standard.
func (card model) modeFor(system sony.System) (bda.DemodMode, bool) {
	for mode, state := range card.states {
		for _, group := range [][]sony.System{state.systems, state.borrowed} {
			for _, candidate := range group {
				if candidate == system {
					return mode, true
				}
			}
		}
	}
	return bda.ModeUnknown, false
}

// demodFor is how the chip is wired for one tuner of the card.
func (card model) demodFor(slot int) sony.Config {
	config := card.demod
	if slot >= 0 && len(card.tunerAddresses) > 0 {
		// Cycled rather than indexed: a card with more tuners than configurations
		// repeats them.
		config.TunerAddress = card.tunerAddresses[slot%len(card.tunerAddresses)]
	}
	if slot >= 0 && len(card.demodAddresses) > 0 {
		config.DemodAddress = card.demodAddresses[slot%len(card.demodAddresses)]
	}
	config.TunerIndex = byte(slot + 1)
	return config
}

// dvbCardStates is what each mode value means on the cards whose mode property
// is one byte of the vendor's DVB numbering.
func dvbCardStates() map[bda.DemodMode]vendorState {
	return map[bda.DemodMode]vendorState{
		bda.ModeDVBT: {
			systems:      []sony.System{sony.SystemDVBT, sony.SystemDVBT2},
			bandwidthMHz: 8,
			carrier:      map[string]string{"DELIVERY_SYSTEM": "DVBT", "FREQUENCY": "474000000", "BANDWIDTH_HZ": "8000000"},
		},
		bda.ModeISDBT: {
			systems:      []sony.System{sony.SystemISDBT},
			bandwidthMHz: 6,
			carrier:      map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473143000", "BANDWIDTH_HZ": "6000000"},
		},
		bda.ModeDVBC: {
			systems:      []sony.System{sony.SystemDVBC},
			bandwidthMHz: 8, symbolRateSPS: 5_274_000,
			carrier: map[string]string{"DELIVERY_SYSTEM": "DVBC", "FREQUENCY": "111000000", "SYMBOL_RATE": "5274"},
		},
		bda.ModeQAMB: {
			systems:       []sony.System{sony.SystemJ83B},
			symbolRateSPS: 5_274_000,
			carrier:       map[string]string{"DELIVERY_SYSTEM": "DVBC/ANNEX_B", "FREQUENCY": "111000000", "SYMBOL_RATE": "5274"},
		},
	}
}

// satelliteOnlyCardStates is for a card that receives nothing but satellite.
func satelliteOnlyCardStates() map[bda.DemodMode]vendorState {
	return map[bda.DemodMode]vendorState{
		bda.ModeDVBS: {
			systems:       []sony.System{sony.SystemDVBS, sony.SystemDVBS2},
			symbolRateSPS: 22_000_000,
			carrier: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1250000",
				"SYMBOL_RATE": "22000000", "POLARIZATION": "HORIZONTAL",
			},
		},
	}
}

// unmodedSatelliteCardStates is for a satellite-only card whose mode is never
// read. The one state is filed under both the value an unasked card answers
// with and the one it would answer if asked.
func unmodedSatelliteCardStates() map[bda.DemodMode]vendorState {
	satellite := satelliteOnlyCardStates()[bda.ModeDVBS]
	return map[bda.DemodMode]vendorState{
		bda.ModeUnknown: satellite,
		bda.ModeDVBS:    satellite,
	}
}

// satelliteCardStates is dvbCardStates with the satellite mode added. Its graph
// is built against a real transponder: a satellite graph is harder to connect.
func satelliteCardStates() map[bda.DemodMode]vendorState {
	states := dvbCardStates()
	states[bda.ModeDVBS] = vendorState{
		systems:       []sony.System{sony.SystemDVBS, sony.SystemDVBS2},
		symbolRateSPS: 22_000_000,
		carrier: map[string]string{
			"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1250000",
			"SYMBOL_RATE": "22000000", "POLARIZATION": "HORIZONTAL",
		},
	}
	return states
}

// models lists the cards this drives.
//
// The Si2183 runs on firmware the vendor driver has already loaded, so it is
// resumed rather than booted: the cold sequence would drop that driver's stream.
//
// Missing: a 6205 or 6281SE's Si2168 has no warm path here; a 6508's RDA5816
// has no driver; the 6902SE, 6904SE, 6910SE and 7901 need ts_autoclock; a
// 6814's chips are from none of these families.
var models = []model{
	{
		match: "TBS 6205se",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
		},
		deliveries: []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		// No ATSC entry, though the chip can do it: the BDA layer has no ATSC
		// tuning space, so no graph can carry the stream out.
		states: dvbCardStates(),
	},
	{
		// The 6281TD is the 6205SE with two tuners, wired the same way. Untested.
		match: "TBS 6281TD",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
		},
		deliveries: []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: dvbCardStates(),
	},
	{
		// The 6522H's first two tuners are CXD6802GL, its last two an M88RS6060
		// apiece. Which chip a tuner has is settled by which tuner it is. Untested.
		match: "TBS 6522H",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
		},
		deliveries: []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		satellite: &m88rs6060.Config{
			DemodAddress: 0x69,
			TunerAddress: 0x2c,
			ClockHz:      27_000_000,
			TSMode:       m88rs6060.TSModeParallel,
		},
		satelliteFrom:       2,
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: satelliteCardStates(),
	},
	{
		// The 6209SE is eight tuners on four buses, two chips to a bus told apart
		// by address cycling in fours, taking their streams serially. Untested,
		// and the serial output is this driver's first.
		match: "TBS 6209SE",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
			TSSerial:     true,
		},
		demodAddresses: []byte{0x64, 0x6c, 0x65, 0x6d},
		deliveries:     []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: dvbCardStates(),
	},
	{
		// The 6909SE is eight satellite tuners, an M88RS6060 apiece, two to a bus.
		// Only the even tuners can put a tone on their lead; the listing does not
		// say so, as a tuner that cannot is still right for channels not needing it.
		match: "TBS 6909SE",
		satellite: &m88rs6060.Config{
			DemodAddress: 0x69,
			TunerAddress: 0x2c,
			ClockHz:      27_000_000,
			TSMode:       m88rs6060.TSModeSerial,
		},
		satelliteAddresses:  []byte{0x69, 0x6b},
		satelliteNoToneAt:   []byte{0x6b},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		// It answers the mode property but is not asked: it shares the 6909's entry.
		states: unmodedSatelliteCardStates(),
	},
	{
		// The 6902 is two satellite tuners, a TAS2101 apiece with an AV2012 behind
		// it, told apart by address. Its driver has no mode property. Untested.
		match: "TBS 6902",
		satelliteTAS: &tas2101Card{
			demod:     tas2101.Config{DemodAddress: 0x60},
			addresses: []byte{0x60, 0x68},
			inits:     [][7]byte{{0xb0, 0x32, 0x81, 0x57, 0x64, 0x9a, 0x33}},
			tuner:     av201x.Config{Address: 0x62, Model: av201x.AV2012, XtalKHz: 27000},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6904 is the 6902 with four tuners on the same alternating
		// addresses. Its tuner answers on a different address.
		match: "TBS 6904",
		satelliteTAS: &tas2101Card{
			demod:     tas2101.Config{DemodAddress: 0x60},
			addresses: []byte{0x60, 0x68},
			inits:     [][7]byte{{0xb0, 0x32, 0x81, 0x57, 0x64, 0x9a, 0x33}},
			tuner:     av201x.Config{Address: 0x63, Model: av201x.AV2012, XtalKHz: 27000},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6910 is two tuners with a common interface slot. Alone of the three
		// its tuners take different board bytes -- the seven that settle the
		// transport pin order -- and its addresses run the other way round. Its
		// AV2018 differs from an AV2012 in two registers, so it is configured as one.
		match: "TBS 6910",
		satelliteTAS: &tas2101Card{
			demod:     tas2101.Config{DemodAddress: 0x68},
			addresses: []byte{0x68, 0x60},
			inits: [][7]byte{
				{0x21, 0x43, 0x65, 0xb0, 0xa8, 0x97, 0xb1},
				{0xb0, 0xa8, 0x21, 0x43, 0x65, 0x97, 0xb1},
			},
			tuner: av201x.Config{Address: 0x62, Model: av201x.AV2012, XtalKHz: 27000},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6504 and the plain 6522 are one Si2183 per tuner, with an Si2157 on
		// the terrestrial side and an AV201x on the satellite one, so the standard
		// rather than the tuner number picks which tuner is used. Two demodulators
		// share a bus, alternating addresses and AGC setting. Untested, and the raw
		// I2C property is assumed to be in the usual layout.
		match: "TBS 6504",
		si2183: &si2183Card{
			demod: si2183.Config{
				TSMode: si2183.TSModeParallel, TSClockGapped: true,
				AlreadyBooted: true,
			},
			addresses:            []byte{0x67, 0x64},
			agcModes:             []byte{0x05, 0x04},
			terrestrial:          &si2157.Config{Variant: si2157.Si2157, IFPort: 1},
			terrestrialAddresses: []byte{0x60, 0x61},
			satellite:            &av201x.Config{Model: av201x.AV2012, XtalKHz: 27000},
			satelliteAddresses:   []byte{0x63, 0x62},
		},
		deliveries: []string{
			"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B",
			"DVB-S", "DVB-S2",
		},
		states: satelliteCardStates(),
	},
	{
		// The 6522 is the 6504 with two tuners instead of four, on the same
		// chips and the same alternating addresses.
		match: "TBS 6522",
		si2183: &si2183Card{
			demod: si2183.Config{
				TSMode: si2183.TSModeParallel, TSClockGapped: true,
				AlreadyBooted: true,
			},
			addresses:            []byte{0x67, 0x64},
			agcModes:             []byte{0x05, 0x04},
			terrestrial:          &si2157.Config{Variant: si2157.Si2157, IFPort: 1},
			terrestrialAddresses: []byte{0x60, 0x61},
			satellite:            &av201x.Config{Model: av201x.AV2012, XtalKHz: 27000},
			satelliteAddresses:   []byte{0x63, 0x62},
		},
		deliveries: []string{
			"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B",
			"DVB-S", "DVB-S2",
		},
		states: satelliteCardStates(),
	},
	{
		// The 6904X is four tuners of the same Si2183 with only the satellite tuner
		// fitted. Alone here it starts the chip's clock the other way round
		// (start_clk_mode).
		match: "TBS 6904X",
		si2183: &si2183Card{
			demod: si2183.Config{
				TSMode: si2183.TSModeParallel, TSClockGapped: true,
				StartClockMode: true, AlreadyBooted: true,
			},
			addresses:          []byte{0x67, 0x64},
			agcModes:           []byte{0x05, 0x04},
			satellite:          &av201x.Config{Model: av201x.AV2012, XtalKHz: 27000},
			satelliteAddresses: []byte{0x63, 0x62},
		},
		deliveries: []string{"DVB-S", "DVB-S2"},
		// Its mode is never read: it shares the 6904's property-table entry, and
		// that driver has no mode property. See tbs.Model6904.
		states: unmodedSatelliteCardStates(),
	},
	{
		// The 6508 is eight tuners of the same Si2183, its addresses and AGC setting
		// alternating the other way round from the 6504's. Its RDA5816 is a dual
		// tuner, so the eight tuners are four parts answering at consecutive addresses.
		match: "TBS 6508",
		si2183: &si2183Card{
			demod: si2183.Config{
				TSMode: si2183.TSModeParallel, TSClockGapped: true,
				AlreadyBooted: true,
			},
			addresses:            []byte{0x64, 0x67},
			agcModes:             []byte{0x04, 0x05},
			terrestrial:          &si2157.Config{Variant: si2157.Si2157, IFPort: 1},
			terrestrialAddresses: []byte{0x61, 0x60},
			satelliteRDA:         &rda5816.Config{XtalKHz: rda5816.Xtal27MHz},
			satelliteAddresses: []byte{
				rda5816.ChannelAAddress, rda5816.ChannelBAddress,
			},
		},
		deliveries: []string{
			"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B",
			"DVB-S", "DVB-S2",
		},
		states: satelliteCardStates(),
	},
	{
		// The 6910X is two satellite tuners, an M88RS6060 on a bus each, streaming in
		// parallel. The plain configuration, which is why its SE sibling is not here.
		match: "TBS 6910X",
		satellite: &m88rs6060.Config{
			DemodAddress: 0x69,
			TunerAddress: 0x2c,
			ClockHz:      27_000_000,
			TSMode:       m88rs6060.TSModeParallel,
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		// As for the 6904X: it shares the 6910's table entry, whose driver has
		// no mode property.
		states: unmodedSatelliteCardStates(),
	},
	{
		// The plain 6590 -- not the SE -- is two Si2183 tuners streaming serially,
		// its addresses the other way round from the 6504's.
		//
		// It and the 6528 have an aerial input switch behind an FPGA pin this
		// backend cannot write (the vendor driver exposes I2C and LNB, not GPIO), so
		// the mode follows the standard and the vendor driver throws the switch
		// while building the graph. That is what modeSelectsFilter is for here.
		match: "TBS 6590",
		si2183: &si2183Card{
			demod: si2183.Config{
				TSMode: si2183.TSModeSerial, TSClockGapped: true,
				AlreadyBooted: true,
			},
			addresses:            []byte{0x64, 0x67},
			agcModes:             []byte{0x04, 0x05},
			terrestrial:          &si2157.Config{Variant: si2157.Si2157, IFPort: 1},
			terrestrialAddresses: []byte{0x60, 0x61},
			satellite:            &av201x.Config{Model: av201x.AV2012, XtalKHz: 27000},
			satelliteAddresses:   []byte{0x63, 0x62},
		},
		deliveries: []string{
			"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B",
			"DVB-S", "DVB-S2",
		},
		modeSelectsFilter: true,
		states:            satelliteCardStates(),
	},
	{
		// The 6528 is one tuner of the same, streaming in parallel, with fixed
		// addresses since there is nothing to alternate between. Same aerial
		// input switch as the 6590.
		match: "TBS 6528",
		si2183: &si2183Card{
			demod: si2183.Config{
				DemodAddress: 0x67, AGCMode: 0x05,
				TSMode: si2183.TSModeParallel, TSClockGapped: true,
				AlreadyBooted: true,
			},
			terrestrial: &si2157.Config{
				Variant: si2157.Si2157, IFPort: 1, Address: 0x61,
			},
			satellite: &av201x.Config{
				Model: av201x.AV2012, XtalKHz: 27000, Address: 0x62,
			},
		},
		deliveries: []string{
			"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B",
			"DVB-S", "DVB-S2",
		},
		modeSelectsFilter: true,
		states:            satelliteCardStates(),
	},
	{
		// The 6909 is eight tuners of one MaxLinear MxL584, the frontend chosen by
		// an index in each command; the card's tuner number is that index.
		//
		// Fed by a quattro block on four leads, the vendor driver's default mode:
		// asking for a voltage or a tone chooses which input a frontend listens to
		// rather than switching anything. The switched mode needs GPIO and an input
		// choice this backend cannot be told, so it is not offered. Untested.
		match: "TBS 6909",
		mxl58x: &mxl58x.Config{
			DemodAddress: 0x60,
			ClockHz:      24_000_000,
			CrystalCap:   12,
			DeviceType:   1, // MxL584
			Multiswitch:  mxl58x.Quattro,
			// Picked up as the vendor driver left it: one firmware download would
			// reset all eight frontends at once.
			AlreadyBooted: true,
		},
		quattroFed:          true,
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		// Its driver has no mode property, and it shares the 6909SE's entry
		// in the property table besides. See tbs.Model6909.
		states: unmodedSatelliteCardStates(),
	},
	{
		// The 6903, 6905 and 6908 are the same two chips in different numbers: an
		// STV0910 (two demodulators) and an STV6120 (two tuners), a pair of the
		// card's tuners sharing both. All three take this configuration. Untested.
		match: "TBS 6903",
		stv091x: &stv091xCard{
			demod: stv091x.Config{
				DemodAddress:  0x68,
				ClockHz:       30_000_000,
				TSMode:        stv091x.TSModeParallel,
				RepeaterLevel: 3,
				// Both halves reach their tuner through the first path's
				// repeater, which is what dual_tuner = 1 means.
				Repeater: stv091x.RepeaterPath1,
				// Taken over rather than brought up: the bring-up is
				// chip-wide, and the other half may be streaming.
				AlreadyBooted: true,
			},
			tuner: stv6120.Config{
				Address: 0x60, XtalKHz: 30000, RDiv: 2,
				AlreadyBooted: true,
			},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6905 is the 6903 with four tuners: two of each chip, one pair
		// per bus.
		match: "TBS 6905",
		stv091x: &stv091xCard{
			demod: stv091x.Config{
				DemodAddress:  0x68,
				ClockHz:       30_000_000,
				TSMode:        stv091x.TSModeParallel,
				RepeaterLevel: 3,
				Repeater:      stv091x.RepeaterPath1,
				AlreadyBooted: true,
			},
			tuner: stv6120.Config{
				Address: 0x60, XtalKHz: 30000, RDiv: 2,
				AlreadyBooted: true,
			},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6908 takes the 6905's configuration; they differ only in how the
		// card's EEPROM and transport inputs are arranged.
		match: "TBS 6908",
		stv091x: &stv091xCard{
			demod: stv091x.Config{
				DemodAddress:  0x68,
				ClockHz:       30_000_000,
				TSMode:        stv091x.TSModeParallel,
				RepeaterLevel: 3,
				Repeater:      stv091x.RepeaterPath1,
				AlreadyBooted: true,
			},
			tuner: stv6120.Config{
				Address: 0x60, XtalKHz: 30000, RDiv: 2,
				AlreadyBooted: true,
			},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6903X is two tuners of an STiD135, wired to demodulators 0 and 2 on
		// aerial inputs 0 and 3. One lead per tuner, its supply switched through
		// the vendor's LNB property.
		match: "TBS 6903X",
		stid135: &stid135Card{
			demod: stid135.Config{
				DemodAddress:      0x68,
				ReferenceClockMHz: 27,
				Multiswitch:       stid135.Direct,
				// Taken over rather than brought up: the bring-up is chip-wide and
				// includes analogue calibration.
				AlreadyBooted: true,
			},
			demodIndexes: []int{0, 2},
			rfInputs:     []int{0, 3},
		},
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6909X is eight tuners of the same chip, two demodulators to an
		// aerial input, fed by a quattro block: every demodulator starts on
		// input 0 and moves as bands are asked for.
		match: "TBS 6909X",
		stid135: &stid135Card{
			demod: stid135.Config{
				DemodAddress:      0x68,
				ReferenceClockMHz: 27,
				Multiswitch:       stid135.Quattro,
				AlreadyBooted:     true,
			},
			demodIndexes: []int{0, 1, 2, 3, 4, 5, 6, 7},
			// Kept for reference only: in this mode the input follows the band
			// asked for.
			rfInputs: []int{0, 0, 1, 1, 2, 2, 3, 3},
		},
		quattroFed:          true,
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		states:              unmodedSatelliteCardStates(),
	},
	{
		// The 6216 is sixteen tuners streaming serially, two demodulators to a
		// bus told apart by address -- and, uniquely here, by tuner address as
		// well: 0x60 on one, 0x63 on the other.
		match: "TBS 6216",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
			TSSerial:     true,
		},
		demodAddresses: []byte{0x64, 0x6c},
		tunerAddresses: []byte{0x60, 0x63},
		deliveries:     []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: dvbCardStates(),
	},
	{
		// The 6504H is eight tuners split down the middle: the first four a
		// CXD2878 apiece streaming serially, the last four M88RS6060s two to a
		// bus, as on the 6522H.
		match: "TBS 6504H",
		demod: sony.Config{
			DemodAddress: 0x6c,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
			TSSerial:     true,
		},
		demodAddresses: []byte{0x6c, 0x64},
		deliveries:     []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		satellite: &m88rs6060.Config{
			DemodAddress: 0x69,
			TunerAddress: 0x2c,
			ClockHz:      27_000_000,
			TSMode:       m88rs6060.TSModeParallel,
		},
		satelliteAddresses:  []byte{0x69, 0x6b},
		satelliteFrom:       4,
		satelliteDeliveries: []string{"DVB-S", "DVB-S2"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: satelliteCardStates(),
	},
	{
		// The 7230 is one tuner on the same serial wiring as a 6290TD's, and
		// takes the same configuration.
		match: "TBS 7230",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
			TSSerial:     true,
		},
		deliveries: []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: dvbCardStates(),
	},
	{
		// The 6290TD is two tuners with a bus each, taking the stream
		// serially. Same caveat as the 6209SE.
		match: "TBS 6290TD",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
			TSSerial:     true,
		},
		deliveries: []string{"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B"},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: dvbCardStates(),
	},
	{
		// The 6590SE carries two demodulators per tuner: a CXD2878 on 0x64 for
		// terrestrial and cable, an M88RS6060 on 0x69 for satellite, each with its
		// own tuner. The card's mode says which is wired to the transport stream,
		// so mode and demodulator are chosen together. Untested.
		match: "TBS 6590SE",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
		},
		satellite: &m88rs6060.Config{
			DemodAddress: 0x69,
			TunerAddress: 0x2c,
			ClockHz:      27_000_000,
			TSMode:       m88rs6060.TSModeParallel,
		},
		deliveries: []string{
			"DVB-T", "DVB-T2", "DVB-C", "ISDB-T", "DVB-C/ANNEX_B",
			"DVB-S", "DVB-S2",
		},
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2878.New(bus, config)
		},
		states: satelliteCardStates(),
	},
	{
		match: "TBS 6812",
		demod: sony.Config{
			DemodAddress: 0x64,
			TunerAddress: 0x60,
			XtalCode:     1, // 24 MHz
			TunerXtal:    sony.TunerXtal24000KHz,
			TLVMode:      true,
		},
		tunerAddresses:    []byte{0x60, 0x61},
		deliveries:        []string{"ISDB-T", "ISDB-S", "ISDB-S3", "DVB-S", "DVB-S2", "DVB-C/ANNEX_B"},
		modeSelectsFilter: true,
		newDemod: func(bus i2c.Bus, config sony.Config) demodulator {
			return cxd2857er.New(bus, config)
		},
		states: map[bda.DemodMode]vendorState{
			// J.83B is borrowed: the card locks it but the vendor has no mode for
			// it. To the tuner it is a terrestrial-side tune, so it goes through the
			// terrestrial mode and is handed back as ISDB-T.
			isdbModeTerrestrial: {
				systems:      []sony.System{sony.SystemISDBT},
				borrowed:     []sony.System{sony.SystemJ83B},
				bandwidthMHz: 6,
				carrier:      map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473143000", "BANDWIDTH_HZ": "6000000"},
			},
			isdbModeSatellite: {
				// The satellite side receives DVB-S and DVB-S2 as well as the Japanese
				// pair, sharing the mode; what separates them is inside the demodulator.
				systems: []sony.System{
					sony.SystemISDBS, sony.SystemISDBS3,
					sony.SystemDVBS, sony.SystemDVBS2,
				},
				symbolRateSPS: 28_860_000,
				carrier:       map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "16400"},
			},
		},
	},
}

// The TBS 6812's mode values, which are not the DVB cards': the same byte that
// means DVB-T on a 6205SE means ISDB-T here.
const (
	isdbModeTerrestrial bda.DemodMode = 1
	isdbModeSatellite   bda.DemodMode = 2
)

// hasSatellite reports whether the card carries a satellite demodulator.
func (card model) hasSatellite() bool {
	return card.satellite != nil || card.satelliteTAS != nil
}

// drivesSlot reports whether this entry covers one tuner of its card: the tuners
// past drives carry silicon this backend has no driver for.
func (card model) drivesSlot(slot int) bool {
	return card.drives == 0 || slot < card.drives
}

// splitBySlot reports whether the card divides its tuners between the two chips.
func (card model) splitBySlot() bool { return card.hasSatellite() && card.satelliteFrom > 0 }

// satelliteOnly reports whether a tuner of a split card is a satellite one.
func (card model) satelliteOnly(slot int) bool {
	return card.splitBySlot() && slot >= card.satelliteFrom
}

// satelliteFor is the satellite demodulator's configuration for one tuner.
func (card model) satelliteFor(slot int) m88rs6060.Config {
	config := *card.satellite
	if slot >= 0 && len(card.satelliteAddresses) > 0 {
		config.DemodAddress = card.satelliteAddresses[slot%len(card.satelliteAddresses)]
	}
	for _, address := range card.satelliteNoToneAt {
		if address == config.DemodAddress {
			config.NoTone = true
		}
	}
	return config
}

// deliveriesFor is what one tuner receives, which differs per tuner on a split
// card.
func (card model) deliveriesFor(slot int) []string {
	if card.stid135 != nil {
		return card.satelliteDeliveries
	}
	if card.stv091x != nil {
		return card.satelliteDeliveries
	}
	if card.mxl58x != nil {
		return card.satelliteDeliveries
	}
	if card.si2183 != nil {
		return card.deliveries
	}
	if card.newDemod == nil {
		return card.satelliteDeliveries
	}
	if card.satelliteOnly(slot) {
		return card.satelliteDeliveries
	}
	return card.deliveries
}

// slotFromName is which tuner of its card a BDA tuner is: the index the card's
// own configuration uses for the bus and the mode byte.
func slotFromName(name string) int {
	lower := strings.ToLower(name)
	for slot := 9; slot >= 1; slot-- {
		if strings.Contains(lower, fmt.Sprintf("tuner %d", slot)) {
			return slot
		}
	}
	return 0
}

// Found is one tuner this package can drive.
type Found struct {
	Name string
	// Selector is the moniker display name, which is what Open takes.
	Selector   string
	Deliveries []string
	// LNB is what a channel may ask of the supply on this tuner's lead, nil where
	// it has none to ask: a terrestrial tuner, a quattro-fed card (its leads are
	// live at their own band), or the second tuner of a 6812 (only the first has
	// the connector wired).
	LNB *coaxdevice.LNBControl
}

// Devices lists the tuners of every card this package knows. Nothing is opened:
// a card is recognised by the name its driver gives each tuner.
func Devices() ([]Found, error) {
	tuners, err := bda.Devices()
	if err != nil {
		return nil, err
	}
	var found []Found
	for _, tuner := range tuners {
		card, ok := modelFor(tuner.Name)
		if !ok {
			continue
		}
		if !card.drivesSlot(slotFromName(tuner.Name)) {
			continue
		}
		deliveries := card.deliveriesFor(slotFromName(tuner.Name))
		found = append(found, Found{
			Name:       tuner.Name,
			Selector:   tuner.Selector,
			Deliveries: deliveries,
			LNB:        card.supplyFor(slotFromName(tuner.Name), deliveries),
		})
	}
	return found, nil
}

// supplyFor is what a channel may ask of the supply on one tuner's lead.
//
// The vendor property (0x15) switches the supply off and picks 13V or 18V. The
// TAS2101 cards (6902, 6904, 6910) keep the supply on the demodulator, which can
// change the voltage but not switch it off. A quattro-fed card's supply is not
// touched at all, and a 6812 has the connector on its first tuner only.
func (card model) supplyFor(slot int, deliveries []string) *coaxdevice.LNBControl {
	satellite := false
	for _, system := range deliveries {
		if s, err := systemFor(system); err == nil && s.IsSatellite() {
			satellite = true
		}
	}
	if !satellite || card.quattroFed {
		return nil
	}
	if strings.Contains(card.match, "6812") && slot > 1 {
		return nil
	}
	return &coaxdevice.LNBControl{Off: card.satelliteTAS == nil, Low: "13V", High: "18V"}
}

func modelFor(name string) (model, bool) {
	for _, candidate := range models {
		if named(name, candidate.match) {
			return candidate, true
		}
	}
	return model{}, false
}

// named reports whether a tuner's name is a tuner of this card, and not of a card
// whose name merely starts the same way. This vendor names distinct cards by
// extending one number -- 6902 and 6902SE, 6904 and 6904X -- and the pairs do
// not carry the same chips, so the match has to end where the name does or at a
// space.
func named(name, match string) bool {
	lower, wanted := strings.ToLower(name), strings.ToLower(match)
	for at := 0; ; {
		found := strings.Index(lower[at:], wanted)
		if found < 0 {
			return false
		}
		found += at
		after := found + len(wanted)
		if after == len(lower) || !isModelCharacter(lower[after]) {
			return true
		}
		at = found + 1
	}
}

func isModelCharacter(character byte) bool {
	return character >= '0' && character <= '9' || character >= 'a' && character <= 'z'
}

// Supports reports whether any card here can be put into a delivery system. Both
// lists are searched, since a satellite-only card has nothing in the other one.
func Supports(deliverySystem string) bool {
	for _, candidate := range models {
		for _, list := range [][]string{candidate.deliveries, candidate.satelliteDeliveries} {
			for _, delivery := range list {
				if tuning.Normalize(delivery) == tuning.Normalize(deliverySystem) {
					return true
				}
			}
		}
	}
	return false
}

// Device is one tuner of a card, driven from here.
type Device struct {
	transport *bda.Device
	// transportStarted is when the vendor's graph was last brought up. See holdTune.
	transportStarted time.Time
	model            model
	// slot picks the bus's tuner address and the mode byte the tuner owns.
	slot   int
	config sony.Config
	bus    *bda.PropertyBus
	demod  demodulator
	// firmwareDir is where a demodulator that loads microcode looks for it.
	firmwareDir string
	// vendorMode is the state the card is put back into on the way out.
	vendorMode bda.DemodMode
	frequency  uint32
	// system is the standard the demodulator was last woken into, and
	// carriedAtTune how much the graph had carried when that tune started.
	// Both are for reusableGraph.
	system        sony.System
	carriedAtTune int64
}

// Open claims one tuner of a card. firmwareDir may be empty until a demodulator
// that loads microcode is asked for.
func Open(selector, firmwareDir string) (*Device, error) {
	transport, err := bda.Open(selector)
	if err != nil {
		return nil, err
	}
	card, ok := modelFor(transport.Name())
	if !ok {
		_ = transport.Close()
		return nil, fmt.Errorf("%q is not a card this backend drives", transport.Name())
	}
	mode, err := transport.Mode()
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("read the card's demodulator mode: %w", err)
	}
	// The listing leaves out the tuners an entry is not for; a selector can
	// still name one, and its addresses would be another tuner's.
	slot := slotFromName(transport.Name())
	if !card.drivesSlot(slot) {
		_ = transport.Close()
		return nil, fmt.Errorf("%q is not a tuner this backend drives on that card", transport.Name())
	}
	return &Device{
		transport:   transport,
		model:       card,
		slot:        slot,
		config:      card.demodFor(slot),
		firmwareDir: firmwareDir,
		vendorMode:  mode,
	}, nil
}

func (device *Device) Name() string { return device.transport.Name() }

// Mode is the demodulator mode the card is in, as the card itself names it.
//
// It is not a per-tuner setting and not free to change: moving it restarts all
// four demodulators, interrupting whatever the other tuners were doing.
func (device *Device) Mode() string { return device.modeName(device.vendorMode) }

// Settings is what the driver says about how the signal is being demodulated,
// read from the BDA demodulator node rather than over I2C.
func (device *Device) Settings() [][2]string { return device.transport.Settings() }

func (device *Device) Supports(deliverySystem string) bool {
	for _, delivery := range device.model.deliveriesFor(device.slot) {
		if tuning.Normalize(delivery) == tuning.Normalize(deliverySystem) {
			return true
		}
	}
	return false
}

// Tune points the hardware at a channel.
//
// The graph comes up first: the demodulator is only reachable through a pin a
// running graph gives a handle to. It is built against the standard the card's
// mode names rather than the one asked for, since that is the one the vendor
// driver will agree to build; what is really demodulated is settled over I2C.
func (device *Device) Tune(properties map[string]string) error {
	// Built even when the graph is a formality: BuildTuneProfile is where the
	// frequency's unit is settled, kHz for satellite and Hz for terrestrial.
	// The supply is this backend's to set (below), so the BDA side is not asked.
	profile, err := bda.BuildDrivenProfile(properties)
	if err != nil {
		return err
	}
	system, err := systemFor(properties["DELIVERY_SYSTEM"])
	if err != nil {
		return err
	}
	streamID, err := tuning.Number(properties, "STREAM_ID", false)
	if err != nil {
		return err
	}
	// ISDB-S3 is chosen over ISDB-S by the stream identifier as well as by name:
	// the two share a delivery system and the identifier's top nibble tells them
	// apart.
	if system == sony.SystemISDBS && cxd2857er.IsISDBS3StreamID(uint32(streamID)) {
		system = sony.SystemISDBS3
	}

	var bandwidthMHz uint32
	if !system.IsSatellite() && system != sony.SystemATSC && system != sony.SystemJ83B {
		bandwidthHz, err := tuning.BandwidthHz(properties)
		if err != nil {
			return err
		}
		bandwidthMHz = sony.BandwidthMHz(uint32(bandwidthHz))
	}
	// DVB-S2 has to be told its symbol rate: unlike the Japanese satellite
	// standards, which fix theirs, the chip carries it in a register.
	symbolRateRequired := system == sony.SystemJ83B ||
		system == sony.SystemDVBS || system == sony.SystemDVBS2
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", symbolRateRequired)
	if err != nil {
		return err
	}
	plpID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}

	rebuilt, err := device.startTransport(system)
	if err != nil {
		return err
	}
	// The supply on the aerial lead is the card's, reached through a vendor property
	// rather than the bus: the graph was built against a formality, so the
	// polarization the BDA request carried was for another channel.
	//
	// A quattro-fed card is left alone: its leads are all live at their own band,
	// and polarization there means which lead to listen to.
	if system.IsSatellite() && !device.model.quattroFed {
		supply, err := tuning.SatelliteSupply(properties)
		if err != nil {
			return err
		}
		if err := tuning.ApplySupply(supply, device.supply().Set); err != nil {
			return err
		}
	}
	wakeup := sony.WakeupParams{
		BandwidthMHz:  bandwidthMHz,
		PLPID:         plpID,
		SymbolRateSPS: uint32(symbolRate),
		StreamID:      uint32(streamID),
	}
	if err := device.demod.Wakeup(system, wakeup); err != nil {
		return fmt.Errorf("wake the demodulator for %s: %w", system, err)
	}
	// The tone comes after the wake-up rather than beside the voltage: its register
	// block is on the chip's satellite side, where the wake-up puts it.
	if system.IsSatellite() {
		// A satellite demodulator with a supply of its own is told after the bridge
		// GPIO.
		if regulator, ok := device.demod.(interface{ SetVoltage(bool, bool) error }); ok {
			supply, err := tuning.SatelliteSupply(properties)
			if err != nil {
				return err
			}
			if err := tuning.ApplySupply(supply, regulator.SetVoltage); err != nil {
				return fmt.Errorf("set the demodulator's LNB voltage: %w", err)
			}
		}
		if toner, ok := device.demod.(interface{ SetTone(bool) error }); ok {
			if err := toner.SetTone(tuning.ToneWanted(properties)); err != nil {
				return fmt.Errorf("set the 22 kHz tone: %w", err)
			}
		}
	}
	device.frequency = uint32(profile.FrequencyKHz)
	if err := device.demod.Tune(device.frequency); err != nil {
		return fmt.Errorf("tune the demodulator: %w", err)
	}
	if !rebuilt {
		// The wake-up took the re-acquisition path, which turns the stream output
		// off on its way in and leaves finishing the tune to the caller.
		if finisher, ok := device.demod.(interface{ TuneEnd() error }); ok {
			if err := finisher.TuneEnd(); err != nil {
				return fmt.Errorf("finish the tune: %w", err)
			}
		}
		// And no graph was built, so no vendor tune is on its way to drag it off.
		return nil
	}
	return device.holdTune(system, wakeup)
}

// holdTune keeps the hardware on the channel it was just asked for.
//
// The vendor driver tunes as it brings its graph up, and that tune can land after
// this one -- measured at about 1.5s on a 6205se. Nothing else shows it: the
// demodulator locks and reports a ratio and a level for the channel it was
// dragged to, so a scan came back with one channel's stream under every name.
// The tuner's own registers are the only witness, so the tune is read back and
// re-applied until it stays put past the moment the vendor's tune lands.
func (device *Device) holdTune(system sony.System, wakeup sony.WakeupParams) error {
	reader, ok := device.demod.(interface{ TunedKHz() (uint32, error) })
	if !ok {
		return nil
	}
	deadline := time.Now().Add(tuneHoldTimeout)
	for {
		at, err := reader.TunedKHz()
		if err != nil {
			// The read-back is a courtesy not every tuner offers; leave it tuned.
			return nil
		}
		if at == device.frequency && time.Since(device.transportStarted) >= vendorTuneSettles {
			return nil
		}
		if time.Now().After(deadline) {
			if at == device.frequency {
				return nil
			}
			return fmt.Errorf("the vendor driver keeps retuning this tuner: asked for %d kHz, it sits at %d kHz",
				device.frequency, at)
		}
		if at != device.frequency {
			// Waking it again too: whatever moved the tuner drove its acquisition.
			if err := device.demod.Wakeup(system, wakeup); err != nil {
				return fmt.Errorf("wake the demodulator for %s: %w", system, err)
			}
			if err := device.demod.Tune(device.frequency); err != nil {
				return fmt.Errorf("tune the demodulator: %w", err)
			}
			// A wake-up that found the demodulator already in this system turns the
			// stream off on its way in; without this the card carries nothing.
			if finisher, ok := device.demod.(interface{ TuneEnd() error }); ok {
				if err := finisher.TuneEnd(); err != nil {
					return fmt.Errorf("finish the tune: %w", err)
				}
			}
		}
		time.Sleep(tuneHoldInterval)
	}
}

const (
	// vendorTuneSettles is how long after the graph came up the vendor driver's own
	// tune may still land. Measured at about 1.5s on a 6205se, plus margin.
	vendorTuneSettles = 2500 * time.Millisecond
	// tuneHoldInterval is how often the tuner is asked where it is.
	tuneHoldInterval = 150 * time.Millisecond
	// tuneHoldTimeout gives up rather than fighting a driver for ever.
	tuneHoldTimeout = 8 * time.Second
)

// startTransport brings the graph up, and the demodulator with it, on every tune.
// Neither depends on the channel, but both depend on things a tune does not leave
// alone:
//
//   - The bus is a pin of the running graph, and the graph goes with the
//     transport stream, so a caller that streams, stops and tunes the next
//     channel came back to a pin that no longer existed (E_HANDLE).
//   - A channel that does not lock leaves the graph standing and starved, and it
//     does not pick up again when the next one locks.
//
// The cost is a graph and one demodulator bring-up per tune, about 65ms median.
// Writing the card's mode is still done only when needed, which attach guards.
func (device *Device) startTransport(system sony.System) (rebuilt bool, err error) {
	rebuilt = !device.reusableGraph(system)
	if rebuilt {
		device.restoreForRebuild()
		if err := device.attach(system); err != nil {
			return false, err
		}
		// When the vendor driver's own tune started; holdTune counts from here.
		device.transportStarted = time.Now()
	}
	// A demodulator already in this standard is left alone, which is what the vendor
	// driver does on a channel change. Initializing one here while the card was
	// carrying a stream cost the transport its sync until a new graph was built.
	if rebuilt || device.system != system {
		demod := device.demodFor(system)
		if err := demod.Initialize(); err != nil {
			return rebuilt, fmt.Errorf("initialize the demodulator: %w", err)
		}
		device.demod = demod
	}
	device.system = system
	device.carriedAtTune = device.transport.Written()
	return rebuilt, nil
}

// reusableGraph is whether the graph standing from the last tune can serve this
// one, which is what makes a channel change cheap: no graph to build and no
// vendor tune to wait out, about 2.4s saved per channel on a 6205se.
//
// Three things have to hold. The bus has to still be a pin of a running graph,
// or every write fails with E_HANDLE. The card has to already be in the mode
// this standard wants, since changing it rebuilds the graph anyway. And the
// graph has to have carried something since the last tune: one left starved by
// a channel that did not lock never picks up again, and running it again does
// not revive it.
func (device *Device) reusableGraph(system sony.System) bool {
	if device.demod == nil || device.bus == nil || !device.transport.TransportLive() {
		return false
	}
	mode, err := device.modeToUse(system)
	if err != nil || mode != device.vendorMode {
		return false
	}
	return device.transport.Written() > device.carriedAtTune
}

// restoreForRebuild puts the demodulator back into the standard the card's own
// mode names, before the graph that is about to be built tunes it.
//
// The vendor driver tunes as it brings a graph up, and on a demodulator left in
// another standard with nothing locked it waits out its own lock timeout rather
// than failing: 4.9s per channel, measured on a 6205se.
//
// Only where the bus is still live: a stream that was taken and closed has taken
// the graph with it. That is exactly where the trouble is, since a channel that
// did not lock takes no stream. It costs a bring-up and a wake, about 130ms.
func (device *Device) restoreForRebuild() {
	if device.demod == nil || device.bus == nil {
		return
	}
	state, ok := device.model.states[device.vendorMode]
	if !ok || len(state.systems) == 0 {
		return
	}
	if _, err := device.transport.Bus(); err != nil {
		return
	}
	restored := device.demodFor(state.systems[0])
	if err := restored.Initialize(); err != nil {
		return
	}
	_ = restored.Wakeup(state.systems[0], sony.WakeupParams{
		BandwidthMHz:  state.bandwidthMHz,
		PLPID:         -1,
		SymbolRateSPS: state.symbolRateSPS,
	})
}

// demodFor picks which of the card's demodulators a standard wants. The card's
// mode settles which is wired to the transport stream, so this choice and the
// mode agree because both are made from the same delivery system.
func (device *Device) demodFor(system sony.System) demodulator {
	switch {
	case device.model.stid135 != nil:
		return newSTID135Demod(device.bus, *device.model.stid135, device.slot)
	case device.model.stv091x != nil:
		return newSTV091xDemod(device.bus, *device.model.stv091x, device.slot)
	case device.model.mxl58x != nil:
		return newMXL58xDemod(device.bus, device.firmwareDir, *device.model.mxl58x, device.slot)
	case device.model.si2183 != nil:
		// One demodulator for the whole card, whichever standard is wanted.
		return newSi2183Demod(device.bus, device.firmwareDir, *device.model.si2183, device.slot)
	case device.model.newDemod == nil:
		// Nothing but the satellite part: no Sony chip to fall through to.
		return device.newSatelliteDemod()
	case device.model.splitBySlot():
		// Which tuner this is settles it; the delivery system has nothing to say.
		if device.model.satelliteOnly(device.slot) {
			return device.newSatelliteDemod()
		}
	case device.model.hasSatellite() && system.IsSatellite():
		return device.newSatelliteDemod()
	}
	return device.model.newDemod(device.bus, device.config)
}

// newSatelliteDemod builds whichever satellite chip this card carries.
func (device *Device) newSatelliteDemod() demodulator {
	if card := device.model.satelliteTAS; card != nil {
		return newTAS2101Demod(device.bus, *card, device.slot)
	}
	return newSatelliteDemod(device.bus, device.firmwareDir, device.model.satelliteFor(device.slot))
}

// refreshBus points the I2C bus at a live graph, building one if the graph the
// bus came from has gone. The transport has a bus to give only while a session
// stands.
func (device *Device) refreshBus() error {
	if device.bus == nil {
		return errors.New("the tuner was never attached")
	}
	bus, err := device.transport.Bus()
	if err != nil {
		state, ok := device.model.states[device.vendorMode]
		if !ok {
			return fmt.Errorf("the card is in %s, which this backend has no graph for",
				device.modeName(device.vendorMode))
		}
		profile, buildErr := bda.BuildDrivenProfile(state.carrier)
		if buildErr != nil {
			return buildErr
		}
		if err := device.transport.StartTransport(profile); err != nil {
			return fmt.Errorf("start the card's transport again: %w", err)
		}
		if bus, err = device.transport.Bus(); err != nil {
			return fmt.Errorf("reach the tuner's I2C bus again: %w", err)
		}
	}
	// Replaced in place: the demodulator was handed this bus and holds it still.
	device.bus.Properties = bus.Properties
	return nil
}

// supply is this tuner's low-noise block supply, on the property set the bus
// uses.
func (device *Device) supply() *tbsvendor.Supply {
	return &tbsvendor.Supply{Properties: device.bus.Properties, Name: device.transport.Name()}
}

// modeName renders a mode the way the card that answered it would: the numbers
// are not a shared vocabulary, and the bare number is the answer with no card
// to ask.
func (device *Device) modeName(mode bda.DemodMode) string {
	if device.transport == nil {
		return fmt.Sprintf("mode %d", byte(mode))
	}
	return device.transport.ModeName(mode)
}

// modeToUse is the mode the card has to be in for this tune. Two things call
// for a change: on a card whose mode picks the demodulator class, it has to
// agree with the side of the tuner the standard is on; and a mode this backend
// has no graph for is a dead end to be moved out of. Recovery passes no system.
func (device *Device) modeToUse(system sony.System) (bda.DemodMode, error) {
	_, usable := device.model.states[device.vendorMode]
	if usable && !device.model.modeSelectsFilter {
		return device.vendorMode, nil
	}
	if system == sony.SystemUnspecified {
		if usable {
			return device.vendorMode, nil
		}
		return bda.ModeUnknown, fmt.Errorf(
			"the card is in %s, which this backend has no graph for",
			device.modeName(device.vendorMode))
	}
	mode, ok := device.model.modeFor(system)
	if !ok {
		return bda.ModeUnknown, fmt.Errorf("this card has no mode that receives %s", system)
	}
	return mode, nil
}

// attach brings the graph up and takes hold of the tuner's I2C bus without
// touching the demodulator: reading what it is set to has to happen before
// anything resets it. system only chooses the mode the card has to be in; the
// graph is always built against a formality.
func (device *Device) attach(system sony.System) error {
	wanted, err := device.modeToUse(system)
	if err != nil {
		return err
	}
	state, ok := device.model.states[wanted]
	if !ok {
		return fmt.Errorf("the card is in %s, which this backend has no graph for",
			device.transport.ModeName(wanted))
	}
	if wanted != device.vendorMode {
		// Tune is the only way in, and it writes the mode from the channel it is
		// given -- so it is given the formality.
		if err := device.transport.Tune(state.carrier); err != nil {
			return fmt.Errorf("put the card into %s: %w", device.transport.ModeName(wanted), err)
		}
		current, err := device.transport.Mode()
		if err != nil {
			return fmt.Errorf("read the card's demodulator mode: %w", err)
		}
		device.vendorMode = current
	} else {
		profile, err := bda.BuildDrivenProfile(state.carrier)
		if err != nil {
			return err
		}
		if err := device.transport.StartTransport(profile); err != nil {
			return fmt.Errorf("start the card's transport: %w", err)
		}
	}
	bus, err := device.transport.Bus()
	if err != nil {
		return fmt.Errorf("reach the tuner's I2C bus: %w", err)
	}
	bus.BehindRepeater(device.config.DemodAddress, device.config.TunerAddress)
	if device.bus != nil {
		// Borrowed from the replaced session, so closing it lets go of nothing new.
		_ = device.bus.Close()
	}
	device.bus = bus
	return nil
}

// Status reads the demodulator rather than asking the vendor driver: BDA offers a
// quality percentage of its own devising and no carrier-to-noise ratio at all.
func (device *Device) Status() (Status, error) {
	if device.demod == nil {
		return Status{}, fmt.Errorf("the tuner is not tuned")
	}
	// The demodulator is reached through a pin of the vendor's graph, and the graph
	// goes when the stream taken from it is closed. The demodulator object outlives
	// it holding the pin, so asking it anything after that calls through freed
	// memory (0xc0000005, seen in the property set). It is asked first.
	if !device.transport.TransportLive() {
		return Status{}, fmt.Errorf("the tuner is not tuned")
	}
	lock, err := device.demod.Lock()
	if err != nil {
		return Status{}, err
	}
	status := Status{Locked: lock.Locked}
	if lock.Synced {
		status.Names = append(status.Names, "SYNC")
	}
	if lock.Locked {
		status.Names = append(status.Names, "LOCK")
	}
	// Both are optional: an unlocked demodulator has no ratio to report.
	if cnr, err := device.demod.CNR(); err == nil {
		status.CNR = Measurement{Value: cnr, Known: true}
	}
	if strength, err := device.demod.SignalStrength(device.frequency); err == nil {
		status.Strength = Measurement{Value: strength, Known: true}
	}
	return status, nil
}

// Measurement is a reading the hardware may not offer. Zero is a real reading,
// so whether one was taken is carried separately.
type Measurement struct {
	Value float64
	Known bool
}

type Status struct {
	Locked   bool
	Names    []string
	CNR      Measurement
	Strength Measurement
}

// OpenStream hands over the transport stream the card's DMA is filling.
func (device *Device) OpenStream() (*bda.Stream, error) {
	return device.transport.OpenStream()
}

// Close puts the demodulator back the way the vendor driver expects to find it,
// and only then lets go. That driver initialises its demodulator once, when the
// mode is written, so a card left in another standard stops working through the
// vendor path until its mode is written twice.
func (device *Device) Close() error {
	device.handBack()
	if device.bus != nil {
		_ = device.bus.Close()
		device.bus = nil
	}
	device.demod = nil
	return device.transport.Close()
}

func (device *Device) handBack() {
	if device.demod == nil {
		return
	}
	// By the time a device is closed the graph has usually gone with the stream, and
	// what the bus holds is then a released COM object -- calling one faults the
	// process. So the graph is put back up first when it is not there.
	if err := device.refreshBus(); err != nil {
		return
	}
	state, ok := device.model.states[device.vendorMode]
	if !ok || len(state.systems) == 0 {
		return
	}
	restored := device.demodFor(state.systems[0])
	if err := restored.Initialize(); err != nil {
		return
	}
	_ = restored.Wakeup(state.systems[0], sony.WakeupParams{
		BandwidthMHz:  state.bandwidthMHz,
		PLPID:         -1,
		SymbolRateSPS: state.symbolRateSPS,
	})
}

// systemFor maps a channel file's delivery system to the demodulator's own.
func systemFor(delivery string) (sony.System, error) {
	switch tuning.DemodDeliverySystem(delivery) {
	case "DVBT":
		return sony.SystemDVBT, nil
	case "DVBT2":
		return sony.SystemDVBT2, nil
	case "DVBC", "DVBC/ANNEXA":
		return sony.SystemDVBC, nil
	case "DVBC/ANNEXB":
		return sony.SystemJ83B, nil
	case "ISDBT":
		return sony.SystemISDBT, nil
	case "ATSC":
		return sony.SystemATSC, nil
	case "DVBS":
		return sony.SystemDVBS, nil
	case "DVBS2", "DVBS2X":
		// The chip has no separate DVB-S2X core; both map to one entry.
		return sony.SystemDVBS2, nil
	case "ISDBS":
		return sony.SystemISDBS, nil
	case "ISDBS3":
		return sony.SystemISDBS3, nil
	}
	return sony.SystemUnspecified, fmt.Errorf("this backend does not support %q", delivery)
}
