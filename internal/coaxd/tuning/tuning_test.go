// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]string{
		"DVB-C/ANNEX_A": "DVBC/ANNEXA",
		"isdb-t":        "ISDBT",
		"ISDB S3":       "ISDBS3",
	} {
		if got := Normalize(value); got != want {
			t.Errorf("Normalize(%q) = %q; want %q", value, got, want)
		}
	}
}

func TestNumber(t *testing.T) {
	t.Parallel()
	properties := map[string]string{"FREQUENCY": " \"557143000\" ", "STREAM_ID": "0x10"}
	if got, err := Number(properties, "FREQUENCY", true); err != nil || got != 557143000 {
		t.Fatalf("Number(FREQUENCY) = %d, %v", got, err)
	}
	if got, err := Number(properties, "STREAM_ID", true); err != nil || got != 0x10 {
		t.Fatalf("Number(STREAM_ID) = %d, %v", got, err)
	}
	if got, err := Number(properties, "SYMBOL_RATE", false); err != nil || got != 0 {
		t.Fatalf("optional missing key = %d, %v; want 0, nil", got, err)
	}
	if _, err := Number(properties, "SYMBOL_RATE", true); err == nil {
		t.Fatal("required missing key succeeded")
	}
	if _, err := Number(map[string]string{"FREQUENCY": "12ab"}, "FREQUENCY", true); err == nil {
		t.Fatal("malformed value succeeded")
	}
}

// TestBandwidthHzPrefersAnExplicitValue checks that a terrestrial file, which
// does carry the key, is taken at its word.
func TestBandwidthHzPrefersAnExplicitValue(t *testing.T) {
	got, err := BandwidthHz(map[string]string{
		"DELIVERY_SYSTEM": "DVB-T",
		"BANDWIDTH_HZ":    "8000000",
		"SYMBOL_RATE":     "5274000",
	})
	if err != nil {
		t.Fatalf("BandwidthHz: %v", err)
	}
	if got != 8_000_000 {
		t.Fatalf("BandwidthHz = %d, want 8000000", got)
	}
}

// The case that made a DVB-C channel file unusable: no BANDWIDTH_HZ, because
// the bandwidth follows from the symbol rate and the system's roll-off.
func TestBandwidthHzDerivesFromTheSymbolRate(t *testing.T) {
	tests := []struct {
		delivery   string
		rollOff    string
		symbolRate string
		want       uint64
	}{
		// 5274000 * 1.15, the Japanese cable symbol rate.
		{delivery: "DVB-C/ANNEX_A", symbolRate: "5274000", want: 6_065_100},
		{delivery: "DVBC", symbolRate: "5274000", want: 6_065_100},
		{delivery: "DVB-C/ANNEX_C", symbolRate: "5274000", want: 5_959_620},
		{delivery: "DVB-S", symbolRate: "21096000", want: 28_479_600},
		{delivery: "ISDB-S", symbolRate: "28860000", want: 38_961_000},
		{delivery: "DVB-S2", symbolRate: "20000000", want: 27_000_000},
		{delivery: "DVB-S2", rollOff: "20", symbolRate: "20000000", want: 24_000_000},
		{delivery: "DVB-S2", rollOff: "25", symbolRate: "20000000", want: 25_000_000},
	}
	for _, test := range tests {
		properties := map[string]string{"DELIVERY_SYSTEM": test.delivery, "SYMBOL_RATE": test.symbolRate}
		if test.rollOff != "" {
			properties["ROLLOFF"] = test.rollOff
		}
		got, err := BandwidthHz(properties)
		if err != nil {
			t.Errorf("BandwidthHz(%s rolloff=%q): %v", test.delivery, test.rollOff, err)
			continue
		}
		if got != test.want {
			t.Errorf("BandwidthHz(%s rolloff=%q) = %d, want %d", test.delivery, test.rollOff, got, test.want)
		}
	}
}

// TestBandwidthHzIsFixedForSixMegahertzSystems covers the systems whose width
// never depends on the symbol rate.
func TestBandwidthHzIsFixedForSixMegahertzSystems(t *testing.T) {
	for _, delivery := range []string{"ATSC", "DVB-C/ANNEX_B", "ISDB-C"} {
		got, err := BandwidthHz(map[string]string{"DELIVERY_SYSTEM": delivery})
		if err != nil {
			t.Errorf("BandwidthHz(%s): %v", delivery, err)
			continue
		}
		if got != 6_000_000 {
			t.Errorf("BandwidthHz(%s) = %d, want 6000000", delivery, got)
		}
	}
}

// TestBandwidthHzStillRequiresTheKeyForTerrestrial checks that the derivation
// does not invent a bandwidth for a system that has no roll-off to derive from.
func TestBandwidthHzStillRequiresTheKeyForTerrestrial(t *testing.T) {
	for _, delivery := range []string{"DVB-T", "DVB-T2"} {
		if _, err := BandwidthHz(map[string]string{"DELIVERY_SYSTEM": delivery}); err == nil {
			t.Errorf("BandwidthHz(%s) should report the missing key", delivery)
		}
	}
}

