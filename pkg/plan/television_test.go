// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plan

import "testing"

// A terrestrial walk tries UHF first: that is where the broadcasts are, and
// starting anywhere else means a minute of carriers that cannot answer.
func TestTerrestrialWalkStartsAtUHF(t *testing.T) {
	walk := TerrestrialWalk()
	if len(walk) != len(Terrestrial()) {
		t.Fatalf("the walk has %d carriers, the plan %d", len(walk), len(Terrestrial()))
	}
	if walk[0].Name != "13" || walk[0].Frequency != 473_142_857 {
		t.Errorf("the walk starts at %s (%d Hz), want channel 13", walk[0].Name, walk[0].Frequency)
	}
	if walk[49].Name != "62" {
		t.Errorf("UHF ends the first stretch at %s, want 62", walk[49].Name)
	}
	if walk[50].Name != "1" {
		t.Errorf("the cable bands follow at %s, want channel 1", walk[50].Name)
	}
	seen := map[string]bool{}
	for _, one := range walk {
		if seen[one.Name] {
			t.Fatalf("%s is walked twice", one.Name)
		}
		seen[one.Name] = true
	}
}
