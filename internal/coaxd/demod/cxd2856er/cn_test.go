// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

func TestSatelliteCurvesMatchTheKernelTables(t *testing.T) {
	for _, test := range []struct {
		name  string
		curve sony.CNCurve
		raw   uint32
		want  int32
	}{
		// DVB-S2 and ISDB-S share one curve, DVB-S has its own, and they do
		// not even start at the same reading.
		{"the shared curve, above it", dvbs2ISDBSCN, 0, 20000},
		{"the shared curve, top", dvbs2ISDBSCN, 20, 20000},
		{"the shared curve, next step", dvbs2ISDBSCN, 21, 19900},
		{"the shared curve, middle", dvbs2ISDBSCN, 1000, 2100},
		{"the shared curve, bottom", dvbs2ISDBSCN, 1455, 0},
		{"the shared curve, below it", dvbs2ISDBSCN, 8191, 0},
		{"DVB-S top", dvbsCN, 20, 20000},
		{"DVB-S bottom", dvbsCN, 830, 0},
		{"DVB-S below the curve", dvbsCN, 8191, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.curve.MdB(test.raw); got != test.want {
				t.Errorf("MdB(%d) = %d, want %d", test.raw, got, test.want)
			}
		})
	}
}

// TestSatelliteCurvesAreOrdered guards the tables themselves: the search only
// works while they descend by reading and ascend by decibels, and a row pasted
// out of order would break it silently.
func TestSatelliteCurvesAreOrdered(t *testing.T) {
	for name, curve := range map[string]sony.CNCurve{"DVB-S": dvbsCN, "DVB-S2 and ISDB-S": dvbs2ISDBSCN} {
		if len(curve)%2 != 0 {
			t.Fatalf("%s curve has an odd number of values, so a pair was lost", name)
		}
		for index := 1; index < curve.Len(); index++ {
			raw, mdB := curve.At(index)
			previousRaw, previousMdB := curve.At(index - 1)
			if raw >= previousRaw {
				t.Fatalf("%s row %d does not descend: %#x after %#x",
					name, index, raw, previousRaw)
			}
			if mdB <= previousMdB {
				t.Fatalf("%s row %d does not rise: %d after %d",
					name, index, mdB, previousMdB)
			}
		}
	}
	if dvbsCN.Len() != 191 || dvbs2ISDBSCN.Len() != 192 {
		t.Errorf("curve lengths are %d and %d pairs, want 191 and 192",
			dvbsCN.Len(), dvbs2ISDBSCN.Len())
	}
}

// TestCNRNeedsASystem guards the default branch: an unwoken chip has no
// measurement register to read, and answering 0 dB would look like a very bad
// signal rather than a programming mistake.
func TestCNRNeedsASystem(t *testing.T) {
	demod := New(&readingBus{values: map[[2]byte]byte{}}, 0xdc, 0xd8, sony.Config{})
	if _, err := demod.CNR(); err == nil {
		t.Error("CNR() on an unwoken chip should fail")
	}
}
