// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package sony

import (
	"math"
	"testing"
)

// The vectors are the decade boundaries and the points the J.83 curves clamp
// to.

func TestIntegerLogarithmsMatchTheKernel(t *testing.T) {
	for _, test := range []struct {
		x    uint32
		want uint32
	}{
		{1, 0}, {10, 100}, {100, 200}, {1000, 300},
		// Not 400: the table-and-interpolation logarithm lands a hundredth
		// short here, and reproducing that is the point of the port.
		{10000, 399},
		{65535, 481}, {200000, 530},
	} {
		if got := MathLog10(test.x); got != test.want {
			t.Errorf("MathLog10(%d) = %d, want %d", test.x, got, test.want)
		}
	}
	for _, test := range []struct {
		x    uint32
		want uint32
	}{
		{69, 423},     // the 32/128 QAM clamp
		{126, 484},    // the 16/64/256 QAM clamp
		{1000, 691},   //
		{24320, 1010}, // the constant the 16/64/256 QAM curve is written around
		{32767, 1040},
	} {
		if got := MathLog(test.x); got != test.want {
			t.Errorf("MathLog(%d) = %d, want %d", test.x, got, test.want)
		}
	}
}

// TestLogarithmsTrackTheRealFunctions checks these really are logarithms
// rather than tables that agree at a handful of points. It also settled which
// implementation to port: a linear approximation of the fractional part, which
// some ports use, drifts up to 6.45 here against this one's 0.78, and the J.83
// curve multiplies that by 95 -- about 0.6 dB.
func TestLogarithmsTrackTheRealFunctions(t *testing.T) {
	worst := 0.0
	for x := uint32(1); x <= 200000; x++ {
		drift := math.Abs(float64(MathLog10(x)) - 100*math.Log10(float64(x)))
		if drift > worst {
			worst = drift
		}
	}
	if worst > 1.1 {
		t.Errorf("MathLog10 drifts by up to %.2f, want at most 1.1", worst)
	}
	worst = 0
	for x := uint32(69); x <= 32767; x++ {
		drift := math.Abs(float64(MathLog(x)) - 100*math.Log(float64(x)))
		if drift > worst {
			worst = drift
		}
	}
	if worst > 1.0 {
		t.Errorf("MathLog drifts by up to %.2f, want at most 1.0", worst)
	}
}
