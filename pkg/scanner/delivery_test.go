// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"strconv"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
)

// TestOneByteReadTwoWays is the whole reason a convention has to be carried.
// ARIB gives the low five bits of the satellite descriptor's sixth byte to one
// modulation field; DVB divides the same five bits into a roll-off, a
// modulation system and a modulation type. Nothing in the descriptor says which
// was meant.
func TestOneByteReadTwoWays(t *testing.T) {
	// Right-hand circular, and low bits 01110: ARIB reads a modulation of 14,
	// DVB reads roll-off 25% (bits 4-3 = 01), second generation (bit 2), and
	// 8PSK (bits 1-0 = 10).
	described := &isdb.SatelliteDelivery{
		FrequencyKHz: 11727480, Polarisation: "R",
		Modulation: 0x0e, SymbolRate: 28860000, FECInner: 3,
	}

	arib, ok := SatelliteChannel(described, "ISDBS", Dish{})
	if !ok {
		t.Fatal("the Japanese reading refused a Japanese transponder")
	}
	// Nothing of the modulation byte reaches the channel: the demodulators are
	// told the standard, and ARIB's numbering is not one a channel file spells.
	for _, unwanted := range []string{"MODULATION", "ROLLOFF", "SYMBOL_RATE", "POLARIZATION", "INNER_FEC"} {
		if value, given := arib[unwanted]; given {
			t.Errorf("the Japanese reading wrote %s=%q", unwanted, value)
		}
	}
	if arib["DELIVERY_SYSTEM"] != "ISDBS" || arib["FREQUENCY"] != "1049480" {
		t.Errorf("the Japanese reading gave %v", arib)
	}

	dvb, ok := SatelliteChannel(described, "DVBS2", Dish{Block: "UNIVERSAL"})
	if !ok {
		t.Fatal("the DVB reading refused the descriptor")
	}
	for name, want := range map[string]string{
		"DELIVERY_SYSTEM": "DVBS2",
		// The downlink as the descriptor gave it: the daemon subtracts the
		// named block's oscillator.
		"FREQUENCY":    "11727480",
		"LNB":          "UNIVERSAL",
		"POLARIZATION": "R",
		"SYMBOL_RATE":  "28860000",
		"MODULATION":   "PSK/8",
		"ROLLOFF":      "25",
		"INNER_FEC":    "3/4",
	} {
		if dvb[name] != want {
			t.Errorf("the DVB reading gave %s=%q, want %q", name, dvb[name], want)
		}
	}
}

// TestTheDeliverySystemComesFromTheDescriptor. One network carries both
// generations on neighbouring transponders -- which is what the
// modulation_system bit is for -- so a DVB-S multiplex found from a DVB-S2 one
// has to be tuned as DVB-S.
func TestTheDeliverySystemComesFromTheDescriptor(t *testing.T) {
	// Bit 2 clear: first generation, QPSK.
	first := &isdb.SatelliteDelivery{FrequencyKHz: 11778000, Polarisation: "H", Modulation: 0x01}
	channel, ok := SatelliteChannel(first, "DVBS2", Dish{Block: "UNIVERSAL"})
	if !ok {
		t.Fatal("refused")
	}
	if channel["DELIVERY_SYSTEM"] != "DVBS" {
		t.Errorf("a first-generation multiplex came out as %q", channel["DELIVERY_SYSTEM"])
	}
	// The roll-off field means nothing on DVB-S, so nothing is written for it.
	if rollOff, given := channel["ROLLOFF"]; given {
		t.Errorf("a first-generation multiplex was given ROLLOFF=%q", rollOff)
	}
	if channel["MODULATION"] != "QPSK" {
		t.Errorf("modulation came out as %q", channel["MODULATION"])
	}
}

// TestNothingIsGuessed. A demodulator told nothing works it out; one told the
// wrong thing does not. Every field the descriptor declined to fill in is left
// out rather than written as a default.
func TestNothingIsGuessed(t *testing.T) {
	// Modulation type 0 is "auto", FEC 0 is "not defined", symbol rate zero is
	// a descriptor that said nothing.
	silent := &isdb.SatelliteDelivery{FrequencyKHz: 11778000, Polarisation: "V", Modulation: 0x04}
	channel, ok := SatelliteChannel(silent, "DVBS2", Dish{})
	if !ok {
		t.Fatal("refused")
	}
	for _, unwanted := range []string{"MODULATION", "SYMBOL_RATE", "INNER_FEC", "LNB", "SAT_NUMBER"} {
		if value, given := channel[unwanted]; given {
			t.Errorf("%s was written as %q where the descriptor said nothing", unwanted, value)
		}
	}
	// What it did say still comes through.
	if channel["DELIVERY_SYSTEM"] != "DVBS2" || channel["POLARIZATION"] != "V" || channel["ROLLOFF"] != "35" {
		t.Errorf("channel = %v", channel)
	}
}

