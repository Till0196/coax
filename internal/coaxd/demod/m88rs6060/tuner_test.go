// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import "testing"

// TestSelectMclkPicksTheFurthestFromAnAliasBoundary pins rs6060_select_mclk
// against independently computed values: for a normal symbol rate it picks
// whichever of the three ADC clocks lands the tuned frequency furthest from a
// clock-alias boundary, and at 46 MSym/s or more it forces 99 MHz regardless
// of frequency.
func TestSelectMclkPicksTheFurthestFromAnAliasBoundary(t *testing.T) {
	for _, test := range []struct {
		name          string
		frequencyMHz  uint32
		symbolRateKSs uint32
		wantReg16     byte
		wantMclkKHz   uint32
	}{
		{"normal rate picks the widest offset", 1550, 27500, 100, 99000},
		{"wide symbol rate forces 99 MHz", 1000, 50000, 100, 99000},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := newFakeBus()
			demod := newTestDemod(bus)
			if err := demod.selectMclk(test.frequencyMHz, test.symbolRateKSs); err != nil {
				t.Fatalf("selectMclk: %v", err)
			}
			if demod.mclkKHz != test.wantMclkKHz {
				t.Errorf("mclkKHz = %d, want %d", demod.mclkKHz, test.wantMclkKHz)
			}
			if got := bus.registers[testTunerAddress][0x16]; got != test.wantReg16 {
				t.Errorf("reg 0x16 = %#02x, want %#02x", got, test.wantReg16)
			}
		})
	}
}

// TestSetTSMclkMatchesAnIndependentFixedPointSimulation pins the fractional
// TS-clock divider split against an independent run of the same fixed-point
// math, covering the N=3 branch (div in (32,48]), which only parallel/common
// mode reaches -- serial mode falls through to N=4.
func TestSetTSMclkMatchesAnIndependentFixedPointSimulation(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	demod.config.TSMode = TSModeParallel
	bus.registers[testTunerAddress][0x15] = 0x00
	bus.registers[testTunerAddress][0x16] = 96
	bus.registers[testTunerAddress][0x1d] = 0x00

	if err := demod.setTSMclk(120000); err != nil {
		t.Fatalf("setTSMclk: %v", err)
	}

	want := map[byte]byte{0x1d: 0x82, 0x1e: 0x0d, 0x1f: 0xdc}
	for register, value := range want {
		if got := bus.registers[testTunerAddress][register]; got != value {
			t.Errorf("reg %#02x = %#02x, want %#02x", register, got, value)
		}
	}
}
