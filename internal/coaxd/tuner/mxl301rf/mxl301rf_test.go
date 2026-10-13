// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mxl301rf

import (
	"bytes"
	"testing"
)

// recorder remembers what was written and answers reads from a queue.
type recorder struct {
	writes  [][]byte
	answers [][]byte
}

func (bus *recorder) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	return nil
}

func (bus *recorder) Read(address byte, length int) ([]byte, error) {
	answer := bus.answers[0]
	bus.answers = bus.answers[1:]
	return answer, nil
}

// The frequency conversion at the two ends of the band and at two channel
// centres that are not whole megahertz. 473142857 Hz is the first UHF channel's
// centre, and 473.142857 MHz in 10.6 fixed point is 30281 plus a fraction that
// rounds up.
func TestFrequencyCode(t *testing.T) {
	for _, want := range []struct {
		frequency uint32
		code      uint16
	}{
		{93000000, 93 << 6},
		{473142857, 30281},
		{515142857, 32969},
		{803142857, 51401},
	} {
		if got := frequencyCode(want.frequency); got != want.code {
			t.Errorf("frequencyCode(%d) = %d, want %d", want.frequency, got, want.code)
		}
	}
}

// A code is the frequency in units of 1/64 MHz, so converting it back has to
// land within half a unit of where it started.
func TestFrequencyCodeIsWithinHalfAStep(t *testing.T) {
	const step = 1000000 / 64
	for frequency := uint32(FrequencyMin); frequency <= FrequencyMax; frequency += 142857 {
		back := int64(frequencyCode(frequency)) * 1000000 / 64
		if difference := back - int64(frequency); difference > step/2 || difference < -step/2 {
			t.Fatalf("frequencyCode(%d) came back as %d, which is %d Hz away",
				frequency, back, difference)
		}
	}
}

func TestTuneRefusesWhatTheChipCannotReach(t *testing.T) {
	// A whole tuning step outside the band, not one hertz: the check allows half
	// a step either side. See bandEdgeSlack.
	const step = 1000000 / 64
	tuner := New(&recorder{}, 0xc4)
	if err := tuner.Tune(FrequencyMin - step); err == nil {
		t.Error("tuning below the band was accepted")
	}
	if err := tuner.Tune(FrequencyMax + step); err == nil {
		t.Error("tuning above the band was accepted")
	}
}

// A frequency far from every spur leaves the placeholder alone; one at a spur
// centre replaces both bytes.
func TestTuneShiftsTheSpurOnlyWhereThereIsOne(t *testing.T) {
	for _, want := range []struct {
		name             string
		frequency        uint32
		value, direction byte
	}{
		{"away from any spur", 473142857, 0x00, 0xa0},
		{"at a spur centre", 205500000, 0x2c, 0xa4},
		{"just inside a narrow threshold", 191300000, 0xe2, 0xa7},
		{"just outside a narrow threshold", 191100000, 0xd4, 0xa7},
	} {
		t.Run(want.name, func(t *testing.T) {
			bus := &recorder{}
			if err := New(bus, 0xc4).Tune(want.frequency); err != nil {
				t.Fatal(err)
			}
			begin := bus.writes[0]
			if begin[12] != want.value || begin[14] != want.direction {
				t.Errorf("spur shift %#02x/%#02x, want %#02x/%#02x",
					begin[12], begin[14], want.value, want.direction)
			}
		})
	}
}

// The chip has no repeated-start register read: a select write then a read, both
// to the same address.
func TestReadRegisterSelectsThenReads(t *testing.T) {
	bus := &recorder{answers: [][]byte{{0x5a}}}
	value, err := New(bus, 0xc4).readRegister(0x18)
	if err != nil {
		t.Fatal(err)
	}
	if value != 0x5a {
		t.Errorf("read %#02x, want 0x5a", value)
	}
	if want := []byte{0xc4, 0xfb, 0x18}; !bytes.Equal(bus.writes[0], want) {
		t.Errorf("selected with %#v, want %#v", bus.writes[0], want)
	}
}

// The level arithmetic: raw 0x2d0 and offset 0x120 leave 0x1b0 eighths of a dB
// above the reference 113 dB below a milliwatt, which is -59 dBm.
func TestSignalStrength(t *testing.T) {
	bus := &recorder{answers: [][]byte{
		{0xd0}, {0x02}, // raw = 0x2d0
		{0x00}, {0x09}, // offset = 0x120
	}}
	level, err := New(bus, 0xc4).SignalStrength()
	if err != nil {
		t.Fatal(err)
	}
	if level != -59000 {
		t.Errorf("signal strength %d, want -59000", level)
	}
}

// Every Japanese terrestrial channel this chip is fitted to receive, the ones
// outside the UHF band included. The edges are the point: a caller that folded
// the centre to kilohertz -- which several of these boards do -- hands back a
// frequency a few hundred hertz off, and the top channel's rounds to just
// outside the band. Without bandEdgeSlack, UHF 62 is refused.
func TestEveryChannelOfTheBandIsAccepted(t *testing.T) {
	for _, band := range []struct {
		name       string
		first      int
		last       int
		zeroKHz    int64
		stepKHz    int64
		firstIndex int
	}{
		{"VHF 1 to 3", 1, 3, 93143, 6000, 1},
		{"cable C13 to C22", 13, 22, 93143 + 3*6000, 6000, 13},
		{"VHF 4 to 12", 4, 12, 93143 + 13*6000, 6000, 4},
		{"cable C23 to C63", 23, 63, 93143 + 22*6000, 6000, 23},
		{"UHF 13 to 62", 13, 62, 473143, 6000, 13},
	} {
		t.Run(band.name, func(t *testing.T) {
			for channel := band.first; channel <= band.last; channel++ {
				kilohertz := band.zeroKHz + int64(channel-band.firstIndex)*band.stepKHz
				if err := New(&recorder{}, 0xc4).Tune(uint32(kilohertz * 1000)); err != nil {
					t.Errorf("channel %d at %d kHz: %v", channel, kilohertz, err)
				}
			}
		})
	}
}
