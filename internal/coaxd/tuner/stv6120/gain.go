// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv6120

// gainPoint is one calibrated pair: front-end gain in hundredths of a decibel
// against the demodulator's AGC reading. Gain falls as the reading rises.
type gainPoint struct {
	gainCentiDB int32
	agc         uint32
}

var gainLookup = []gainPoint{
	{7429, 0}, {7368, 18711}, {7214, 23432}, {7090, 25123},
	{6988, 26305}, {6897, 27100}, {6809, 27741}, {6728, 28271},
	{6645, 28737}, {6571, 29120}, {6494, 29504}, {6416, 29857},
	{6341, 30180}, {6263, 30490}, {6179, 30815}, {6101, 31088},
	{6028, 31345}, {5956, 31600}, {5883, 31840}, {5801, 32096},
	{5730, 32320}, {5659, 32544}, {5582, 32752}, {5498, 32960},
	{5414, 33184}, {5340, 33392}, {5271, 33584}, {5198, 33775},
	{5125, 33967}, {5048, 34160}, {4963, 34352}, {4884, 34543},
	{4820, 34719}, {4740, 34910}, {4666, 35103}, {4582, 35295},
	{4500, 35488}, {4426, 35680}, {4365, 35870}, {4279, 36095},
	{4113, 36289}, {4020, 36500}, {3930, 36704}, {3838, 36912},
	{3738, 37152}, {3648, 37375}, {3544, 37600}, {3458, 37823},
	{3358, 38048}, {3281, 38240}, {3191, 38479}, {3091, 38720},
	{2993, 38976}, {2900, 39226}, {2792, 39520}, {2692, 39792},
	{2592, 40064}, {2497, 40351}, {2392, 40640}, {2290, 40976},
	{2189, 41295}, {2088, 41631}, {1999, 41934}, {1875, 42354},
	{1764, 42815}, {1637, 43263}, {1537, 43743}, {1412, 44288},
	{1291, 44913}, {1188, 45712}, {1080, 46720}, {976, 48164},
	{930, 50816}, {898, 65534}, {880, 65535},
}

// lookupGain interpolates linearly between the bracketing points, clamping to
// the ends outside the table.
func lookupGain(table []gainPoint, reading uint32) int32 {
	last := len(table) - 1
	if reading <= table[0].agc {
		return table[0].gainCentiDB
	}
	if reading >= table[last].agc {
		return table[last].gainCentiDB
	}
	lower, upper := 0, last
	for upper-lower > 1 {
		middle := (upper + lower) / 2
		if table[lower].agc <= reading && reading <= table[middle].agc {
			upper = middle
		} else {
			lower = middle
		}
	}
	span := int32(table[upper].agc) - int32(table[lower].agc)
	gain := table[lower].gainCentiDB
	if span != 0 {
		gain += (int32(reading) - int32(table[lower].agc)) *
			(table[upper].gainCentiDB - table[lower].gainCentiDB) / span
	}
	return gain
}

// The corrections get_rf_strength applies on top of the table. The table was
// measured at 12 dB of baseband gain, so a receiver running the chip's own
// 6 dB has to be given the difference back; the tilt is how much gain the band
// slope costs per 120 MHz of tuned frequency above 1550 MHz.
const (
	referenceBasebandGainDB = 12
	chipBasebandGainDB      = 6
	tiltPerStep             = 6
)

// RFGainCentiDB turns the demodulator's AGC reading into the front end's gain in
// hundredths of a decibel (get_rf_strength). The demodulator subtracts it from
// the power measured at its own input to get a level on the aerial lead.
func RFGainCentiDB(agc uint16, frequencyKHz uint32) int32 {
	gain := lookupGain(gainLookup, uint32(agc))
	gain += 100 * (chipBasebandGainDB - referenceBasebandGainDB)

	// Counted in units of ten megahertz.
	frequency := int32(frequencyKHz / 10000)
	if frequency < 159 {
		// The image-rejection filter in front of the mixer gives away two
		// decibels below 1590 MHz.
		gain -= 200
	}
	gain -= ((frequency - 155) * tiltPerStep / 12) * 10
	return gain
}

// RFGainCentiDB reports the gain for this half. Both the reading and the
// correction belong to the package rather than to one half, so this just calls
// the package-level function.
func (tuner *Tuner) RFGainCentiDB(agc uint16, frequencyKHz uint32) int32 {
	return RFGainCentiDB(agc, frequencyKHz)
}
