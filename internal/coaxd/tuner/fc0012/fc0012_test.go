// Copyright (c) 2012 Hans-Frieder Vogt; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package fc0012

import (
	"errors"
	"testing"
)

// The demodulator this tuner sits behind, as the PX-W3U3's board addresses it.
const testDemod = 0x30

// fakeBus records the transfers the driver makes and answers reads from a
// register map.
type fakeBus struct {
	registers map[byte]byte
	// readback holds registers whose value is the chip's answer rather than what
	// was last written. The oscillator calibration register is one: a write
	// starts the calibration and the result is read from the same address.
	readback map[byte]byte
	writes   [][]byte
	selected byte
}

func newFakeBus() *fakeBus {
	return &fakeBus{registers: map[byte]byte{}, readback: map[byte]byte{}}
}

func (bus *fakeBus) answer(register byte) byte {
	if value, fixed := bus.readback[register]; fixed {
		return value
	}
	return bus.registers[register]
}

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testDemod {
		return errors.New("a transfer went somewhere other than the demodulator")
	}
	if len(data) < 2 || data[0] != repeater {
		return errors.New("a transfer did not open the demodulator's repeater")
	}
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	switch {
	case data[1] == writeAddress && len(data) == 4:
		bus.registers[data[2]] = data[3]
	case data[1] == writeAddress && len(data) == 3:
		bus.selected = data[2]
	case data[1] == readAddress:
	default:
		return errors.New("a transfer was addressed to neither half of the tuner")
	}
	return nil
}

func (bus *fakeBus) Read(byte, int) ([]byte, error) {
	return []byte{bus.answer(bus.selected)}, nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if err := bus.Write(address, write); err != nil {
		return nil, err
	}
	out := make([]byte, readLength)
	out[0] = bus.answer(bus.selected)
	return out, nil
}

// writesTo returns every value written to one tuner register, in order.
func (bus *fakeBus) writesTo(register byte) []byte {
	var values []byte
	for _, write := range bus.writes {
		if len(write) == 4 && write[1] == writeAddress && write[2] == register {
			values = append(values, write[3])
		}
	}
	return values
}

func newTestTuner(bus *fakeBus) *Tuner {
	return New(bus, testDemod, Config{})
}

// The three bytes that are the board's own choice among the chip's alternatives
// are the ones a slip in the table would touch.
func TestInitializeWritesTheBoardTable(t *testing.T) {
	bus := newFakeBus()
	if err := newTestTuner(bus).Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	for register, want := range map[byte]byte{
		0x01: 0x05, 0x08: 0xff, 0x09: 0x6e,
		// The board's own three: the AGC up-down mode, the AGC reading's
		// starting point, and the amplifier at full gain.
		0x0c: 0xf8, 0x12: 0x1b, 0x13: 0x10,
		0x15: 0x04,
	} {
		if got := bus.registers[register]; got != want {
			t.Errorf("register %#02x is %#02x, want %#02x", register, got, want)
		}
	}
	// Register 0x07's crystal bit stays clear on a 36 MHz board.
	if got := bus.registers[0x07]; got != 0x00 {
		t.Errorf("register 0x07 is %#02x, want 0x00 for a 36 MHz crystal", got)
	}
	if _, written := bus.registers[0x00]; written {
		t.Error("register 0 does not exist and was written anyway")
	}
}

func TestInitializeSetsTheCrystalBit(t *testing.T) {
	for _, xtal := range []uint32{Xtal27MHz, Xtal28800Hz} {
		bus := newFakeBus()
		if err := New(bus, testDemod, Config{XtalKHz: xtal}).Initialize(); err != nil {
			t.Fatalf("Initialize at %d kHz: %v", xtal, err)
		}
		if got := bus.registers[0x07]; got != 0x20 {
			t.Errorf("at %d kHz register 0x07 is %#02x, want 0x20", xtal, got)
		}
	}
	bus := newFakeBus()
	if err := New(bus, testDemod, Config{XtalKHz: 12345}).Initialize(); err == nil {
		t.Error("a crystal the driver has no settings for was accepted")
	}
}

// The PLL arithmetic, worked through by hand.
//
// 557.142857 MHz truncates to 557142 kHz: below 593334 and above 445000, so the
// multiplier is 6 and registers 5 and 6 start at 0x0a and 0x00. The oscillator
// runs at 3342852 kHz, above 3060000, so register 6 gains the high-band bit
// 0x08.
//
// Half the 36 MHz crystal is 18000 kHz. 3342852/18000 = 185 remainder 12852,
// and the remainder is at least half, so the divider rounds up to 186. 186/8 =
// 23 remainder 2, so register 1 is 2 and register 2 is 0x17.
//
// The fraction is taken before the rounding: (12852<<15)/18000 = 23396, which
// gains 32768: 0xdb64, so register 3 is 0xdb and register 4 is 0x64. Register 6
// ends as 0x08|0x20 with its top two bits replaced by the 6 MHz encoding 0x80:
// 0xa8. Register 5 ends as 0x0a|0x07.
func TestTuneWorksOutTheDivider(t *testing.T) {
	bus := newFakeBus()
	tuner := newTestTuner(bus)
	if err := tuner.Tune(557_142_857, 6_000_000); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	for register, want := range map[byte]byte{
		1: 0x02, 2: 0x17, 3: 0xdb, 4: 0x64, 5: 0x0f, 6: 0xa8,
	} {
		got := bus.writesTo(register)
		if len(got) == 0 {
			t.Errorf("register %d was never written", register)
			continue
		}
		if got[0] != want {
			t.Errorf("register %d was written %#02x, want %#02x", register, got[0], want)
		}
	}
}

