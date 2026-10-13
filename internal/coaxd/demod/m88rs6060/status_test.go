// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import "testing"

// TestCNRAppliesTheMillidecibelScaleNotACentidecibelOne guards a unit mistake
// this port made once while translating: mesLog10 stores 10000*log10(ratio),
// i.e. millidecibels, not centidecibels, so reaching dB divides by 1000.
func TestCNRAppliesTheMillidecibelScaleNotACentidecibelOne(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	demod.system = "DVBS2"

	// noise = (buf[1]<<6 | buf[0]&0x3f) >> 2, signal = buf[2]^2 >> 1. buf1=2
	// gives noise 32 and buf2=16 gives signal 128, a clean ratio of 4, which
	// lands on an exact table entry: mesLog10[3] = 6021 mdB = 6.021 dB.
	bus.registers[testDemodAddress][0x8c] = 0x00
	bus.registers[testDemodAddress][0x8d] = 0x02
	bus.registers[testDemodAddress][0x8e] = 16

	got, err := demod.cnr()
	if err != nil {
		t.Fatalf("cnr: %v", err)
	}
	if want := 6.021; got != want {
		t.Errorf("cnr() = %v, want %v", got, want)
	}
}

// TestLockMaskMatchesTheKernelsPerSystemBitPattern pins the register 0x0D
// patterns counting as locked -- 0xF7 for DVB-S, 0x8F for DVB-S2/S2X, each
// both mask and expected value. Tune's poll loop waits for the same two.
func TestLockMaskMatchesTheKernelsPerSystemBitPattern(t *testing.T) {
	if mask, want := lockMask("DVBS"); mask != 0xf7 || want != 0xf7 {
		t.Errorf("DVBS lock mask/want = %#02x/%#02x, want 0xf7/0xf7", mask, want)
	}
	if mask, want := lockMask("DVBS2"); mask != 0x8f || want != 0x8f {
		t.Errorf("DVBS2 lock mask/want = %#02x/%#02x, want 0x8f/0x8f", mask, want)
	}
}
