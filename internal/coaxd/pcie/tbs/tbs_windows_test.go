// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"slices"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/demod/m88rs6060"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/demod/stid135"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// TestEveryDeliveryHasAMode: no card advertises a delivery system no mode of
// its own receives.
func TestEveryDeliveryHasAMode(t *testing.T) {
	for _, card := range models {
		for _, delivery := range card.deliveries {
			system, err := systemFor(delivery)
			if err != nil {
				t.Errorf("%s advertises %q, which maps to no demodulator system: %v",
					card.match, delivery, err)
				continue
			}
			if _, ok := card.modeFor(system); !ok {
				t.Errorf("%s advertises %q, which no mode of its own receives",
					card.match, delivery)
			}
		}
	}
}

// TestBorrowedIsNotHandedBack pins the distinction the 6812 needs: J.83B is
// reached through the terrestrial mode, but the vendor driver has no mode for
// it, so a tuner must never be handed back in it. Failure is quiet -- the card
// works until the vendor driver next uses it.
func TestBorrowedIsNotHandedBack(t *testing.T) {
	for _, card := range models {
		for mode, state := range card.states {
			if len(state.systems) == 0 {
				t.Errorf("%s mode %d has nothing to hand back", card.match, mode)
			}
			for _, borrowed := range state.borrowed {
				for _, native := range state.systems {
					if borrowed == native {
						t.Errorf("%s mode %d lists %s as both its own and borrowed",
							card.match, mode, borrowed)
					}
				}
			}
		}
	}
}

// TestCarriersAreBuildable: every mode's formality channel is one the BDA
// layer accepts and names that mode's own standard, since tuning it is what
// writes the mode when a card has to be moved into it.
func TestCarriersAreBuildable(t *testing.T) {
	for _, card := range models {
		for mode, state := range card.states {
			if _, err := bda.BuildTuneProfile(state.carrier); err != nil {
				t.Errorf("%s mode %d has an unbuildable carrier: %v", card.match, mode, err)
				continue
			}
			// The raw delivery name is compared, not the built profile's: the
			// profile normalises names its own way, and the raw name is what
			// systemFor is given in a real tune.
			delivery := state.carrier["DELIVERY_SYSTEM"]
			system, err := systemFor(delivery)
			if err != nil {
				t.Errorf("%s mode %d carrier names %q: %v",
					card.match, mode, delivery, err)
				continue
			}
			if system != state.systems[0] {
				t.Errorf("%s mode %d carrier is %s but the mode's own standard is %s",
					card.match, mode, system, state.systems[0])
			}
		}
	}
}

// TestTBS6812: a tuner address per slot, MMT/TLV on both, J.83B borrowed
// through the terrestrial mode, the Japanese satellite pair native to the
// satellite one, and the delivery names normalizing like a channel file's.
func TestTBS6812(t *testing.T) {
	card, ok := modelFor("TBS 6812 Tuner 1")
	if !ok {
		t.Fatal("the 6812 is not recognised by its tuner's name")
	}
	if slot := slotFromName("TBS 6812 Tuner 1"); slot != 1 {
		t.Errorf("second tuner resolved to slot %d, want 1", slot)
	}
	for slot, want := range map[int]byte{0: 0x60, 1: 0x61} {
		config := card.demodFor(slot)
		if config.TunerAddress != want {
			t.Errorf("slot %d tuner address is %#x, want %#x", slot, config.TunerAddress, want)
		}
		if config.TunerIndex != byte(slot+1) {
			t.Errorf("slot %d tuner index is %d, want %d", slot, config.TunerIndex, slot+1)
		}
		if !config.TLVMode {
			t.Errorf("slot %d should carry MMT/TLV", slot)
		}
	}
	for system, wantMode := range map[sony.System]bda.DemodMode{
		sony.SystemISDBT:  isdbModeTerrestrial,
		sony.SystemJ83B:   isdbModeTerrestrial,
		sony.SystemISDBS:  isdbModeSatellite,
		sony.SystemISDBS3: isdbModeSatellite,
	} {
		mode, ok := card.modeFor(system)
		if !ok || mode != wantMode {
			t.Errorf("%s resolves to mode %d (found=%v), want %d", system, mode, ok, wantMode)
		}
	}
	// J.83B is borrowed, so the terrestrial mode hands back ISDB-T.
	if got := card.states[isdbModeTerrestrial].systems[0]; got != sony.SystemISDBT {
		t.Errorf("terrestrial mode hands back %s, want ISDB-T", got)
	}
	if !Supports("DVB-C/ANNEX_B") || !Supports("ISDBS3") {
		t.Error("the 6812's delivery systems should be reported as supported")
	}
	if tuning.Normalize("DVB-C/ANNEX_B") != tuning.Normalize("DVBC/ANNEX_B") {
		t.Error("the delivery name this card advertises should normalize like a channel file's")
	}
}

