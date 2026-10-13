// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import "testing"

// TestTheV2sSecondPairIsUnitsTwoAndThree: the bridge at position one streams
// units two and three, whose demodulators sit at 0x24 and 0x26 behind the
// first bridge.
func TestTheV2sSecondPairIsUnitsTwoAndThree(t *testing.T) {
	for at, want := range [][2]byte{{0x20, 0x22}, {0x24, 0x26}} {
		got := v2WiringAt(at)
		if got.demodTerrestrial != want[0] || got.demodSatellite != want[1] {
			t.Errorf("position %d is wired to %#02x/%#02x, want %#02x/%#02x",
				at, got.demodTerrestrial, got.demodSatellite, want[0], want[1])
		}
		if got.terrestrialName != "NM120" || got.satelliteName != "TDA20142" {
			t.Errorf("position %d names its tuners %q/%q", at, got.terrestrialName, got.satelliteName)
		}
	}
}

func TestParentPort(t *testing.T) {
	for port, want := range map[string]string{"1-2.3": "1-2", "1-2.3.4": "1-2.3", "3-1": "3-1", "": ""} {
		if got := parentPort(port); got != want {
			t.Errorf("parentPort(%q) = %q, want %q", port, got, want)
		}
	}
}
