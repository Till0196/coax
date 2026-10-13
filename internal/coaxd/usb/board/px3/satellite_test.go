// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"bytes"
	"testing"
)

// These boards were shipped to receive twenty-four transponders, and the bytes
// the satellite tuner is known to be programmed with for each of them are the
// chip's arithmetic worked out ahead of time. This package drives that
// arithmetic instead, which reaches everything in between as well.
//
// That is only safe if it lands on the same bytes for those twenty-four, so
// they are kept here and checked against what actually goes out on the bus.

// transponderTable is the twenty-four known-good settings: a downlink
// frequency and the eight bytes the tuner takes for it.
var transponderTable = [24]struct {
	downlinkKHz uint64
	registers   [8]byte
}{
	{11_727_480, [8]byte{0x28, 0x29, 0xe0, 0xd2, 0xe4, 0xf4, 0xd6, 0x48}},
	{11_765_840, [8]byte{0x24, 0x40, 0xe0, 0xe2, 0xe4, 0xf4, 0xe6, 0x44}},
	{11_804_200, [8]byte{0x24, 0x66, 0xe0, 0xe2, 0xe4, 0xf4, 0xe6, 0x44}},
	{11_842_560, [8]byte{0x24, 0x8d, 0xe0, 0x20, 0xe4, 0xf4, 0x24, 0x44}},
	{11_880_920, [8]byte{0x24, 0xb3, 0xe0, 0x20, 0xe4, 0xf4, 0x24, 0x44}},
	{11_919_280, [8]byte{0x24, 0xd9, 0xe0, 0x20, 0xe4, 0xf4, 0x24, 0x44}},
	{11_957_640, [8]byte{0x25, 0x00, 0xe0, 0x20, 0xe4, 0xf4, 0x24, 0x45}},
	{11_996_000, [8]byte{0x25, 0x26, 0xe0, 0x40, 0xe4, 0xf4, 0x44, 0x45}},
	{12_034_360, [8]byte{0x25, 0x4c, 0xe0, 0x40, 0xe4, 0xf4, 0x44, 0x45}},
	{12_072_720, [8]byte{0x25, 0x73, 0xe0, 0x40, 0xe4, 0xf4, 0x44, 0x45}},
	{12_111_080, [8]byte{0x25, 0x99, 0xe0, 0x40, 0xe4, 0xf4, 0x44, 0x45}},
	{12_149_440, [8]byte{0x25, 0xbf, 0xe0, 0x60, 0xe4, 0xf4, 0x64, 0x45}},
	{12_291_000, [8]byte{0x26, 0x4d, 0xe0, 0x60, 0xe4, 0xf4, 0x64, 0x46}},
	{12_331_000, [8]byte{0x26, 0x75, 0xe0, 0x80, 0xe4, 0xf4, 0x84, 0x46}},
	{12_371_000, [8]byte{0x26, 0x9d, 0xe0, 0x80, 0xe4, 0xf4, 0x84, 0x46}},
	{12_411_000, [8]byte{0x26, 0xc5, 0xe0, 0x80, 0xe4, 0xf4, 0x84, 0x46}},
	{12_451_000, [8]byte{0x26, 0xed, 0xe0, 0x80, 0xe4, 0xf4, 0x84, 0x46}},
	{12_491_000, [8]byte{0x27, 0x15, 0xe0, 0xa0, 0xe4, 0xf4, 0xa4, 0x47}},
	{12_531_000, [8]byte{0x27, 0x3d, 0xe0, 0xa0, 0xe4, 0xf4, 0xa4, 0x47}},
	{12_571_000, [8]byte{0x27, 0x65, 0xe0, 0xa0, 0xe4, 0xf4, 0xa4, 0x47}},
	{12_611_000, [8]byte{0x27, 0x8d, 0xe0, 0xa0, 0xe4, 0xf4, 0xa4, 0x47}},
	{12_651_000, [8]byte{0x27, 0xb5, 0xe0, 0xc0, 0xe4, 0xf4, 0xc4, 0x47}},
	{12_691_000, [8]byte{0x27, 0xdd, 0xe0, 0xc0, 0xe4, 0xf4, 0xc4, 0x47}},
	{12_731_000, [8]byte{0x28, 0x05, 0xe0, 0xc0, 0xe4, 0xf4, 0xc4, 0x48}},
}

// writesFor is the four writes the eight bytes go out as: the first four, the
// fifth on its own, then the sixth and seventh, then the eighth.
func writesFor(registers [8]byte) [][]byte {
	return [][]byte{
		{registers[0], registers[1], registers[2], registers[3]},
		{registers[4]},
		{registers[5], registers[6]},
		{registers[7]},
	}
}

