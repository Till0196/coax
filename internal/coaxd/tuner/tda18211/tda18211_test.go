// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tda18211

import (
	"bytes"
	"testing"
)

// fakeBus records every write and answers reads with zeroes; nothing in these
// tests depends on read data.
type fakeBus struct{ writes [][]byte }

func (bus *fakeBus) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

func TestLookupPicksFirstBoundAtOrAbove(t *testing.T) {
	table := []tableEntry{{100, 1}, {200, 2}, {300, 3}}
	for _, test := range []struct {
		frequency uint32
		want      uint32
	}{{50, 1}, {100, 1}, {101, 2}, {200, 2}, {250, 3}, {300, 3}, {9999, 3}} {
		if got := lookup(table, test.frequency); got != test.want {
			t.Errorf("lookup(%d) = %d; want %d", test.frequency, got, test.want)
		}
	}
}

// TestSynthesiserMatchesReference pins the synthesiser arithmetic so the
// fixed-point scaling cannot drift. The first byte of each expectation is the
// post divider, straight from the table; the rest is the computed divider.
func TestSynthesiserMatchesReference(t *testing.T) {
	for _, test := range []struct {
		name      string
		frequency uint32
		// calibration is registers 0x08..0x0b.
		calibration [4]byte
		// main is registers 0x0c..0x0f.
		main [4]byte
	}{
		{"ch13", 473143, [4]byte{163, 104, 40, 2}, [4]byte{32, 119, 73, 36}},
		{"ch21", 521143, [4]byte{157, 106, 119, 111}, [4]byte{23, 123, 20, 146}},
		{"ch30", 575143, [4]byte{156, 108, 102, 221}, [4]byte{21, 117, 163, 109}},
		{"ch52", 707143, [4]byte{153, 99, 221, 37}, [4]byte{19, 122, 58, 73}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tuner := New(&fakeBus{})
			tuner.setCalibrationPLL(test.frequency + 3000)
			got := [4]byte{tuner.registers[0x08], tuner.registers[0x09], tuner.registers[0x0a], tuner.registers[0x0b]}
			if got != test.calibration {
				t.Errorf("calibration PLL = %v; want %v", got, test.calibration)
			}

			tuner = New(&fakeBus{})
			tuner.setMainPLL(test.frequency + 4000)
			got = [4]byte{tuner.registers[0x0c], tuner.registers[0x0d], tuner.registers[0x0e], tuner.registers[0x0f]}
			if got != test.main {
				t.Errorf("main PLL = %v; want %v", got, test.main)
			}
		})
	}
}

// TestCalPLLBandEdges checks the lookup on both sides of two bounds that are
// easy to get wrong, 159750 and 319500 kHz. Neither band is used in Japan, so a
// mistake in either goes unnoticed on the air.
func TestCalPLLBandEdges(t *testing.T) {
	for _, test := range []struct {
		frequency uint32
		want      uint32
	}{
		{146500, 0xbc30}, {146600, 0xbb2c}, {159750, 0xbb2c}, {159751, 0xba28},
		{293000, 0xac18}, {319000, 0xab16}, {319500, 0xab16}, {319501, 0xaa14},
	} {
		if got := lookup(calPLLTable, test.frequency); got != test.want {
			t.Errorf("calibration PLL at %d kHz = %#04x; want %#04x", test.frequency, got, test.want)
		}
	}
}

// TestMainPLLKeepsTheSeventhOffset checks that the 1/7 MHz offset Japanese
// terrestrial channels sit on reaches the divider, by comparing a frequency
// carrying it against the same channel without it.
func TestMainPLLKeepsTheSeventhOffset(t *testing.T) {
	offset := New(&fakeBus{})
	offset.setMainPLL(477143)
	plain := New(&fakeBus{})
	plain.setMainPLL(477000)
	if offset.registers[0x0f] == plain.registers[0x0f] && offset.registers[0x0e] == plain.registers[0x0e] {
		t.Error("the 1/7 MHz offset made no difference to the divider")
	}
}

func TestPowerUpDataStaysInsideTheRegisterImage(t *testing.T) {
	spans := []struct {
		name   string
		offset int
		length int
	}{
		{"powerUpImage", 0x01, len(powerUpImage)},
		{"agc1", regExtended17, 1},
		{"agc2", regExtended20, 1},
	}
	for _, band := range irBands {
		spans = append(spans,
			struct {
				name   string
				offset int
				length int
			}{"irBand.measure", regEasyProg3, len(band.measure)},
			struct {
				name   string
				offset int
				length int
			}{"irBand.calPLL", regEasyProg3, len(band.calPLL)})
	}
	for _, span := range spans {
		if span.offset+span.length > registerCount {
			t.Errorf("%s spans registers %d..%d, past the image of %d",
				span.name, span.offset, span.offset+span.length, registerCount)
		}
	}
	if len(agc1Ramp) == 0 || len(agc2Ramp) == 0 {
		t.Error("an AGC ramp is empty")
	}
}

func TestInitializeLoadsTheImageAndParksTheChip(t *testing.T) {
	bus := &fakeBus{}
	tuner := New(bus)
	if err := tuner.Initialize(); err != nil {
		t.Fatal(err)
	}
	// Power-up opens by loading the cold-start image at offset 1.
	want := append([]byte{0x01}, powerUpImage...)
	if !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("first write = %x; want %x", bus.writes[0], want)
	}
	// Initialize finishes by parking the power state and IF settings.
	if tuner.registers[0x05] != 0x19 || tuner.registers[0x06] != 0x60 {
		t.Errorf("registers 0x05/0x06 = %#02x/%#02x; want 0x19/0x60", tuner.registers[0x05], tuner.registers[0x06])
	}
}

func TestTuneStaysWithinTheRegisterImage(t *testing.T) {
	bus := &fakeBus{}
	tuner := New(bus)
	if err := tuner.Tune(473143); err != nil {
		t.Fatal(err)
	}
	for index, write := range bus.writes {
		if len(write) < 2 {
			t.Fatalf("write %d is empty", index)
		}
		if int(write[0])+len(write)-1 > registerCount {
			t.Errorf("write %d spans past the register image: offset %d, %d bytes", index, write[0], len(write)-1)
		}
	}
}