// TestModeToUse pins when this backend moves a card's mode. The 6205SE's is
// normally untouched, since the demodulator is driven over I2C and the graph is
// a formality -- except when the card is parked in a mode this backend has no
// graph for, where the card-wide switch is the only way out.
func TestModeToUse(t *testing.T) {
	dvb, ok := modelFor("TBS 6205se Tuner 0")
	if !ok {
		t.Fatal("the 6205SE is not recognised")
	}
	isdb, ok := modelFor("TBS 6812 Tuner 0")
	if !ok {
		t.Fatal("the 6812 is not recognised")
	}
	for _, test := range []struct {
		name    string
		card    model
		in      bda.DemodMode
		want    sony.System
		expect  bda.DemodMode
		wantErr bool
	}{
		{"6205SE in a mode it can use is left alone",
			dvb, bda.ModeDVBT, sony.SystemISDBT, bda.ModeDVBT, false},
		{"6205SE parked in DVB-S is moved to what the channel needs",
			dvb, bda.ModeDVBS, sony.SystemISDBT, bda.ModeISDBT, false},
		{"6205SE parked in DVB-S with no channel to aim at is an error",
			dvb, bda.ModeDVBS, sony.SystemUnspecified, bda.ModeUnknown, true},
		{"6205SE recovery in a usable mode is left alone",
			dvb, bda.ModeISDBT, sony.SystemUnspecified, bda.ModeISDBT, false},
		{"6812 always follows the standard's side",
			isdb, isdbModeTerrestrial, sony.SystemISDBS3, isdbModeSatellite, false},
		{"6812 reaches J.83B through its terrestrial mode",
			isdb, isdbModeSatellite, sony.SystemJ83B, isdbModeTerrestrial, false},
		{"6812 recovery is left alone",
			isdb, isdbModeSatellite, sony.SystemUnspecified, isdbModeSatellite, false},
	} {
		device := &Device{model: test.card, vendorMode: test.in}
		got, err := device.modeToUse(test.want)
		if (err != nil) != test.wantErr {
			t.Errorf("%s: error = %v, wantErr %v", test.name, err, test.wantErr)
			continue
		}
		if err == nil && got != test.expect {
			t.Errorf("%s: mode = %d, want %d", test.name, got, test.expect)
		}
	}
}