// TestADishWithASwitchSaysWhichPort. A dish reached through a switch needs the
// position sent with every tune, and only the person who installed it knows.
func TestADishWithASwitchSaysWhichPort(t *testing.T) {
	described := &isdb.SatelliteDelivery{FrequencyKHz: 11778000, Polarisation: "H", Modulation: 0x05}
	channel, _ := SatelliteChannel(described, "DVBS2", Dish{Block: "UNIVERSAL", Position: "2"})
	if channel["SAT_NUMBER"] != "2" {
		t.Errorf("SAT_NUMBER = %q, want 2", channel["SAT_NUMBER"])
	}
}

// TestNothingReachableIsRefused: a descriptor that named nowhere, or nowhere
// this installation can reach, is not a channel.
func TestNothingReachableIsRefused(t *testing.T) {
	if _, ok := SatelliteChannel(nil, "ISDBS", Dish{}); ok {
		t.Error("a missing descriptor made a channel")
	}
	if _, ok := SatelliteChannel(nil, "DVBS2", Dish{}); ok {
		t.Error("a missing descriptor made a channel")
	}
	// Outside what the Japanese block converts.
	outside := &isdb.SatelliteDelivery{FrequencyKHz: 10700000, Polarisation: "R"}
	if _, ok := SatelliteChannel(outside, "ISDBS", Dish{}); ok {
		t.Error("a downlink the Japanese block cannot reach made a channel")
	}
	// A descriptor with no frequency at all says nothing on either reading.
	if _, ok := SatelliteChannel(&isdb.SatelliteDelivery{}, "DVBS2", Dish{}); ok {
		t.Error("a descriptor with no frequency made a channel")
	}
}

// TestCableChannelReadsTheOperatorsOwnAccount. A cable's frequencies are
// whoever runs it, so there is no plan to walk and the network table is the
// only account there is. Its modulation has a byte of its own, so there is one
// reading rather than two.
func TestCableChannelReadsTheOperatorsOwnAccount(t *testing.T) {
	described := &isdb.CableDelivery{
		FrequencyHz: 474000000, Modulation: 5, SymbolRate: 6900000, FECInner: 15,
	}
	channel, ok := CableChannel(described, "DVBC/ANNEX_A")
	if !ok {
		t.Fatal("refused")
	}
	for name, want := range map[string]string{
		"DELIVERY_SYSTEM": "DVBC/ANNEX_A",
		"FREQUENCY":       "474000000",
		"MODULATION":      "QAM/256",
		"SYMBOL_RATE":     "6900000",
		// Fifteen is "none", which is a real answer and not the same as the
		// descriptor declining to say.
		"INNER_FEC": "NONE",
	} {
		if channel[name] != want {
			t.Errorf("%s = %q, want %q", name, channel[name], want)
		}
	}

	// Nothing said, nothing written.
	silent, ok := CableChannel(&isdb.CableDelivery{FrequencyHz: 474000000}, "ISDBC")
	if !ok {
		t.Fatal("refused")
	}
	for _, unwanted := range []string{"MODULATION", "SYMBOL_RATE", "INNER_FEC"} {
		if value, given := silent[unwanted]; given {
			t.Errorf("%s was written as %q where the descriptor said nothing", unwanted, value)
		}
	}
	if _, ok := CableChannel(nil, "DVBC/ANNEX_A"); ok {
		t.Error("a missing descriptor made a channel")
	}
}

// TestConventionFollowsWhatWasTuned. A network table describes its own
// network, so the multiplex it was read out of and the multiplexes it
// describes are of a piece.
func TestConventionFollowsWhatWasTuned(t *testing.T) {
	for _, one := range []struct {
		system string
		want   Convention
	}{
		{"ISDBS", ARIB},
		{"ISDBS3", ARIB},
		{"DVBS", DVB},
		{"DVBS2", DVB},
		// Spelled as a channel file might, which is the same system.
		{"ISDB-S", ARIB},
		{"DVB-S2", DVB},
	} {
		if got := ConventionFor(one.system); got != one.want {
			t.Errorf("ConventionFor(%q) = %v, want %v", one.system, got, one.want)
		}
	}
}

