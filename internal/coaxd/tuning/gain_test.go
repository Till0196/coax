// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import "testing"

func TestTheGainVocabulary(t *testing.T) {
	for written, want := range map[string]Gain{
		"":         {},
		"auto":     {Kind: GainAuto},
		"MIN":      {Kind: GainMin},
		" max ":    {Kind: GainMax},
		"\"off\"":  {Kind: GainMin},
		"on":       {Kind: GainMax},
		"+13dB":    {Kind: GainDecibels, Decibels: 13},
		"-9.9db":   {Kind: GainDecibels, Decibels: -9.9},
		"0 DB":     {Kind: GainDecibels},
		"19.7 dB ": {Kind: GainDecibels, Decibels: 19.7},
	} {
		got, err := ParseGainValue(written)
		if err != nil {
			t.Errorf("GAIN=%q: %v", written, err)
			continue
		}
		if got != want {
			t.Errorf("GAIN=%q read as %+v, want %+v", written, got, want)
		}
	}
}

// A step number is the obvious thing to write and is refused because it does
// not travel: the same file would ask for very nearly the least gain on one
// receiver and for "on" on another.
func TestABareNumberIsNotAGain(t *testing.T) {
	for _, written := range []string{"0", "1", "23", "13.5", "0x10", "high gain", "dB", "+dB"} {
		if got, err := ParseGainValue(written); err == nil {
			t.Errorf("GAIN=%q was accepted as %+v", written, got)
		}
	}
}

func TestTheTwoSpellingsAreOneSetting(t *testing.T) {
	gain, err := ParseGain(map[string]string{LNAProperty: "off"})
	if err != nil {
		t.Fatalf("LNA=off: %v", err)
	}
	if gain.Kind != GainMin {
		t.Errorf("LNA=off read as %v, want the bottom of the range", gain)
	}
	if _, err := ParseGain(map[string]string{GainProperty: "max", LNAProperty: "on"}); err == nil {
		t.Error("a channel that set both keys was accepted")
	}
	// One of them empty is not "both given": a file may carry the key without
	// a value.
	if _, err := ParseGain(map[string]string{GainProperty: "max", LNAProperty: ""}); err != nil {
		t.Errorf("an empty LNA beside a GAIN was refused: %v", err)
	}
}

func TestChoosingAStep(t *testing.T) {
	ladder := NewGainLadder(-9.9, -6.5, 0, 7.1, 19.7)
	for _, one := range []struct {
		written string
		want    int
	}{
		{"min", 0},
		{"max", 4},
		{"-9.9dB", 0},
		{"-9dB", 0},   // nearer -9.9 than -6.5
		{"-7dB", 1},   // nearer -6.5
		{"+7dB", 3},   // nearest is 7.1
		{"+100dB", 4}, // above the top
		{"-100dB", 0}, // below the bottom
	} {
		gain, err := ParseGainValue(one.written)
		if err != nil {
			t.Fatalf("%s: %v", one.written, err)
		}
		step, err := ladder.Choose(gain)
		if err != nil {
			t.Errorf("%s: %v", one.written, err)
			continue
		}
		if step != one.want {
			t.Errorf("%s chose step %d (%s), want %d", one.written, step, ladder.Describe(step), one.want)
		}
	}
}

// Where the hardware does not say what its settings are worth, a level has
// nothing to be near and rounding it to an end would invent a figure.
func TestASwitchRefusesALevel(t *testing.T) {
	ladder := NewGainSwitch()
	level, err := ParseGainValue("+13dB")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ladder.Choose(level); err == nil {
		t.Error("a switch accepted a level in decibels")
	}
	for written, want := range map[string]int{"min": 0, "max": 1} {
		gain, err := ParseGainValue(written)
		if err != nil {
			t.Fatal(err)
		}
		step, err := ladder.Choose(gain)
		if err != nil {
			t.Errorf("%s: %v", written, err)
			continue
		}
		if step != want {
			t.Errorf("%s chose step %d, want %d", written, step, want)
		}
	}
	if got := ladder.Describe(0); got != "min" {
		t.Errorf("a switch describes its bottom as %q", got)
	}
}

// A tuner reports the level it settled on, and that spelling has to be one a
// channel file could carry.
func TestWhatIsReportedCanBeWrittenBack(t *testing.T) {
	for _, decibels := range []float64{-9.9, 0, 7.1, 19.7} {
		written := DecibelString(decibels)
		gain, err := ParseGainValue(written)
		if err != nil {
			t.Errorf("%s came back unreadable: %v", written, err)
			continue
		}
		if gain.Kind != GainDecibels || gain.Decibels != decibels {
			t.Errorf("%s read back as %+v", written, gain)
		}
	}
}

func TestRefusingGainOnlyFiresWhenItWasAskedFor(t *testing.T) {
	if err := RefuseGain(map[string]string{"FREQUENCY": "473143000"}, "this board"); err != nil {
		t.Errorf("a channel that said nothing about gain was refused: %v", err)
	}
	err := RefuseGain(map[string]string{GainProperty: "min"}, "this board")
	if err == nil {
		t.Fatal("a board with no gain setting accepted GAIN=min")
	}
	if got := err.Error(); !contains(got, "min") || !contains(got, "this board") {
		t.Errorf("the refusal reads %q, which names neither the board nor the setting", got)
	}
}

func contains(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