// TestModelsCoverOnlyTheirOwnTuners checks the drives guard, which keeps this
// backend off a tuner whose chip it has no driver for. The 6522H is why it
// exists: its last two tuners carry a satellite chip that once had no driver
// here.
func TestModelsCoverOnlyTheirOwnTuners(t *testing.T) {
	// Every card in the table is driven whole, so the guard stops none of the
	// names below.
	for name := range map[string]bool{
		"TBS 6522H Tuner 0":  true,
		"TBS 6522H Tuner 1":  true,
		"TBS 6522H Tuner 2":  true,
		"TBS 6522H Tuner 3":  true,
		"TBS 6205se Tuner 3": true,
		"TBS 6281TD Tuner 1": true,
		"TBS 6902 Tuner 1":   true,
	} {
		card, known := modelFor(name)
		if !known {
			t.Errorf("%q is not recognised", name)
			continue
		}
		if !card.drivesSlot(slotFromName(name)) {
			t.Errorf("%q was stopped by the guard, and its card is driven whole", name)
		}
	}

	// The other side of the guard, which no entry in the table exercises: an
	// entry that covers only the first two tuners of its card.
	partial := model{match: "TBS 0000", drives: 2}
	for slot, want := range map[int]bool{0: true, 1: true, 2: false, 3: false} {
		if got := partial.drivesSlot(slot); got != want {
			t.Errorf("an entry driving 2 tuners answers %v for tuner %d, want %v", got, slot, want)
		}
	}
}

// TestEveryModelKnowsWhatItsModesMean guards the table: every card has modes,
// one demodulator driver, and a mode for each delivery system it offers.
func TestEveryModelKnowsWhatItsModesMean(t *testing.T) {
	for _, card := range models {
		if len(card.states) == 0 {
			t.Errorf("%s has no modes", card.match)
		}
		// The Sony part, or on satellite-only cards one of the satellite parts.
		if card.newDemod == nil && !card.hasSatellite() &&
			card.si2183 == nil && card.mxl58x == nil &&
			card.stv091x == nil && card.stid135 == nil {
			t.Errorf("%s has no driver", card.match)
		}
		chips := 0
		for _, carried := range []bool{
			card.satellite != nil, card.satelliteTAS != nil,
			card.si2183 != nil, card.mxl58x != nil,
			card.stv091x != nil, card.stid135 != nil,
		} {
			if carried {
				chips++
			}
		}
		if chips > 1 {
			t.Errorf("%s carries %d satellite chips at once", card.match, chips)
		}
		for _, delivery := range card.deliveries {
			system, err := systemFor(delivery)
			if err != nil {
				t.Errorf("%s offers %q: %v", card.match, delivery, err)
				continue
			}
			if _, mapped := card.modeFor(system); !mapped {
				t.Errorf("%s offers %q with no mode to reach it in", card.match, delivery)
			}
		}
	}
}

// TestDemodAddressesCycleWithTheBus: on a card with two chips per bus the
// address tells them apart, and a wrong one configures another demodulator.
func TestDemodAddressesCycleWithTheBus(t *testing.T) {
	card, ok := modelFor("TBS 6209SE Tuner 0")
	if !ok {
		t.Fatal("the 6209 is not in the table")
	}
	want := []byte{0x64, 0x6c, 0x65, 0x6d, 0x64, 0x6c, 0x65, 0x6d}
	for slot, address := range want {
		if got := card.demodFor(slot).DemodAddress; got != address {
			t.Errorf("tuner %d talks to %#02x, want %#02x", slot, got, address)
		}
	}
	// A card with a bus per tuner leaves them all on one address.
	single, ok := modelFor("TBS 6205se Tuner 3")
	if !ok {
		t.Fatal("the 6205SE is not in the table")
	}
	for slot := 0; slot < 4; slot++ {
		if got := single.demodFor(slot).DemodAddress; got != 0x64 {
			t.Errorf("6205SE tuner %d talks to %#02x, want 0x64", slot, got)
		}
	}
}

// TestTheWiringIsPerCard checks the transport-stream wiring: serial must not
// be configured as parallel or the other way round. It is a property of the
// card, so slot 0 stands for all of its tuners.
func TestTheWiringIsPerCard(t *testing.T) {
	for name, serial := range map[string]bool{
		"TBS 6205se Tuner 0": false,
		"TBS 6281TD Tuner 0": false,
		"TBS 6522H Tuner 0":  false,
		"TBS 6812 Tuner 0":   false,
		"TBS 6209SE Tuner 0": true,
		"TBS 6290TD Tuner 1": true,
	} {
		card, ok := modelFor(name)
		if !ok {
			t.Errorf("%q is not in the table", name)
			continue
		}
		if got := card.demodFor(0).TSSerial; got != serial {
			t.Errorf("%q: serial = %v, want %v", name, got, serial)
		}
	}
}

