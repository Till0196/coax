// Copyright (c) Davin <smiledavin@gmail.com>; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package rda5816

import "time"

// lockAttempts and lockInterval are the wait for the synthesizer: a hundred
// and thirty looks two milliseconds apart, performed both after a tune and
// whenever the frontend asks for status.
const (
	lockAttempts = 130
	lockInterval = 2 * time.Millisecond
)

// PLLLocked waits for both of the synthesizer's lock indications, and answers
// false if they never both appear. It returns as soon as they do, so the wait
// is only paid when the chip is not settled.
func (tuner *Tuner) PLLLocked() (bool, error) {
	for attempt := 0; attempt < lockAttempts; attempt++ {
		status, err := tuner.read(registerLock)
		if err != nil {
			return false, err
		}
		if status&lockMask == lockMask {
			return true, nil
		}
		time.Sleep(lockInterval)
	}
	return false, nil
}

// agcPoints is the piecewise-linear AGC-to-dBm calibration curve, in
// (AGC reading, tenths of a dBm) pairs. It is the same curve the AV201x uses,
// the two parts having been calibrated to present the same reading to the
// demodulator above them.
var agcPoints = [][2]int32{
	{0, 90}, {82, -50}, {100, -263}, {116, -361}, {140, -463},
	{162, -563}, {173, -661}, {187, -761}, {210, -861},
	{223, -891}, {254, -904}, {255, -910},
}

// SignalStrengthFromAGC converts a raw AGC reading (0..255) to dBm along the
// calibration curve. The reading is the demodulator's, not this chip's: the
// RDA5816 has no level register of its own, so the number the frontend already
// has is what gets converted.
//
// A reading of 255 falls off the end of the curve. The last segment is
// extended over it, which is what every other value in the table gets.
func SignalStrengthFromAGC(agc byte) float64 {
	reading := int32(agc)
	index := len(agcPoints) - 1
	for candidate := range agcPoints {
		if agcPoints[candidate][0] > reading {
			index = candidate
			break
		}
	}
	if index == 0 {
		index = 1
	}
	lower, upper := agcPoints[index-1], agcPoints[index]
	slope := (upper[1] - lower[1]) * 1000 / (upper[0] - lower[0])
	tenthsDBm := lower[1] + ((reading-lower[0])*slope+500)/1000
	return float64(tenthsDBm) / 10
}
