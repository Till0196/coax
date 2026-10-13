// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package fwextract recovers firmware images from the vendor packages that
// carry them, so that no firmware binary has to be redistributed.
package fwextract

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// IT9175FirmwareSize is the size of a complete it9175-firmware.bin.
const IT9175FirmwareSize = 7912

// firmwareBlock is a run of bytes loaded at one address within a bank.
type firmwareBlock struct {
	addr uint16
	data []byte
}

// firmwareBank is one bank of the image.
type firmwareBank struct {
	index  uint8
	blocks []firmwareBlock
}

// ExtractIT9175Firmware recovers it9175-firmware.bin from an ITE IT9175/IT9179
// Windows BDA driver (IT9175BDA.sys).
//
// The driver does not hold the image in the layout the chip is fed from. It
// holds the already-packetised form: a run of CMD_FW_SCATTER_WR payloads, each
// carrying a bank index and a block count, then that many descriptors and the
// data for them in reverse order. A payload carries at most 44 bytes, so
// re-joining fragments whose addresses are contiguous is what rebuilds the
// image format.
func ExtractIT9175Firmware(driver []byte) ([]byte, error) {
	run := findLongestScatterRun(driver)
	segments := run.segments
	if len(segments) == 0 {
		return nil, errors.New("no IT9175 firmware scatter packets found")
	}
	image := encodeIT9175Image(joinScatterSegments(segments))
	if len(image) != IT9175FirmwareSize {
		return nil, fmt.Errorf("rebuilt image is %d bytes, want %d", len(image), IT9175FirmwareSize)
	}
	return image, nil
}

// joinScatterSegments re-joins the fragments the packetiser split the image
// into.  A fragment continues the previous block when it stays in the same bank
// and starts where the previous one ended.
func joinScatterSegments(segments []scatterSegment) []firmwareBank {
	var banks []firmwareBank
	for _, segment := range segments {
		bank := len(banks) - 1
		if bank < 0 || banks[bank].index != segment.bank {
			banks = append(banks, firmwareBank{index: segment.bank, blocks: []firmwareBlock{{addr: segment.addr, data: append([]byte(nil), segment.data...)}}})
			continue
		}
		blocks := banks[bank].blocks
		last := len(blocks) - 1
		if int(blocks[last].addr)+len(blocks[last].data) == int(segment.addr) {
			blocks[last].data = append(blocks[last].data, segment.data...)
			banks[bank].blocks = blocks
			continue
		}
		banks[bank].blocks = append(blocks, firmwareBlock{addr: segment.addr, data: append([]byte(nil), segment.data...)})
	}
	return banks
}

// encodeIT9175Image writes the banks out in the image format: a one byte bank
// index followed by (length, address, data) blocks, a zero length block ending
// the bank, and a zero bank index ending the image.
func encodeIT9175Image(banks []firmwareBank) []byte {
	var image []byte
	for _, bank := range banks {
		image = append(image, bank.index)
		for _, block := range bank.blocks {
			image = binary.BigEndian.AppendUint16(image, uint16(len(block.data)))
			image = binary.BigEndian.AppendUint16(image, block.addr)
			image = append(image, block.data...)
		}
		image = append(image, 0x00, 0x00)
	}
	return append(image, 0x00)
}
