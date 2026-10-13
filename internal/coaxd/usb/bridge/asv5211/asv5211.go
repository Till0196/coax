// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package asv5211 drives the ASICEN ASV5211 USB bridge.
package asv5211

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/trace"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// transport is *usbio.Transport, as an interface so the command layer can be
// exercised without hardware.
type transport interface {
	Control(requestType, request byte, value, index uint16, buffer []byte, timeoutMS uint32) (int, error)
	Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error)
	Close() error
	Name() string
	ProductID() uint16
}

// Bridge is a warm ASV5211. Control commands share one firmware mailbox and are
// serialised; bulk stream reads may run concurrently.
type Bridge struct {
	usb transport
	mu  sync.Mutex
	// i2cMu holds one I2C transaction together. A write is several commands
	// filling the firmware's one buffer and a last one sending it, so two
	// transactions interleaved would send each other's bytes; the vendor driver
	// takes a lock across the whole write for the same reason.
	i2cMu sync.Mutex
	// streamsMu is apart from mu so a stream read never waits behind a command.
	streamsMu sync.Mutex
	streams   [2]usbio.EndpointReader
}

// New wraps an opened USB device; Close releases the USB transport too.
func New(device *usbio.Transport) *Bridge {
	return &Bridge{usb: device, streams: [2]usbio.EndpointReader{
		usbio.ReaderOf(device, 0x81), usbio.ReaderOf(device, 0x82)}}
}

// Name reports the bus:address of the underlying USB device.
func (bridge *Bridge) Name() string { return bridge.usb.Name() }

// ProductID reports the USB product ID the device enumerated with.
func (bridge *Bridge) ProductID() uint16 { return bridge.usb.ProductID() }

// Close releases the USB device.
func (bridge *Bridge) Close() error {
	for _, stream := range bridge.streams {
		if stream != nil {
			_ = stream.Stop()
		}
	}
	return bridge.usb.Close()
}

func (bridge *Bridge) command(request byte, value, index uint16, dataLength int) ([]byte, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	response := make([]byte, dataLength+1)
	read, err := bridge.usb.Control(0xc0, request, value, index, response, timeoutMS)
	if err != nil {
		return nil, err
	}
	if read != len(response) {
		return nil, fmt.Errorf("ASV5211 command %#02x returned %d bytes; want %d", request, read, len(response))
	}
	if response[0] != 1 {
		trace.Printf("ASV5211 %s: request %#02x value %#04x index %#04x answered status %#02x",
			bridge.usb.Name(), request, value, index, response[0])
		return nil, fmt.Errorf("ASV5211 command %#02x status %#02x", request, response[0])
	}
	return response[1:], nil
}

func (bridge *Bridge) rawCommand(request byte, value, index uint16, responseLength int) ([]byte, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	response := make([]byte, responseLength)
	read, err := bridge.usb.Control(0xc0, request, value, index, response, timeoutMS)
	if err != nil {
		return nil, err
	}
	if read != len(response) {
		return nil, fmt.Errorf("ASV5211 command %#02x returned %d bytes; want %d", request, read, len(response))
	}
	return response, nil
}

// customerInfoLength is what request 0x0c answers with after its status byte.
const customerInfoLength = 0x39

// CustomerInfo reads the block the firmware keeps about the board it is on. Its
// last byte carries, in its top two bits, where this bridge sits among the
// bridges of one enclosure.
func (bridge *Bridge) CustomerInfo() ([]byte, error) {
	return bridge.command(0x0c, 0, 0, customerInfoLength)
}

// I2C returns the firmware's logical bus target (0x20, 0x22, ...).
func (bridge *Bridge) I2C(target byte) i2c.Bus { return bus{bridge: bridge, target: target} }

type bus struct {
	bridge *Bridge
	target byte
}

// checkAddress rejects any address but the bus's target: the firmware routes
// each logical bus to one device, so nothing else can answer.
func (bus bus) checkAddress(address byte) error {
	if address != bus.target {
		return fmt.Errorf("ASV5211 bus %#02x cannot address %#02x", bus.target, address)
	}
	return nil
}

func (bus bus) Write(address byte, data []byte) error {
	if err := bus.checkAddress(address); err != nil {
		return err
	}
	bus.bridge.i2cMu.Lock()
	defer bus.bridge.i2cMu.Unlock()
	err := bus.write(data, false)
	if err != nil {
		trace.Printf("ASV5211 %s: I2C write to %#02x [% x]: %v", bus.bridge.usb.Name(), bus.target, data, err)
	}
	return err
}

