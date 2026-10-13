// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices; MaxLinear; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package mxl58x

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// The MBIN container: a file header followed by segments, each with a header
// naming where in the chip's address space it belongs.
const (
	mbinFileHeaderID    = 'M'
	mbinSegmentHeaderID = 'S'

	mbinFileHeaderLength    = 16
	mbinSegmentHeaderLength = 8
)

// deviceType568 is MXL_HYDRA_DEVICE_568, the one member of the family that
// comes out of reset differently from the rest.
const deviceType568 = 8

// loadFirmware writes the blob into a chip whose firmware is not running, then
// waits for it to start. There is no warm variant: this path begins by holding
// the CPU in reset.
func (demod *Demod) loadFirmware() error {
	image, err := device.ReadFirmware(demod.firmwareDir, firmwareName)
	if err != nil {
		return err
	}

	// CPU into reset; stop the blocks that would move data while its memory is
	// rewritten.
	if err := demod.regWriteField(fieldCPUSoftResetN, 0); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	for _, register := range []uint32{regResetTransportFIFO, regResetBaseband, regResetCrossbar} {
		if err := demod.regWrite(register, 0); err != nil {
			return err
		}
	}
	if err := demod.regWrite(regModulesClock2, disableClock2); err != nil {
		return err
	}
	// Read for its side effect: the software and host interrupt status is
	// clear-on-read.
	if _, err := demod.regRead(regRootClock); err != nil {
		return err
	}

	if err := demod.writeImage(image); err != nil {
		return err
	}

	if demod.config.DeviceType == deviceType568 {
		// The 568's second processor is brought out of reset on its own, then
		// told to accept messages from the main one.
		time.Sleep(10 * time.Millisecond)
		if err := demod.regWrite(0x90720000, 1); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
		if err := demod.regWrite(0x9076b510, 1); err != nil {
			return err
		}
	} else {
		if err := demod.regWriteField(fieldCPUSoftResetN, 1); err != nil {
			return err
		}
		// How long the firmware takes to boot; there is nothing to poll until
		// the heartbeat starts.
		time.Sleep(150 * time.Millisecond)
	}

	// Demodulator n to transport output n, three bits each.
	if err := demod.regWrite(regCrossbarDemodSelect, 0x76543210); err != nil {
		return err
	}

	alive, err := demod.firmwareIsAlive()
	if err != nil {
		return err
	}
	if !alive {
		return errors.New("MxL58x firmware was written but its heartbeat never started")
	}
	// Register values read too soon after the first heartbeats lie.
	time.Sleep(50 * time.Millisecond)

	// Which member of the family this is: the first command the firmware is
	// sent; everything it does with tuners and outputs depends on it.
	if err := demod.sendCommand(commandDeviceConfigSKU, words(demod.config.DeviceType)); err != nil {
		return err
	}
	// The bond option and mask version are readable too but have no caller here.
	return nil
}

func (demod *Demod) writeImage(image []byte) error {
	if len(image) < mbinFileHeaderLength {
		return fmt.Errorf("MxL58x firmware %s is %d bytes, too short to hold a header", firmwareName, len(image))
	}
	if image[0] != mbinFileHeaderID {
		return fmt.Errorf("MxL58x firmware %s starts with %#02x, not an MBIN file header", firmwareName, image[0])
	}
	segments := int(image[3])

	if err := demod.regWrite(regFirmwareDownloadSign, 0); err != nil {
		return err
	}

	offset := mbinFileHeaderLength
	for index := 0; index < segments; index++ {
		if offset+mbinSegmentHeaderLength > len(image) {
			return fmt.Errorf("MxL58x firmware %s ends inside segment %d's header", firmwareName, index)
		}
		header := image[offset : offset+mbinSegmentHeaderLength]
		if header[0] != mbinSegmentHeaderID {
			return fmt.Errorf("MxL58x firmware %s segment %d starts with %#02x, not a segment header",
				firmwareName, index, header[0])
		}
		// Big-endian in the container, whatever the chip's own order is.
		length := int(header[1])<<16 | int(header[2])<<8 | int(header[3])
		address := binary.BigEndian.Uint32(header[4:8])
		body := offset + mbinSegmentHeaderLength
		if body+length > len(image) {
			return fmt.Errorf("MxL58x firmware %s segment %d claims %d bytes and the file has %d left",
				firmwareName, index, length, len(image)-body)
		}
		// The 568's second processor's memory, which no image for the parts in
		// this tree contains: a segment landing there aborts the download.
		if address&0x90760000 == 0x90760000 || address&0x90740000 == 0x90740000 {
			return fmt.Errorf("MxL58x firmware %s segment %d is addressed at %#08x, which this driver cannot write",
				firmwareName, index, address)
		}
		if err := demod.writeSegment(address, image[body:body+length]); err != nil {
			return err
		}
		offset = body + (length+3)/4*4
	}
	return nil
}

// writeSegment is write_fw_segment: blocks, the last padded to a whole number of
// 32-bit words, every word byte-reversed. The reversal is the container's, not
// the transfer's -- the image stores each instruction word most significant byte
// first and the chip wants the other order.
func (demod *Demod) writeSegment(address uint32, data []byte) error {
	for written := 0; written < len(data); {
		length := len(data) - written
		if length > maxBlockWrite {
			length = maxBlockWrite
		}
		block := make([]byte, (length+3)/4*4)
		copy(block, data[written:written+length])
		flipWords(block)
		if err := demod.writeBlock(address, block); err != nil {
			return err
		}
		written += len(block)
		address += uint32(len(block))
	}
	return nil
}

// flipWords is flip_data_in_dword: reverse the bytes of every 32-bit word in
// place. The slice is always a whole number of words.
func flipWords(data []byte) {
	for index := 0; index+4 <= len(data); index += 4 {
		data[index], data[index+3] = data[index+3], data[index]
		data[index+1], data[index+2] = data[index+2], data[index+1]
	}
}
