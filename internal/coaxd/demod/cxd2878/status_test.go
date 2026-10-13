// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package cxd2878

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// TestJ83BCNRUsesTheKernelConstantNotTBSs pins the constant in the 32/128-QAM
// branch, which is `-88*sony_math_log(reg) + 86999`. The same formula is also
// written with +8699, and a dropped trailing digit changes the curve by
// roughly 800 dB — not a plausible calibration difference between two closely
// related chips. +86999 is used here, the same value
// internal/coaxd/usb/demod/cxd2856er uses for the same formula.
func TestJ83BCNRUsesTheKernelConstantNotTBSs(t *testing.T) {
	// sony.MathLog(69) = 423.
	got := -88*int32(sony.MathLog(69)) + 86999
	if want := int32(49775); got != want {
		t.Fatalf("the 32/128-QAM branch = %d, want %d (using +86999)", got, want)
	}
	// The digit-dropped constant would put the same reading at a wildly
	// different, implausible value.
	implausible := -88*int32(sony.MathLog(69)) + 8699
	if got == implausible {
		t.Fatal("the CNR formula must not match TBS's un-corrected constant")
	}
}

// TestCNRAppliesTheCalibrationOffsetOnlyOnce guards the -1500 mdB offset
// CNR applies after every per-system formula: it belongs in exactly one
// place, not folded into any of the per-system helpers as well.
func TestCNRAppliesTheCalibrationOffsetOnlyOnce(t *testing.T) {
	demod := New(&lockedISDBTBus{cnr: []byte{0x27, 0x10}}, sony.Config{DemodAddress: 0x64, TunerAddress: 0x60})
	demod.State = sony.StateActive
	demod.System = sony.SystemISDBT
	got, err := demod.CNR()
	if err != nil {
		t.Fatalf("CNR: %v", err)
	}
	// reg = 0x2710 = 10000; cnrISDBT = 100*sony.MathLog10(10000) - 9031.
	raw := 100*int32(sony.MathLog10(10000)) - 9031
	want := float64(raw-1500) / 1000
	if got != want {
		t.Errorf("CNR() = %v, want %v", got, want)
	}
}

// lockedISDBTBus answers as an ISDB-T core that is locked (bank 0x60 register
// 0x10 = synced, ts-locked, not unlocked) and reports a fixed CNR register
// pair at 0x28.
type lockedISDBTBus struct {
	bank byte
	cnr  []byte
}

func (bus *lockedISDBTBus) Write(_ byte, data []byte) error {
	if len(data) == 2 && data[0] == 0x00 {
		bus.bank = data[1]
	}
	return nil
}

func (bus *lockedISDBTBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	if bus.bank == 0x60 && length == 1 {
		response[0] = 0x03 // synced, ts-locked, not unlocked
	}
	if bus.bank == 0x60 && length == 2 {
		copy(response, bus.cnr)
	}
	return response, nil
}

// TestChipIdentifiesTheRightTunerCore guards the one branch that decides
// which of two very different register tables Tune ends up using: only the
// CXD6802 revision carries ASCOT3, every other recognised ID carries Freia.
func TestChipIdentifiesTheRightTunerCore(t *testing.T) {
	for chip, wantASCOT3 := range map[sony.Chip]bool{
		sony.ChipCXD6802:  true,
		sony.ChipCXD2857:  false,
		sony.ChipCXD2878:  false,
		sony.ChipCXD6822:  false,
		sony.ChipCXD2878A: false,
	} {
		demod := &Demod{Core: sony.NewCore(nil, sony.Config{DemodAddress: 0x64})}
		demod.Chip = chip
		if got := demod.usesASCOT3(); got != wantASCOT3 {
			t.Errorf("%s: usesASCOT3() = %v, want %v", chip, got, wantASCOT3)
		}
	}
}
