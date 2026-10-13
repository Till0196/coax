// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2857er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// countingBus answers reads of any length out of a per-bank map.
type countingBus struct {
	bank     byte
	register byte
	values   map[[2]byte][]byte
}

func (bus *countingBus) Write(_ byte, data []byte) error {
	switch {
	case len(data) == 2 && data[0] == 0x00:
		bus.bank = data[1]
	case len(data) == 1:
		bus.register = data[0]
	}
	return nil
}

func (bus *countingBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	copy(response, bus.values[[2]byte{bus.bank, bus.register}])
	return response, nil
}

func countFor(t *testing.T, system sony.System, values map[[2]byte][]byte) ErrorCount {
	t.Helper()
	demod := New(&countingBus{values: values}, sony.Config{DemodAddress: 0x64, TunerAddress: 0x60})
	demod.System = system
	count, err := demod.ErrorCount()
	if err != nil {
		t.Fatalf("ErrorCount() in %s: %v", system, err)
	}
	return count
}

// TestErrorCountReachesTheFamilyCounters checks each system lands on the right
// counter and carries the right basis: without the basis a caller cannot tell a
// bit error rate from a ratio against an unnamed period.
func TestErrorCountReachesTheFamilyCounters(t *testing.T) {
	for _, test := range []struct {
		system sony.System
		values map[[2]byte][]byte
		want   ErrorCount
	}{
		{
			// ISDB-T counts whole packets: 2 packets of 204 bytes.
			system: sony.SystemISDBT,
			values: map[[2]byte][]byte{
				{0x60, 0x5b}: {0x00, 0x02},
				{0x60, 0x16}: {0x00, 0x00, 0x07},
			},
			want: ErrorCount{Errors: 7, Total: 2 * 204 * 8, Basis: ErrorsPerBit},
		},
		{
			// The cable counter shares DVB-T's layout in bank 0x40, and its
			// denominator is the chip's own period.
			system: sony.SystemDVBC,
			values: map[[2]byte][]byte{
				{0x40, 0x39}: {0x01},
				{0x40, 0x22}: {0x00, 0x05},
				{0x40, 0x6f}: {0x00},
			},
			want: ErrorCount{Errors: 5, Total: 256, Basis: ErrorsPerCounterPeriod},
		},
		{
			// J.83B is the other cable core and reads the same bank.
			system: sony.SystemJ83B,
			values: map[[2]byte][]byte{
				{0x40, 0x39}: {0x01},
				{0x40, 0x22}: {0x00, 0x09},
				{0x40, 0x6f}: {0x01},
			},
			want: ErrorCount{Errors: 9, Total: 4096 << 1, Basis: ErrorsPerCounterPeriod},
		},
		{
			// The satellite Viterbi counter reports both halves itself.
			system: sony.SystemISDBS,
			values: map[[2]byte][]byte{
				{0xa0, 0x35}: {0x01, 0x00, 0x00, 0x0b, 0, 0, 0, 0, 0x00, 0x10, 0x00},
			},
			want: ErrorCount{Errors: 11, Total: 0x1000, Basis: ErrorsPerBit},
		},
	} {
		got := countFor(t, test.system, test.values)
		if got != test.want {
			t.Errorf("%s: ErrorCount() = %+v, want %+v", test.system, got, test.want)
		}
	}
}

// TestErrorCountSaysWhenItHasNone, rather than returning a zero count that a
// caller would read as a channel with no errors on it.
func TestErrorCountSaysWhenItHasNone(t *testing.T) {
	demod := New(&countingBus{values: map[[2]byte][]byte{}}, sony.Config{DemodAddress: 0x64})
	for _, system := range []sony.System{sony.SystemUnspecified, sony.SystemATSC, sony.SystemISDBS3} {
		demod.System = system
		if _, err := demod.ErrorCount(); err == nil {
			t.Errorf("ErrorCount() in %s returned a count", system)
		}
	}
	// A counter that has not finished says so through the family's own error.
	demod.System = sony.SystemDVBC
	if _, err := demod.ErrorCount(); err == nil {
		t.Error("an unfinished cable measurement was reported as a count")
	}
}
