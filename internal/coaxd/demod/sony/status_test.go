// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "testing"

// TestISDBTCNRAgainstCapturedHardware checks the ISDB-T carrier-to-noise
// reading against a trace from a TBS 6812 locked to channel 21 at 521142857 Hz:
// the chip answered 0x17C9 in bank 0x60 register 0x28, and 27.27 dB is the
// answer for it. The 1.5 dB taken off at the end belongs to the driver, and is
// applied here the way a driver applies it.
func TestISDBTCNRAgainstCapturedHardware(t *testing.T) {
	reg := uint32(0x17)<<8 | uint32(0xc9)
	mdB := 100*int32(MathLog10(reg)) - 9031
	// Compared in thousandths, because the decibel figure a driver derives
	// from this is a float and 27.27 has no exact binary form.
	if got := mdB - 1500; got != 27269 {
		t.Errorf("ISDB-T CNR = %d mdB, want 27269, which the driver reports as 27.27 dB", got)
	}
}

// TestCableCNRIsNotTheVendorsSimplifiedOne records where this port parts company with a
// "simplified DVB-C SNR using log10" sometimes used for the CXD2857ER:
//
//	snr = -950 * log10(reg) + 36500
//
// It does not work. Read from a TBS 6812 locked to a 256-QAM cable channel at -40 dBm,
// register 0x4C in bank 0x40 answered 0x01D3, and that formula turns it into -218.65 dB.
// Sony's formula gives 36.02 dB, which fits a strong 256-QAM signal.
func TestCableCNRIsNotTheVendorsSimplifiedOne(t *testing.T) {
	reg := uint32(0x01&0x1f)<<8 | uint32(0xd3)
	if reg != 467 {
		t.Fatalf("captured register decodes to %d, want 467", reg)
	}
	// 16/64/256-QAM, which is the branch a 256-QAM channel takes.
	ours := -95*int32(MathLog(reg)) + 95941 - 1500
	if ours != 36016 {
		t.Errorf("cable CNR = %d mdB, want 36016 (36.02 dB)", ours)
	}
	theirs := -950*int32(MathLog10(reg)) + 36500 - 1500
	if theirs > -200000 {
		t.Errorf("the vendor formula should still be the broken one: %d mdB", theirs)
	}
}
