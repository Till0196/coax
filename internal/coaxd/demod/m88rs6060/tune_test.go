// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

import "testing"

// TestSetPLLFreqMatchesAnIndependentIntegerSimulation pins rs6060_set_pll_freq's
// divider math against independently computed values: the >=1550 MHz band
// (refDiv 19, reg 0x2C = 0x02) and the >=1070 MHz one, on the 1.5x reference
// divider (refDiv 10, reg 0x2C = 0x00).
func TestSetPLLFreqMatchesAnIndependentIntegerSimulation(t *testing.T) {
	for _, test := range []struct {
		frequencyMHz uint32
		want         map[byte]byte
	}{
		{1550, map[byte]byte{0x27: 0x04, 0x28: 0x0e, 0x29: 0x04, 0x2a: 0x0e, 0x36: 0x13, 0x39: 0x13, 0x2c: 0x02}},
		{1200, map[byte]byte{0x27: 0x12, 0x28: 0xb0, 0x29: 0x12, 0x2a: 0xb0, 0x36: 0x0a, 0x39: 0x0a, 0x2c: 0x00}},
	} {
		bus := newFakeBus()
		demod := newTestDemod(bus)
		if err := demod.setPLLFreq(test.frequencyMHz); err != nil {
			t.Fatalf("setPLLFreq(%d): %v", test.frequencyMHz, err)
		}
		for register, value := range test.want {
			if got := bus.registers[testTunerAddress][register]; got != value {
				t.Errorf("%d MHz: tuner reg %#02x = %#02x, want %#02x", test.frequencyMHz, register, got, value)
			}
		}
	}
}

// TestSetBBClampsTheChannelFilterToItsUsableRange guards rs6060_set_bb's clamp
// at both ends -- a narrow transponder asks for a corner below the 6 MHz
// floor, a wide one above the 43 MHz ceiling -- plus an unclamped case.
func TestSetBBClampsTheChannelFilterToItsUsableRange(t *testing.T) {
	for _, test := range []struct {
		symbolRateKSs uint32
		lpfOffsetKHz  int32
		want          byte
	}{
		{1000, 3000, 6}, // 1000*9/14+2000+3000 = 5642 -> clamped up to 6000
		{27500, 0, 19},  // 27500*9/14+2000 = 19678 -> 19 MHz
		{80000, 0, 43},  // 80000*9/14+2000 = 53428 -> clamped down to 43000
	} {
		bus := newFakeBus()
		demod := newTestDemod(bus)
		if err := demod.setBB(test.symbolRateKSs, test.lpfOffsetKHz); err != nil {
			t.Fatalf("setBB: %v", err)
		}
		if got := bus.registers[testTunerAddress][0x40]; got != test.want {
			t.Errorf("setBB(%d, %d) wrote reg 0x40 = %d, want %d",
				test.symbolRateKSs, test.lpfOffsetKHz, got, test.want)
		}
	}
}

// TestTuneReplaysTheDefaultRegisterTableAndSelectsTheSystem is a fake-bus smoke
// test: a DVB-S2 tune must reach regTableDefault, program the symbol-rate
// divider, and take the DVB-S2 branch of the register 0x08 update.
func TestTuneReplaysTheDefaultRegisterTableAndSelectsTheSystem(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	// Report the lock byte up front so Tune's 150-iteration poll exits on its
	// first pass instead of sleeping three seconds. 0xF7 skips the multistream
	// ISI enumeration, which only 0x8F triggers.
	bus.registers[testDemodAddress][0x0d] = 0xf7

	if err := demod.Tune(Params{
		DeliverySystem: "DVBS2",
		FrequencyKHz:   1_550_000,
		SymbolRate:     27_500_000,
		StreamID:       -1,
	}); err != nil {
		t.Fatalf("Tune: %v", err)
	}

	last := regTableDefault[len(regTableDefault)-1]
	if got := bus.registers[testDemodAddress][last[0]]; got != last[1] {
		t.Errorf("reg %#02x = %#02x, want %#02x from regTableDefault", last[0], got, last[1])
	}
	// mclk is 99000 at 1550 MHz, so the symbol rate divider is
	// ((27500<<15) + 99000/4) / (99000/2) = 901144750/49500 -> 18204 = 0x471C.
	if got := bus.registers[testDemodAddress][0x61]; got != 0x1c {
		t.Errorf("symbol rate low byte = %#02x, want 0x1c", got)
	}
	if got := bus.registers[testDemodAddress][0x62]; got != 0x47 {
		t.Errorf("symbol rate high byte = %#02x, want 0x47", got)
	}
	// Reg 0xE0 is not the tell: the DVB-S branch writes 0xF8 there, but so did
	// regTableDefault earlier. Check reg 0x08 instead -- the DVB-S2 branch's
	// |= 0x44 keeps bit 2 set, which the DVB-S branch clears.
	if got := bus.registers[testDemodAddress][0x08]; got&0x44 != 0x44 {
		t.Errorf("reg 0x08 = %#02x, want the DVB-S2 branch's 0x44 bits set", got)
	}
	if demod.system != "DVBS2" {
		t.Errorf("system = %q, want DVBS2", demod.system)
	}
}
