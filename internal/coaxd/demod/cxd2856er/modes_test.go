// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import (
	"bytes"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"math"
	"testing"
)

// TestIFFrequencyRegistersMatchTheKernelFormula pins the calculation that
// replaces the hardcoded tables the other ports carry, deriving the answer from
// the IF and the crystal. The division truncates, so these vectors are the
// truncated ones.
func TestIFFrequencyRegistersMatchTheKernelFormula(t *testing.T) {
	for _, test := range []struct {
		name    string
		ifHz    uint32
		xtalKHz uint32
		want    [3]byte
	}{
		// The fallback IFs for DVB-T, on the 20.5 MHz parts.
		{"4.80 MHz at 41 MHz", 4_800_000, 20500, [3]byte{0x1d, 0xf8, 0x81}},
		{"4.20 MHz at 41 MHz", 4_200_000, 20500, [3]byte{0x1a, 0x39, 0x71}},
		{"3.60 MHz at 41 MHz", 3_600_000, 20500, [3]byte{0x16, 0x7a, 0x61}},
		// A 24 MHz crystal samples at 48 MHz, so the same IF lands lower.
		// This is the case the boards here actually use.
		{"4.80 MHz at 48 MHz", 4_800_000, 24000, [3]byte{0x19, 0x99, 0x99}},
		{"3.60 MHz at 48 MHz", 3_600_000, 24000, [3]byte{0x13, 0x33, 0x33}},
		{"3.50 MHz at 48 MHz", 3_500_000, 24000, [3]byte{0x12, 0xaa, 0xaa}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ifFrequencyRegisters(test.ifHz, test.xtalKHz); got != test.want {
				t.Errorf("ifFrequencyRegisters(%d, %d) = %#v, want %#v",
					test.ifHz, test.xtalKHz, got, test.want)
			}
		})
	}
}

// TestIFFrequencyRegistersTruncate states the rounding rule outright: 3.6 MHz
// divides evenly and would pass either way, so no vector at that frequency
// would catch a rounding implementation.
func TestIFFrequencyRegistersTruncate(t *testing.T) {
	// 4.8 MHz at 41 MHz is 1964161.87 in the register's units. Truncation
	// gives 0x1df881; rounding would give 0x1df882, which is what the ported
	// tables elsewhere contain.
	if got := ifFrequencyRegisters(4_800_000, 20500); got[2] != 0x81 {
		t.Errorf("the low byte is %#02x, want 0x81: the division must truncate", got[2])
	}
}

// TestEverySystemHasAModeValue guards the switch that decides which demodulator
// core runs: a system added to the enum but not to the table would wake the chip
// into whatever mode it was last in. Modes must stay distinct -- Annex A, Annex
// C and ISDB-C are one system here (sony.SystemDVBC), the IF included.
func TestEverySystemHasAModeValue(t *testing.T) {
	seen := map[byte]sony.System{}
	for _, system := range []sony.System{
		sony.SystemISDBT, sony.SystemISDBS, sony.SystemDVBC,
		sony.SystemDVBT, sony.SystemDVBT2, sony.SystemDVBS, sony.SystemDVBS2,
	} {
		mode, err := demodMode(system)
		if err != nil {
			t.Errorf("demodMode(%s): %v", system, err)
			continue
		}
		if other, clash := seen[mode]; clash {
			t.Errorf("%s and %s both select mode %#02x", system, other, mode)
		}
		seen[mode] = system
		if system.String() == "unspecified" {
			t.Errorf("%d has no name", int(system))
		}
	}
	if _, err := demodMode(sony.SystemUnspecified); err == nil {
		t.Error("an unspecified system should not select any mode")
	}
}

// TestAnnexCTakesTheAnnexACore: SYS_DVBC_ANNEX_A and SYS_DVBC_ANNEX_C both
// reach the 0x04 sequence, and only SYS_DVBC_ANNEX_B reaches 0x08. Annex B is
// sony.SystemJ83B.
func TestAnnexCTakesTheAnnexACore(t *testing.T) {
	cable, err := demodMode(sony.SystemDVBC)
	if err != nil {
		t.Fatal(err)
	}
	if cable != 0x04 {
		t.Errorf("the J.83 Annex A/C core selects %#02x, want 0x04", cable)
	}
	trellis, err := demodMode(sony.SystemJ83B)
	if err != nil {
		t.Fatal(err)
	}
	if trellis != 0x08 {
		t.Errorf("Annex B selects %#02x, want 0x08", trellis)
	}
}

