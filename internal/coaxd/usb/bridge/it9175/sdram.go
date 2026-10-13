// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it9175

// The SDRAM on the boards that carry it is retimed per channel so its switching
// harmonics fall outside the channel being received. This table gives the clock
// for each Japanese broadcast channel, in the order the bands run.
var sdramClocks = [113]byte{
	// VHF 1-3 and CATV C13-C21.
	73, 78, 73, 77, 67, 72, 75, 60, 63, 61, 64, 61,
	// CATV C22 and VHF 4-7.
	65, 63, 70, 77, 73,
	// VHF 8-12 and CATV C23.
	75, 78, 80, 61, 62, 65,
	// CATV C24-C63.
	68, 69, 71, 72, 78, 80, 79, 80, 61, 62, 64, 65, 67, 68, 60, 63,
	69, 62, 80, 75, 66, 77, 61, 68, 76, 69, 80, 77, 75, 72, 60, 66,
	65, 66, 80, 64, 67, 60, 62, 60,
	// UHF 13-62.
	64, 66, 66, 67, 67, 79, 80, 61, 62, 71, 62, 65, 66, 64, 67, 60,
	68, 79, 80, 62, 63, 64, 65, 66, 66, 66, 67, 60, 74, 61, 79, 64,
	65, 65, 66, 60, 73, 60, 68, 62, 69, 64, 80, 79, 79, 60, 60, 60,
	63, 61,
}

// sdramBandStart is where each band begins in the table, with the total on the
// end so a band's length is the difference between neighbours.
var sdramBandStart = [6]int{0, 12, 17, 23, 63, 113}

// sdramBandLow is the centre frequency in kHz of each band's first channel,
// less half a channel, so that dividing by the 6 MHz spacing gives the channel
// number within the band.
var sdramBandLow = [5]uint32{90143, 164143, 192143, 230143, 470143}

// sdramClock reports the clock for the channel a frequency falls in. Bands are
// searched from the top so that the highest one containing the frequency wins,
// and a frequency below every band takes the first entry.
func sdramClock(frequencyKHz uint32) byte {
	for band := len(sdramBandLow) - 1; band >= 0; band-- {
		offset := int64(frequencyKHz) - int64(sdramBandLow[band])
		if offset < 0 {
			continue
		}
		channel := int(offset / 6000)
		if last := sdramBandStart[band+1] - sdramBandStart[band] - 1; channel > last {
			channel = last
		}
		return sdramClocks[channel+sdramBandStart[band]]
	}
	return sdramClocks[0]
}