// TestTBS6909SE: eight satellite tuners, two chips per bus told apart by
// address, only the even ones with their tone pin brought out.
func TestTBS6909SE(t *testing.T) {
	card, ok := modelFor("TBS 6909SE Tuner 3")
	if !ok {
		t.Fatal("the 6909SE is not recognised")
	}
	if card.newDemod != nil {
		t.Error("this card has no Sony part to fall back on")
	}
	if card.satellite == nil {
		t.Fatal("the satellite configuration is missing")
	}
	for slot, want := range map[int]struct {
		address byte
		noTone  bool
	}{
		0: {0x69, false},
		1: {0x6b, true},
		2: {0x69, false},
		7: {0x6b, true},
	} {
		got := card.satelliteFor(slot)
		if got.DemodAddress != want.address {
			t.Errorf("tuner %d is at %#02x, want %#02x", slot, got.DemodAddress, want.address)
		}
		if got.NoTone != want.noTone {
			t.Errorf("tuner %d NoTone = %v, want %v", slot, got.NoTone, want.noTone)
		}
		if got.TunerAddress != 0x2c {
			t.Errorf("tuner %d tuner address %#02x, want 0x2c", slot, got.TunerAddress)
		}
	}
	for _, slot := range []int{0, 1, 7} {
		got := card.deliveriesFor(slot)
		if len(got) != 2 || got[0] != "DVB-S" || got[1] != "DVB-S2" {
			t.Errorf("tuner %d receives %v", slot, got)
		}
	}
}

// TestSplitCardsChooseByTuner: the 6504H and 6522H put their two chips on
// different tuners, so the tuner number settles which chip it has.
func TestSplitCardsChooseByTuner(t *testing.T) {
	for _, one := range []struct {
		name      string
		from      int
		satellite []byte
	}{
		{"TBS 6522H Tuner 1", 2, nil},
		{"TBS 6504H Tuner 1", 4, []byte{0x69, 0x6b}},
	} {
		card, ok := modelFor(one.name)
		if !ok {
			t.Fatalf("%s is not recognised", one.name)
		}
		if !card.splitBySlot() || card.satelliteFrom != one.from {
			t.Errorf("%s splits at %d, want %d", one.name, card.satelliteFrom, one.from)
		}
		for slot := 0; slot < one.from; slot++ {
			if card.satelliteOnly(slot) {
				t.Errorf("%s tuner %d should be the terrestrial half", one.name, slot)
			}
			if got := card.deliveriesFor(slot); !slices.Equal(got, card.deliveries) {
				t.Errorf("%s tuner %d receives %v, want the card's %v",
					one.name, slot, got, card.deliveries)
			}
		}
		if !card.satelliteOnly(one.from) {
			t.Errorf("%s tuner %d should be the satellite half", one.name, one.from)
		}
		if got := card.deliveriesFor(one.from); !slices.Equal(got, card.satelliteDeliveries) {
			t.Errorf("%s tuner %d receives %v, want the satellite half's %v",
				one.name, one.from, got, card.satelliteDeliveries)
		}
		if len(one.satellite) != 0 {
			if got := card.satelliteFor(one.from).DemodAddress; got != one.satellite[0] {
				t.Errorf("%s tuner %d is at %#02x, want %#02x", one.name, one.from, got, one.satellite[0])
			}
			if got := card.satelliteFor(one.from + 1).DemodAddress; got != one.satellite[1] {
				t.Errorf("%s tuner %d is at %#02x, want %#02x", one.name, one.from+1, got, one.satellite[1])
			}
		}
	}
}

