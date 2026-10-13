// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"strings"
	"testing"
)

// The Japanese premium service is the case this exists for: two satellites
// combined onto one lead, the same oscillator for both, and the 22 kHz tone
// choosing between them. The numbers are szap-s2j's.
func TestJapanesePremiumDownconverts(t *testing.T) {
	for _, one := range []struct {
		alias string
		tone  bool
	}{
		{"JCSAT3A", false}, // 128 degrees east
		{"JCSAT4B", true},  // 124 degrees east
		{"JCSAT4A", true},  // the same block under its older name
	} {
		lnbf, ok := LookupLNBf(one.alias)
		if !ok {
			t.Fatalf("no LNBf called %q", one.alias)
		}
		// A transponder at the bottom of the band.
		intermediate, tone, err := lnbf.Downconvert(12_268_000)
		if err != nil {
			t.Fatalf("%s: %v", one.alias, err)
		}
		if intermediate != 12_268_000-11_200_000 {
			t.Errorf("%s: 12268 MHz became %d kHz, want %d", one.alias,
				intermediate, 12_268_000-11_200_000)
		}
		if tone != one.tone {
			t.Errorf("%s: tone = %v, want %v", one.alias, tone, one.tone)
		}
	}
}

// A universal block has two oscillators and the tone picks the band.
func TestUniversalSwitchesBandOnTheTone(t *testing.T) {
	lnbf, ok := LookupLNBf("UNIVERSAL")
	if !ok {
		t.Fatal("no UNIVERSAL LNBf")
	}
	// Below the switch: the low oscillator, no tone.
	intermediate, tone, err := lnbf.Downconvert(11_000_000)
	if err != nil || intermediate != 11_000_000-9_750_000 || tone {
		t.Errorf("low band: %d kHz tone=%v (%v)", intermediate, tone, err)
	}
	// At and above it: the high oscillator, with the tone.
	intermediate, tone, err = lnbf.Downconvert(12_000_000)
	if err != nil || intermediate != 12_000_000-10_600_000 || !tone {
		t.Errorf("high band: %d kHz tone=%v (%v)", intermediate, tone, err)
	}
}

// An intermediate frequency handed over where a downlink was expected: an
// oscillator subtracted from it looks like a channel and is not.
func TestDownconvertRefusesAFrequencyOutsideTheBlocksBand(t *testing.T) {
	lnbf, _ := LookupLNBf("110BS")
	if _, _, err := lnbf.Downconvert(1_049_480); err == nil {
		t.Error("an intermediate frequency should not be downconverted again")
	}
}

// The aliases libdvbv5 uses are understood, so a channel file written for the
// DVB tools names its block the same way here.
func TestTheLibdvbv5AliasesAreKnown(t *testing.T) {
	for _, alias := range []string{"UNIVERSAL", "EXTENDED", "C-BAND", "110BS", "DBS"} {
		if _, ok := LookupLNBf(alias); !ok {
			t.Errorf("libdvbv5 knows %q and this does not", alias)
		}
	}
	// And the spelling is forgiving in the same way every other property is.
	if _, ok := LookupLNBf(" \"c band\" "); !ok {
		t.Error("the name should be read the way other properties are")
	}
}

// Naming no block converts nothing, which is what keeps every channel file
// already in use working: they carry the intermediate frequency.
func TestApplyLNBfLeavesAChannelAloneWhenNoBlockIsNamed(t *testing.T) {
	properties := map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480"}
	if err := ApplyLNBf(properties); err != nil {
		t.Fatalf("ApplyLNBf: %v", err)
	}
	if properties["FREQUENCY"] != "1049480" {
		t.Errorf("the frequency was changed to %q", properties["FREQUENCY"])
	}
	if _, given := properties["TONE"]; given {
		t.Error("a tone was invented for a channel that named no block")
	}
}

// Naming one rewrites the channel: the downlink becomes the intermediate
// frequency, the tone is filled in, and the key itself is consumed.
func TestApplyLNBfRewritesTheChannel(t *testing.T) {
	properties := map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "12268000", "LNB": "JCSAT4A",
	}
	if err := ApplyLNBf(properties); err != nil {
		t.Fatalf("ApplyLNBf: %v", err)
	}
	if properties["FREQUENCY"] != "1068000" {
		t.Errorf("FREQUENCY = %q, want 1068000", properties["FREQUENCY"])
	}
	if properties["TONE"] != "ON" {
		t.Errorf("TONE = %q, want ON", properties["TONE"])
	}
	// The name stays, because the Windows backend's driver wants the downlink
	// and converts for itself, and it comes back as the block's own name.
	// JCSAT4A is szap-s2j's name for the 124 degree block and what the file
	// above wrote; the satellite has been JCSAT-4B since 2012.
	if properties["LNB"] != "JCSAT4B" {
		t.Errorf("LNB = %q, want the block's current name", properties["LNB"])
	}
}

// A channel that names its own tone keeps it: the block's is a default for the
// band, and a file that says otherwise knows something this table does not.
func TestApplyLNBfKeepsAToneTheChannelNamed(t *testing.T) {
	properties := map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "12268000",
		"LNB": "JCSAT4A", "TONE": "OFF",
	}
	if err := ApplyLNBf(properties); err != nil {
		t.Fatalf("ApplyLNBf: %v", err)
	}
	if properties["TONE"] != "OFF" {
		t.Errorf("TONE = %q, want the OFF the channel asked for", properties["TONE"])
	}
}

