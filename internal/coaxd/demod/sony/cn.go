// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

// The satellite carrier-to-noise curves are tables rather than formulas: the
// chip reports a raw estimate that falls as the signal improves, and a
// descending table turns it into decibels. Every satellite path in the family
// -- ISDB-S, ISDB-S3, DVB-S, DVB-S2 -- reads one this way, so the walk is
// here; the curves themselves stay with the registers they come from.

// CNCurve is one such curve, flattened: raw reading and millidecibels
// alternating, raw descending and decibels ascending.
//
// It is a flat list rather than a slice of two-field rows because these curves
// run to hundreds of entries, and four pairs to a line is what makes a stray
// value visible at a glance. A curve with an odd number of values is malformed
// by construction -- worth a test in the package that carries one.
type CNCurve []int32

// Len is the number of pairs in the curve.
func (curve CNCurve) Len() int { return len(curve) / 2 }

// At is the raw reading and the millidecibels of one pair.
func (curve CNCurve) At(index int) (raw uint32, mdB int32) {
	return uint32(curve[2*index]), curve[2*index+1]
}

// MdB turns a raw reading into millidecibels: walk the curve for the first
// pair the reading has passed and answer with the one before it. A reading
// past the end takes the last pair.
func (curve CNCurve) MdB(raw uint32) int32 {
	index := 1
	for ; index < curve.Len(); index++ {
		if entry, _ := curve.At(index); entry < raw {
			break
		}
	}
	_, mdB := curve.At(index - 1)
	return mdB
}