// TestTBS6216CyclesBothAddresses: the 6216 alternates its tuner address as
// well as its demodulator's -- no other card here does -- and has more tuners
// than configurations, so both cycle.
func TestTBS6216CyclesBothAddresses(t *testing.T) {
	card, ok := modelFor("TBS 6216 Tuner 5")
	if !ok {
		t.Fatal("the 6216 is not recognised")
	}
	for slot, want := range map[int][2]byte{
		0:  {0x64, 0x60},
		1:  {0x6c, 0x63},
		2:  {0x64, 0x60},
		15: {0x6c, 0x63},
	} {
		got := card.demodFor(slot)
		if got.DemodAddress != want[0] || got.TunerAddress != want[1] {
			t.Errorf("tuner %d is at %#02x/%#02x, want %#02x/%#02x",
				slot, got.DemodAddress, got.TunerAddress, want[0], want[1])
		}
		if !got.TSSerial {
			t.Errorf("tuner %d should take its stream serially", slot)
		}
	}
}

// TestTAS2101Cards: the three cards whose satellite chip is the Tmax part with
// a tuner beside it rather than the single-package Montage one.
func TestTAS2101Cards(t *testing.T) {
	for _, one := range []struct {
		name    string
		slot    int
		demod   byte
		tuner   byte
		init0   byte
		unmoded bool
	}{
		{"TBS 6902 Tuner 0", 0, 0x60, 0x62, 0xb0, true},
		{"TBS 6902 Tuner 1", 1, 0x68, 0x62, 0xb0, true},
		{"TBS 6904 Tuner 2", 2, 0x60, 0x63, 0xb0, true},
		{"TBS 6904 Tuner 3", 3, 0x68, 0x63, 0xb0, true},
		{"TBS 6910 Tuner 0", 0, 0x68, 0x62, 0x21, true},
		{"TBS 6910 Tuner 1", 1, 0x60, 0x62, 0xb0, true},
	} {
		card, ok := modelFor(one.name)
		if !ok {
			t.Errorf("%s is not recognised", one.name)
			continue
		}
		if card.satelliteTAS == nil {
			t.Errorf("%s carries no TAS2101", one.name)
			continue
		}
		if card.newDemod != nil {
			t.Errorf("%s has no Sony part to fall back on", one.name)
		}
		demod, tuner := card.satelliteTAS.configFor(one.slot)
		if demod.DemodAddress != one.demod {
			t.Errorf("%s is at %#02x, want %#02x", one.name, demod.DemodAddress, one.demod)
		}
		if demod.Init[0] != one.init0 {
			t.Errorf("%s takes board bytes starting %#02x, want %#02x",
				one.name, demod.Init[0], one.init0)
		}
		if tuner.Address != one.tuner {
			t.Errorf("%s tuner is at %#02x, want %#02x", one.name, tuner.Address, one.tuner)
		}
		// These cards have no mode property, so their one state is filed under
		// the value an unasked card answers with. Every row is such a card;
		// the flag is kept for one that is not.
		if one.unmoded {
			if _, filed := card.states[bda.ModeUnknown]; !filed {
				t.Errorf("%s has no state to build a graph from", one.name)
			}
		}
		got := card.deliveriesFor(one.slot)
		if len(got) != 2 || got[0] != "DVB-S" || got[1] != "DVB-S2" {
			t.Errorf("%s receives %v", one.name, got)
		}
	}
}

// TestSatelliteOnlyCardsAreNotRecovered guards the table invariant recoverOne
// leans on: a card with no Sony driver must not name a Sony address either, or
// the repair path would read a register where nothing is listening.
func TestSatelliteOnlyCardsAreNotRecovered(t *testing.T) {
	for _, card := range models {
		if card.newDemod != nil {
			continue
		}
		if card.demod.DemodAddress != 0 {
			t.Errorf("%s has no Sony driver but names a Sony address", card.match)
		}
	}
}