// TestSatelliteAndJ83Grouping checks the two predicates the wake-up and sleep
// paths branch on: getting one wrong sends a system through the other core's
// sequence, which is silent until nothing locks.
func TestSatelliteAndJ83Grouping(t *testing.T) {
	for system, want := range map[sony.System]bool{
		sony.SystemISDBS: true, sony.SystemDVBS: true, sony.SystemDVBS2: true,
		sony.SystemISDBT: false, sony.SystemDVBT: false, sony.SystemDVBT2: false,
		sony.SystemDVBC: false, sony.SystemUnspecified: false,
	} {
		if got := system.IsSatellite(); got != want {
			t.Errorf("%s.IsSatellite() = %v, want %v", system, got, want)
		}
	}
	for system, want := range map[sony.System]bool{
		sony.SystemDVBC: true, sony.SystemJ83B: true,
		sony.SystemISDBT: false, sony.SystemDVBT: false, sony.SystemISDBS: false,
	} {
		if got := j83(system); got != want {
			t.Errorf("j83(%s) = %v, want %v", system, got, want)
		}
	}
}

// TestDefaultIFFollowsTheChannelWidth records the fallback IFs, which apply
// because the CXD2858ER does not report an IF of its own.
func TestDefaultIFFollowsTheChannelWidth(t *testing.T) {
	for _, test := range []struct {
		system    sony.System
		bandwidth uint32
		want      uint32
	}{
		{sony.SystemDVBT, 8, 4_800_000},
		{sony.SystemDVBT, 7, 4_200_000},
		{sony.SystemDVBT, 6, 3_600_000},
		{sony.SystemDVBT2, 8, 4_800_000},
		{sony.SystemISDBT, 6, 3_600_000},
		{sony.SystemDVBC, 6, 3_700_000},
		// The satellite systems have no IF of this kind.
		{sony.SystemISDBS, 0, 0},
	} {
		if got := defaultIFHz(test.system, test.bandwidth); got != test.want {
			t.Errorf("defaultIFHz(%s, %d) = %d, want %d",
				test.system, test.bandwidth, got, test.want)
		}
	}
}

// TestSupportsAdmitsEverySystemTheFamilyShares: Supports withholds no system on
// the strength of the chip identity. Annex C is the Annex A core at a different
// roll-off and both take mode 0x04; Annex B -- the core some parts may lack --
// is offered on every part too (see TestJ83BIsOfferedOnEveryPart).
func TestSupportsAdmitsEverySystemTheFamilyShares(t *testing.T) {
	every := []sony.System{
		sony.SystemISDBT, sony.SystemISDBS, sony.SystemDVBC,
		sony.SystemDVBT, sony.SystemDVBT2, sony.SystemDVBS, sony.SystemDVBS2,
	}
	for _, chip := range []sony.Chip{
		sony.ChipCXD2856, sony.ChipCXD2857, sony.ChipCXD2878, 0,
	} {
		demod := New(&readingBus{values: map[[2]byte]byte{}}, 0xdc, 0xd8, sony.Config{})
		demod.Chip = chip
		for _, system := range every {
			if !demod.Supports(system) {
				t.Errorf("%s: Supports(%s) = false, want true", chip, system)
			}
		}
		if demod.Supports(sony.SystemUnspecified) {
			t.Errorf("%s: Supports(unspecified) should be false", chip)
		}
	}
}

