// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv6120

import (
	"errors"
	"testing"
)

const testAddress = 0x60

// fakeBus is a register map with a log of what was written to it.
type fakeBus struct {
	registers map[byte]byte
	writes    [][]byte
}

func newFakeBus() *fakeBus { return &fakeBus{registers: map[byte]byte{}} }

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testAddress {
		return errors.New("wrong address")
	}
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	for offset, value := range data[1:] {
		bus.registers[data[0]+byte(offset)] = value
	}
	// A block write asks the chip to recalibrate, and a real chip clears the two
	// request bits once it has.
	if len(data) == 8 {
		last := data[0] + 6
		bus.registers[last] &^= calibrationBusy
	}
	return nil
}

// Read only satisfies i2c.Bus; the driver reads through WriteRead below.
func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if address != testAddress || len(write) != 1 {
		return nil, errors.New("unexpected transfer")
	}
	out := make([]byte, readLength)
	for offset := range out {
		out[offset] = bus.registers[write[0]+byte(offset)]
	}
	return out, nil
}

func newTestTuner(bus *fakeBus, index int) *Tuner {
	return New(bus, Config{Address: testAddress, Index: index, XtalKHz: 30000, RDiv: 2})
}

func TestInitializeWritesTheWholePackagesTable(t *testing.T) {
	bus := newFakeBus()
	if err := newTestTuner(bus, 0).Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if len(bus.writes) != 1 {
		t.Fatalf("Initialize made %d transfers, want one", len(bus.writes))
	}
	if bus.writes[0][0] != 0x00 || len(bus.writes[0]) != 26 {
		t.Fatalf("Initialize wrote %d bytes at %#02x, want 26 at 0x00",
			len(bus.writes[0]), bus.writes[0][0])
	}
	// Both halves' blocks must have come out of the one transfer.
	for register, want := range map[byte]byte{0x02: 0xce, 0x08: 0x0e, 0x0c: 0xce, 0x12: 0x0e} {
		if got := bus.registers[register]; got != want {
			t.Errorf("register %#02x = %#02x, want %#02x", register, got, want)
		}
	}
}

// TestSetLOMatchesTheReferenceArithmeticByHand pins the seven-register block
// against set_lof worked out on paper, either side of the prescaler's 1191 MHz
// boundary.
//
// 1550 MHz: prescaler 2 (PDIV 0), VCO 3100000 kHz, divider 3100000*2/30000 =
// 206 (0xce), remainder 20000 -> fraction 174762, charge pump 3. Filter
// 19850000 Hz, index 14. Bytes: 0xce; divider bit 8 with the fraction's low
// seven (0x54); the fraction's next eight (0x55); table 0x0d masked to 0x08
// plus charge pump and the fraction's top three (0x3d); table 0x32 masked to
// 0x12 plus PDIV 0 and the filter index (0x1e); 0x44 untouched; table 0x0e
// masked to 0x08 with both calibrations requested (0x0e).
//
// 950 MHz: prescaler 4 (PDIV 1), VCO 3800000 kHz, charge pump 5, divider 253,
// fraction 87381.
func TestSetLOMatchesTheReferenceArithmeticByHand(t *testing.T) {
	for _, test := range []struct {
		name          string
		frequencyHz   uint32
		symbolRateSPS uint32
		index         int
		base          byte
		want          [7]byte
	}{
		{
			name: "1550 MHz on the first half", frequencyHz: 1_550_000_000,
			symbolRateSPS: 22_000_000, index: 0, base: 0x02,
			want: [7]byte{0xce, 0x54, 0x55, 0x3d, 0x1e, 0x44, 0x0e},
		},
		{
			name: "950 MHz on the second half", frequencyHz: 950_000_000,
			symbolRateSPS: 22_000_000, index: 1, base: 0x0c,
			want: [7]byte{0xfd, 0xaa, 0xaa, 0x5a, 0x3e, 0x44, 0x0e},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := newFakeBus()
			tuner := newTestTuner(bus, test.index)
			if err := tuner.Tune(test.frequencyHz, test.symbolRateSPS); err != nil {
				t.Fatalf("Tune: %v", err)
			}
			block := bus.writes[len(bus.writes)-1]
			if block[0] != test.base || len(block) != 8 {
				t.Fatalf("the block went to %#02x as %d bytes, want %#02x as 8",
					block[0], len(block), test.base)
			}
			for offset, want := range test.want {
				// The fake chip clears the calibration request as a real one
				// does, so the transfer is checked, not the register map.
				if got := block[1+offset]; got != want {
					t.Errorf("block byte %d = %#02x, want %#02x", offset, got, want)
				}
			}
		})
	}
}