// TestBandwidthHzReportsAMissingSymbolRate keeps the error specific when a
// cable channel names neither of the two keys it could be derived from.
func TestBandwidthHzReportsAMissingSymbolRate(t *testing.T) {
	_, err := BandwidthHz(map[string]string{"DELIVERY_SYSTEM": "DVB-C/ANNEX_A"})
	if err == nil {
		t.Fatal("a cable channel with no symbol rate should fail")
	}
	if !strings.Contains(err.Error(), "SYMBOL_RATE") {
		t.Fatalf("error %q does not mention the key that is missing", err)
	}
}

// Which spellings name J.83 Annex A or Annex C. The Si2168 and the
// Si2141/Si2157 both key off this, so getting Annex C or ISDB-C wrong here
// silently tunes the channel as terrestrial.
func TestIsJ83AnnexAC(t *testing.T) {
	for delivery, want := range map[string]bool{
		"DVB-C":         true,
		"DVBC":          true,
		"DVB-C/ANNEX_A": true,
		"DVB-C/ANNEX_C": true,
		"ISDB-C":        true,
		"ISDBC":         true,
		// Annex B is trellis-coded and is not the same system, whatever it is
		// carried over.
		"DVB-C/ANNEX_B": false,
		"DVB-T":         false,
		"ISDB-T":        false,
		// ISDB-S reaches some homes over fibre; that still does not make it
		// one of these.
		"ISDB-S": false,
		"DVB-S2": false,
	} {
		if got := IsJ83AnnexAC(delivery); got != want {
			t.Errorf("IsJ83AnnexAC(%q) = %v, want %v", delivery, got, want)
		}
	}
}

// The wider test the tuners key off: every annex is one cable band to a channel
// filter, so Annex B belongs here even though it is a different system to a
// demodulator.
func TestIsJ83(t *testing.T) {
	for delivery, want := range map[string]bool{
		"DVB-C":         true,
		"DVB-C/ANNEX_A": true,
		"DVB-C/ANNEX_B": true,
		"DVBC/ANNEXB":   true,
		"DVB-C/ANNEX_C": true,
		"ISDB-C":        true,
		"DVB-T":         false,
		"ISDB-T":        false,
		"DVB-S2":        false,
	} {
		if got := IsJ83(delivery); got != want {
			t.Errorf("IsJ83(%q) = %v, want %v", delivery, got, want)
		}
	}
}

// The spelling gap this closes: channel files write QAM/256, datasheets and
// drivers write QAM256, and a mismatch picks the wrong constellation.
func TestNormalizeModulation(t *testing.T) {
	for modulation, want := range map[string]string{
		"QAM/256": "QAM256",
		"QAM256":  "QAM256",
		"qam/64":  "QAM64",
		"QAM_16":  "QAM16",
		"8VSB":    "8VSB",
		"":        "",
	} {
		if got := NormalizeModulation(modulation); got != want {
			t.Errorf("NormalizeModulation(%q) = %q, want %q", modulation, got, want)
		}
	}
}

// From a MyGica T230C2 on a Japanese cable feed: the board says it receives
// "DVB-C" -- a bare DVB-C always means Annex A -- while the channel file it
// locks says "DVBC/ANNEX_A". Matching by name alone reported the channel as
// unsupported on a tuner that was receiving it.
func TestSpellingsOfOneSystemAreTreatedAsOne(t *testing.T) {
	same := [][2]string{
		{"DVB-C", "DVBC/ANNEX_A"},
		{"DVB-C/ANNEX_A", "DVBC"},
		{"ISDB-C", "DVBC/ANNEX_C"},
		{"ISDBC", "DVB-C/ANNEX_C"},
		{"DVB-T", "DVBT"},
	}
	for _, pair := range same {
		if !SameDelivery(pair[0], pair[1]) {
			t.Errorf("%q and %q are the same system", pair[0], pair[1])
		}
	}
	// Annex B really is a different system: its trellis coding is not
	// something an Annex A demodulator can lock.
	different := [][2]string{
		{"DVB-C", "DVBC/ANNEX_B"},
		{"ISDB-C", "DVB-C/ANNEX_B"},
		{"DVB-T", "DVB-T2"},
		{"ISDB-T", "ISDB-S"},
	}
	for _, pair := range different {
		if SameDelivery(pair[0], pair[1]) {
			t.Errorf("%q and %q are different systems", pair[0], pair[1])
		}
	}
}

// A listing still reads the way the board describes itself, and only gains the
// spellings a caller may hold.
func TestExpandDeliveriesKeepsWhatWasThere(t *testing.T) {
	expanded := ExpandDeliveries([]string{"DVB-T", "DVB-T2", "DVB-C", "DVB-C/ANNEX_B", "ISDB-C"})
	want := []string{"DVB-T", "DVB-T2", "DVB-C", "DVB-C/ANNEX_B", "ISDB-C", "DVB-C/ANNEX_A", "DVB-C/ANNEX_C"}
	if len(expanded) != len(want) {
		t.Fatalf("expanded to %v, want %v", expanded, want)
	}
	for index := range want {
		if expanded[index] != want[index] {
			t.Fatalf("expanded to %v, want %v", expanded, want)
		}
	}
	// Expanding twice must not grow the list again.
	if again := ExpandDeliveries(expanded); len(again) != len(expanded) {
		t.Errorf("expanding an expanded list grew it: %v", again)
	}
	if len(ExpandDeliveries(nil)) != 0 {
		t.Error("a board that says nothing gained a delivery system")
	}
}

