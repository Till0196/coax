// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package qm1d1b0004

import (
	"bytes"
	"testing"
)

// recorder remembers what was written and answers every read with zeros.
type recorder struct{ writes [][]byte }

func (bus *recorder) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	return nil
}

func (bus *recorder) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

const address = 0xc0

// The whole of one tune, worked out by hand for one transponder.
//
// The loop steps in crystalKHz/4 = 1000 kHz, so 1049480 kHz rounds to a divider
// word of 1049. That frequency is in the second band, whose control byte 0xd2
// has the prescaler bit set, so the word is repacked as (1049<<1 &^ 0x1f) |
// (1049 & 0x0f) = 0x829. The filter is a constant 9 = 0b1001, split as 0b10
// into the first byte and 0b01 into the second.
func TestOneTuneEndToEnd(t *testing.T) {
	bus := &recorder{}
	if err := New(bus, address, Config{}).Tune(1_049_480); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{
		{address, 0x40 | 0x08, 0x29, 0xe0, 0xd2},
		{address, 0xe4},
		{address, 0xe4 | 0x10, 0xd2 | 0x04},
	}
	if len(bus.writes) != len(want) {
		t.Fatalf("wrote %d times, want %d: %#v", len(bus.writes), len(want), bus.writes)
	}
	for index, one := range want {
		if !bytes.Equal(bus.writes[index], one) {
			t.Errorf("write %d was %#v, want %#v", index, bus.writes[index], one)
		}
	}
}

// Both ends of every band are checked, because a boundary written with the wrong
// comparison moves a whole band. The second band ends at 1073 MHz rather than
// 1072: the chip's tables are keyed in whole megahertz and the comparison there
// admits 1072.
func TestTheBandsAreWhereTheTableSaysTheyAre(t *testing.T) {
	for _, one := range []struct {
		frequency uint32
		control   byte
	}{
		{950_000, 0xb2},
		{985_999, 0xb2},
		{986_000, 0xd2},
		{1_072_999, 0xd2},
		{1_073_000, 0xe2},
		{1_153_999, 0xe2},
		{1_154_000, 0x20},
		{1_290_999, 0x20},
		{1_291_000, 0x40},
		{1_446_999, 0x40},
		{1_447_000, 0x60},
		{1_614_999, 0x60},
		{1_615_000, 0x80},
		{1_790_999, 0x80},
		{1_791_000, 0xa0},
		{1_971_999, 0xa0},
		{1_972_000, 0xc0},
		{2_150_000, 0xc0},
	} {
		if got := controlFor(one.frequency); got != one.control {
			t.Errorf("%d kHz takes %#02x, want %#02x", one.frequency, got, one.control)
		}
	}
}

// The three bands below 1154 MHz prescale by sixteen and the rest by
// thirty-two, packing the divider word differently. A word packed the wrong way
// tunes somewhere else entirely and nothing in the chip would say so.
func TestThePrescaledBandsRepackTheDivider(t *testing.T) {
	for _, one := range []struct {
		name      string
		frequency uint32
		word      uint16
	}{
		// 1000000 kHz over the 1000 kHz step is 1000 = 0x3e8, and the band
		// ending at 1073 MHz prescales:
		// (0x3e8<<1) &^ 0x1f | (0x3e8 & 0x0f) = 0x7c0 | 0x8.
		{"prescaled", 1_000_000, 0x7c8},
		// 1200 MHz is in the fourth band, which does not prescale, so the word
		// is the divider as it is: 1200 = 0x4b0.
		{"not prescaled", 1_200_000, 0x4b0},
	} {
		t.Run(one.name, func(t *testing.T) {
			bus := &recorder{}
			if err := New(bus, address, Config{}).Tune(one.frequency); err != nil {
				t.Fatal(err)
			}
			first := bus.writes[0]
			word := uint16(first[1]&0x3f)<<8 | uint16(first[2])
			if word != one.word {
				t.Errorf("divider word %#x, want %#x", word, one.word)
			}
		})
	}
}

