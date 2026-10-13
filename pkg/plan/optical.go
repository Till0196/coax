// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plan

import "strconv"

// The optical distribution the 124/128 degree east service is remodulated onto:
// a hundred and eight carriers, evenly spaced, on a grid that is neither the
// cable plan nor the terrestrial one.
//
// The carriers have no published channel numbers, so the channel files name
// their sections by frequency and the numbering a channel definition file gets
// is ours: it starts where the carriers start.
const (
	opticalFirstKHz     = 73_750
	opticalSpacingKHz   = 6_500
	opticalCarrierCount = 108
)

var opticalCarriers = buildOpticalPlan()

func Optical() []Carrier { return opticalCarriers }

func buildOpticalPlan() []Carrier {
	list := make([]Carrier, 0, opticalCarrierCount)
	for index := range opticalCarrierCount {
		hz := (opticalFirstKHz + index*opticalSpacingKHz) * 1000
		// Named in kilohertz, which is how the channel file names them and
		// the only name these carriers have.
		list = append(list, Carrier{Name: strconv.Itoa(hz / 1000), Frequency: hz})
	}
	return list
}

// OpticalPTXChannel numbers a carrier of the distribution from its frequency
// in hertz:
//
//	freq kHz = 73750 + ptx * 6500    ptx 0..107
func OpticalPTXChannel(hz int) (int, bool) {
	offset := hz - opticalFirstKHz*1000
	spacing := opticalSpacingKHz * 1000
	if offset < 0 || offset%spacing != 0 {
		return 0, false
	}
	if index := offset / spacing; index < opticalCarrierCount {
		return index, true
	}
	return 0, false
}
