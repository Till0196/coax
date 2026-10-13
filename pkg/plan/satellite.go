// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plan

import (
	"fmt"
	"slices"
)

// SatelliteBand is a run of transponders of the 110 degree east plan, evenly
// spaced in intermediate frequency.
//
// The numbering is the broadcasters'. As DX Antenna sets out the plan,
// right-hand BS transponders are the odd numbers and left-hand ones the even,
// right-hand CS the even ND numbers and left-hand CS the odd. The two
// polarisations come down from different oscillators, 10678 MHz and 9505 MHz,
// which is the 1173 MHz between a transponder's bands.
type SatelliteBand struct {
	// Prefixes are the names a transponder of this band answers to; the first
	// is the one it is written under.
	Prefixes []string
	// FirstNumber is the first transponder's number; the numbers go up in twos
	// from it, each hand of polarisation interleaved with the other.
	FirstNumber int
	FirstKHz    int
	SpacingKHz  int
	Count       int
}

// SatelliteBands are the four runs, in the order a name or a frequency is tried
// against them, which is also the order their physical channel numbers are
// given out in.
//
// Left-hand CS is here for the numbering rather than for anything on it: those
// transmissions ended on 2024-03-31, but a scan of a feed that still carries
// something must not be told there is no such channel. ND 25 and ND 26 are in
// the published table and not here, both landing outside the intermediate
// frequency bands by this arithmetic.
var SatelliteBands = []SatelliteBand{
	{[]string{"BS"}, 1, 1_049_480, 38_360, 12},
	{[]string{"ND", "CS"}, 2, 1_613_000, 40_000, 12},
	{[]string{"BS"}, 2, 2_241_660, 38_360, 12},
	{[]string{"ND", "CS"}, 1, 2_766_000, 40_000, 12},
}

// rightHandSatelliteBands is how many of the bands above are right-hand
// circular, which is the polarisation every installation can receive: left-hand
// needs a block and a distribution that pass a band the older ones stop short
// of.
const rightHandSatelliteBands = 2

type SatelliteTransponder struct {
	// Name is what it is called: BS01, ND02.
	Name string
	// Number is the transponder number within its satellite's numbering.
	Number       int
	FrequencyKHz int
	// PTXChannel is its position in the plan, which is the physical channel
	// number a channel definition file writes.
	PTXChannel int
	RightHand  bool
}

func (band SatelliteBand) Transponder(index int) SatelliteTransponder {
	number := band.FirstNumber + index*2
	return SatelliteTransponder{
		Name:         SatelliteTransponderName(band.Prefixes[0], number),
		Number:       number,
		FrequencyKHz: band.FirstKHz + index*band.SpacingKHz,
	}
}

// Index is where a transponder number falls in this band, and whether it falls
// in it at all. A number whose parity is wrong belongs to the other hand of
// the same satellite and is found in a band further down.
func (band SatelliteBand) Index(number int) (int, bool) {
	if number < band.FirstNumber || (number-band.FirstNumber)%2 != 0 {
		return 0, false
	}
	index := (number - band.FirstNumber) / 2
	if index >= band.Count {
		return 0, false
	}
	return index, true
}

func (band SatelliteBand) Carries(prefix string) bool {
	return slices.Contains(band.Prefixes, prefix)
}

// SatelliteTransponderName is the satellite's prefix and a two-digit number.
func SatelliteTransponderName(prefix string, number int) string {
	return fmt.Sprintf("%s%02d", prefix, number)
}

var satelliteTransponders = buildSatellitePlan()

func buildSatellitePlan() []SatelliteTransponder {
	var list []SatelliteTransponder
	for number, band := range SatelliteBands {
		for index := range band.Count {
			transponder := band.Transponder(index)
			transponder.PTXChannel = len(list)
			transponder.RightHand = number < rightHandSatelliteBands
			list = append(list, transponder)
		}
	}
	return list
}

// SatelliteTransponderAt names the transponder at an intermediate frequency in
// kilohertz, or reports that nothing of this plan is there.
func SatelliteTransponderAt(frequencyKHz int) (SatelliteTransponder, bool) {
	for _, band := range SatelliteBands {
		offset := frequencyKHz - band.FirstKHz
		if offset < 0 || offset%band.SpacingKHz != 0 {
			continue
		}
		index := offset / band.SpacingKHz
		if index >= band.Count {
			continue
		}
		return satelliteTransponders[satelliteBandStart(band)+index], true
	}
	return SatelliteTransponder{}, false
}

// RightHandSatelliteTransponders are the transponders every installation can
// reach, where anything looking for a network starts.
func RightHandSatelliteTransponders() []SatelliteTransponder {
	var list []SatelliteTransponder
	for _, transponder := range satelliteTransponders {
		if transponder.RightHand {
			list = append(list, transponder)
		}
	}
	return list
}

func satelliteBandStart(wanted SatelliteBand) int {
	start := 0
	for _, band := range SatelliteBands {
		if band.FirstKHz == wanted.FirstKHz {
			return start
		}
		start += band.Count
	}
	return start
}

// The oscillators a Japanese low-noise block uses. Right-hand circular is the
// long-standing polarisation and comes down against 10678 MHz; left-hand was
// added for the ISDB-S3 services and comes down against 9505 MHz.
const (
	rightHandOscillatorKHz = 10_678_000
	leftHandOscillatorKHz  = 9_505_000
)

// The stretches of intermediate frequency each polarisation arrives on.
var (
	rightHandBandKHz = [2]int{1_032_000, 2_072_000}
	leftHandBandKHz  = [2]int{2_224_000, 3_224_000}
)

// IntermediateFrequencyKHz is where a receiver tunes a downlink frequency, or
// nothing if that is not somewhere a receiver here is given. The answer is
// checked against the stretch its polarisation should have landed on, which is
// what catches a descriptor read wrongly: subtracting either oscillator yields
// a number, and only the right band says which.
func IntermediateFrequencyKHz(downlinkKHz int, leftHand bool) (int, bool) {
	oscillator, band := rightHandOscillatorKHz, rightHandBandKHz
	if leftHand {
		oscillator, band = leftHandOscillatorKHz, leftHandBandKHz
	}
	intermediate := downlinkKHz - oscillator
	if intermediate < band[0] || intermediate > band[1] {
		return 0, false
	}
	return intermediate, true
}