// TestSetLOClampsTheChannelFilter guards both ends of the filter index, which
// counts megahertz above 5 MHz and has thirty-two steps.
func TestSetLOClampsTheChannelFilter(t *testing.T) {
	for _, test := range []struct {
		cutoffHz uint32
		want     byte
	}{
		{1_000_000, 0}, // below the 5 MHz the index counts from
		{25_000_000, 20},
		{60_000_000, 31}, // above the thirty-two steps the field holds
	} {
		bus := newFakeBus()
		tuner := newTestTuner(bus, 0)
		if err := tuner.setLO(1_550_000_000, test.cutoffHz); err != nil {
			t.Fatalf("setLO: %v", err)
		}
		// PDIV is 0 at this frequency, so the register's low five bits are
		// the filter index ORed onto the table's own 0x12.
		if got := bus.registers[0x06] & 0x1f; got != test.want|0x12 {
			t.Errorf("cutoff %d Hz gave filter index %#02x, want %#02x",
				test.cutoffHz, got, test.want|0x12)
		}
	}
}

// TestWaitCalibratedReportsAChipThatNeverSettles uses a chip that holds its
// calibration request set, as a real one does when its VCO cannot reach the
// frequency.
func TestWaitCalibratedReportsAChipThatNeverSettles(t *testing.T) {
	stuck := &stuckBus{fakeBus: newFakeBus()}
	tuner := New(stuck, Config{Address: testAddress, XtalKHz: 30000, RDiv: 2})
	if err := tuner.setLO(1_550_000_000, 20_000_000); err == nil {
		t.Fatal("a chip that never finished calibrating was accepted")
	}
}

// stuckBus is a fakeBus that puts the calibration-request bits back after
// every block write, so they never read as cleared.
type stuckBus struct{ *fakeBus }

func (bus *stuckBus) Write(address byte, data []byte) error {
	if err := bus.fakeBus.Write(address, data); err != nil {
		return err
	}
	if len(data) == 8 {
		bus.registers[data[0]+6] |= calibrationBusy
	}
	return nil
}

// TestRFGainCentiDBMatchesTheReferenceCorrections works get_rf_strength out by
// hand at two points; the baseband correction is a flat 100*(6-12) = -600.
//
// Reading 0 is the table's first entry, 74.29 dB; 1550 MHz is below the 159
// under which the image-rejection filter's two decibels are charged, tilt 0 ->
// 7429-600-200 = 6629. Reading 23432 is the third entry, 72.14 dB; 2000 MHz has
// no filter correction and a tilt of 220 -> 7214-600-220 = 6394.
func TestRFGainCentiDBMatchesTheReferenceCorrections(t *testing.T) {
	for _, test := range []struct {
		agc          uint16
		frequencyKHz uint32
		want         int32
	}{
		{0, 1_550_000, 6629},
		{23432, 2_000_000, 6394},
	} {
		if got := RFGainCentiDB(test.agc, test.frequencyKHz); got != test.want {
			t.Errorf("RFGainCentiDB(%d, %d) = %d, want %d",
				test.agc, test.frequencyKHz, got, test.want)
		}
	}
}

