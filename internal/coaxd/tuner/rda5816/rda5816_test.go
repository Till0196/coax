// Copyright (c) Davin <smiledavin@gmail.com>; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package rda5816

import (
	"errors"
	"testing"
)

// transfer is one write with the address it went to: this driver reaches two
// addresses, and which one a write landed on is half of what is being checked.
type transfer struct {
	address byte
	data    []byte
}

// fakeBus is a register map per address, with a log of what was written to it.
type fakeBus struct {
	registers map[byte]map[byte]byte
	writes    []transfer
}

func newFakeBus() *fakeBus {
	return &fakeBus{registers: map[byte]map[byte]byte{
		ChannelAAddress: {},
		ChannelBAddress: {},
	}}
}

func (bus *fakeBus) Write(address byte, data []byte) error {
	registers, known := bus.registers[address]
	if !known {
		return errors.New("no chip at that address")
	}
	bus.writes = append(bus.writes, transfer{address, append([]byte(nil), data...)})
	for offset, value := range data[1:] {
		registers[data[0]+byte(offset)] = value
	}
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	registers, known := bus.registers[address]
	if !known || len(write) != 1 {
		return nil, errors.New("unexpected transfer")
	}
	out := make([]byte, readLength)
	for offset := range out {
		out[offset] = registers[write[0]+byte(offset)]
	}
	return out, nil
}

// lastWriteTo returns the value last written to one register of one half.
func (bus *fakeBus) lastWriteTo(address, register byte) (byte, bool) {
	for index := len(bus.writes) - 1; index >= 0; index-- {
		write := bus.writes[index]
		if write.address == address && write.data[0] == register && len(write.data) == 2 {
			return write.data[1], true
		}
	}
	return 0, false
}

func (bus *fakeBus) wroteTo(address byte) bool {
	for _, write := range bus.writes {
		if write.address == address {
			return true
		}
	}
	return false
}

func TestPLLWord(t *testing.T) {
	for _, test := range []struct {
		name        string
		frequencyHz uint32
		xtalKHz     uint32
		word        uint32
		lowBand     bool
	}{
		// 1550 MHz is above the split, so the synthesizer runs at the wanted
		// frequency: 1550 * 2^21/27.
		{"high band on 27 MHz", 1_550_000_000, Xtal27MHz, 1550 * stepsPer27MHz, false},
		// 950 MHz is at or below it, so the word is for twice the frequency.
		{"low band on 27 MHz", 950_000_000, Xtal27MHz, 1900 * stepsPer27MHz, true},
		// The split itself belongs to the low band.
		{"the split is low band", 1_075_000_000, Xtal27MHz, 2150 * stepsPer27MHz, true},
		{"just above the split", 1_076_000_000, Xtal27MHz, 1076 * stepsPer27MHz, false},
		{"high band on 24 MHz", 1_550_000_000, Xtal24MHz, 1550 * stepsPer24MHz, false},
		{"low band on 24 MHz", 950_000_000, Xtal24MHz, 1900 * stepsPer24MHz, true},
		// Anything finer than a megahertz is dropped.
		{"sub-megahertz is truncated", 1_550_999_999, Xtal27MHz, 1550 * stepsPer27MHz, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			word, lowBand := pllWord(test.frequencyHz, test.xtalKHz)
			if word != test.word || lowBand != test.lowBand {
				t.Fatalf("pllWord(%d, %d) = %d, %v; want %d, %v",
					test.frequencyHz, test.xtalKHz, word, lowBand, test.word, test.lowBand)
			}
		})
	}
}

func TestFilterCode(t *testing.T) {
	for _, test := range []struct {
		name          string
		symbolRateSPS uint32
		want          byte
	}{
		{"a typical transponder", 22_000_000, filterEnable | 18},
		{"a narrow one", 2_000_000, filterEnable | 5},
		// The 4 MHz margin alone exceeds the minimum, so the lower clamp is
		// reached only by a symbol rate of zero.
		{"no symbol rate at all", 0, filterEnable | 4},
		{"wider than the filter goes", 60_000_000, filterEnable | 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := filterCode(test.symbolRateSPS); got != test.want {
				t.Fatalf("filterCode(%d) = %#02x, want %#02x", test.symbolRateSPS, got, test.want)
			}
		})
	}
}