// TestNamesAreNotMistakenForTheirSiblings guards how a card is recognised. This
// vendor names distinct cards by extending one number and the pairs carry
// different chips -- a 6902 has a TAS2101 where a 6902SE has an M88RS6060 -- so
// a substring match would hand one card's registers to the other.
func TestNamesAreNotMistakenForTheirSiblings(t *testing.T) {
	for name, want := range map[string]string{
		"TBS 6902 Tuner 0":   "TBS 6902",
		"TBS 6904 Tuner 0":   "TBS 6904",
		"TBS 6904X Tuner 0":  "TBS 6904X",
		"TBS 6522 Tuner 0":   "TBS 6522",
		"TBS 6522H Tuner 0":  "TBS 6522H",
		"TBS 6504 Tuner 0":   "TBS 6504",
		"TBS 6504H Tuner 0":  "TBS 6504H",
		"TBS 6812 Tuner 1":   "TBS 6812",
		"TBS 6909SE Tuner 0": "TBS 6909SE",
		"TBS 6909 Tuner 7":   "TBS 6909",
		"TBS 6903 Tuner 1":   "TBS 6903",
		"TBS 6903x Tuner 0":  "TBS 6903X",
		"TBS 6905 Tuner 3":   "TBS 6905",
		"TBS 6908 Tuner 0":   "TBS 6908",
		"TBS 6909x Tuner 0":  "TBS 6909X",
		"TBS 6910 Tuner 0":   "TBS 6910",
		"TBS 6910X Tuner 0":  "TBS 6910X",
		"TBS 6590 Tuner 0":   "TBS 6590",
		"TBS 6590SE Tuner 0": "TBS 6590SE",
		"TBS 6528 Tuner 0":   "TBS 6528",
		// Cards with no entry are left alone rather than handed a sibling's.
		"TBS 6902SE Tuner 0": "",
		"TBS 6904SE Tuner 0": "",
		"TBS 6910SE Tuner 0": "",
		"TBS 6209 Tuner 0":   "",
		"TBS 6205 Tuner 0":   "",
	} {
		card, known := modelFor(name)
		got := ""
		if known {
			got = card.match
		}
		if got != want {
			t.Errorf("%q resolved to %q, want %q", name, got, want)
		}
	}
}

// TestSi2183Cards: the wiring of the cards built round one Silicon Labs
// demodulator with a tuner on each side of the band.
func TestSi2183Cards(t *testing.T) {
	for _, one := range []struct {
		name        string
		slot        int
		demod       byte
		agc         byte
		terrestrial byte
		satellite   byte
	}{
		{"TBS 6504 Tuner 0", 0, 0x67, 0x05, 0x60, 0x63},
		{"TBS 6504 Tuner 1", 1, 0x64, 0x04, 0x61, 0x62},
		{"TBS 6522 Tuner 1", 1, 0x64, 0x04, 0x61, 0x62},
		// The 6508 alternates the other way round, and its satellite tuner is
		// the two halves of an RDA5816 rather than an AV201x.
		{"TBS 6508 Tuner 0", 0, 0x64, 0x04, 0x61, 0x08},
		{"TBS 6508 Tuner 3", 3, 0x67, 0x05, 0x60, 0x09},
		// The 6904X has no terrestrial tuner.
		{"TBS 6904X Tuner 0", 0, 0x67, 0x05, 0, 0x63},
		// The plain 6590 runs the other way round again; the 6528 has one
		// tuner and so nothing to alternate.
		{"TBS 6590 Tuner 0", 0, 0x64, 0x04, 0x60, 0x63},
		{"TBS 6590 Tuner 1", 1, 0x67, 0x05, 0x61, 0x62},
		{"TBS 6528 Tuner 0", 0, 0x67, 0x05, 0x61, 0x62},
	} {
		card, ok := modelFor(one.name)
		if !ok || card.si2183 == nil {
			t.Errorf("%s is not one of these cards", one.name)
			continue
		}
		demod, terrestrial, satellite := card.si2183.configFor(one.slot)
		if demod.DemodAddress != one.demod {
			t.Errorf("%s is at %#02x, want %#02x", one.name, demod.DemodAddress, one.demod)
		}
		if demod.AGCMode != one.agc {
			t.Errorf("%s AGC mode is %#02x, want %#02x", one.name, demod.AGCMode, one.agc)
		}
		// Picked up from the vendor driver rather than started, which is the
		// whole reason these can be driven at all.
		if !demod.AlreadyBooted {
			t.Errorf("%s would reboot a chip the vendor driver is using", one.name)
		}
		if one.terrestrial == 0 {
			if terrestrial != nil {
				t.Errorf("%s should have no terrestrial tuner", one.name)
			}
		} else if terrestrial == nil || terrestrial.Address != one.terrestrial {
			t.Errorf("%s terrestrial tuner is %v, want %#02x", one.name, terrestrial, one.terrestrial)
		}
		if one.satellite == 0 {
			if satellite.fitted() {
				t.Errorf("%s should have no satellite tuner", one.name)
			}
		} else if !satellite.fitted() || satellite.Address() != one.satellite {
			t.Errorf("%s satellite tuner is %#02x, want %#02x",
				one.name, satellite.Address(), one.satellite)
		}
	}
}