// TestTheAdvancedServicesKeepTheirOwnSystem. The TLV pass tunes as
// ISDB-S3 and what it finds has to say so: a channel recorded as ISDB-S is
// tuned as ISDB-S afterwards, which is not what it is.
func TestTheAdvancedServicesKeepTheirOwnSystem(t *testing.T) {
	// BS-14 left-hand, a TLV stream.
	described := &isdb.SatelliteDelivery{FrequencyKHz: 11976820, Polarisation: "L"}
	channel, ok := SatelliteChannel(described, "ISDBS3", Dish{})
	if !ok {
		t.Fatal("refused")
	}
	if channel["DELIVERY_SYSTEM"] != "ISDBS3" {
		t.Errorf("DELIVERY_SYSTEM = %q, want ISDBS3", channel["DELIVERY_SYSTEM"])
	}
	if channel["FREQUENCY"] != "2471820" {
		t.Errorf("FREQUENCY = %q, want 2471820", channel["FREQUENCY"])
	}
}

// TestCableEntriesReadANetworksOwnAccount exercises the road nothing walks yet.
// Discovery on a cable walks the Japanese band plan, because a Japanese
// operator's table describes one stream however many carriers it is asked on.
// Everywhere else the table is the only account there is.
func TestCableEntriesReadANetworksOwnAccount(t *testing.T) {
	on := func(hz uint32, tsid uint16) isdb.NetworkTransport {
		return isdb.NetworkTransport{
			TransportStreamID: tsid,
			Cable: &isdb.CableDelivery{
				FrequencyHz: hz, Modulation: 3, SymbolRate: 6900000, FECInner: 15,
			},
		}
	}
	networks := []isdb.NetworkInfo{{NetworkID: 1, Transports: []isdb.NetworkTransport{
		on(482000000, 2),
		on(474000000, 1),
		// Mentioned without saying where, so it cannot be tuned.
		{TransportStreamID: 3},
		// And the same stream twice is still one channel.
		on(474000000, 1),
	}}}
	entries := cableEntries(networks, "DVBC/ANNEX_A")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(entries), entries)
	}
	// Up the band, so the same cable gives the same report twice running.
	if entries[0].Name != "474000000" || entries[1].Name != "482000000" {
		t.Errorf("entries are named %q and %q", entries[0].Name, entries[1].Name)
	}
	first := entries[0].Properties
	if first["MODULATION"] != "QAM/64" || first["SYMBOL_RATE"] != "6900000" {
		t.Errorf("the first channel is %v", first)
	}
	if entries[0].Source != "the network information table" {
		t.Errorf("the channel says it came from %q", entries[0].Source)
	}
}

// TestThePlanKeepsItsFrequencyAndTakesTheOperatorsSettings. The plan is the
// band and the table is how each carrier of it is modulated. Neither is the
// whole answer: a table that names three carriers has not said the other
// hundred are empty, and a plan cannot say what symbol rate an operator runs.
func TestThePlanKeepsItsFrequencyAndTakesTheOperatorsSettings(t *testing.T) {
	planned := []channelconfig.Entry{
		// C23, at the offset a Japanese cable actually carries.
		{Name: "C23", Source: "the built-in band plan", Properties: map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "225142857",
			"SYMBOL_RATE": "5274000", "MODULATION": "QAM/AUTO",
		}},
		// C24, which the operator says nothing about.
		{Name: "C24", Source: "the built-in band plan", Properties: map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "231142857",
			"SYMBOL_RATE": "5274000", "MODULATION": "QAM/AUTO",
		}},
	}
	named := []channelconfig.Entry{
		// The same carrier, rounded to the nominal figure as a table does,
		// and running something other than the plan's guess.
		{Name: "225000000", Source: "the network information table", Properties: map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "225000000",
			"SYMBOL_RATE": "6900000", "MODULATION": "QAM/64",
		}},
		// And one outside the band the plan covers, which this walk was not
		// asked to look at.
		{Name: "770000000", Source: "the network information table", Properties: map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "770000000",
			"SYMBOL_RATE": "6900000", "MODULATION": "QAM/256",
		}},
	}

	merged := mergeCarriers(planned, named)
	// One carrier in, one carrier out: the plan decides what is walked.
	if len(merged) != 2 {
		t.Fatalf("got %d carriers, want 2: %#v", len(merged), merged)
	}

	// The matched one keeps the plan's name and frequency -- tuning the
	// rounded figure asks the demodulator to find its way back -- and takes
	// the operator's settings.
	first := merged[0]
	if first.Name != "C23" || first.Properties["FREQUENCY"] != "225142857" {
		t.Errorf("the matched carrier came out as %s at %s", first.Name, first.Properties["FREQUENCY"])
	}
	if first.Properties["SYMBOL_RATE"] != "6900000" || first.Properties["MODULATION"] != "QAM/64" {
		t.Errorf("the matched carrier kept the plan's guess: %v", first.Properties)
	}
	// Untouched where the operator said nothing.
	if merged[1].Name != "C24" || merged[1].Properties["SYMBOL_RATE"] != "5274000" {
		t.Errorf("the unmentioned carrier changed: %v", merged[1])
	}
	// The plan's own entries are not written into: the plan is built once and
	// a second band must see what the first one saw.
	if planned[0].Properties["SYMBOL_RATE"] != "5274000" {
		t.Errorf("the merge wrote back into the plan: %v", planned[0].Properties)
	}
}

