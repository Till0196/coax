// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package dvbsky drives the DVBSky-style USB bridge found on MyGica and DVBSky
// sticks: a bulk command protocol on endpoints 0x01/0x81 that carries I2C and
// GPIO, with the transport stream arriving on 0x82.
package dvbsky

import (
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// commandTimeoutMS bounds each bulk command transfer and its response.
const commandTimeoutMS = 2000

// maxI2CLength is the largest I2C transfer the bulk command frame can carry.
const maxI2CLength = 60

// maxResponse is the largest answer the protocol has, which is a full I2C read
// and its status byte.
const maxResponse = maxI2CLength + 1

// drainTimeoutMS is how long an unread answer is waited for at start-up. It is
// short because the answer, if there is one, is already at the endpoint: the
// wait is only long enough to tell "nothing there" from "not yet".
const drainTimeoutMS = 100

// BulkDevice is the subset of usbio.Transport the bridge needs.
type BulkDevice interface {
	Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error)
}

// Bridge is a DVBSky USB bridge. It implements i2c.Bus.
type Bridge struct {
	device BulkDevice
	stream usbio.EndpointReader
	mu     sync.Mutex
}

// New wraps an opened USB device and starts a fresh conversation with it.
func New(device BulkDevice) *Bridge {
	bridge := &Bridge{device: device, stream: usbio.ReaderOf(device, 0x82)}
	bridge.drain()
	return bridge
}

// drain reads away anything the response endpoint is still holding.
//
// The protocol is a strict alternation, so an answer nobody collected is the
// answer the next command will appear to get, and every exchange after it is
// one behind. Linux and Windows discard it when the interface is released;
// macOS keeps it across runs, where it made a whole session's transfers answer
// the question before them.
func (bridge *Bridge) drain() {
	leftover := make([]byte, maxResponse)
	for attempt := 0; attempt < 8; attempt++ {
		if _, err := bridge.device.Bulk(0x81, leftover, drainTimeoutMS); err != nil {
			return
		}
	}
}

func (bridge *Bridge) exchange(command []byte, responseLength int) ([]byte, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	written, err := bridge.device.Bulk(0x01, command, commandTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("DVBSky command write: %w", err)
	}
	if written != len(command) {
		return nil, fmt.Errorf("DVBSky command short write: %d of %d", written, len(command))
	}
	// The stream controls, 0x36 and 0x37, answer nothing: reading after one costs
	// the full command timeout and then fails. Nothing is left at the endpoint by
	// skipping the read, since every command that does answer reads it below.
	if responseLength == 0 {
		return nil, nil
	}
	// A full-sized buffer, so an answer longer than expected is taken off the
	// endpoint rather than left half there. A bulk read ends at a short
	// packet, so asking for more than the device sends costs nothing.
	answer := make([]byte, maxResponse)
	read, err := bridge.device.Bulk(0x81, answer, commandTimeoutMS)
	if err != nil {
		return nil, fmt.Errorf("DVBSky command response: %w", err)
	}
	if read < responseLength {
		return nil, fmt.Errorf("DVBSky command short response: %d of %d", read, responseLength)
	}
	return answer[:responseLength], nil
}

// GPIO drives one of the bridge's general-purpose pins.
func (bridge *Bridge) GPIO(port, value byte) error {
	_, err := bridge.exchange([]byte{0x0e, port, value}, 1)
	return err
}

// resetStep is one GPIO write of a reset sequence and the settle time after it.
type resetStep struct {
	port, value byte
	delay       time.Duration
}

// resetSequence brings the demodulator behind the bridge out of reset.
//
// These three pins and these waits are what every T230 this backend supports
// comes up with -- the Si2157 one and all five Si2141 ones alike -- and are
// what the sticks are shipped and tested to work with.
//
// The Linux driver disagrees.
// It has two sequences: 0x04/0x83/0xc0 for most of the family and these pins
// only for the T230A. On a T230 nothing drives 0x04 or 0xc0 at all, so what
// Linux does for the other five looks like a sequence carried over from the
// DVBSky boards it also serves.
//
// A product that needs something else would need its own table.
var resetSequence = []resetStep{
	{0x87, 0, 0},
	{0x86, 1, 0},
	{0x80, 0, 50 * time.Millisecond},
	{0x80, 1, 50 * time.Millisecond},
}

// Reset toggles the reset pins for the demodulator behind the bridge.
func (bridge *Bridge) Reset() error {
	for _, step := range resetSequence {
		if err := bridge.GPIO(step.port, step.value); err != nil {
			return err
		}
		time.Sleep(step.delay)
	}
	return nil
}

// Write performs an I2C write to a device address. The command frame's
// one-byte length field caps a single transfer at 60 data bytes.
func (bridge *Bridge) Write(address byte, data []byte) error {
	if len(data) > maxI2CLength {
		return fmt.Errorf("DVBSky: a %d-byte I2C write exceeds the bridge's %d-byte limit", len(data), maxI2CLength)
	}
	command := append([]byte{0x08, address, byte(len(data))}, data...)
	_, err := bridge.exchange(command, 1)
	return err
}

// Read performs an I2C read from a device address, capped at 60 bytes the
// same way Write is; the response carries a status byte before the data.
func (bridge *Bridge) Read(address byte, length int) ([]byte, error) {
	if length > maxI2CLength {
		return nil, fmt.Errorf("DVBSky: a %d-byte I2C read exceeds the bridge's %d-byte limit", length, maxI2CLength)
	}
	response, err := bridge.exchange([]byte{0x09, 0, byte(length), address}, length+1)
	if err != nil {
		return nil, err
	}
	return response[1:], nil
}

// Stream starts or stops the transport stream endpoint.
func (bridge *Bridge) Stream(enable bool) error {
	// What is still outstanding would otherwise carry the last channel's packets
	// into the next stream.
	stopped := bridge.stream.Stop()
	if _, err := bridge.exchange([]byte{0x37, 0, 0}, 0); err != nil {
		return err
	}
	if stopped != nil {
		return stopped
	}
	if !enable {
		return nil
	}
	time.Sleep(20 * time.Millisecond)
	if err := bridge.stream.Start(); err != nil {
		return err
	}
	_, err := bridge.exchange([]byte{0x36, 3, 0}, 0)
	return err
}

// ReadStream reads transport stream packets. It returns usbio.ErrTimeout when
// the device had nothing ready.
func (bridge *Bridge) ReadStream(buffer []byte, timeoutMS uint32) (int, error) {
	return bridge.stream.Read(buffer, timeoutMS)
}

var _ i2c.Bus = (*Bridge)(nil)