// TestSatelliteTransportModesAreStated guards a default that means the wrong
// thing: the Montage part's transport mode is serial at the zero value, while
// most cards carrying it stream in parallel, so every entry has to state it.
func TestSatelliteTransportModesAreStated(t *testing.T) {
	for name, want := range map[string]m88rs6060.TSMode{
		"TBS 6522H Tuner 2":  m88rs6060.TSModeParallel,
		"TBS 6504H Tuner 4":  m88rs6060.TSModeParallel,
		"TBS 6590SE Tuner 0": m88rs6060.TSModeParallel,
		"TBS 6910X Tuner 0":  m88rs6060.TSModeParallel,
		"TBS 6909SE Tuner 0": m88rs6060.TSModeSerial,
	} {
		card, ok := modelFor(name)
		if !ok || card.satellite == nil {
			t.Errorf("%s carries no Montage part", name)
			continue
		}
		if got := card.satelliteFor(slotFromName(name)).TSMode; got != want {
			t.Errorf("%s hands its stream over as %d, want %d", name, got, want)
		}
	}
}

// TestTBS6909IsOneChip pins the card that is one demodulator, not eight: it
// tells frontends apart by an index in the command payload rather than by I2C
// address, and a wrong index tunes another tuner while reporting this one's
// lock.
func TestTBS6909IsOneChip(t *testing.T) {
	card, ok := modelFor("TBS 6909 Tuner 5")
	if !ok || card.mxl58x == nil {
		t.Fatal("the 6909 is not recognised as a single-chip card")
	}
	if card.newDemod != nil || card.satellite != nil || card.satelliteTAS != nil {
		t.Error("this card carries no other demodulator")
	}
	if !card.mxl58x.AlreadyBooted {
		t.Error("a firmware download here would reset all eight frontends")
	}
	// The card's tuner number is the frontend index, one for one.
	for _, slot := range []int{0, 5, 7} {
		if got := mxl58xConfigFor(*card.mxl58x, slot).DemodIndex; got != uint32(slot) {
			t.Errorf("tuner %d drives frontend %d", slot, got)
		}
	}
	// One chip, so one address for all of them.
	if card.mxl58x.DemodAddress != 0x60 {
		t.Errorf("the chip is at %#02x, want 0x60", card.mxl58x.DemodAddress)
	}
}