func TestSignalStrengthFromAGC(t *testing.T) {
	for _, test := range []struct {
		agc  byte
		want float64
	}{
		{0, 9.0},
		{82, -5.0},
		{91, -15.5},
		// Past the curve's last point, where the last segment is extended.
		{255, -90.9},
	} {
		if got := SignalStrengthFromAGC(test.agc); got != test.want {
			t.Fatalf("SignalStrengthFromAGC(%d) = %v, want %v", test.agc, got, test.want)
		}
	}
}

func TestInitializeBringsUpBothHalvesFromTheFirst(t *testing.T) {
	bus := newFakeBus()
	tuner := New(bus, Config{})
	if err := tuner.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// The reset is asserted and released before anything else.
	if len(bus.writes) < 2 ||
		bus.writes[0].data[0] != registerControl || bus.writes[0].data[1] != controlResetAssert ||
		bus.writes[1].data[0] != registerControl || bus.writes[1].data[1] != controlResetRelease {
		t.Fatalf("the first two writes were %v and %v, not the reset", bus.writes[0], bus.writes[1])
	}
	// A board that says nothing gets the 27 MHz clock divider.
	if value, ok := bus.lastWriteTo(ChannelAAddress, 0x75); !ok || value != 0x06 {
		t.Fatalf("0x75 was written %#02x (present %v), want the 27 MHz value 0x06", value, ok)
	}
	// This half enables itself outright.
	if value, ok := bus.lastWriteTo(ChannelAAddress, registerControl); !ok || value != controlEnableChannel {
		t.Fatalf("this half's control ended at %#02x, want %#02x", value, controlEnableChannel)
	}
	// The other half is enabled by having its bit set, not by being rewritten.
	if value, ok := bus.lastWriteTo(ChannelBAddress, registerControl); !ok || value != controlEnable {
		t.Fatalf("the other half's control ended at %#02x, want %#02x", value, controlEnable)
	}
	// The RF switch is set here and cleared there, on top of whatever else the
	// register holds.
	if got := bus.registers[ChannelAAddress][registerBand]; got != 0xd4|rfSwitchMask {
		t.Fatalf("this half's 0x65 ended at %#02x, want %#02x", got, 0xd4|rfSwitchMask)
	}
	if got := bus.registers[ChannelBAddress][registerBand]; got&rfSwitchMask != 0 {
		t.Fatalf("the other half's 0x65 ended at %#02x, want the switch bits clear", got)
	}
}

func TestInitializeFromTheSecondHalfLeavesTheFirstAlone(t *testing.T) {
	bus := newFakeBus()
	if err := New(bus, Config{Address: ChannelBAddress}).Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if bus.wroteTo(ChannelAAddress) {
		t.Fatal("the second half wrote to the first, which only the first may do")
	}
	if value, ok := bus.lastWriteTo(ChannelBAddress, registerControl); !ok || value != controlEnableChannel {
		t.Fatalf("the second half's control ended at %#02x, want %#02x", value, controlEnableChannel)
	}
}

func TestInitializeTakesTheOtherCrystal(t *testing.T) {
	bus := newFakeBus()
	if err := New(bus, Config{XtalKHz: Xtal24MHz}).Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if value, ok := bus.lastWriteTo(ChannelAAddress, 0x75); !ok || value != 0x07 {
		t.Fatalf("0x75 was written %#02x (present %v), want the 24 MHz value 0x07", value, ok)
	}
	// The band-switch thresholds move with the crystal too.
	if value, ok := bus.lastWriteTo(ChannelAAddress, 0x7a); !ok || value != 0xb0 {
		t.Fatalf("0x7a was written %#02x (present %v), want the 24 MHz value 0xb0", value, ok)
	}
}

