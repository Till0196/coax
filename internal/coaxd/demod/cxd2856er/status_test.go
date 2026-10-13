// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// readingBus answers a one-byte read out of a per-bank map.
type readingBus struct {
	bank     byte
	register byte
	values   map[[2]byte]byte
}

func (bus *readingBus) Write(_ byte, data []byte) error {
	switch {
	case len(data) == 2 && data[0] == 0x00:
		bus.bank = data[1]
	case len(data) == 1:
		bus.register = data[0]
	}
	return nil
}

func (bus *readingBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	response[0] = bus.values[[2]byte{bus.bank, bus.register}]
	return response, nil
}

func lockFor(t *testing.T, system sony.System, values map[[2]byte]byte) sony.Lock {
	t.Helper()
	demod := New(&readingBus{values: values}, 0xdc, 0xd8, sony.Config{})
	demod.System = system
	demod.State = sony.StateActive
	lock, err := demod.Lock()
	if err != nil {
		t.Fatalf("sony.Lock() in %s: %v", system, err)
	}
	return lock
}

// TestLockReadsTheRightRegisterPerSystem: every system keeps its lock in a
// different bank, and reading the wrong one returns a plausible byte rather
// than an error, so a mix-up is silent.
func TestLockReadsTheRightRegisterPerSystem(t *testing.T) {
	for _, test := range []struct {
		system sony.System
		values map[[2]byte]byte
		want   sony.Lock
	}{
		// ISDB-T: bank 0x60 r10, sync bit 1, lock bit 0, give-up bit 4.
		{sony.SystemISDBT, map[[2]byte]byte{{0x60, 0x10}: 0x03}, sony.Lock{Synced: true, Locked: true}},
		{sony.SystemISDBT, map[[2]byte]byte{{0x60, 0x10}: 0x10}, sony.Lock{Failed: true}},
		// DVB-T lives in bank 0x10 and DVB-T2 in bank 0x20, with the same bits.
		{sony.SystemDVBT, map[[2]byte]byte{{0x10, 0x10}: 0x26}, sony.Lock{Synced: true, Locked: true}},
		{sony.SystemDVBT2, map[[2]byte]byte{{0x20, 0x10}: 0x26}, sony.Lock{Synced: true, Locked: true}},
		// Reading DVB-T's bank while in DVB-T2 must not produce a lock.
		{sony.SystemDVBT2, map[[2]byte]byte{{0x10, 0x10}: 0x26}, sony.Lock{}},
		// J.83 needs the demodulator bit before the stream bit counts.
		{sony.SystemDVBC, map[[2]byte]byte{{0x40, 0x88}: 0x01, {0x40, 0x10}: 0x20}, sony.Lock{Synced: true, Locked: true}},
		{sony.SystemDVBC, map[[2]byte]byte{{0x40, 0x88}: 0x00, {0x40, 0x10}: 0x20}, sony.Lock{}},
		{sony.SystemDVBC, map[[2]byte]byte{{0x40, 0x88}: 0x01, {0x40, 0x10}: 0x00}, sony.Lock{Synced: true}},
		// ISDB-S reads register 0x12, DVB-S register 0x11.
		{sony.SystemISDBS, map[[2]byte]byte{{0xa0, 0x12}: 0x40}, sony.Lock{Synced: true, Locked: true}},
		{sony.SystemISDBS, map[[2]byte]byte{{0xa0, 0x11}: 0x04}, sony.Lock{}},
		{sony.SystemDVBS, map[[2]byte]byte{{0xa0, 0x11}: 0x04}, sony.Lock{Synced: true, Locked: true}},
		{sony.SystemDVBS2, map[[2]byte]byte{{0xa0, 0x11}: 0x04}, sony.Lock{Synced: true, Locked: true}},
		{sony.SystemDVBS, map[[2]byte]byte{{0xa0, 0x12}: 0x40}, sony.Lock{}},
	} {
		if got := lockFor(t, test.system, test.values); got != test.want {
			t.Errorf("%s: sony.Lock() = %+v, want %+v", test.system, got, test.want)
		}
	}
}

// TestDVBTInvalidStateReportsNothing covers the all-ones sync field: the chip
// is confused, not synchronised, and reporting a lock from it would be worse
// than reporting none.
func TestDVBTInvalidStateReportsNothing(t *testing.T) {
	// 0x27 has the lock bit set as well, so anything that ignored the invalid
	// state would report a lock here.
	if got := lockFor(t, sony.SystemDVBT, map[[2]byte]byte{{0x10, 0x10}: 0x27}); got != (sony.Lock{}) {
		t.Errorf("an invalid hardware state reported %+v, want nothing", got)
	}
}

// TestLockNeedsASystem guards the default branch: an unwoken chip has no lock
// register, and answering "not locked" would look like a tuning failure.
func TestLockNeedsASystem(t *testing.T) {
	demod := New(&readingBus{values: map[[2]byte]byte{}}, 0xdc, 0xd8, sony.Config{})
	if _, err := demod.Lock(); err == nil {
		t.Error("sony.Lock() on an unwoken chip should fail")
	}
}

