// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package r848

import "testing"

// recordingBus captures every write and answers reads from replies, falling
// back to zeroes once that sequence runs out, so a calibration path can be
// driven without real silicon.
type recordingBus struct {
	writes  [][]byte
	replies [][]byte
	reply   int
}

func (bus *recordingBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *recordingBus) Read(_ byte, length int) ([]byte, error) {
	out := make([]byte, length)
	if bus.reply < len(bus.replies) {
		copy(out, bus.replies[bus.reply])
		bus.reply++
	}
	return out, nil
}

// lastWriteTo returns the value most recently written to a register by a
// single-register write, and whether there was one.
func (bus *recordingBus) lastWriteTo(register byte) (byte, bool) {
	for i := len(bus.writes) - 1; i >= 0; i-- {
		if len(bus.writes[i]) == 2 && bus.writes[i][0] == register {
			return bus.writes[i][1], true
		}
	}
	return 0, false
}

// TestSetTrackingFilterWritesRegister08NotRegister0B pins the first of two bugs
// this port corrects: the vendor writes the tracking-filter value to register
// 0x0B instead of 0x08. See setTrackingFilter in pll.go.
func TestSetTrackingFilterWritesRegister08NotRegister0B(t *testing.T) {
	bus := &recordingBus{}
	tuner := New(bus, Config{Address: 0x7a})
	tuner.registers = initRegistersHybrid
	before0B := *tuner.shadow(0x0b)

	if err := tuner.setTrackingFilter(500_000, tf82n270n); err != nil {
		t.Fatalf("setTrackingFilter: %v", err)
	}

	if _, ok := bus.lastWriteTo(0x08); !ok {
		t.Error("setTrackingFilter never wrote register 0x08")
	}
	if _, ok := bus.lastWriteTo(0x0b); ok {
		t.Error("setTrackingFilter wrote register 0x0B, which is the vendor bug")
	}
	if got := *tuner.shadow(0x0b); got != before0B {
		t.Errorf("shadow R11 = %#02x, want it left at %#02x", got, before0B)
	}
}

// TestSectionReturnsItsResultRatherThanAlwaysFailing pins the second bug:
// section throwing its result away and failing unconditionally. See section in
// imr.go.
func TestSectionReturnsItsResultRatherThanAlwaysFailing(t *testing.T) {
	bus := &recordingBus{}
	tuner := New(bus, Config{Address: 0x7a})
	tuner.registers = initRegistersHybrid

	got, err := tuner.section(imrPoint{gainX: 0x05, phaseY: 0x03, value: 200})
	if err != nil {
		t.Fatalf("section returned an error, which is the vendor bug: %v", err)
	}
	// The fake bus reads back zeroes, so every measurement ties at 0 and the
	// search settles on the first column. What matters is that a point came
	// back at all.
	if got.value != 0 {
		t.Errorf("section value = %d, want the measured 0", got.value)
	}
}
