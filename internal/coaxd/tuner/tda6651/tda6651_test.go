// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tda6651

import (
	"bytes"
	"testing"
)

// recorder logs every transfer, address first; the chip has no readback, so
// that log is all these tests can check.
type recorder struct{ writes [][]byte }

func (bus *recorder) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	return nil
}

// Read only satisfies i2c.Bus; the driver never reads.
func (bus *recorder) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

const address = 0xc2

// One tune worked out by hand, for the first UHF channel.
//
// 473142857 plus the 57 MHz intermediate frequency is 530142857, which is
// 3711 sevenths of a megahertz exactly -- the point of the odd step. 3711 is
// 0x0e7f. The band is the one ending at 550 MHz, so the two bytes are 0xc1 and
// 0x84.
func TestOneTuneEndToEnd(t *testing.T) {
	bus := &recorder{}
	if err := New(bus, address).Tune(473_142_857); err != nil {
		t.Fatal(err)
	}
	want := []byte{address, 0x0e, 0x7f, 0xc1, 0x84}
	if !bytes.Equal(bus.writes[0], want) {
		t.Errorf("wrote %#v, want %#v", bus.writes[0], want)
	}
}

// Every channel of the Japanese plan the chip is fitted to receive lands on a
// whole number of steps, so the divider should place the chip on the channel
// centre exactly. Checked against the step as it really is -- a seventh of a
// megahertz -- rather than against stepHz, since using stepHz here would be
// checking the arithmetic against itself.
func TestEveryChannelLandsExactly(t *testing.T) {
	for _, band := range []struct {
		name       string
		first      int
		last       int
		firstHz    int64
		firstIndex int
	}{
		{"VHF 1 to 3", 1, 3, 93_142_857, 1},
		{"cable C13 to C22", 13, 22, 111_142_857, 13},
		{"VHF 4 to 12", 4, 12, 173_142_857, 4},
		{"cable C23 to C63", 23, 63, 225_142_857, 23},
		{"UHF 13 to 62", 13, 62, 473_142_857, 13},
	} {
		t.Run(band.name, func(t *testing.T) {
			for channel := band.first; channel <= band.last; channel++ {
				frequency := band.firstHz + int64(channel-band.firstIndex)*6_000_000
				if frequency > FrequencyMax {
					t.Fatalf("channel %d is at %d Hz, above the chip's %d", channel, frequency, FrequencyMax)
				}
				if err := New(&recorder{}, address).Tune(frequency); err != nil {
					t.Errorf("channel %d at %d Hz: %v", channel, frequency, err)
					continue
				}
				// Where the chip really goes, kept in sevenths so that
				// nothing is rounded on the way.
				sevenths := Divider(frequency)*1_000_000 - intermediateHz*7
				if away := sevenths - frequency*7; away > 3 || away < -3 {
					t.Errorf("channel %d asked for %d Hz and lands %v Hz away",
						channel, frequency, float64(away)/7)
				}
			}
		})
	}
}

// A frequency belongs to the first band whose ceiling is at or above it, and a
// frequency exactly on a ceiling belongs to that band rather than the next.
func TestTheBandBoundariesAreInclusive(t *testing.T) {
	for _, one := range []struct {
		frequency int64
		band      byte
	}{
		{90_000_000, 0x81},
		{140_000_000, 0x81},
		{140_000_001, 0xa1},
		{170_000_000, 0xa1},
		{170_000_001, 0x62},
		{550_000_000, 0x84},
		{550_000_001, 0xa4},
		{770_000_000, 0xe4},
	} {
		bus := &recorder{}
		if err := New(bus, address).Tune(one.frequency); err != nil {
			t.Errorf("%d Hz: %v", one.frequency, err)
			continue
		}
		if got := bus.writes[0][4]; got != one.band {
			t.Errorf("%d Hz takes band %#02x, want %#02x", one.frequency, got, one.band)
		}
	}
}

func TestTuneRefusesWhatTheChipCannotReach(t *testing.T) {
	tuner := New(&recorder{}, address)
	if err := tuner.Tune(FrequencyMin - 1); err == nil {
		t.Error("tuning below the band was accepted")
	}
	if err := tuner.Tune(FrequencyMax + 1); err == nil {
		t.Error("tuning above the band was accepted")
	}
}

func TestInitializeSendsTheBoardsOpeningBytes(t *testing.T) {
	bus := &recorder{}
	if err := New(bus, address).Initialize(); err != nil {
		t.Fatal(err)
	}
	want := []byte{address, 0x0e, 0x7f, 0xc1, 0x80, 0x80}
	if !bytes.Equal(bus.writes[0], want) {
		t.Errorf("wrote %#v, want %#v", bus.writes[0], want)
	}
}
