// Copyright (c) 2017, 2022 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5520.c, tbs5520se.c, tbs5530.c) by Davin.

// Package tbs holds the vendor control protocol the TBS 5520, 5520SE and 5530 USB
// bridges share: control transfers carrying firmware download, I2C and GPIO, with
// the transport stream arriving on bulk endpoint 0x82. The board packages layer
// their bring-up sequences and per-board I2C shapes on top; on the wire side only
// the chip name in error messages tells the bridges apart.
package tbs

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// USB vendor requests. Every bridge in the family answers the same request
// numbers; not every board uses all of them.
const (
	RequestI2CWriteAddressed = 0x80 // write, with the target address in the payload
	RequestI2CWriteRegister  = 0x90 // write a register pointer, then...
	RequestI2CRead           = 0x91 // ...collect the read's response
	RequestI2CReadAddressed  = 0x93 // start a read from a specific address
	RequestFirmware          = 0xa0
	RequestGPIOFixed         = 0x8a // fixed-purpose GPIO writes (bring-up, LNB, stream source)
	RequestGPIO              = 0xb7 // a second fixed-purpose GPIO write
)

// MaxI2CLength is the largest single I2C message the bridge's 64-byte control
// buffer can carry.
const MaxI2CLength = 62

// controlTimeoutMS bounds every control transfer.
const controlTimeoutMS = 2000

// Bridge is the control plumbing of an opened TBS bridge. The chip name only
// prefixes error messages.
type Bridge struct {
	usb    *usbio.Transport
	chip   string
	stream usbio.EndpointReader
}

// New wraps an opened USB device, naming the chip for error messages; Close
// releases the USB transport too.
func New(device *usbio.Transport, chip string) *Bridge {
	return &Bridge{usb: device, chip: chip, stream: usbio.ReaderOf(device, 0x82)}
}

// ControlWrite sends one vendor control write and checks it went out whole.
func (bridge *Bridge) ControlWrite(request byte, data []byte) error {
	written, err := bridge.usb.Control(0x40, request, 0, 0, data, controlTimeoutMS)
	if err != nil {
		return fmt.Errorf("%s control write %#02x: %w", bridge.chip, request, err)
	}
	if written != len(data) {
		return fmt.Errorf("%s control write %#02x: short write %d of %d", bridge.chip, request, written, len(data))
	}
	return nil
}

// ControlRead collects one vendor control read of exactly length bytes.
func (bridge *Bridge) ControlRead(request byte, length int) ([]byte, error) {
	data := make([]byte, length)
	read, err := bridge.usb.Control(0xc0, request, 0, 0, data, controlTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("%s control read %#02x: %w", bridge.chip, request, err)
	}
	if read != length {
		return nil, fmt.Errorf("%s control read %#02x: short read %d of %d", bridge.chip, request, read, length)
	}
	return data, nil
}

// maxFirmware is the most an image may be: it is written to the
// microcontroller's memory from address zero, and from 0xe200 on that memory
// is the chip's own registers -- the one that stops and starts the CPU among
// them.
const maxFirmware = 0xe200

// LoadFirmware uploads the bridge's microcontroller firmware. The CPU is
// stopped, the image is sent in 64-byte chunks, and the CPU is restarted.
func (bridge *Bridge) LoadFirmware(data []byte) error {
	if len(data) == 0 || len(data) > maxFirmware {
		return fmt.Errorf("%s firmware is %d bytes; an image is 1 to %d", bridge.chip, len(data), maxFirmware)
	}
	for _, register := range []uint16{0x7f92, 0xe600} {
		if err := bridge.writeFirmwareRegister(register, 1); err != nil {
			return fmt.Errorf("stop the %s CPU: %w", bridge.chip, err)
		}
	}
	for offset := 0; offset < len(data); offset += 0x40 {
		end := min(offset+0x40, len(data))
		chunk := data[offset:end]
		written, err := bridge.usb.Control(0x40, RequestFirmware, uint16(offset), 0, chunk, controlTimeoutMS)
		if err != nil {
			return fmt.Errorf("%s firmware transfer at %#x: %w", bridge.chip, offset, err)
		}
		if written != len(chunk) {
			return fmt.Errorf("%s firmware transfer at %#x: short write %d of %d", bridge.chip, offset, written, len(chunk))
		}
	}
	for _, register := range []uint16{0x7f92, 0xe600} {
		if err := bridge.writeFirmwareRegister(register, 0); err != nil {
			return fmt.Errorf("restart the %s CPU: %w", bridge.chip, err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (bridge *Bridge) writeFirmwareRegister(register uint16, value byte) error {
	data := []byte{value}
	written, err := bridge.usb.Control(0x40, RequestFirmware, register, 0, data, controlTimeoutMS)
	if err != nil {
		return err
	}
	if written != 1 {
		return fmt.Errorf("short write %d of 1", written)
	}
	return nil
}

// Write is an I2C write: the family's one-message shape, with the target address
// in the payload.
func (bridge *Bridge) Write(address byte, data []byte) error {
	if len(data) > MaxI2CLength {
		return fmt.Errorf("%s: a %d-byte I2C write exceeds the bridge's %d-byte limit", bridge.chip, len(data), MaxI2CLength)
	}
	payload := append([]byte{byte(len(data) + 1), address << 1}, data...)
	return bridge.ControlWrite(RequestI2CWriteAddressed, payload)
}

// Read is an I2C read: a request naming the address and length, then a transfer
// collecting the response.
func (bridge *Bridge) Read(address byte, length int) ([]byte, error) {
	if length > MaxI2CLength {
		return nil, fmt.Errorf("%s: a %d-byte I2C read exceeds the bridge's %d-byte limit", bridge.chip, length, MaxI2CLength)
	}
	if err := bridge.ControlWrite(RequestI2CReadAddressed, []byte{byte(length), address<<1 | 1}); err != nil {
		return nil, err
	}
	return bridge.ControlRead(RequestI2CRead, length)
}

// ReadStream reads transport stream packets from the bulk endpoint. It returns
// usbio.ErrTimeout when the device had nothing ready.
func (bridge *Bridge) ReadStream(buffer []byte, timeoutMS uint32) (int, error) {
	return bridge.stream.Read(buffer, timeoutMS)
}

// StartReading puts the reads on the stream endpoint in flight.
func (bridge *Bridge) StartReading() error { return bridge.stream.Start() }

// StopReading withdraws the reads still outstanding on the stream endpoint, so
// the next stream does not begin with what the last channel sent.
func (bridge *Bridge) StopReading() error { return bridge.stream.Stop() }

// Name reports the bus:address of the underlying USB device.
func (bridge *Bridge) Name() string { return bridge.usb.Name() }

// Close releases the USB device.
func (bridge *Bridge) Close() error {
	_ = bridge.stream.Stop()
	return bridge.usb.Close()
}