func TestApplyLNBfRefusesWhatItCannotDo(t *testing.T) {
	// A name nothing answers to, reported with the ones that do.
	err := ApplyLNBf(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "12268000", "LNB": "NOSUCHBLOCK",
	})
	if err == nil || !strings.Contains(err.Error(), "UNIVERSAL") {
		t.Errorf("an unknown block should be refused with the list, got %v", err)
	}
	// A block on a terrestrial channel is a mistake rather than a no-op.
	if err := ApplyLNBf(map[string]string{
		"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473142857", "LNB": "UNIVERSAL",
	}); err == nil {
		t.Error("an LNBf on a terrestrial channel should be refused")
	}
}

// A satellite downlink is written in kilohertz, and this package's
// hertz-or-kilohertz rule does not hold here: a Ku-band downlink in kilohertz
// is twelve million, which that rule would take for hertz and divide away.
// Hertz is unreachable for Ku anyway -- the API's frequency field is 32 bits
// and 12.268 GHz does not fit -- but C band does fit, so the guard is checked
// below.
func TestApplyLNBfReadsAKuDownlinkAsKilohertz(t *testing.T) {
	properties := map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "12268000", "LNB": "JCSAT3A",
	}
	if err := ApplyLNBf(properties); err != nil {
		t.Fatalf("ApplyLNBf: %v", err)
	}
	if properties["FREQUENCY"] != "1068000" {
		t.Errorf("FREQUENCY became %q, want 1068000", properties["FREQUENCY"])
	}
}

// A C-band block mixes downwards from an oscillator above the band, so the
// difference runs the other way. Subtracting without noticing would wrap.
func TestCBandMixesDownFromAboveTheBand(t *testing.T) {
	lnbf, ok := LookupLNBf("C-BAND")
	if !ok {
		t.Fatal("no C-BAND LNBf")
	}
	intermediate, tone, err := lnbf.Downconvert(3_700_000)
	if err != nil {
		t.Fatalf("Downconvert: %v", err)
	}
	if intermediate != 5_150_000-3_700_000 {
		t.Errorf("3700 MHz became %d kHz, want %d", intermediate, 5_150_000-3_700_000)
	}
	if tone {
		t.Error("a single-oscillator block should not ask for the tone")
	}
	// The same frequency written in hertz, which fits in the API's 32-bit
	// field for C band even though it does not for Ku.
	properties := map[string]string{
		"DELIVERY_SYSTEM": "DVBS", "FREQUENCY": "3700000000", "LNB": "C-BAND",
	}
	if err := ApplyLNBf(properties); err != nil {
		t.Fatalf("ApplyLNBf: %v", err)
	}
	if properties["FREQUENCY"] != "1450000" {
		t.Errorf("FREQUENCY became %q, want 1450000", properties["FREQUENCY"])
	}
}

// A backend whose driver does its own downconversion asks for the downlink
// back, and has to get exactly what the channel wrote.
func TestDownlinkUndoesTheConversion(t *testing.T) {
	for _, one := range []struct {
		alias    string
		downlink uint64
	}{
		{"JCSAT3A", 12_268_000},   // single oscillator, tone off
		{"JCSAT4B", 12_268_000},   // single oscillator, tone on
		{"UNIVERSAL", 11_000_000}, // low band
		{"UNIVERSAL", 12_000_000}, // high band, which the tone selected
		{"C-BAND", 3_700_000},     // mixes down from above the band
	} {
		lnbf, ok := LookupLNBf(one.alias)
		if !ok {
			t.Fatalf("no LNBf called %q", one.alias)
		}
		intermediate, tone, err := lnbf.Downconvert(one.downlink)
		if err != nil {
			t.Fatalf("%s %d: %v", one.alias, one.downlink, err)
		}
		if back := lnbf.DownlinkKHz(intermediate, tone); back != one.downlink {
			t.Errorf("%s: %d kHz went to %d and came back %d",
				one.alias, one.downlink, intermediate, back)
		}
	}
}

// The one place a satellite being replaced reaches this program: the 124 degree
// slot was JCSAT-4A until 2012 and has been JCSAT-4B since, but szap-s2j --
// where these entries come from -- still writes the older name, and a channel
// file written against it has to keep tuning.
func TestOlderNamesStillFindTheirBlock(t *testing.T) {
	current, ok := LookupLNBf("JCSAT4B")
	if !ok {
		t.Fatal("JCSAT4B is not known")
	}
	for _, name := range []string{"JCSAT4A", "jcsat4a", " JCSAT4A ", "\"JCSAT4A\""} {
		older, ok := LookupLNBf(name)
		if !ok {
			t.Errorf("%q is not known", name)
			continue
		}
		if older.Alias != current.Alias || older.LowLO != current.LowLO || older.Tone != current.Tone {
			t.Errorf("%q found %+v, want the same block as JCSAT4B", name, older)
		}
	}
	// Both names are offered, so somebody who wrote either and had it refused
	// for another reason sees the one they wrote.
	var listed struct{ current, older bool }
	for _, alias := range LNBfAliases() {
		switch alias {
		case "JCSAT4B":
			listed.current = true
		case "JCSAT4A":
			listed.older = true
		}
	}
	if !listed.current || !listed.older {
		t.Errorf("the listing offers JCSAT4B=%v JCSAT4A=%v, want both", listed.current, listed.older)
	}
}