// A channel that names only the transponder takes relative slot zero, which is
// what a player tuning by frequency to read the NIT relies on. A slot and a
// TSID pass through as written for the backend to tell apart.
func TestStreamIDTakesSlotZeroWhenTheChannelDoesNotSay(t *testing.T) {
	for _, one := range []struct {
		properties map[string]string
		want       uint16
	}{
		{map[string]string{}, 0},
		{map[string]string{"STREAM_ID": " "}, 0},
		{map[string]string{"STREAM_ID": "0"}, 0},
		{map[string]string{"STREAM_ID": "3"}, 3},
		{map[string]string{"STREAM_ID": "16625"}, 16625},
		{map[string]string{"STREAM_ID": "0x40f1"}, 0x40f1},
	} {
		got, err := StreamID(one.properties)
		if err != nil {
			t.Errorf("%v: %v", one.properties, err)
			continue
		}
		if got != one.want {
			t.Errorf("%v gave %d, want %d", one.properties, got, one.want)
		}
	}
	for _, value := range []string{"65536", "-1", "slot"} {
		if _, err := StreamID(map[string]string{"STREAM_ID": value}); err == nil {
			t.Errorf("STREAM_ID=%q was accepted", value)
		}
	}
}

// The identifier is eight bits. A file carrying more names the same stream
// dvbv5-zap and the drivers behind it would tune, so it is masked not refused.
func TestPLPTakesTheLowEightBitsOfAWiderStreamID(t *testing.T) {
	for _, one := range []struct {
		value string
		want  int
	}{
		{"", -1},
		{"0", 0},
		{"3", 3},
		{"255", 255},
		{"256", 0},
		{"8193", 1},
		{"65535", 255},
		// A packed sequence sits above the identifier and does not disturb it.
		{"0x0C000101", 1},
	} {
		got, err := PLP(map[string]string{"STREAM_ID": one.value})
		if err != nil {
			t.Errorf("STREAM_ID=%q: %v", one.value, err)
			continue
		}
		if got != one.want {
			t.Errorf("STREAM_ID=%q gave %d, want %d", one.value, got, one.want)
		}
	}
}

// The scrambling sequence has a property of its own, and the packed form the
// DVB-S2 multistream files carry is read back out of the STREAM_ID.
func TestScramblingComesFromItsPropertyOrThePackedStreamID(t *testing.T) {
	// 0x0C000101: mode 3, sequence 1, identifier 1.
	index, err := Scrambling(map[string]string{"STREAM_ID": "0x0C000101"})
	if err != nil || index != 1 {
		t.Errorf("a packed STREAM_ID gave scrambling %d, %v, want 1", index, err)
	}
	// Nothing above the identifier means the default sequence.
	if index, err := Scrambling(map[string]string{"STREAM_ID": "3"}); err != nil || index != 0 {
		t.Errorf("a bare STREAM_ID gave scrambling %d, %v, want 0", index, err)
	}
	index, err = Scrambling(map[string]string{ScramblingProperty: "31"})
	if err != nil || index != 31 {
		t.Errorf("scrambling = %d, %v, want 31", index, err)
	}
	if _, err := Scrambling(map[string]string{ScramblingProperty: "262144"}); err == nil {
		t.Error("a scrambling index past eighteen bits was accepted")
	}
}

// TestDemodDeliverySystemFoldsTheJapaneseCableName: a demodulator's table lists
// DVB-C, and a channel asked for as ISDB-C -- the name this country uses, and
// what coax reports -- was refused on hardware that receives it.
func TestDemodDeliverySystemFoldsTheJapaneseCableName(t *testing.T) {
	for _, spelling := range []string{"ISDBC", "ISDB-C", "isdb-c", "DVBC", "DVB-C", "DVB-C/ANNEX_A", "DVB-C/ANNEX_C", "J.83C"} {
		if got := DemodDeliverySystem(spelling); got != "DVBC/ANNEXA" {
			t.Errorf("DemodDeliverySystem(%q) = %q, want DVBC/ANNEXA", spelling, got)
		}
	}
	// Annex B is a different system, and everything else is left as Normalize
	// left it.
	for _, one := range []struct{ in, want string }{
		{"DVB-C/ANNEX_B", "DVBC/ANNEXB"},
		{"J83B", "J83B"},
		{"ISDB-T", "ISDBT"},
		{"DVB-S2", "DVBS2"},
	} {
		if got := DemodDeliverySystem(one.in); got != one.want {
			t.Errorf("DemodDeliverySystem(%q) = %q, want %q", one.in, got, one.want)
		}
	}
}