// TestSTV091xHalvesAreCrossed pins what looks like a mistake and is not: one
// STV0910 is two demodulators and one STV6120 two tuners, shared by a pair of
// the card's tuners but wired crossed -- the demodulator half is the tuner
// number's low bit, the tuner half its complement. Straightening it would point
// each demodulator at the other's aerial.
func TestSTV091xHalvesAreCrossed(t *testing.T) {
	for _, name := range []string{"TBS 6903", "TBS 6905", "TBS 6908"} {
		card, ok := modelFor(name + " Tuner 0")
		if !ok || card.stv091x == nil {
			t.Errorf("%s is not one of these cards", name)
			continue
		}
		for slot, want := range map[int][2]int{
			0: {0, 1}, 1: {1, 0}, 2: {0, 1}, 3: {1, 0},
		} {
			demod, tuner := card.stv091x.halvesFor(slot)
			if got := [2]int{demod.Index, tuner.Index}; got != want {
				t.Errorf("%s tuner %d uses demodulator %d and tuner half %d, want %v",
					name, slot, got[0], got[1], want)
			}
		}
		// Both chips are taken over rather than brought up: either bring-up is
		// chip-wide and the other half may be somebody else's tuner.
		if !card.stv091x.demod.AlreadyBooted || !card.stv091x.tuner.AlreadyBooted {
			t.Errorf("%s would reset the sibling tuner on every tune", name)
		}
	}
}

// TestSTID135CardsPickTheirOwnDemodulators: the two cards built on the chip
// that is eight demodulators at one address. Nothing on the bus distinguishes
// them, so a wrong index tunes another tuner's demodulator -- and on the 6903X
// the two in use are not the first two.
func TestSTID135CardsPickTheirOwnDemodulators(t *testing.T) {
	for _, one := range []struct {
		name    string
		slot    int
		demod   int
		rfInput int
		quattro bool
	}{
		{"TBS 6903X Tuner 0", 0, 0, 0, false},
		{"TBS 6903X Tuner 1", 1, 2, 3, false},
		{"TBS 6909X Tuner 0", 0, 0, 0, true},
		{"TBS 6909X Tuner 5", 5, 5, 2, true},
		{"TBS 6909X Tuner 7", 7, 7, 3, true},
	} {
		card, ok := modelFor(one.name)
		if !ok || card.stid135 == nil {
			t.Errorf("%s is not one of these cards", one.name)
			continue
		}
		config := card.stid135.configFor(one.slot)
		if config.Demod != one.demod {
			t.Errorf("%s drives demodulator %d, want %d", one.name, config.Demod, one.demod)
		}
		if config.RFInput != one.rfInput {
			t.Errorf("%s is wired to input %d, want %d", one.name, config.RFInput, one.rfInput)
		}
		if quattro := config.Multiswitch == stid135.Quattro; quattro != one.quattro {
			t.Errorf("%s quattro = %v, want %v", one.name, quattro, one.quattro)
		}
		if !config.AlreadyBooted {
			t.Errorf("%s would try to bring up a chip seven other tuners are on", one.name)
		}
	}
}

// TestQuattroCardsLeaveTheSupplyAlone guards the one place where writing the
// vendor's LNB property would be wrong: a quattro block feeds four live leads,
// each at its own band and polarization, so switching the supply on one takes
// it from whichever tuner is listening.
func TestQuattroCardsLeaveTheSupplyAlone(t *testing.T) {
	for name, want := range map[string]bool{
		"TBS 6909 Tuner 0":   true,
		"TBS 6909X Tuner 0":  true,
		"TBS 6903X Tuner 0":  false,
		"TBS 6903 Tuner 0":   false,
		"TBS 6909SE Tuner 0": false,
	} {
		card, ok := modelFor(name)
		if !ok {
			t.Errorf("%s is not recognised", name)
			continue
		}
		if card.quattroFed != want {
			t.Errorf("%s quattroFed = %v, want %v", name, card.quattroFed, want)
		}
	}
}