// pairBus answers a two-register read out of a per-bank map.
type pairBus struct {
	bank     byte
	register byte
	values   map[[2]byte][2]byte
}

func (bus *pairBus) Write(_ byte, data []byte) error {
	switch {
	case len(data) == 2 && data[0] == 0x00:
		bus.bank = data[1]
	case len(data) == 1:
		bus.register = data[0]
	}
	return nil
}

func (bus *pairBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	pair := bus.values[[2]byte{bus.bank, bus.register}]
	if length >= 2 {
		response[0], response[1] = pair[0], pair[1]
	}
	return response, nil
}

func strengthFor(t *testing.T, system sony.System, values map[[2]byte][2]byte) float64 {
	t.Helper()
	demod := New(&pairBus{values: values}, 0xdc, 0xd8, sony.Config{})
	demod.System = system
	demod.State = sony.StateActive
	level, err := demod.SignalStrength()
	if err != nil {
		t.Fatalf("SignalStrength() in %s: %v", system, err)
	}
	return level
}

// TestSignalStrengthReadsTheRightRegisterPerSystem is the same hazard the lock
// registers have: each system keeps its AGC somewhere else, and reading the
// wrong bank returns a plausible number rather than an error.
func TestSignalStrengthReadsTheRightRegisterPerSystem(t *testing.T) {
	for _, test := range []struct {
		system sony.System
		values map[[2]byte][2]byte
		// The expected level, worked out from the straight line for that
		// system with the reading below.
		want float64
	}{
		// ISDB-T: bank 0x60 r26, 12 bits shifted up by four.
		// 0x0800 << 4 = 32768; 32768*3775/1000 - 90185 = 33514 mdBm.
		{sony.SystemISDBT, map[[2]byte][2]byte{{0x60, 0x26}: {0x08, 0x00}}, 33.514},
		// DVB-T: bank 0x10, a different line. 32768*366/100 - 89520 = 30410.
		{sony.SystemDVBT, map[[2]byte][2]byte{{0x10, 0x26}: {0x08, 0x00}}, 30.410},
		// DVB-T2 shares DVB-T's line but lives in bank 0x20.
		{sony.SystemDVBT2, map[[2]byte][2]byte{{0x20, 0x26}: {0x08, 0x00}}, 30.410},
		// J.83: bank 0x40 r49. 32768*4045/1000 - 85224 = 47322 mdBm.
		{sony.SystemDVBC, map[[2]byte][2]byte{{0x40, 0x49}: {0x08, 0x00}}, 47.322},
		{sony.SystemDVBC, map[[2]byte][2]byte{{0x40, 0x49}: {0x08, 0x00}}, 47.322},
	} {
		if got := strengthFor(t, test.system, test.values); got != test.want {
			t.Errorf("%s: SignalStrength() = %v dBm, want %v", test.system, got, test.want)
		}
		// The same reading in another system's bank must not be picked up.
		if got := strengthFor(t, test.system, map[[2]byte][2]byte{{0x77, 0x26}: {0x08, 0x00}}); got == test.want {
			t.Errorf("%s: read the AGC out of the wrong bank", test.system)
		}
	}
}

// TestSatelliteLevelRounding covers the satellite AGC, the one conversion here
// with no decibel scale behind it. The rounding step is what a transcription
// would lose.
func TestSatelliteLevelRounding(t *testing.T) {
	for _, test := range []struct {
		agc  [2]byte
		want float64
	}{
		// 0 gain is the top of the range: (9700 - 0) * -10 = -97000 mdBm.
		{[2]byte{0x00, 0x00}, -97},
		// One step of AGC is 1400/403 = 3.47 tenths of a dB, and the rounding
		// takes it to 3 rather than 4.
		{[2]byte{0x00, 0x01}, -96.97},
		{[2]byte{0x01, 0x00}, -88.11},
		// The widest a 13-bit reading goes. The answer is not a level any
		// aerial produces; it is here to pin the arithmetic at the end of the
		// range, where a lost rounding step would show up.
		{[2]byte{0x1f, 0xff}, 187.55},
	} {
		got := strengthFor(t, sony.SystemISDBS, map[[2]byte][2]byte{{0xa0, 0x1f}: test.agc})
		if got != test.want {
			t.Errorf("an AGC of %#v gave %v dBm, want %v", test.agc, got, test.want)
		}
	}
}

// TestSignalStrengthNeedsASystem guards the default branch.
func TestSignalStrengthNeedsASystem(t *testing.T) {
	demod := New(&pairBus{values: map[[2]byte][2]byte{}}, 0xdc, 0xd8, sony.Config{})
	if _, err := demod.SignalStrength(); err == nil {
		t.Error("SignalStrength() on an unwoken chip should fail")
	}
}