func (bus bus) write(data []byte, noStop bool) error {
	for offset := 0; offset < len(data); offset += 3 {
		end := offset + 3
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]
		var packed [3]byte
		copy(packed[:], chunk)
		if _, err := bus.bridge.command(0x0d, uint16(byte(offset))|uint16(packed[0])<<8,
			uint16(packed[1])|uint16(packed[2])<<8, len(chunk)); err != nil {
			return err
		}
	}
	stop := uint16(0)
	if noStop {
		stop = 1
	}
	_, err := bus.bridge.command(0x0e, uint16(bus.target)|stop<<8, 0, len(data))
	return err
}

func (bus bus) Read(address byte, length int) ([]byte, error) {
	if err := bus.checkAddress(address); err != nil {
		return nil, err
	}
	bus.bridge.i2cMu.Lock()
	defer bus.bridge.i2cMu.Unlock()
	return bus.read(length)
}

func (bus bus) read(length int) ([]byte, error) {
	return bus.bridge.command(0x19, uint16(bus.target), 0, length)
}

func (bus bus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if err := bus.checkAddress(address); err != nil {
		return nil, err
	}
	bus.bridge.i2cMu.Lock()
	defer bus.bridge.i2cMu.Unlock()
	if err := bus.write(write, true); err != nil {
		trace.Printf("ASV5211 %s: I2C write to %#02x [% x] before a read: %v", bus.bridge.usb.Name(), bus.target, write, err)
		return nil, err
	}
	data, err := bus.read(readLength)
	if err != nil {
		trace.Printf("ASV5211 %s: I2C read of %d from %#02x after [% x]: %v", bus.bridge.usb.Name(), readLength, bus.target, write, err)
	}
	return data, err
}

// SetGPIO updates selected bits in the primary GPIO bank.
func (bridge *Bridge) SetGPIO(value, mask byte) error {
	response, err := bridge.rawCommand(0x08, uint16(value)|uint16(mask)<<8, 0, 1)
	if err != nil {
		return err
	}
	if len(response) != 1 {
		return errors.New("ASV5211 GPIO response is empty")
	}
	return nil
}

// SetGPIOEx updates selected bits in the secondary GPIO bank.
func (bridge *Bridge) SetGPIOEx(value, mask byte) error {
	response, err := bridge.rawCommand(0x10, uint16(value)|uint16(mask)<<8, 0, 1)
	if err != nil {
		return err
	}
	if len(response) != 1 {
		return errors.New("ASV5211 extended GPIO response is empty")
	}
	return nil
}

// StartStream enables one of the two firmware stream channels.
func (bridge *Bridge) StartStream(channel int) error {
	if channel < 0 || channel > 1 {
		return fmt.Errorf("ASV5211 stream %d is outside 0..1", channel)
	}
	if err := bridge.stream(channel).Start(); err != nil {
		return err
	}
	_, err := bridge.rawCommand(0x06, uint16(channel), 0, 1)
	return err
}

// StopStream disables one of the two firmware stream channels.
func (bridge *Bridge) StopStream(channel int) error {
	if channel < 0 || channel > 1 {
		return fmt.Errorf("ASV5211 stream %d is outside 0..1", channel)
	}
	// What is still outstanding would otherwise carry the last channel's packets
	// into the next stream.
	stopped := bridge.stream(channel).Stop()
	_, err := bridge.rawCommand(0x07, uint16(channel), 0, 1)
	if err != nil {
		return err
	}
	return stopped
}

