// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import "testing"

// TestCalcPLSGoldCodeMatchesAnIndependentLFSRSimulation pins the LFSR port
// against independently computed vectors: the first few codes, one that has
// to be walked forward from checkpoint 0, and 262142 (2^18-2), which walks
// forward from the last checkpoint in plsList.
func TestCalcPLSGoldCodeMatchesAnIndependentLFSRSimulation(t *testing.T) {
	for _, test := range []struct {
		gold uint32
		want [3]byte
	}{
		{0, [3]byte{0x01, 0x00, 0x00}},
		{1, [3]byte{0x00, 0x00, 0x02}},
		{2, [3]byte{0x00, 0x00, 0x01}},
		{5, [3]byte{0x00, 0x20, 0x00}},
		{1000, [3]byte{0x22, 0x55, 0x00}},
		{262142, [3]byte{0x02, 0x00, 0x00}},
	} {
		if got := calcPLSGoldCode(test.gold); got != test.want {
			t.Errorf("calcPLSGoldCode(%d) = %#v, want %#v", test.gold, got, test.want)
		}
	}
}
