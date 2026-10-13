// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package qm1d1c0042

import (
	"testing"
)

// recorder is an I2C master that remembers what was written and answers every
// read with one byte, which is what this chip's reads are.
type recorder struct {
	writes [][]byte
	answer byte
}

func (bus *recorder) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	return nil
}

func (bus *recorder) Read(address byte, length int) ([]byte, error) {
	return []byte{bus.answer}, nil
}

// written returns the last value written to a register, and whether it was
// written at all.
func (bus *recorder) written(register byte) (byte, bool) {
	value, found := byte(0), false
	for _, write := range bus.writes {
		if len(write) == 3 && write[1] == register {
			value, found = write[2], true
		}
	}
	return value, found
}

const address = 0xc6

func TestInitializeChoosesTheImageTheChipAsksFor(t *testing.T) {
	for row, image := range initialRegisters {
		bus := &recorder{answer: image[0x00]}
		tuner := New(bus, address, Config{LPF: true})
		if err := tuner.Initialize(); err != nil {
			t.Fatalf("row %d: %v", row, err)
		}
		// The upper half of the image goes out exactly as it came in. The
		// lower half does not: 0x0c is changed before it is written, and
		// waking the chip afterwards rewrites 0x01 and 0x05.
		for register := byte(0x11); register < registerCount; register++ {
			value, found := bus.written(register)
			if !found {
				t.Fatalf("row %d: register %#02x was not written", row, register)
			}
			if value != image[register] {
				t.Errorf("row %d: register %#02x written as %#02x, want %#02x",
					row, register, value, image[register])
			}
		}
		// 0x0d to 0x10 are not writable and must not have been written.
		for register := byte(0x0d); register <= 0x10; register++ {
			if _, found := bus.written(register); found {
				t.Errorf("row %d: register %#02x is not writable and was written", row, register)
			}
		}
	}
}

func TestInitializeRefusesAChipItDoesNotKnow(t *testing.T) {
	bus := &recorder{answer: 0x00}
	if err := New(bus, address, Config{}).Initialize(); err == nil {
		t.Error("a chip answering 0x00 was accepted")
	}
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

// The divider and the fraction, worked out by hand: 1049180 kHz over a 16 MHz
// crystal is 65.57375, so the whole part rounds to 66 and what is left is
// negative, -0.42625 of a crystal. The chip wants that as a 22-bit two's
// complement number in units of 2**-20, so its top bit is set.
func TestTuneWorksOutTheDivider(t *testing.T) {
	bus := &recorder{}
	tuner := New(bus, address, Config{LPF: true})
	tuner.regs = initialRegisters[0]
	if err := tuner.Tune(1049180); err != nil {
		t.Fatal(err)
	}

	// whole = 66: (66-12)/4 = 13 into 0x06, and 66-4*14-5 = 5 into 0x07.
	if value, _ := bus.written(0x06); value&0x3f != 13 {
		t.Errorf("register 0x06 = %#02x, want its low bits to be 13", value)
	}
	if value, _ := bus.written(0x07); value&0x0f != 5 {
		t.Errorf("register 0x07 = %#02x, want its low nibble to be 5", value)
	}

	// The fraction, reassembled from the three registers it is split across.
	low, _ := bus.written(0x0b)
	middle, _ := bus.written(0x0a)
	high, _ := bus.written(0x09)
	fraction := int64(high&0x3f)<<16 | int64(middle)<<8 | int64(low)
	if fraction < 1<<21 {
		t.Fatalf("fraction %#x is positive, and 1049180 kHz rounds up", fraction)
	}
	// Back to hertz: (whole + (fraction - 2**22)/2**20) * crystal.
	back := (66<<20 + fraction - 1<<22) * 16000 >> 20
	if difference := back - 1049180; difference > 1 || difference < -1 {
		t.Errorf("the divider comes back as %d kHz, which is %d away", back, difference)
	}
}

// Every frequency in the band must land in a row of the table, because a
// frequency in none of them would leave the previous tune's divider in place.
func TestEveryFrequencyHasABand(t *testing.T) {
	for frequency := uint32(FrequencyMin); frequency <= FrequencyMax; frequency += 1000 {
		found := false
		for index := 0; index < len(bands)-1; index++ {
			if frequency < bands[index].ceiling && frequency >= bands[index+1].ceiling {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%d kHz is in no band", frequency)
		}
	}
}

// Sleep and Wake are opposites, and each has to leave the shadow registers
// where the other found them.
func TestSleepAndWakeAreOpposites(t *testing.T) {
	tuner := New(&recorder{}, address, Config{})
	tuner.regs = initialRegisters[0]
	before := tuner.regs
	if err := tuner.Sleep(); err != nil {
		t.Fatal(err)
	}
	if tuner.regs == before {
		t.Fatal("sleeping changed nothing")
	}
	if err := tuner.Wake(); err != nil {
		t.Fatal(err)
	}
	if tuner.regs != before {
		t.Errorf("waking left the registers at %#v, want %#v", tuner.regs, before)
	}
}