func TestTuneRefusesAWidthTheChipCannotEncode(t *testing.T) {
	if err := newTestTuner(newFakeBus()).Tune(557_142_857, 5_000_000); err == nil {
		t.Error("a 5 MHz channel was accepted")
	}
}

// When the calibration lands against the top of its range the band bit is
// dropped and the calibration is run again.
func TestTuneRecalibratesWhenTheOscillatorSettlesAtItsEnd(t *testing.T) {
	bus := newFakeBus()
	// The channel above puts the oscillator in the high band, and this makes
	// the calibration read back past 0x3c.
	bus.readback[registerVCOCal] = 0x3d
	if err := newTestTuner(bus).Tune(557_142_857, 6_000_000); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	written := bus.writesTo(0x06)
	if len(written) != 2 {
		t.Fatalf("register 6 was written %d times, want 2", len(written))
	}
	if written[1] != written[0]&^0x08 {
		t.Errorf("the retry wrote %#02x, want the band bit cleared from %#02x", written[1], written[0])
	}
	// Five writes to the calibration register: a start (0x80, 0x00), the extra
	// 0x00 before the wait, and the retry's start (0x80, 0x00).
	if starts := bus.writesTo(registerVCOCal); len(starts) != 5 {
		t.Errorf("the calibration register was written %d times, want 5", len(starts))
	}
}

// The twenty-four steps are not the chip's own numbering; mapped through the
// gain table they must come out in order, -9.9 dB to +19.7 dB, with one pair of
// equal neighbours -- hence no-decrease rather than a strict rise.
func TestTheAmplifierLadderNeverGoesBackwards(t *testing.T) {
	previous, ok := LNAGainDB(0)
	if !ok {
		t.Fatal("step 0 has no gain")
	}
	if previous != -9.9 {
		t.Errorf("step 0 is %v dB, want -9.9", previous)
	}
	for step := 1; step < LNASteps; step++ {
		gain, ok := LNAGainDB(step)
		if !ok {
			t.Fatalf("step %d has no gain", step)
		}
		if gain < previous {
			t.Errorf("step %d is %v dB, below step %d at %v dB", step, gain, step-1, previous)
		}
		previous = gain
	}
	if previous != 19.7 {
		t.Errorf("the top step is %v dB, want 19.7", previous)
	}
	if _, ok := LNAGainDB(LNASteps); ok {
		t.Error("a step past the end of the ladder has a gain")
	}
}

func TestSetLNAGainWritesTheStep(t *testing.T) {
	bus := newFakeBus()
	tuner := newTestTuner(bus)
	// The board starts at the top of the ladder, which is the chip's 0x10.
	if err := tuner.SetLNAGain(LNASteps - 1); err != nil {
		t.Fatalf("SetLNAGain: %v", err)
	}
	if got := bus.registers[registerLNAGain]; got != 0x10 {
		t.Errorf("the top step wrote %#02x, want 0x10", got)
	}
	if err := tuner.SetLNAGain(0); err != nil {
		t.Fatalf("SetLNAGain: %v", err)
	}
	if got := bus.registers[registerLNAGain]; got != 0x02 {
		t.Errorf("the bottom step wrote %#02x, want 0x02", got)
	}
	if err := tuner.SetLNAGain(LNASteps); err == nil {
		t.Error("a step past the end of the ladder was accepted")
	}
}

// The level arithmetic. With the amplifier at the chip's 0x10 (top of the
// ladder, +19.7 dB) and an AGC reading of 0x00, the automatic gain is
// (|0-7| - 2 + 0)*2 = 10 and the level is -8 - 10 - 19 = -37 dBm; the 19 rather
// than 19.7 is the truncation to whole decibels.
func TestSignalStrengthFollowsTheReference(t *testing.T) {
	bus := newFakeBus()
	bus.readback[registerLNAGain] = 0x10
	bus.readback[registerAGCReading] = 0x00
	strength, err := newTestTuner(bus).SignalStrength()
	if err != nil {
		t.Fatalf("SignalStrength: %v", err)
	}
	if strength != -37 {
		t.Errorf("the level is %v dBm, want -37", strength)
	}
}