func TestInitializeRefusesACrystalWithNoTable(t *testing.T) {
	if err := New(newFakeBus(), Config{XtalKHz: 16000}).Initialize(); err == nil {
		t.Fatal("a crystal the reference driver has no table for was accepted")
	}
}

func TestInitializeIsANoOpTheSecondTime(t *testing.T) {
	bus := newFakeBus()
	tuner := New(bus, Config{})
	if err := tuner.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	count := len(bus.writes)
	if err := tuner.Initialize(); err != nil {
		t.Fatalf("Initialize again: %v", err)
	}
	if len(bus.writes) != count {
		t.Fatalf("the second Initialize wrote %d more registers, want none", len(bus.writes)-count)
	}
}

func TestTuneLoadsTheDividerAndTheBand(t *testing.T) {
	bus := newFakeBus()
	bus.registers[ChannelAAddress][registerLock] = lockMask
	tuner := New(bus, Config{})
	if err := tuner.Tune(1_550_000_000, 22_000_000); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	word := uint32(1550 * stepsPer27MHz)
	for offset := 0; offset < 4; offset++ {
		want := byte(word >> (24 - 8*offset))
		if got := bus.registers[ChannelAAddress][registerPLL+byte(offset)]; got != want {
			t.Fatalf("divider byte %d was %#02x, want %#02x", offset, got, want)
		}
	}
	if got := bus.registers[ChannelAAddress][registerBand]; got&(bandHigh|bandLow) != bandHigh {
		t.Fatalf("0x65 ended at %#02x, want the high band selected", got)
	}
	if got := bus.registers[ChannelAAddress][registerFilter]; got != filterCode(22_000_000) {
		t.Fatalf("the filter was set to %#02x, want %#02x", got, filterCode(22_000_000))
	}
	// The tune is bracketed by the two control values, the second last of all.
	if value, ok := bus.lastWriteTo(ChannelAAddress, registerControl); !ok || value != controlTune {
		t.Fatalf("the control register ended at %#02x, want %#02x", value, controlTune)
	}
}

func TestTuneSelectsTheLowBandBelowTheSplit(t *testing.T) {
	bus := newFakeBus()
	bus.registers[ChannelAAddress][registerLock] = lockMask
	// Something already selected the high band, and it has to be cleared.
	bus.registers[ChannelAAddress][registerBand] = bandHigh
	if err := New(bus, Config{}).Tune(950_000_000, 22_000_000); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if got := bus.registers[ChannelAAddress][registerBand]; got&(bandHigh|bandLow) != bandLow {
		t.Fatalf("0x65 ended at %#02x, want the low band selected", got)
	}
}

func TestTuneReportsASynthesizerThatNeverSettles(t *testing.T) {
	bus := newFakeBus()
	// Only one of the two lock indications ever appears.
	bus.registers[ChannelAAddress][registerLock] = 0x01
	if err := New(bus, Config{}).Tune(1_550_000_000, 22_000_000); err == nil {
		t.Fatal("a PLL that never locked was reported as tuned")
	}
}

func TestPLLLocked(t *testing.T) {
	bus := newFakeBus()
	bus.registers[ChannelAAddress][registerLock] = lockMask | 0xf0
	locked, err := New(bus, Config{}).PLLLocked()
	if err != nil {
		t.Fatalf("PLLLocked: %v", err)
	}
	if !locked {
		t.Fatal("both lock indications were set and the chip was called unlocked")
	}
}

func TestIdentifySplitsTheIdentifierAcrossTheNibble(t *testing.T) {
	bus := newFakeBus()
	bus.registers[ChannelAAddress][registerChipID] = 0x58
	bus.registers[ChannelAAddress][registerRevision] = 0x16
	chip, revision, err := New(bus, Config{}).Identify()
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if chip != 0x581 || revision != 6 {
		t.Fatalf("Identify = %#03x revision %d, want 0x581 revision 6", chip, revision)
	}
}