// TestNominalRatesAreTheCrystalOverTheSymbolRate: the TRCG nominal rate register
// is a 38-bit fixed-point 8 * crystal / symbol rate, both in MHz. Deriving it
// rather than copying a constant catches a transcription slip -- 0x17A0800000
// for ISDB-T is this number plus 82.7 ppm. See isdbtBands.
func TestNominalRatesAreTheCrystalOverTheSymbolRate(t *testing.T) {
	// nominalRate renders 8 * xtal / symbolRate as the register's 38-bit
	// fixed point: six bits of integer, thirty-two of fraction.
	nominalRate := func(xtalMHz, symbolNumerator, symbolDenominator float64) []byte {
		exact := 8 * xtalMHz * symbolDenominator / symbolNumerator
		whole := uint64(math.Floor(exact))
		fraction := uint64(math.Round((exact - math.Floor(exact)) * (1 << 32)))
		return []byte{
			byte(whole), byte(fraction >> 24), byte(fraction >> 16),
			byte(fraction >> 8), byte(fraction),
		}
	}
	for _, test := range []struct {
		what        string
		got         []byte
		numerator   float64
		denominator float64
	}{
		// ISDB-T's symbol rate is 512/63 MHz, from ARIB STD-B31.
		{"ISDB-T 6 MHz", isdbtBands[6].nominalRate, 512, 63},
		// The DVB terrestrial tables are deliberately not checked: they hold the
		// 20.5 MHz column, and checking them would pin a defect in place. See
		// the note on dvbtBands.
	} {
		want := nominalRate(24, test.numerator, test.denominator)
		if !bytes.Equal(test.got, want) {
			t.Errorf("%s nominal rate is %#v, want %#v for a 24 MHz crystal",
				test.what, test.got, want)
		}
	}
}

// TestJ83BIsOfferedOnEveryPart: whether a given part carries the trellis-coded
// core is unsettled, so the system is not refused on a datasheet's omission.
func TestJ83BIsOfferedOnEveryPart(t *testing.T) {
	for _, chip := range []sony.Chip{
		sony.ChipCXD2856, sony.ChipCXD2857, sony.ChipCXD2878, 0,
	} {
		demod := New(&readingBus{values: map[[2]byte]byte{}}, 0xdc, 0xd8, sony.Config{})
		demod.Chip = chip
		if !demod.Supports(sony.SystemJ83B) {
			t.Errorf("%s: Supports(J.83B) = false", chip)
		}
	}
}

// TestJ83BSelectsTheOtherCableCore checks that Annex B takes mode 0x08, which
// does not collide with the core Annex A and Annex C share, and that it still
// counts as a J.83 system.
func TestJ83BSelectsTheOtherCableCore(t *testing.T) {
	annexB, err := demodMode(sony.SystemJ83B)
	if err != nil {
		t.Fatal(err)
	}
	annexA, err := demodMode(sony.SystemDVBC)
	if err != nil {
		t.Fatal(err)
	}
	if annexB != 0x08 {
		t.Errorf("J.83B selects mode %#02x, want 0x08", annexB)
	}
	if annexB == annexA {
		t.Error("Annex B and Annex A select the same core")
	}
	if !j83(sony.SystemJ83B) {
		t.Error("J.83B is not counted as a J.83 system")
	}
}

// TestJ83BBandSettingFollowsTheSymbolRate: the two configurations are different
// signals. Writing the American search range at a Japanese optical channel has
// the demodulator hunt for a rate the carrier does not use, which reads as a
// dead line rather than a driver bug.
func TestJ83BBandSettingFollowsTheSymbolRate(t *testing.T) {
	for _, test := range []struct {
		rateKS    uint32
		wantRegs  []byte
		wantReg14 byte
		wantIFMHz float64
		whatItIs  string
	}{
		{5_057, []byte{0x07, 0x26, 0x06, 0xBE}, 0x14, 3.7, "the American pair"},
		{5_361, []byte{0x07, 0x26, 0x06, 0xBE}, 0x14, 3.7, "the American pair"},
		{5_600, []byte{0x07, 0x77, 0x07, 0x77}, 0x10, 3.75, "Japan's optical distribution"},
	} {
		band := sony.J83BBandFor(test.rateKS * 1000)
		if band.Register14 != test.wantReg14 || band.IFMHz != test.wantIFMHz {
			t.Errorf("%d kSym/s (%s): register 0x14 = %#02x, IF = %v MHz; want %#02x, %v MHz",
				test.rateKS, test.whatItIs, band.Register14, band.IFMHz, test.wantReg14, test.wantIFMHz)
		}
		if string(band.SymbolRate) != string(test.wantRegs) {
			t.Errorf("%d kSym/s (%s): search range %#v, want %#v",
				test.rateKS, test.whatItIs, band.SymbolRate, test.wantRegs)
		}
	}
}
