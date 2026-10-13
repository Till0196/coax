// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import "testing"

// A channel naming the block is handed to Windows as the downlink with the block
// beside it, because a BDA driver does the subtraction itself and that is what
// decides whether the 22 kHz tone belongs on the lead.
func TestNamingTheBlockHandsOverTheDownlink(t *testing.T) {
	for _, one := range []struct {
		name          string
		properties    map[string]string
		frequency     int32
		low, high, sw int32
	}{
		{
			name: "the 124/128 degrees east satellites, one oscillator",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1068000",
				"SYMBOL_RATE": "23303000", "LNB": "JCSAT3A", "TONE": "OFF",
			},
			frequency: 12_268_000,
			// One oscillator said twice: a driver reads that as no band switch.
			low: 11_200_000, high: 11_200_000, sw: 0,
		},
		{
			name: "a universal block in its low band",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1250000",
				"SYMBOL_RATE": "22000000", "LNB": "UNIVERSAL", "TONE": "OFF",
			},
			frequency: 11_000_000,
			low:       9_750_000, high: 10_600_000, sw: 11_700_000,
		},
		{
			name: "and in its high band, which the tone selected",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1400000",
				"SYMBOL_RATE": "22000000", "LNB": "UNIVERSAL", "TONE": "ON",
			},
			frequency: 12_000_000,
			low:       9_750_000, high: 10_600_000, sw: 11_700_000,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			profile, err := BuildTuneProfile(one.properties)
			if err != nil {
				t.Fatalf("BuildTuneProfile: %v", err)
			}
			if profile.FrequencyKHz != one.frequency {
				t.Errorf("frequency = %d, want the downlink %d", profile.FrequencyKHz, one.frequency)
			}
			if !profile.LNB.Known() {
				t.Fatal("the block was not carried over")
			}
			if profile.LNB.LowOscillatorKHz != one.low ||
				profile.LNB.HighOscillatorKHz != one.high ||
				profile.LNB.SwitchKHz != one.sw {
				t.Errorf("LNB = %+v, want low=%d high=%d switch=%d",
					profile.LNB, one.low, one.high, one.sw)
			}
		})
	}
}

// A channel naming no block is handed over exactly as written: those files carry
// the intermediate frequency and expect nobody to touch it.
func TestNamingNoBlockLeavesTheFrequencyAlone(t *testing.T) {
	profile, err := BuildTuneProfile(map[string]string{
		"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "16400",
	})
	if err != nil {
		t.Fatalf("BuildTuneProfile: %v", err)
	}
	if profile.FrequencyKHz != 1_049_480 {
		t.Errorf("frequency = %d, want it left at 1049480", profile.FrequencyKHz)
	}
	if profile.LNB.Known() {
		t.Errorf("a block was invented: %+v", profile.LNB)
	}
}
