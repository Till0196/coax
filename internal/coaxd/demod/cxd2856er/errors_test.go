// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// runBus answers reads of any length out of a per-bank map.
type runBus struct {
	bank     byte
	register byte
	values   map[[2]byte][]byte
}

func (bus *runBus) Write(_ byte, data []byte) error {
	switch {
	case len(data) == 2 && data[0] == 0x00:
		bus.bank = data[1]
	case len(data) == 1:
		bus.register = data[0]
	}
	return nil
}

func (bus *runBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	copy(response, bus.values[[2]byte{bus.bank, bus.register}])
	return response, nil
}

func countFor(t *testing.T, system sony.System, values map[[2]byte][]byte) ErrorCount {
	t.Helper()
	demod := New(&runBus{values: values}, 0xdc, 0xd8, sony.Config{})
	demod.System = system
	demod.State = sony.StateActive
	count, err := demod.ErrorCount()
	if err != nil {
		t.Fatalf("ErrorCount() in %s: %v", system, err)
	}
	return count
}

// TestErrorCountBasisPerSystem is the point of returning a pair rather than a
// rate: most of these systems count bits while DVB-T and DVB-C count something
// the chip never names, and a caller has to be able to tell which it was
// given.
func TestErrorCountBasisPerSystem(t *testing.T) {
	for _, test := range []struct {
		system sony.System
		values map[[2]byte][]byte
		want   ErrorCount
	}{
		{
			// ISDB-T counts whole packets: 100 packets x 204 bytes x 8 bits.
			system: sony.SystemISDBT,
			values: map[[2]byte][]byte{
				{0x60, 0x5b}: {0x00, 0x64},
				{0x60, 0x16}: {0x00, 0x00, 0x2a},
			},
			want: ErrorCount{Errors: 42, Total: 100 * 204 * 8, Basis: ErrorsPerBit},
		},
		{
			// DVB-T's period register selects 4096<<3 with no stated unit.
			system: sony.SystemDVBT,
			values: map[[2]byte][]byte{
				{0x10, 0x39}: {0x01},
				{0x10, 0x22}: {0x00, 0x2a},
				{0x10, 0x6f}: {0x03},
			},
			want: ErrorCount{Errors: 42, Total: 4096 << 3, Basis: ErrorsPerCounterPeriod},
		},
		{
			// A period selector of zero means 256 rather than 4096.
			system: sony.SystemDVBC,
			values: map[[2]byte][]byte{
				{0x40, 0x39}: {0x01},
				{0x40, 0x22}: {0x00, 0x07},
				{0x40, 0x6f}: {0x00},
			},
			want: ErrorCount{Errors: 7, Total: 256, Basis: ErrorsPerCounterPeriod},
		},
		{
			// DVB-T2 counts LDPC frames, and the short frame is 16200 bits.
			system: sony.SystemDVBT2,
			values: map[[2]byte][]byte{
				{0x20, 0x39}: {0x10, 0x00, 0x00, 0x2a},
				{0x20, 0x6f}: {0x02},
				{0x22, 0x5e}: {0x00},
			},
			want: ErrorCount{Errors: 42, Total: 4 * 16200, Basis: ErrorsPerBit},
		},
		{
			// The normal frame is 64800 bits.
			system: sony.SystemDVBT2,
			values: map[[2]byte][]byte{
				{0x20, 0x39}: {0x10, 0x00, 0x00, 0x2a},
				{0x20, 0x6f}: {0x02},
				{0x22, 0x5e}: {0x01},
			},
			want: ErrorCount{Errors: 42, Total: 4 * 64800, Basis: ErrorsPerBit},
		},
		{
			// The satellite Viterbi counter reports both halves itself.
			system: sony.SystemDVBS,
			values: map[[2]byte][]byte{
				{0xa0, 0x35}: {0x01, 0x00, 0x00, 0x2a, 0, 0, 0, 0, 0x00, 0x27, 0x10},
			},
			want: ErrorCount{Errors: 42, Total: 10000, Basis: ErrorsPerBit},
		},
		{
			// ISDB-S goes through the same Viterbi counter.
			system: sony.SystemISDBS,
			values: map[[2]byte][]byte{
				{0xa0, 0x35}: {0x01, 0x00, 0x00, 0x2a, 0, 0, 0, 0, 0x00, 0x27, 0x10},
			},
			want: ErrorCount{Errors: 42, Total: 10000, Basis: ErrorsPerBit},
		},
		{
			// DVB-S2 counts 64800-bit frames, 2^3 of them here.
			system: sony.SystemDVBS2,
			values: map[[2]byte][]byte{
				{0xb2, 0x30}: {0x01, 0x00, 0x00, 0x00, 0x2a},
				{0xa0, 0x7a}: {0x03},
			},
			want: ErrorCount{Errors: 42, Total: 8 * 64800, Basis: ErrorsPerBit},
		},
	} {
		if got := countFor(t, test.system, test.values); got != test.want {
			t.Errorf("%s: ErrorCount() = %+v, want %+v", test.system, got, test.want)
		}
	}
}

// TestErrorCountWaitsForTheValidBit covers the flag each counter carries: an
// unfinished measurement holds a stale count, and reporting it as zero errors
// would read as a perfect signal.
func TestErrorCountWaitsForTheValidBit(t *testing.T) {
	for _, test := range []struct {
		system sony.System
		values map[[2]byte][]byte
	}{
		{sony.SystemDVBT, map[[2]byte][]byte{{0x10, 0x39}: {0x00}}},
		{sony.SystemDVBT2, map[[2]byte][]byte{{0x20, 0x39}: {0x00, 0, 0, 0}}},
		{sony.SystemDVBS, map[[2]byte][]byte{{0xa0, 0x35}: make([]byte, 11)}},
		{sony.SystemDVBS2, map[[2]byte][]byte{{0xb2, 0x30}: {0x00, 0, 0, 0, 0}}},
		// ISDB-T has no valid bit; a packet count of zero is what says so.
		{sony.SystemISDBT, map[[2]byte][]byte{{0x60, 0x5b}: {0x00, 0x00}}},
	} {
		demod := New(&runBus{values: test.values}, 0xdc, 0xd8, sony.Config{})
		demod.System = test.system
		demod.State = sony.StateActive
		if _, err := demod.ErrorCount(); err == nil {
			t.Errorf("%s: an unfinished measurement should not be reported", test.system)
		}
	}
}

// TestErrorCountRate checks the convenience the pair still allows, including
// that it does not divide by a zero total.
func TestErrorCountRate(t *testing.T) {
	if got := (ErrorCount{Errors: 1, Total: 1000}).Rate(); got != 0.001 {
		t.Errorf("Rate() = %v, want 0.001", got)
	}
	if got := (ErrorCount{Errors: 1}).Rate(); got != 0 {
		t.Errorf("Rate() with no total = %v, want 0", got)
	}
}

// TestErrorCountNeedsASystem guards the default branch.
func TestErrorCountNeedsASystem(t *testing.T) {
	demod := New(&runBus{values: map[[2]byte][]byte{}}, 0xdc, 0xd8, sony.Config{})
	if _, err := demod.ErrorCount(); err == nil {
		t.Error("ErrorCount() on an unwoken chip should fail")
	}
}
