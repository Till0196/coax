// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import (
	"fmt"
	"testing"
)

// TestIFFrequencyRegistersMatchesTheReferenceRounding pins
// SONY_DEMOD_MAKE_IFFREQ_CONFIG(iffreq) = (u32)((iffreq/48.0)*16777216.0 +
// 0.5). Unlike the CXD2856ER's, it always divides by 48 MHz regardless of the
// demodulator's crystal, and rounds rather than truncating.
func TestIFFrequencyRegistersMatchesTheReferenceRounding(t *testing.T) {
	for _, test := range []struct {
		mhz  float64
		want [3]byte
	}{
		{3.6, [3]byte{0x13, 0x33, 0x33}},
		{4.2, [3]byte{0x16, 0x66, 0x66}},
		{4.8, [3]byte{0x19, 0x99, 0x9a}},
		{3.5, [3]byte{0x12, 0xaa, 0xab}},
		{3.7, [3]byte{0x13, 0xbb, 0xbc}},
		{4.9, [3]byte{0x1a, 0x22, 0x22}},
		{3.55, [3]byte{0x12, 0xee, 0xef}},
		{4.15, [3]byte{0x16, 0x22, 0x22}},
		{4.75, [3]byte{0x19, 0x55, 0x55}},
		{3.75, [3]byte{0x14, 0x00, 0x00}},
	} {
		if got := IFFrequencyRegisters(test.mhz); got != test.want {
			t.Errorf("IFFrequencyRegisters(%v) = %#v, want %#v", test.mhz, got, test.want)
		}
	}
}

// scriptedBus records every write and answers every read with zero bytes. It
// exists to let a sequence run far enough to check which registers it touches,
// not to model a real chip.
type scriptedBus struct {
	writes [][]byte
}

func (bus *scriptedBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *scriptedBus) Read(_ byte, length int) ([]byte, error) {
	// Every read here is either a masked write's read-back (content does not
	// matter) or the chip ID probe, which the tests that need a specific ID
	// set up their own bus for.
	return make([]byte, length), nil
}

// TestWakeupDVBTWritesTheBandwidthTable checks the DVB-T path reaches its
// band-setting table and writes the IF register from it.
func TestWakeupDVBTWritesTheBandwidthTable(t *testing.T) {
	bus := &scriptedBus{}
	demod := NewCore(bus, Config{DemodAddress: 0x64, TunerAddress: 0x60})
	demod.State = StateSleep
	if err := demod.SetBandwidthDVBT(6); err != nil {
		t.Fatalf("setBandwidthDVBT: %v", err)
	}
	found := false
	for _, w := range bus.writes {
		if len(w) == 4 && w[0] == 0xb6 {
			found = true
			want := IFFrequencyRegisters(3.6)
			if w[1] != want[0] || w[2] != want[1] || w[3] != want[2] {
				t.Errorf("IF register = %#v, want %#v", w[1:], want)
			}
		}
	}
	if !found {
		t.Fatal("setBandwidthDVBT never wrote the IF frequency register 0xB6")
	}
}

// TestISDBTChannelWidthIsPerBandwidthNotAlwaysEightMegahertz pins register
// 0xD7, the channel-width setting, to the bandwidth. 0x00 means 8 MHz, so a
// literal 0x00 in all three branches would demodulate every 6 and 7 MHz
// ISDB-T channel with an 8 MHz front end. DVB-T keys the same register the
// same way, and the loop checks the two tables agree.
func TestISDBTChannelWidthIsPerBandwidthNotAlwaysEightMegahertz(t *testing.T) {
	for width, want := range map[uint32]byte{8: 0x00, 7: 0x02, 6: 0x04} {
		if got := isdbtBands[width].channelWidth; got != want {
			t.Errorf("isdbtBands[%d].channelWidth = %#02x, want %#02x", width, got, want)
		}
		// DVB-T reaches the same register through the same field, so the two
		// tables must not disagree about what a given channel width means.
		if got := dvbtBands[width].channelWidth; got != want {
			t.Errorf("dvbtBands[%d].channelWidth = %#02x, want %#02x", width, got, want)
		}
	}
}

// TestISDBTLatencyValuesAreNotDuplicated guards the demodulator-latency
// register 0xD9, whose three bandwidths have three distinct settings: the
// 8 MHz one is easily confused with the 7 MHz one.
func TestISDBTLatencyValuesAreNotDuplicated(t *testing.T) {
	for width, want := range map[uint32][]byte{
		8: {0x13, 0xFC}, 7: {0x1A, 0xFA}, 6: {0x1F, 0x79},
	} {
		got := isdbtBands[width].trl
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("isdbtBands[%d].trl = %#v, want %#v", width, got, want)
		}
	}
}

// TestSetTSClockModeTurnsRateControlOffOnlyWhereItShould pins the one bit that
// varies by system. Register 0xD3 bit 0 is "TS rate control off": set for
// ISDB-T and ATSC, cleared for everything else.
func TestSetTSClockModeTurnsRateControlOffOnlyWhereItShould(t *testing.T) {
	for system, want := range map[System]byte{
		SystemISDBT: 0x01,
		SystemATSC:  0x01,
		SystemDVBT:  0x00,
		SystemDVBT2: 0x00,
		SystemDVBC:  0x00,
		SystemJ83B:  0x00,
	} {
		bus := &scriptedBus{}
		demod := NewCore(bus, Config{DemodAddress: 0x64, TunerAddress: 0x60})
		if err := demod.SetTSClockMode(system); err != nil {
			t.Fatalf("SetTSClockMode(%v): %v", system, err)
		}
		got, ok := lastValueWritten(bus, 0xd3)
		if !ok {
			t.Fatalf("%v: register 0xD3 was never written", system)
		}
		if got&0x01 != want {
			t.Errorf("%v: 0xD3 bit 0 = %d, want %d", system, got&0x01, want)
		}
	}
}

