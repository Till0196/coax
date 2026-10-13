// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

import "testing"

// TestReverseBitsMirrorsEachByte pins the read path's byte swap: every register
// the R848 sends back arrives bit-reversed, so getting this wrong would
// silently corrupt every lock check and calibration measurement.
func TestReverseBitsMirrorsEachByte(t *testing.T) {
	for _, test := range []struct{ in, want byte }{
		{0x00, 0x00}, {0xff, 0xff}, {0x01, 0x80}, {0x80, 0x01},
		{0x0f, 0xf0}, {0xf0, 0x0f}, {0xa5, 0xa5}, {0x12, 0x48},
		{0x3c, 0x3c}, {0x40, 0x02},
	} {
		if got := reverseBits(test.in); got != test.want {
			t.Errorf("reverseBits(%#02x) = %#02x, want %#02x", test.in, got, test.want)
		}
	}
}

// TestBasebandFilterCoversTheBandwidthLadder guards R848_DVBS_Setting's
// bandwidth ladder, whose original has no else branch: a bandwidth above 38000
// kHz not caught by one of the wide-band cases would leave Coarse uninitialised
// in C. Every input must land on a defined step here.
func TestBasebandFilterCoversTheBandwidthLadder(t *testing.T) {
	for _, test := range []struct {
		bandwidthKHz uint32
		coarse, fine byte
	}{
		{4000, 0, 0}, // below the narrowest step
		{7000, 0, 1},
		{27500, 11, 1}, // a common DVB-S transponder
		{38000, 16, 1}, // the top of the fixed ladder
		{50000, 23, 1}, // the 38000-62360 formula: (50000-38000)/1740+16 = 22, +1
		{65000, 31, 1},
		{80000, 39, 1}, // the >67400 formula: (80000-67400)/1600+31 = 38, +1
	} {
		coarse, fine := basebandFilter(test.bandwidthKHz)
		if coarse != test.coarse || fine != test.fine {
			t.Errorf("basebandFilter(%d) = (%d, %d), want (%d, %d)",
				test.bandwidthKHz, coarse, fine, test.coarse, test.fine)
		}
	}
}
