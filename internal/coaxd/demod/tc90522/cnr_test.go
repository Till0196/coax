// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tc90522

import (
	"math"
	"testing"
)

// The vectors below pin CNval_calcS and CNval_calcT at the ends of their
// domains, at the saturation points either side of them, and across enough of
// the middle to catch a curve that has been reshaped rather than merely
// shifted.

func TestCNRHundredthsDBSMatchesTheReferenceCurve(t *testing.T) {
	for _, test := range []struct{ raw, want uint32 }{
		{0, 91},      // below the domain: saturates low
		{2999, 91},   // the last raw value below it
		{3000, 5886}, // the top of the curve
		{3001, 5748},
		{5000, 2572},
		{10000, 1719},
		{20000, 1039},
		{37000, 106}, // the bottom of the curve
		{37001, 100}, // past the domain: saturates high
		{65535, 100}, // the widest a 16-bit register can report
	} {
		if got := cnrHundredthsDBS(test.raw); got != test.want {
			t.Errorf("cnrHundredthsDBS(%d) = %d, want %d", test.raw, got, test.want)
		}
	}
}

func TestCNRHundredthsDBTMatchesTheReferenceCurve(t *testing.T) {
	for _, test := range []struct{ raw, want uint32 }{
		{0, 4000},    // the counter falls as the signal improves, so
		{1403, 4000}, // a small reading saturates at the good end
		{1404, 4000},
		{1405, 3999},
		{2000, 3756},
		{10000, 2864},
		{100000, 1848},
		{1000000, 877},
		{5000000, 333}, // the bottom of the curve
		{5000001, 100}, // past the domain: saturates at the bad end
	} {
		if got := cnrHundredthsDBT(test.raw); got != test.want {
			t.Errorf("cnrHundredthsDBT(%d) = %d, want %d", test.raw, got, test.want)
		}
	}
}

// TestCNRCurvesFallAsTheCounterRises is the property the vectors alone cannot
// show: a transcription slip in the fixed-point arithmetic tends to produce a
// curve that doubles back somewhere in the middle, which would report a better
// ratio for a worse signal.
//
// The satellite curve is strictly monotonic. The terrestrial one is not quite:
// its fixed-point rounding wobbles upwards by a single hundredth of a dB at 137
// points, which is part of the curve rather than something to smooth away.
func TestCNRCurvesFallAsTheCounterRises(t *testing.T) {
	previous := cnrHundredthsDBS(3000)
	for raw := uint32(3001); raw <= 37000; raw++ {
		current := cnrHundredthsDBS(raw)
		if current > previous {
			t.Fatalf("cnrHundredthsDBS rises at %d: %d after %d", raw, current, previous)
		}
		previous = current
	}
	previous = cnrHundredthsDBT(1404)
	for raw := uint32(1405); raw <= 5000000; raw++ {
		current := cnrHundredthsDBT(raw)
		if current > previous+1 {
			t.Fatalf("cnrHundredthsDBT jumps up at %d: %d after %d", raw, current, previous)
		}
		previous = current
	}
	// The ends must still be the right way round, or a curve that crept
	// upwards a hundredth at a time would slip past the bound above.
	if cnrHundredthsDBT(1404) <= cnrHundredthsDBT(5000000) {
		t.Fatal("the terrestrial curve does not fall from end to end")
	}
}

// The terrestrial curve is the same one recpt1 applies to the raw counter it
// reads from an Earthsoft card, which is what lets one conversion serve every
// driver here. recpt1 writes it in floating point and this driver in fixed
// point, so the two are checked against each other rather than one being taken
// on trust.
func TestTerrestrialCurveAgreesWithRecpt1(t *testing.T) {
	recpt1 := func(raw float64) float64 {
		// calc_cn in recpt1core.c, for a terrestrial channel.
		power := math.Log10(5505024/raw) * 10
		return 0.000024*power*power*power*power -
			0.0016*power*power*power +
			0.0398*power*power +
			0.5491*power + 3.0965
	}
	// The range a sixteen-bit reading can carry, which is what the PT1 driver
	// reports: it reads the low two bytes of the counter and leaves the top
	// one behind.
	for raw := uint32(1404); raw <= 65535; raw += 37 {
		mine, measured := CNRFromRawT(raw)
		if !measured {
			t.Fatalf("CNRFromRawT(%d) reported no measurement", raw)
		}
		theirs := recpt1(float64(raw))
		if away := mine - theirs; away > 0.01 || away < -0.01 {
			t.Fatalf("CNRFromRawT(%d) = %v, recpt1 says %v", raw, mine, theirs)
		}
	}
}

// A counter the chip has not filled in is not a reading, and turning it into
// one would report the best signal the curve can express for the worst case
// there is. Measured on a PT3: a terrestrial core answers 0 for about a second
// after it locks, and 0 through the curve is 40 dB.
func TestACounterTheChipHasNotTakenIsNotAMeasurement(t *testing.T) {
	if _, measured := CNRFromRawT(0); measured {
		t.Error("a terrestrial counter of 0 was taken as a measurement")
	}
	if _, measured := CNRFromRawT(1); !measured {
		t.Error("a terrestrial counter of 1 was not taken as a measurement")
	}
	// The satellite curve is undefined below its floor and saturates there,
	// which is the same thing said in the other direction.
	for _, raw := range []uint32{0, 1, satelliteFloor - 1} {
		if _, measured := CNRFromRawS(raw); measured {
			t.Errorf("a satellite counter of %d was taken as a measurement", raw)
		}
	}
	if _, measured := CNRFromRawS(satelliteFloor); !measured {
		t.Errorf("a satellite counter of %d was not taken as a measurement", satelliteFloor)
	}
}
