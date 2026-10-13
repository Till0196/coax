// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package sony

import "testing"

// The expected numbers are the codes and reference dividers each tuner core
// takes, from the x_pon and tune blocks of the ASCOT3 and the Freia.

func TestASCOT3XtalCodeCoversEveryCrystal(t *testing.T) {
	want := map[TunerXtal]byte{
		TunerXtal16000KHz: 0x10,
		TunerXtal20500KHz: 0xd4,
		TunerXtal24000KHz: 0x18,
		TunerXtal41000KHz: 0x69,
	}
	for xtal, expected := range want {
		got, err := ASCOT3XtalCode(xtal)
		if err != nil {
			t.Errorf("ASCOT3XtalCode(%s) = %v, want 0x%02x", xtal, err, expected)
			continue
		}
		if got != expected {
			t.Errorf("ASCOT3XtalCode(%s) = 0x%02x, want 0x%02x", xtal, got, expected)
		}
	}
}

func TestASCOT3ReferenceCoversEveryCrystal(t *testing.T) {
	want := map[TunerXtal]byte{
		TunerXtal16000KHz: 0x02,
		TunerXtal20500KHz: 0x02,
		TunerXtal24000KHz: 0x03,
		TunerXtal41000KHz: 0x05,
	}
	for xtal, expected := range want {
		got, err := ASCOT3Reference(xtal)
		if err != nil {
			t.Errorf("ASCOT3Reference(%s) = %v, want 0x%02x", xtal, err, expected)
			continue
		}
		if got != expected {
			t.Errorf("ASCOT3Reference(%s) = 0x%02x, want 0x%02x", xtal, got, expected)
		}
	}
}

// The 20.5 MHz crystal is the only one that moves this byte, and the cable
// systems pin it whatever is fitted. Getting that backwards was the original
// defect, so it is checked both ways round.
func TestASCOT3LocalTuningFollowsTheCrystalOnlyOffCable(t *testing.T) {
	for _, one := range []struct {
		xtal  TunerXtal
		cable bool
		want  byte
	}{
		{TunerXtal16000KHz, false, 0xb4},
		{TunerXtal20500KHz, false, 0x8c},
		{TunerXtal24000KHz, false, 0xb4},
		{TunerXtal41000KHz, false, 0xb4},
		{TunerXtal16000KHz, true, 0x1c},
		{TunerXtal20500KHz, true, 0x1c},
		{TunerXtal24000KHz, true, 0x1c},
		{TunerXtal41000KHz, true, 0x1c},
	} {
		if got := ASCOT3LocalTuning(one.xtal, one.cable); got != one.want {
			t.Errorf("ASCOT3LocalTuning(%s, cable=%v) = 0x%02x, want 0x%02x",
				one.xtal, one.cable, got, one.want)
		}
	}
}

func TestFreiaServesTwoCrystalsAndRefusesTheRest(t *testing.T) {
	for _, one := range []struct {
		xtal      TunerXtal
		code, ref byte
	}{
		{TunerXtal16000KHz, 0x10, 0x02},
		{TunerXtal24000KHz, 0x18, 0x03},
	} {
		code, err := FreiaXtalCode(one.xtal)
		if err != nil || code != one.code {
			t.Errorf("FreiaXtalCode(%s) = 0x%02x, %v, want 0x%02x", one.xtal, code, err, one.code)
		}
		ref, err := FreiaReference(one.xtal)
		if err != nil || ref != one.ref {
			t.Errorf("FreiaReference(%s) = 0x%02x, %v, want 0x%02x", one.xtal, ref, err, one.ref)
		}
	}
	// The Freia core has no number for these two, and falling back to its
	// 16 MHz ones would put a wrong code on a real chip, so they are refused.
	for _, xtal := range []TunerXtal{TunerXtal20500KHz, TunerXtal41000KHz} {
		if _, err := FreiaXtalCode(xtal); err == nil {
			t.Errorf("FreiaXtalCode(%s) answered a code; the core has none", xtal)
		}
		if _, err := FreiaReference(xtal); err == nil {
			t.Errorf("FreiaReference(%s) answered a divider; the core has none", xtal)
		}
	}
}

// A value outside the enumeration used to reach the chip as a zero, which is
// not a crystal code at all.
func TestUnknownCrystalIsRefusedRatherThanZero(t *testing.T) {
	unknown := TunerXtal(99)
	if _, err := ASCOT3XtalCode(unknown); err == nil {
		t.Error("ASCOT3XtalCode answered for a crystal it does not know")
	}
	if _, err := ASCOT3Reference(unknown); err == nil {
		t.Error("ASCOT3Reference answered for a crystal it does not know")
	}
	if _, err := FreiaXtalCode(unknown); err == nil {
		t.Error("FreiaXtalCode answered for a crystal it does not know")
	}
	if _, err := FreiaReference(unknown); err == nil {
		t.Error("FreiaReference answered for a crystal it does not know")
	}
}
