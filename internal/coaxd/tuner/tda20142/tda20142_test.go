// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tda20142

import (
	"fmt"
	"strings"
	"testing"
)

// bench answers reads from registers with a fixed pattern and records every
// byte the tuner puts on the bus, so a test can state the sequence the part
// sees rather than the calls the code makes.
type bench struct {
	registers [0x30]byte
	log       []string
	selected  byte
}

func newBench() *bench {
	b := &bench{}
	// A pattern with both halves of every byte populated, so a write that
	// keeps the wrong bits shows up.
	for i := range b.registers {
		b.registers[i] = byte(i) | 0x50
	}
	// What the filter calibration reads back: 0x11 non-zero ends the wait and
	// 0x10 carries the correction.
	b.registers[0x11] = 0x01
	b.registers[0x10] = 0x37
	return b
}

func (b *bench) Write(address byte, data []byte) error {
	if address != 0xa8 {
		return fmt.Errorf("unexpected address %#02x", address)
	}
	switch len(data) {
	case 1:
		b.selected = data[0]
	case 2:
		b.log = append(b.log, fmt.Sprintf("%02x=%02x", data[0], data[1]))
		if int(data[0]) < len(b.registers) {
			b.registers[data[0]] = data[1]
		}
	default:
		return fmt.Errorf("unexpected write of %d bytes", len(data))
	}
	return nil
}

func (b *bench) Read(address byte, length int) ([]byte, error) {
	if address != 0xa8 || length != 1 {
		return nil, fmt.Errorf("unexpected read of %d bytes from %#02x", length, address)
	}
	return []byte{b.registers[b.selected]}, nil
}

// TestNewFollowsThePartsBringUpSequence pins the bytes New puts on the bus. The
// sequence is the one the part requires, in the order it requires: the register
// set first, then the oscillator calibration pulse on 0x18, then the filter
// calibration and the correction it leaves in 0x10 going into 0x0f.
func TestNewFollowsThePartsBringUpSequence(t *testing.T) {
	b := newBench()
	if _, err := New(b); err != nil {
		t.Fatalf("New: %v", err)
	}
	want := strings.Join([]string{
		// configure
		"02=d3", "06=7f", "07=ff", "0f=df", "18=58", "1a=da", "22=ff", "23=72",
		"25=7d", "27=d7", "24=44", "0d=5d", "09=b1", "0a=7f", "0b=7b", "0c=00",
		"19=fa", "1b=5b", "21=71", "10=b7", "14=64",
		// calibrateOscillator
		"1a=da", "18=59", "18=d9", "1b=db", "18=d8",
		// calibrateFilter, then the correction read out of 0x10
		"0f=9f", "13=63", "12=d2", "10=b7", "10=b7", "0f=bf",
		"0f=b7", "0f=f7", "0f=d7", "12=52", "0d=3d",
	}, " ")
	if got := strings.Join(b.log, " "); got != want {
		t.Errorf("bus traffic\n got %s\nwant %s", got, want)
	}
}

// TestFilterSettingsForTheISDBSCarrier pins the two settings the fixed ISDB-S
// carrier lands on, and the edges of the brackets around it.
func TestFilterSettingsForTheISDBSCarrier(t *testing.T) {
	bandwidth := occupiedBandwidth(symbolRate, rollOff)
	if bandwidth != 31494 {
		t.Fatalf("occupied bandwidth = %d kHz, want 31494", bandwidth)
	}
	if got := dataRateSetting(bandwidth); got != 0x0c {
		t.Errorf("data rate setting = %#02x, want 0x0c", got)
	}
	if got := filterSetting(bandwidth); got != 0x4e {
		t.Errorf("filter setting = %#02x, want 0x4e", got)
	}
	for _, c := range []struct {
		bandwidth uint64
		dataRate  byte
		filter    byte
	}{
		{2000 + 2849, 0x0f, 0x13},   // just inside the narrowest bracket
		{2000 + 2850, 0x0f, 0x18},   // and just past it
		{2000 + 29699, 0x0c, 0x4e},  // the bracket the ISDB-S carrier sits in
		{2000 + 29700, 0x0c, 0x5d},  // the next one up
		{2000 + 99999, 0x08, 0x7f},  // the widest the table covers
		{2000 + 100000, 0x08, 0x7f}, // wider than that stays there
	} {
		if got := dataRateSetting(c.bandwidth); got != c.dataRate {
			t.Errorf("data rate setting at %d kHz = %#02x, want %#02x", c.bandwidth, got, c.dataRate)
		}
		if got := filterSetting(c.bandwidth); got != c.filter {
			t.Errorf("filter setting at %d kHz = %#02x, want %#02x", c.bandwidth, got, c.filter)
		}
	}
}