// lastValueWritten finds the value most recently written to a register by a
// two-byte write, ignoring the bank selects that surround it.
func lastValueWritten(bus *scriptedBus, register byte) (byte, bool) {
	for i := len(bus.writes) - 1; i >= 0; i-- {
		if len(bus.writes[i]) == 2 && bus.writes[i][0] == register {
			return bus.writes[i][1], true
		}
	}
	return 0, false
}

// TestBandwidthTablesCoverEveryChannelWidth guards against a table that
// silently has no entry for a channel width its board actually needs.
func TestBandwidthTablesCoverEveryChannelWidth(t *testing.T) {
	for _, width := range []uint32{5, 6, 7, 8} {
		if _, ok := dvbtBands[width]; !ok {
			t.Errorf("dvbtBands has no %d MHz entry", width)
		}
		if _, ok := dvbt2Bands[width]; !ok {
			t.Errorf("dvbt2Bands has no %d MHz entry", width)
		}
	}
	for _, width := range []uint32{6, 7, 8} {
		if _, ok := isdbtBands[width]; !ok {
			t.Errorf("isdbtBands has no %d MHz entry", width)
		}
	}
}

// TestSerialWiringWritesTheRegistersParallelDoesNot: a serially wired board
// takes the clock mode and the duty mode; a parallel one must not be given
// them, because there those bits mean something else.
func TestSerialWiringWritesTheRegistersParallelDoesNot(t *testing.T) {
	parallel := &scriptedBus{}
	if err := NewCore(parallel, Config{DemodAddress: 0x64, TunerAddress: 0x60}).
		SetTSClockMode(SystemDVBT); err != nil {
		t.Fatalf("parallel: %v", err)
	}
	if _, written := lastValueWritten(parallel, 0xd1); written {
		t.Error("a parallel board should not be given the serial duty mode")
	}

	serial := &scriptedBus{}
	if err := NewCore(serial, Config{DemodAddress: 0x64, TunerAddress: 0x60, TSSerial: true}).
		SetTSClockMode(SystemDVBT); err != nil {
		t.Fatalf("serial: %v", err)
	}
	duty, written := lastValueWritten(serial, 0xd1)
	if !written {
		t.Fatal("a serial board needs the duty mode")
	}
	if duty&0x03 != 0x01 {
		t.Errorf("serial duty mode = %d, want 1", duty&0x03)
	}
}

// TestConfigureTSSelectsTheWiring checks the bit that decides it, and the pin
// the serial output leaves on.
func TestConfigureTSSelectsTheWiring(t *testing.T) {
	for name, test := range map[string]struct {
		config   Config
		wantMode byte // 0xC4 bit 7
		wantMask byte // which data pins are the board's
	}{
		"parallel":          {Config{}, 0x00, 0xff},
		"serial on TSDATA0": {Config{TSSerial: true}, 0x80, 0x01},
		"serial on TSDATA7": {Config{TSSerial: true, TSSerialData: true}, 0x80, 0x80},
	} {
		config := test.config
		config.DemodAddress, config.TunerAddress = 0x64, 0x60
		bus := &scriptedBus{}
		core := NewCore(bus, config)
		if err := core.ConfigureTS(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var mode byte
		var found bool
		for _, write := range bus.writes {
			// The mode is written with a mask, which this bus records as a
			// read-modify-write ending in a two-byte write of 0xC4.
			if len(write) == 2 && write[0] == 0xc4 {
				mode, found = write[1], true
				break
			}
		}
		if !found {
			t.Fatalf("%s: register 0xC4 was never written", name)
		}
		if mode&0x80 != test.wantMode {
			t.Errorf("%s: 0xC4 bit 7 = %#02x, want %#02x", name, mode&0x80, test.wantMode)
		}
		if got := core.tsDataPinMask(); got != test.wantMask {
			t.Errorf("%s: data pin mask %#02x, want %#02x", name, got, test.wantMask)
		}
	}
}

// TestParallelWiringIsUnchangedByTheSerialSupport is a golden sequence: the
// serial wiring was added to a routine that had only ever done the parallel
// one, and every working board is parallel, so these writes must not move.
func TestParallelWiringIsUnchangedByTheSerialSupport(t *testing.T) {
	bus := &scriptedBus{}
	core := NewCore(bus, Config{DemodAddress: 0x64, TunerAddress: 0x60, TSClockMask: 1})
	if err := core.ConfigureTS(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"00 00", // bank 0
		"c4 00", // parallel: OSERIALEN clear, and no serial bits after it
		"00 02", // bank 2
		"e4 00", // the third mode, off
		"00 00", // bank 0
		"c6 01", // the clock mask, on both of its registers
		"00 60",
		"52 01",
		"00 00",
		"95 01", // drive strength: clock, then sync, valid and data
		"95 14",
		"96 54",
		"97 54",
	}
	var got []string
	for _, write := range bus.writes {
		if len(write) != 2 {
			// A masked write reads the register first; the read is not part
			// of the sequence being pinned.
			continue
		}
		got = append(got, fmt.Sprintf("%02x %02x", write[0], write[1]))
	}
	if len(got) != len(want) {
		t.Fatalf("parallel wiring wrote %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("write %d was %q, want %q", i, got[i], want[i])
		}
	}
}