// The filter is a constant, not a calculation: both boards receive one thing and
// write the same two bytes for it.
func TestTheFilterIsTheSameWhateverIsTuned(t *testing.T) {
	for _, frequency := range []uint32{FrequencyMin, 1_049_480, 1_500_000, FrequencyMax} {
		bus := &recorder{}
		if err := New(bus, address, Config{}).Tune(frequency); err != nil {
			t.Fatalf("%d kHz: %v", frequency, err)
		}
		third := bus.writes[2]
		lpf := (third[1]&0x18)>>1 | (third[2]&0x0c)>>2
		if lpf != 9 {
			t.Errorf("%d kHz: filter %d, want 9", frequency, lpf)
		}
	}
}

// A board that asks for a settled charge pump takes a fourth write, which is
// the first byte again with the pump moved on and the divider untouched.
func TestTheSettledPumpRepeatsTheDivider(t *testing.T) {
	bus := &recorder{}
	if err := New(bus, address, Config{Pump: 1, SettledPump: 2}).Tune(1_049_480); err != nil {
		t.Fatal(err)
	}
	if len(bus.writes) != 4 {
		t.Fatalf("wrote %d times, want 4: %#v", len(bus.writes), bus.writes)
	}
	first, last := bus.writes[0][1], bus.writes[3][1]
	if first != 0x20|0x08 {
		t.Errorf("the first byte was %#02x, want %#02x", first, 0x20|0x08)
	}
	if last != 0x40|0x08 {
		t.Errorf("the last byte was %#02x, want %#02x", last, 0x40|0x08)
	}
	if first&0x1f != last&0x1f {
		t.Errorf("the last byte carries divider %#02x, want the first byte's %#02x", last&0x1f, first&0x1f)
	}
}

// A board that leaves SettledPump clear takes three writes.
func TestWithoutASettledPumpThereIsNoFourthWrite(t *testing.T) {
	bus := &recorder{}
	if err := New(bus, address, Config{}).Tune(1_049_480); err != nil {
		t.Fatal(err)
	}
	if len(bus.writes) != 3 {
		t.Errorf("wrote %d times, want 3: %#v", len(bus.writes), bus.writes)
	}
}

// The lock bit is the only thing the chip reports, and a board that can read it
// waits for it rather than assuming the tune took.
func TestAwaitingLockReadsUntilTheChipSaysSo(t *testing.T) {
	bus := &lockingRecorder{lockAfter: 3}
	if err := New(bus, address, Config{AwaitLock: true}).Tune(1_049_480); err != nil {
		t.Fatal(err)
	}
	if bus.reads != 4 {
		t.Errorf("read %d times, want 4", bus.reads)
	}
}

// A loop that never pulls in is a failed tune, not a silent one.
func TestAwaitingLockGivesUp(t *testing.T) {
	bus := &lockingRecorder{lockAfter: -1}
	err := New(bus, address, Config{AwaitLock: true}).Tune(1_049_480)
	if err == nil {
		t.Fatal("a tuner that never locked was reported as tuned")
	}
	if bus.reads != lockAttempts {
		t.Errorf("read %d times, want %d", bus.reads, lockAttempts)
	}
}

// A board that does not read is not made to: nothing comes back from the bus.
func TestWithoutAwaitLockNothingIsRead(t *testing.T) {
	bus := &lockingRecorder{lockAfter: 0}
	if err := New(bus, address, Config{}).Tune(1_049_480); err != nil {
		t.Fatal(err)
	}
	if bus.reads != 0 {
		t.Errorf("read %d times, want none", bus.reads)
	}
}

// lockingRecorder answers reads with the lock bit clear until lockAfter of
// them have gone by. A negative lockAfter never locks.
type lockingRecorder struct {
	recorder
	lockAfter int
	reads     int
}

func (bus *lockingRecorder) Read(address byte, length int) ([]byte, error) {
	answer := make([]byte, length)
	if bus.lockAfter >= 0 && bus.reads >= bus.lockAfter {
		answer[0] = lockedBit
	}
	bus.reads++
	return answer, nil
}

func TestTuneRefusesWhatTheChipCannotReach(t *testing.T) {
	tuner := New(&recorder{}, address, Config{})
	if err := tuner.Tune(FrequencyMin - 1); err == nil {
		t.Error("tuning below the band was accepted")
	}
	if err := tuner.Tune(FrequencyMax + 1); err == nil {
		t.Error("tuning above the band was accepted")
	}
}