func (bridge *Bridge) readCF(channel int, register byte) (byte, error) {
	if channel < 0 || channel > 1 {
		return 0, fmt.Errorf("ASV5211 channel filter %d is outside 0..1", channel)
	}
	if channel == 1 {
		register |= 0x80
	}
	data, err := bridge.command(0x04, uint16(register), 0, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (bridge *Bridge) writeCF(channel int, register, value byte) error {
	if channel < 0 || channel > 1 {
		return fmt.Errorf("ASV5211 channel filter %d is outside 0..1", channel)
	}
	if channel == 1 {
		register |= 0x80
	}
	_, err := bridge.command(0x05, uint16(register)|uint16(value)<<8, 0, 1)
	return err
}

// filterRegisters is how many registers a channel filter has: the PID table,
// the control register at 0x40 and the words after it.
const filterRegisters = 0x45

// ResetFilter resets a channel filter with its registers kept, and sets the
// control register's bit 2 to match the reset's mode. The PX-W3U3 V2 has it
// done to a channel before streaming it, with mode 1; the Linux library's
// USB_FilterReset has the same shape. The reset clears the filter, so its
// registers are read first and put back after, skipping runs of three that are
// all zero.
func (bridge *Bridge) ResetFilter(channel int, mode byte) error {
	if channel < 0 || channel > 1 {
		return fmt.Errorf("ASV5211 channel filter %d is outside 0..1", channel)
	}
	high := byte(0)
	if channel == 1 {
		high = 0x80
	}
	registers := make([]byte, 0, filterRegisters)
	for len(registers) < filterRegisters {
		count := min(filterRegisters-len(registers), 0x20)
		data, err := bridge.command(0x04, uint16(byte(len(registers))|high), 0, count)
		if err != nil {
			return fmt.Errorf("read channel filter %d: %w", channel, err)
		}
		registers = append(registers, data...)
	}
	if mode != 0 {
		registers[0x40] |= 0x04
	} else {
		registers[0x40] &^= 0x04
	}
	if _, err := bridge.rawCommand(0x09, uint16(channel)|uint16(mode)<<8, 0, 1); err != nil {
		return fmt.Errorf("reset channel filter %d: %w", channel, err)
	}
	for start := 0; start < filterRegisters; start += 3 {
		chunk := registers[start:min(start+3, filterRegisters)]
		if chunk[0] == 0 && (len(chunk) < 2 || chunk[1] == 0) && (len(chunk) < 3 || chunk[2] == 0) {
			continue
		}
		var packed [3]byte
		copy(packed[:], chunk)
		if _, err := bridge.command(0x05, uint16(byte(start)|high)|uint16(packed[0])<<8,
			uint16(packed[1])|uint16(packed[2])<<8, len(chunk)); err != nil {
			return fmt.Errorf("restore channel filter %d: %w", channel, err)
		}
	}
	return nil
}

// Bits of a channel filter's control register, 0x40. Bit 2 is the mode
// ResetFilter sets.
const (
	// filterInput takes the channel's TS input in.
	filterInput = 0x03
	// filterRun starts the channel.
	filterRun = 0x08
)

// SetFilterInput takes a channel filter's TS input in or out (bits 0 and 1 of
// its control register), as the vendor libraries do when they bring a channel
// up (USB_FilterONOFF on Linux, the V2 driver's filter open).
func (bridge *Bridge) SetFilterInput(channel int, on bool) error {
	return bridge.updateFilterControl(channel, filterInput, on)
}

// SetFilterRun starts or stops a channel filter (bit 3 of its control
// register). Both vendor drivers set it only once the bulk transfers are queued
// and the channel started with request 0x06, and clear it first on the way
// down; USB_FilterStart_Stop on Linux.
func (bridge *Bridge) SetFilterRun(channel int, on bool) error {
	return bridge.updateFilterControl(channel, filterRun, on)
}

func (bridge *Bridge) updateFilterControl(channel int, bits byte, on bool) error {
	value, err := bridge.readCF(channel, 0x40)
	if err != nil {
		return err
	}
	if on {
		value |= bits
	} else {
		value &^= bits
	}
	return bridge.writeCF(channel, 0x40, value)
}

// SetFilterWord writes a sixteen-bit channel filter register, high byte first.
func (bridge *Bridge) SetFilterWord(channel int, register byte, value uint16) error {
	if err := bridge.writeCF(channel, register, byte(value>>8)); err != nil {
		return err
	}
	return bridge.writeCF(channel, register+1, byte(value))
}

// ReadStream reads channel 0 from endpoint 0x81 or channel 1 from 0x82.
func (bridge *Bridge) ReadStream(channel int, buffer []byte, timeout uint32) (int, error) {
	if channel < 0 || channel > 1 {
		return 0, fmt.Errorf("ASV5211 stream %d is outside 0..1", channel)
	}
	return bridge.stream(channel).Read(buffer, timeout)
}

// stream is a channel's reader, made on first use for a bridge put together
// around something other than a Transport.
func (bridge *Bridge) stream(channel int) usbio.EndpointReader {
	bridge.streamsMu.Lock()
	defer bridge.streamsMu.Unlock()
	if bridge.streams[channel] == nil {
		bridge.streams[channel] = usbio.ReaderOf(bridge.usb, byte(0x81+channel))
	}
	return bridge.streams[channel]
}
