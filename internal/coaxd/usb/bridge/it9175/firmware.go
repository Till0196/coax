// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it9175

import (
	"errors"
	"fmt"
	"os"
)

// FirmwareFile is the image the chip needs; supply it in the firmware directory.
const FirmwareFile = "it9175-firmware.bin"

// The image is a sequence of banks. A bank opens with its one-byte index and
// then holds blocks of "length, address, data", each big-endian, until a
// zero-length block ends it; a zero bank index ends the image.
//
// The chip takes it through scatter-write commands instead, each carrying up to
// maxScatterSegments pieces and at most maxScatterData bytes of descriptor and
// data together. Blocks are split across commands as the budget runs out.
const (
	maxScatterSegments = 3
	maxScatterData     = 44
	// blockHeader is the length and address a block opens with.
	blockHeader = 4
	// segmentDescriptor is the address and length one piece contributes to a
	// scatter-write command's header.
	segmentDescriptor = 3
)

// errFirmwareFormat reports an image that is not in the layout above.
var errFirmwareFormat = errors.New("the IT9175 firmware image is malformed")

// loadFirmware downloads the image and starts it.
func (device *Device) loadFirmware(path string) error {
	blob, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read required IT9175 firmware %s: %w", path, err)
	}
	frames, err := packFirmware(blob)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// The download runs with the chip's I2C bus at 366 kHz, which is what the
	// boot ROM expects; the working speed is set afterwards during bring-up.
	if err := device.WriteRegister(0xf103, 0x07); err != nil {
		return err
	}
	for index, frame := range frames {
		if err := device.scatterWrite(frame); err != nil {
			return fmt.Errorf("download IT9175 firmware block %d of %d: %w", index+1, len(frames), err)
		}
	}
	if err := device.boot(); err != nil {
		return err
	}
	if err := device.identify(); err != nil {
		return err
	}
	if device.firmware == 0 {
		return errors.New("the IT9175 firmware did not start")
	}
	return nil
}

// scatterWrite sends one packed command.
func (device *Device) scatterWrite(frame []byte) error {
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	copy(device.payload(), frame)
	return device.command(0, commandScatterWrite, len(frame), 0)
}

// boot starts the downloaded image.
func (device *Device) boot() error {
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	if err := device.command(0, commandBoot, 0, 0); err != nil {
		return fmt.Errorf("boot the IT9175 firmware: %w", err)
	}
	return nil
}

// packFirmware turns the stored image into the scatter-write commands the chip
// takes, splitting blocks so each command stays inside its budget. The data
// pieces are laid out in the reverse of their descriptors' order, which is what
// the chip is known to accept; the descriptor order has never been tried on
// hardware.
func packFirmware(blob []byte) ([][]byte, error) {
	var frames [][]byte
	offset := 0
	for bankIndex := 0; ; bankIndex++ {
		if offset >= len(blob) {
			return nil, fmt.Errorf("%w: no bank index at offset %#x", errFirmwareFormat, offset)
		}
		bank := blob[offset]
		if bankIndex != 0 && bank == 0 {
			// A zero index after the first bank ends the image.
			return frames, nil
		}
		offset++
		next, banked, err := packBank(blob, offset, bank)
		if err != nil {
			return nil, err
		}
		frames = append(frames, banked...)
		// Step over the zero-length block that ended the bank.
		offset = next + 2
	}
}

// packBank packs one bank, starting at the first block's header, and reports
// where its terminator begins.
func packBank(blob []byte, offset int, bank byte) (int, [][]byte, error) {
	var (
		frames      [][]byte
		descriptors [maxScatterSegments][segmentDescriptor]byte
		staged      [maxScatterSegments][]byte
		segments    int
		filled      int
		// index is how far into the current block the packing has got, and is
		// zero whenever the next block's header is due.
		index     int
		blockSize int
		address   int
	)
	for {
		if index == 0 {
			if offset+blockHeader > len(blob) {
				return 0, nil, fmt.Errorf("%w: truncated block header at offset %#x", errFirmwareFormat, offset)
			}
			blockSize = int(blob[offset])<<8 | int(blob[offset+1])
			address = int(blob[offset+2])<<8 | int(blob[offset+3])
			offset += blockHeader
			if blockSize == 0 {
				return 0, nil, fmt.Errorf("%w: empty block at offset %#x", errFirmwareFormat, offset-blockHeader)
			}
			if offset+blockSize > len(blob) {
				return 0, nil, fmt.Errorf("%w: block at offset %#x runs past the end", errFirmwareFormat, offset-blockHeader)
			}
		}

		budget := maxScatterData - filled
		remaining := blockSize - index
		var take int
		switch {
		case remaining+segmentDescriptor <= budget:
			take = remaining
			filled += remaining + segmentDescriptor
		case budget > segmentDescriptor:
			take = budget - segmentDescriptor
			filled = maxScatterData
		default:
			// Fewer bytes are left than a descriptor costs. For every image
			// seen so far the flush below runs first, so reaching here means
			// the image is not laid out the way the format allows.
			return 0, nil, fmt.Errorf("%w: block at offset %#x cannot be split", errFirmwareFormat, offset)
		}

		start := address + index
		descriptors[segments] = [segmentDescriptor]byte{byte(start >> 8), byte(start), byte(take)}
		staged[segments] = blob[offset+index : offset+index+take]
		index += take
		segments++

		if index >= blockSize {
			index = 0
			offset += blockSize
			if offset+2 > len(blob) {
				return 0, nil, fmt.Errorf("%w: no block terminator at offset %#x", errFirmwareFormat, offset)
			}
			if blob[offset] == 0 && blob[offset+1] == 0 {
				blockSize = 0
			}
		}
		if filled >= maxScatterData || segments >= maxScatterSegments || blockSize == 0 {
			frames = append(frames, buildScatterFrame(bank, descriptors[:segments], staged[:segments]))
			filled = 0
			segments = 0
		}
		if blockSize == 0 {
			return offset, frames, nil
		}
	}
}

// buildScatterFrame assembles one command: a fixed header, then one descriptor
// per piece, then the pieces themselves in reverse order.
func buildScatterFrame(bank byte, descriptors [][segmentDescriptor]byte, staged [][]byte) []byte {
	length := blockHeader + len(descriptors)*segmentDescriptor
	for _, piece := range staged {
		length += len(piece)
	}
	frame := make([]byte, 0, length)
	frame = append(frame, 0x03, bank, 0, byte(len(descriptors)))
	for _, descriptor := range descriptors {
		frame = append(frame, descriptor[:]...)
	}
	for index := len(staged) - 1; index >= 0; index-- {
		frame = append(frame, staged[index]...)
	}
	return frame
}