// TestLookupGainClampsOutsideTheTable checks the two ends, clamped before the
// search runs at all.
func TestLookupGainClampsOutsideTheTable(t *testing.T) {
	if got := lookupGain(gainLookup, 0); got != 7429 {
		t.Errorf("a reading of zero gave %d, want the table's first value 7429", got)
	}
	if got := lookupGain(gainLookup, 65535); got != 880 {
		t.Errorf("a full-scale reading gave %d, want the table's last value 880", got)
	}
}

// TestTuneRefusesAnUnwiredTuner guards the one thing Config cannot default:
// without XtalKHz and RDiv the PLL arithmetic would divide by zero.
func TestTuneRefusesAnUnwiredTuner(t *testing.T) {
	bus := newFakeBus()
	tuner := New(bus, Config{Address: testAddress})
	if err := tuner.Tune(1_550_000_000, 22_000_000); err == nil {
		t.Fatal("a tuner with no crystal was tuned")
	}
}

// TestResumeWritesOnlyThisHalvesBlock: a package whose other half another
// adapter may be locked on must come out of Initialize with only this half's
// seven registers written -- for the second half, 0x0c..0x12 and nothing else.
func TestResumeWritesOnlyThisHalvesBlock(t *testing.T) {
	bus := newFakeBus()
	bus.registers[0x00] = tunerInit[0]
	tuner := New(bus, Config{
		Address: testAddress, Index: 1, XtalKHz: 30000, RDiv: 2, AlreadyBooted: true,
	})
	if err := tuner.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if len(bus.writes) != 1 {
		t.Fatalf("the warm path made %d transfers, want one", len(bus.writes))
	}
	block := bus.writes[0]
	if block[0] != 0x0c || len(block) != 8 {
		t.Fatalf("the warm path wrote %d bytes at %#02x, want 8 at 0x0c", len(block), block[0])
	}
	// This half's share of the table is byte for byte the first half's, which
	// is why New can seed its cache from one of them regardless of index.
	for offset, want := range tunerInit[2:9] {
		if got := block[1+offset]; got != want {
			t.Errorf("block byte %d = %#02x, want the table's %#02x", offset, got, want)
		}
	}
	for _, register := range []byte{0x01, 0x02, 0x08, 0x09, 0x0a, 0x0b, 0x13, 0x18} {
		if _, written := bus.registers[register]; written {
			t.Errorf("the warm path wrote %#02x, which is not this half's", register)
		}
	}
}

// TestResumeRefusesAChipConfiguredForADifferentReferenceDivider: Config.RDiv
// never reaches a register, so the divider in use is whatever wrote the init
// table into register 0x00. A chip holding anything but the table's value is
// refused rather than mistuned silently.
func TestResumeRefusesAChipConfiguredForADifferentReferenceDivider(t *testing.T) {
	bus := newFakeBus()
	bus.registers[0x00] = 0x33
	tuner := New(bus, Config{
		Address: testAddress, Index: 0, XtalKHz: 30000, RDiv: 2, AlreadyBooted: true,
	})
	if err := tuner.Initialize(); err == nil {
		t.Fatal("a chip configured for a different reference divider was resumed")
	}
	if len(bus.writes) != 0 {
		t.Fatalf("the refused chip was written to %d times", len(bus.writes))
	}
}

// TestResumeLeavesTheTunerReadyToTune: the register cache has to be seeded, or
// the first tune would build its block on zeroes instead of the table's bits.
func TestResumeLeavesTheTunerReadyToTune(t *testing.T) {
	bus := newFakeBus()
	bus.registers[0x00] = tunerInit[0]
	tuner := New(bus, Config{
		Address: testAddress, Index: 0, XtalKHz: 30000, RDiv: 2, AlreadyBooted: true,
	})
	if err := tuner.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := tuner.Tune(1_550_000_000, 22_000_000); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	// The same block the cold-started tuner produces for this channel.
	want := [7]byte{0xce, 0x54, 0x55, 0x3d, 0x1e, 0x44, 0x0e}
	block := bus.writes[len(bus.writes)-1]
	for offset, value := range want {
		if got := block[1+offset]; got != value {
			t.Errorf("block byte %d = %#02x, want %#02x", offset, got, value)
		}
	}
}
