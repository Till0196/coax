// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package r850

import "testing"

func TestReverseBits(t *testing.T) {
	t.Parallel()
	// The chip returns its status registers bit-reversed.
	for value, want := range map[byte]byte{
		0x00: 0x00, 0xff: 0xff, 0x01: 0x80, 0x80: 0x01, 0x0f: 0xf0, 0xa5: 0xa5,
	} {
		if got := reverseBits(value); got != want {
			t.Errorf("reverseBits(%#02x) = %#02x; want %#02x", value, got, want)
		}
	}
	// Reversing twice is the identity.
	for value := 0; value < 256; value++ {
		if got := reverseBits(reverseBits(byte(value))); got != byte(value) {
			t.Fatalf("reverseBits is not its own inverse at %#02x", value)
		}
	}
}

func TestISDBTTablesCoverTheBand(t *testing.T) {
	t.Parallel()
	// Every VHF/UHF channel centre has to find a frequency table entry, both at
	// the 4063 kHz IF the boards use and at the tables' IF-agnostic entries.
	for _, ifFrequency := range []uint32{4063, 0} {
		for frequency := uint32(90000); frequency <= 770000; frequency += 6000 {
			found := false
			for _, entry := range isdbTFrequencyParams {
				if (entry.ifFrequency == 0 || entry.ifFrequency == ifFrequency) &&
					(entry.rfMin == 0 || entry.rfMin <= frequency) &&
					(entry.rfMax == 0 || entry.rfMax >= frequency) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("no ISDB-T frequency parameters for %d kHz at IF %d", frequency, ifFrequency)
			}
		}
	}
}

func TestISDBTSystemParamsExistForBothRevisions(t *testing.T) {
	t.Parallel()
	// The boards ask for 6 MHz at 4063 kHz; both chip revisions must have it.
	for chip := 0; chip < 2; chip++ {
		found := false
		for _, entry := range isdbTParams[chip] {
			if entry.bandwidth == int(Bandwidth6M) && entry.ifFrequency == 4063 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("chip revision %d has no 6 MHz / 4063 kHz entry", chip)
		}
	}
}

func TestSystemTablesAreWiredUp(t *testing.T) {
	t.Parallel()
	// Every system the driver accepts needs both of its tables.
	for _, system := range []System{SystemDVBT, SystemDVBT2, SystemDVBT21, SystemDVBC, SystemJ83B, SystemISDBT, SystemDTMB, SystemATSC} {
		if len(systemTables[system][0]) == 0 || len(systemTables[system][1]) == 0 {
			t.Errorf("system %d has no system parameters", system)
		}
		if len(frequencyTables[system]) == 0 {
			t.Errorf("system %d has no frequency parameters", system)
		}
	}
}

func TestRegisterImagesLeaveTheReadOnlyRangeZero(t *testing.T) {
	t.Parallel()
	// The writable range starts at 0x08; below it the images stay zero.
	for name, image := range map[string]*[registerCount]byte{
		"init": &initRegisters, "sleep": &sleepRegisters, "wakeup": &wakeupRegisters,
		"imr": &imrCalibrationRegisters, "lpf": &lpfCalibrationRegisters,
	} {
		for index := 0; index < firstWritableRegister; index++ {
			if image[index] != 0 {
				t.Errorf("%s register image has a non-zero read-only entry at %#02x", name, index)
			}
		}
	}
}
