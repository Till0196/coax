// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// cfbHeader is a compound-file header with 512-byte sectors and no FAT, DIFAT,
// directory or mini FAT sectors yet.
func cfbHeader() []byte {
	header := make([]byte, cfbHeaderSize)
	copy(header, cfbSignature)
	binary.LittleEndian.PutUint16(header[0x1E:], 9)
	binary.LittleEndian.PutUint16(header[0x20:], 6)
	binary.LittleEndian.PutUint32(header[0x38:], 4096)
	for _, at := range []int{0x30, 0x3C, 0x44} {
		binary.LittleEndian.PutUint32(header[at:], cfbEndOfChain)
	}
	for i := 0; i < 109; i++ {
		binary.LittleEndian.PutUint32(header[0x4C+i*4:], 0xFFFFFFFF)
	}
	return header
}

func TestCompoundFileRefusesOddSectorSizes(t *testing.T) {
	data := append(cfbHeader(), make([]byte, 512)...)
	binary.LittleEndian.PutUint16(data[0x1E:], 40)
	if _, err := ExtractCompoundFile(data, newExpansionBudget()); err == nil {
		t.Error("took a sector size of 2^40")
	}
}

func TestCompoundFileDIFATLoopEnds(t *testing.T) {
	data := append(cfbHeader(), make([]byte, 512)...)
	// The DIFAT starts at sector 0, whose last word points back at itself.
	binary.LittleEndian.PutUint32(data[0x44:], 0)
	if _, err := ExtractCompoundFile(data, newExpansionBudget()); err == nil {
		t.Error("followed a DIFAT chain that loops")
	}
}

func TestCompoundFileShortLastSector(t *testing.T) {
	// The only FAT sector is the last, and it is short.
	data := append(cfbHeader(), make([]byte, 100)...)
	binary.LittleEndian.PutUint32(data[0x4C:], 0)
	_, _ = ExtractCompoundFile(data, newExpansionBudget())
}

func TestCabinetHeaderReservePastTheEnd(t *testing.T) {
	cabinet := buildCabinet(t, "driver.sys", []byte("driver"), cabCompressNone)
	binary.LittleEndian.PutUint16(cabinet[0x1E:], cabFlagReservePresen)
	reserved := append(append([]byte{}, cabinet[:cabHeaderSize]...), 0xFF, 0xFF, 0, 0)
	if _, err := ExtractCabinet(reserved, wantedInCabinet, newExpansionBudget()); err == nil {
		t.Error("took a header reserve running past the end")
	}
}

// TestCabinetDamageDoesNotPanic damages every byte of a small cabinet in turn.
func TestCabinetDamageDoesNotPanic(t *testing.T) {
	for _, compress := range []uint16{cabCompressNone, cabCompressMSZip} {
		cabinet := buildCabinet(t, "driver.sys", bytes.Repeat([]byte("driver "), 50), compress)
		for at := range cabinet {
			for _, value := range []byte{0x00, 0xFF, 0x7F} {
				damaged := append([]byte{}, cabinet...)
				damaged[at] = value
				_, _ = ExtractCabinet(damaged, func(string) bool { return true }, newExpansionBudget())
			}
		}
	}
}

func TestCabinetChargesTheBudget(t *testing.T) {
	cabinet := buildCabinet(t, "driver.sys", bytes.Repeat([]byte("driver "), 50), cabCompressMSZip)
	if _, err := ExtractCabinet(cabinet, wantedInCabinet, &expansionBudget{remaining: 100}); err == nil {
		t.Error("expanded past the budget")
	}
}