// TestOneCarrierIsNotTakenTwice. A table that names two carriers a plan puts
// in one place must not have both folded onto it, and neither leftover is
// walked: the plan is the band.
func TestOneCarrierIsNotTakenTwice(t *testing.T) {
	planned := []channelconfig.Entry{
		{Name: "C23", Properties: map[string]string{"FREQUENCY": "225142857"}},
	}
	named := []channelconfig.Entry{
		{Name: "a", Properties: map[string]string{"FREQUENCY": "225000000", "SYMBOL_RATE": "1"}},
		{Name: "b", Properties: map[string]string{"FREQUENCY": "225100000", "SYMBOL_RATE": "2"}},
	}
	merged := mergeCarriers(planned, named)
	if len(merged) != 1 {
		t.Fatalf("got %d carriers, want 1: %#v", len(merged), merged)
	}
	if merged[0].Properties["SYMBOL_RATE"] != "1" {
		t.Errorf("the plan's carrier took %q", merged[0].Properties["SYMBOL_RATE"])
	}
}

// TestTheWalkIsThePlansOwnLength. A table naming carriers of the band the plan
// already covers must not lengthen the walk: a scan that says 113 carriers and
// then starts a 114th has counted something twice.
func TestTheWalkIsThePlansOwnLength(t *testing.T) {
	planned := make([]channelconfig.Entry, 0, 113)
	for step := 0; step < 113; step++ {
		planned = append(planned, channelconfig.Entry{
			Properties: map[string]string{
				"FREQUENCY": strconv.Itoa(93_000_000 + step*6_000_000),
			},
		})
	}
	// What an operator's table says about carriers of that same band, rounded
	// as a table rounds them.
	var named []channelconfig.Entry
	for _, hz := range []int{111_000_000, 225_000_000, 471_000_000} {
		named = append(named, channelconfig.Entry{
			Properties: map[string]string{
				"FREQUENCY": strconv.Itoa(hz), "SYMBOL_RATE": "6900000",
			},
		})
	}
	merged := mergeCarriers(planned, named)
	if len(merged) != len(planned) {
		t.Fatalf("the walk is %d carriers, want the plan's %d", len(merged), len(planned))
	}
	took := 0
	for _, carrier := range merged {
		if carrier.Properties["SYMBOL_RATE"] == "6900000" {
			took++
		}
	}
	if took != len(named) {
		t.Errorf("%d carriers took the operator's settings, want %d", took, len(named))
	}
}

// TestACarrierWithNoReadableFrequencyIsNobodys. Two figures that cannot be
// compared are not the same carrier, and must not be folded together.
func TestACarrierWithNoReadableFrequencyIsNobodys(t *testing.T) {
	planned := []channelconfig.Entry{
		{Name: "C23", Properties: map[string]string{"FREQUENCY": "225142857"}},
	}
	named := []channelconfig.Entry{
		{Name: "nowhere", Properties: map[string]string{"FREQUENCY": ""}},
	}
	merged := mergeCarriers(planned, named)
	if len(merged) != 1 {
		t.Fatalf("got %d carriers, want 1: %#v", len(merged), merged)
	}
	if _, folded := merged[0].Properties["SYMBOL_RATE"]; folded {
		t.Error("a carrier with no frequency was folded onto one that has one")
	}
}