// TestEveryTransponderIsProgrammedTheWayTheBoardDoes is the whole basis for
// computing rather than looking up. Twenty-four frequencies, every byte.
func TestEveryTransponderIsProgrammedTheWayTheBoardDoes(t *testing.T) {
	oscillator, ok := satelliteOscillatorKHz()
	if !ok {
		t.Fatalf("no low-noise block is called %q", satelliteBlockAlias)
	}
	for _, entry := range transponderTable {
		intermediate := entry.downlinkKHz - oscillator
		bus := &recordingBus{}
		if err := (tableSatelliteTuner{tuner: newSatelliteTuner(bus)}).Tune(intermediate); err != nil {
			t.Fatalf("%d kHz: Tune: %v", intermediate, err)
		}
		if bus.address != satelliteAddress {
			t.Fatalf("%d kHz: the writes went to %#02x, want %#02x", intermediate, bus.address, satelliteAddress)
		}
		want := writesFor(entry.registers)
		if len(bus.writes) != len(want) {
			t.Fatalf("%d kHz: the tuner was written to %d times, want %d: %#v",
				intermediate, len(bus.writes), len(want), bus.writes)
		}
		for index, expected := range want {
			if !bytes.Equal(bus.writes[index], expected) {
				t.Errorf("%d kHz (%d kHz downlink): write %d was % 02x, want % 02x",
					intermediate, entry.downlinkKHz, index, bus.writes[index], expected)
			}
		}
	}
}

// TestTheTableIsJapansTransponders keeps the fixture itself honest: twelve BS
// transponders 38.36 MHz apart, then twelve CS-110 ones 40 MHz apart.
func TestTheTableIsJapansTransponders(t *testing.T) {
	for index := 1; index < 12; index++ {
		if step := transponderTable[index].downlinkKHz - transponderTable[index-1].downlinkKHz; step != 38_360 {
			t.Errorf("BS entries %d and %d are %d kHz apart, want 38360", index-1, index, step)
		}
	}
	for index := 13; index < 24; index++ {
		if step := transponderTable[index].downlinkKHz - transponderTable[index-1].downlinkKHz; step != 40_000 {
			t.Errorf("CS entries %d and %d are %d kHz apart, want 40000", index-1, index, step)
		}
	}
	if got := transponderTable[0].downlinkKHz; got != 11_727_480 {
		t.Errorf("the first BS transponder is %d kHz, want 11727480", got)
	}
	if got := transponderTable[12].downlinkKHz; got != 12_291_000 {
		t.Errorf("the first CS-110 transponder is %d kHz, want 12291000", got)
	}
}

// TestTheFinalByteCarriesTheSameDivider is what makes the last write a single
// byte rather than a re-tune: it repeats the divider the first byte set and
// moves only the charge pump.
func TestTheFinalByteCarriesTheSameDivider(t *testing.T) {
	for _, entry := range transponderTable {
		first, last := entry.registers[0], entry.registers[7]
		if first&0x1f != last&0x1f {
			t.Errorf("%d kHz: the last byte %#02x does not carry the divider the first byte %#02x set",
				entry.downlinkKHz, last, first)
		}
		if first>>5 != tuningChargePump || last>>5 != settledChargePump {
			t.Errorf("%d kHz: the charge pump goes %d then %d, want %d then %d",
				entry.downlinkKHz, first>>5, last>>5, tuningChargePump, settledChargePump)
		}
	}
}

// TestTheSatelliteTunerReachesBetweenTransponders is the point of computing:
// a frequency the board was never shipped to receive is tuned rather than
// refused.
func TestTheSatelliteTunerReachesBetweenTransponders(t *testing.T) {
	bus := &recordingBus{}
	// One kilohertz off BS-1, which is not one of the twenty-four.
	if err := (tableSatelliteTuner{tuner: newSatelliteTuner(bus)}).Tune(1_049_481); err != nil {
		t.Fatalf("a frequency between transponders was refused: %v", err)
	}
	if len(bus.writes) != 4 {
		t.Errorf("the tuner was written to %d times, want 4", len(bus.writes))
	}
}

// TestTheSatelliteTunerRefusesWhatTheChipCannotReach is the other side of it.
// A downlink frequency is the mistake to catch: every backend here is handed
// the intermediate frequency, and 11,727,480 is what a channel file names.
func TestTheSatelliteTunerRefusesWhatTheChipCannotReach(t *testing.T) {
	for _, frequency := range []uint64{11_727_480, 0, 2_150_001} {
		bus := &recordingBus{}
		if err := (tableSatelliteTuner{tuner: newSatelliteTuner(bus)}).Tune(frequency); err == nil {
			t.Errorf("%d kHz was accepted", frequency)
		}
		if len(bus.writes) != 0 {
			t.Errorf("%d kHz was refused but still wrote %d times", frequency, len(bus.writes))
		}
	}
}

// TestTheBlockThisBoardAssumesIsTheOneInTheTable keeps the conversion honest:
// the oscillator comes out of the shared LNBf table rather than being written
// down here.
func TestTheBlockThisBoardAssumesIsTheOneInTheTable(t *testing.T) {
	oscillator, ok := satelliteOscillatorKHz()
	if !ok {
		t.Fatalf("no low-noise block is called %q", satelliteBlockAlias)
	}
	if oscillator != 10_678_000 {
		t.Errorf("%s oscillates at %d kHz, want 10678000", satelliteBlockAlias, oscillator)
	}
	if got := transponderTable[0].downlinkKHz - oscillator; got != 1_049_480 {
		t.Errorf("the first entry converts to %d kHz, want BS-1's 1049480", got)
	}
}
