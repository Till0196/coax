// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// The framing, register access and firmware loader below are the AF9035 family
// protocol, which the Linux media subsystem's af9035 driver documents under
// GPL-2.0-or-later.

// Package it9175 drives the ITE IT9175/IT9179 single-chip ISDB-T receiver: USB
// bridge, OFDM demodulator and tuner on one die, running downloadable firmware.
//
// With no I2C bus between separate ICs, this package carries the demodulator
// and tuner too and the board package above it only names the product. The
// register space is unified: addresses with bits 16-23 set reach the
// demodulator, the rest the USB link.
package it9175

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/pkg/usbio"
)

// Commands carried in the frame header, as command<<8 | mailbox is not the
// order the wire uses: the mailbox goes first, then the command code.
const (
	commandMemoryRead   = 0x00
	commandMemoryWrite  = 0x01
	commandConfigRead   = 0x04
	commandConfigWrite  = 0x05
	commandQueryInfo    = 0x22
	commandBoot         = 0x23
	commandScatterWrite = 0x29
)

// Bulk endpoints. Control runs on its own pair so it stays usable while the
// stream endpoint is busy.
const (
	endpointCommandOut = 0x02
	endpointCommandIn  = 0x81
	endpointStreamIn   = 0x84
)

const (
	controlTimeoutMS = 400
	// frameSize is the chip's command buffer. Both directions have to fit in
	// it, which is what bounds a register write to 52 bytes of payload.
	frameSize = 64
	// requestHeader is the length, mailbox, command and sequence bytes a frame
	// opens with; responseHeader is the length, sequence and result it answers
	// with. checksumLength closes both.
	requestHeader  = 4
	responseHeader = 3
	checksumLength = 2
	// maxWriteLength is what is left of a frame for a register write's payload.
	maxWriteLength = frameSize - requestHeader - checksumLength
)

// streamPackets is how many transport packets the chip gathers before completing
// a stream transfer. It sets the frame-size registers, so it belongs to the chip
// rather than to the caller's buffer.
const streamPackets = 305

// chipID is what an IT9175FN reports: chip type 0x9175 with version 0x8301.
const chipID = 0x91758301

// Device is an opened IT9175. Concurrent register access is serialised; stream
// reads bypass the lock so they cannot be held up by a status poll.
type Device struct {
	usb    *usbio.Transport
	stream *usbio.BulkReader

	// commandMu guards the sequence number and the shared frame buffer, which
	// are not re-entrant.
	commandMu sync.Mutex
	frame     [frameSize]byte
	sequence  byte

	chipVersion uint32
	firmware    uint32
	tunerID     byte

	// clockMode is what the chip reports its demodulator clock to be, and picks
	// the crystal figures the tuning arithmetic runs on.
	clockMode byte
	xtal      uint16
	fdiv      uint8
	// iqikCal seeds the chip's I/Q imbalance calibration register.
	iqikCal byte
	// bandwidthMode is the channel width; ISDB-T only ever uses 6 MHz, mode 0.
	bandwidthMode byte
	// boundaries are the tuning band edges in kHz, recomputed from the chip's
	// measured clock during bring-up and so per-device.
	boundaries [boundaryCount]uint32

	// card is the slot the chip carries beside the tuner. Its own lock keeps a
	// T=1 conversation, which spans several commands, from being interleaved
	// with another, while a tuner status poll may still run between them.
	card cardState
}

// New wraps an opened USB device. Nothing is sent until Open runs.
func New(transport *usbio.Transport) *Device {
	device := &Device{usb: transport, stream: transport.BulkReader(endpointStreamIn)}
	device.boundaries = defaultBoundaries
	return device
}

// Name reports the USB bus:address the device was found at.
func (device *Device) Name() string { return device.usb.Name() }

// ID reports the vendor and product pair the device enumerated with.
func (device *Device) ID() usbio.ID { return device.usb.ID() }

// TunerID reports the tuner variant the configuration EEPROM named, which is
// 0x70 on every FSUSB2i seen so far.
func (device *Device) TunerID() byte { return device.tunerID }

// command runs one framed exchange. writeLength is the staged payload's length;
// readLength is how many response bytes the caller wants, or zero for none. The
// caller must hold commandMu: the payload is staged in the shared frame buffer.
func (device *Device) command(mailbox, code byte, writeLength, readLength int) error {
	if writeLength > maxWriteLength {
		return fmt.Errorf("IT9175 command %#02x payload is too large: %d", code, writeLength)
	}
	length := requestHeader + writeLength + checksumLength
	sequence := device.sequence
	device.sequence++

	frame := device.frame[:length]
	frame[0] = byte(length - 1)
	frame[1] = mailbox
	frame[2] = code
	frame[3] = sequence
	sum := checksum(frame[1 : length-2])
	frame[length-2] = byte(sum >> 8)
	frame[length-1] = byte(sum)

	written, err := device.usb.Bulk(endpointCommandOut, frame, controlTimeoutMS)
	if err != nil {
		return fmt.Errorf("IT9175 command %#02x: %w", code, err)
	}
	if written != length {
		return fmt.Errorf("IT9175 command %#02x: sent %d of %d bytes", code, written, length)
	}

	// Every command is acknowledged, firmware scatter writes included. Leaving
	// an answer unread would desynchronise the next command, which would check
	// its sequence number against the previous reply.
	response := device.frame[:frameSize]
	read, err := device.usb.Bulk(endpointCommandIn, response, controlTimeoutMS)
	if err != nil {
		return fmt.Errorf("IT9175 command %#02x response: %w", code, err)
	}
	if read < responseHeader+checksumLength {
		return fmt.Errorf("IT9175 command %#02x: short response of %d bytes", code, read)
	}
	response = response[:read]
	want := checksum(response[1 : read-2])
	got := uint16(response[read-2])<<8 | uint16(response[read-1])
	if want != got {
		return fmt.Errorf("IT9175 command %#02x: checksum %#04x, want %#04x", code, got, want)
	}
	if response[1] != sequence {
		return fmt.Errorf("IT9175 command %#02x: sequence %#02x, want %#02x", code, response[1], sequence)
	}
	if response[2] != 0 {
		return fmt.Errorf("IT9175 command %#02x rejected with result %d", code, response[2])
	}
	if readLength != 0 && read < responseHeader+readLength+checksumLength {
		return fmt.Errorf("IT9175 command %#02x: %d response bytes, want %d", code, read-responseHeader-checksumLength, readLength)
	}
	return nil
}

// checksum is the chip's frame check: a ones-complement sum of the frame's
// big-endian 16-bit words, taken over everything between the length byte and
// the checksum itself.
func checksum(data []byte) uint16 {
	var sum uint16
	index := 0
	for ; index+1 < len(data); index += 2 {
		sum += uint16(data[index])<<8 | uint16(data[index+1])
	}
	if index < len(data) {
		sum += uint16(data[index]) << 8
	}
	return ^sum
}

// payload is where a command's arguments go and its answer comes back.
func (device *Device) payload() []byte { return device.frame[requestHeader:] }

// response is where a command's answer starts.
func (device *Device) response() []byte { return device.frame[responseHeader:] }

// ReadRegisters reads consecutive registers. Bits 16-23 of the address select
// the mailbox, which is how the demodulator half of the chip is reached.
func (device *Device) ReadRegisters(register uint32, length int) ([]byte, error) {
	if length <= 0 || length > frameSize-responseHeader-checksumLength {
		return nil, fmt.Errorf("IT9175 register read of %d bytes is out of range", length)
	}
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	device.stageRegisterHeader(register, length)
	if err := device.command(byte(register>>16), commandMemoryRead, 6, length); err != nil {
		return nil, fmt.Errorf("read IT9175 register %#06x: %w", register, err)
	}
	return append([]byte(nil), device.response()[:length]...), nil
}

// ReadRegister reads one register.
func (device *Device) ReadRegister(register uint32) (byte, error) {
	data, err := device.ReadRegisters(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// WriteRegisters writes consecutive registers.
func (device *Device) WriteRegisters(register uint32, data []byte) error {
	if len(data) == 0 || 6+len(data) > maxWriteLength {
		return fmt.Errorf("IT9175 register write of %d bytes is out of range", len(data))
	}
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	device.stageRegisterHeader(register, len(data))
	copy(device.payload()[6:], data)
	if err := device.command(byte(register>>16), commandMemoryWrite, 6+len(data), 0); err != nil {
		return fmt.Errorf("write IT9175 register %#06x: %w", register, err)
	}
	return nil
}

// WriteRegister writes one register.
func (device *Device) WriteRegister(register uint32, value byte) error {
	return device.WriteRegisters(register, []byte{value})
}

// WriteRegisterMask writes the bits the mask selects, reading the register
// first when only some of them are wanted.
func (device *Device) WriteRegisterMask(register uint32, value, mask byte) error {
	if mask == 0 || mask == 0xff {
		return device.WriteRegister(register, value)
	}
	current, err := device.ReadRegister(register)
	if err != nil {
		return err
	}
	return device.WriteRegister(register, value&mask|current&^mask)
}

// stageRegisterHeader lays a register access's arguments into the frame; the
// caller holds commandMu.
func (device *Device) stageRegisterHeader(register uint32, length int) {
	payload := device.payload()
	payload[0] = byte(length)
	// The second byte selects the chip's processor; every access uses the same.
	payload[1] = 2
	payload[2] = 0
	payload[3] = 0
	payload[4] = byte(register >> 8)
	payload[5] = byte(register)
}

// regValue is one entry of a register table applied verbatim.
type regValue struct {
	register uint32
	value    byte
}

// regMask is one entry of a register table applied through a bit mask.
type regMask struct {
	register uint32
	value    byte
	mask     byte
}

func (device *Device) writeValues(table []regValue) error {
	for _, entry := range table {
		if err := device.WriteRegister(entry.register, entry.value); err != nil {
			return err
		}
	}
	return nil
}

func (device *Device) writeMasks(table []regMask) error {
	for _, entry := range table {
		if err := device.WriteRegisterMask(entry.register, entry.value, entry.mask); err != nil {
			return err
		}
	}
	return nil
}

// identify reads the chip's type and version and the firmware state. The same
// registers serve the whole family: 0x1222 carries the version and the 16-bit
// type, 0x384f the pre-chip version that completes it.
func (device *Device) identify() error {
	data, err := device.ReadRegisters(0x1222, 3)
	if err != nil {
		return err
	}
	version := uint32(data[0])
	chipType := uint32(data[2])<<8 | uint32(data[1])

	prechip, err := device.ReadRegister(0x384f)
	if err != nil {
		return err
	}
	version |= uint32(prechip) << 8
	device.chipVersion = chipType<<16 | version
	if device.chipVersion != chipID {
		return fmt.Errorf("unsupported ITE chip %04x_%04x", chipType, version)
	}
	return device.readFirmwareVersion()
}

// readFirmwareVersion asks the chip which image it is running, or zero when it
// is still on its boot ROM.
func (device *Device) readFirmwareVersion() error {
	device.commandMu.Lock()
	defer device.commandMu.Unlock()
	device.payload()[0] = 1
	if err := device.command(0, commandQueryInfo, 1, 4); err != nil {
		return fmt.Errorf("query the IT9175 firmware version: %w", err)
	}
	answer := device.response()
	device.firmware = uint32(answer[3])<<24 | uint32(answer[2])<<16 | uint32(answer[1])<<8 | uint32(answer[0])
	return nil
}

// readConfig takes the tuner variant out of the configuration EEPROM. A board
// whose EEPROM is silent is assumed to be an FSUSB2i, the only product this chip
// is known to appear in here.
func (device *Device) readConfig() error {
	present, err := device.ReadRegister(0x461c)
	if err != nil {
		return err
	}
	if present == 0 {
		device.tunerID = defaultTunerID
		return nil
	}
	tuner, err := device.ReadRegister(eepromBase + eepromTunerID)
	if err != nil {
		return err
	}
	device.tunerID = tuner
	return nil
}

// EEPROM layout. Only the tuner identifier is needed; the IR, TS-mode and
// tuner-IF entries beside it do not change how a channel is acquired.
const (
	eepromBase    = 0x499c
	eepromTunerID = 0x34
	// defaultTunerID is what an FSUSB2i's EEPROM holds, and what is assumed
	// when the EEPROM cannot be read.
	defaultTunerID = 0x70
)

// Open brings the chip up: identify it, read its configuration, download the
// firmware if it is still on its boot ROM, and initialise the demodulator and
// tuner. It is left tuned to nothing, with its stream endpoint idle.
func (device *Device) Open(firmwarePath string) error {
	if err := device.identify(); err != nil {
		return err
	}
	if err := device.readConfig(); err != nil {
		return fmt.Errorf("read the IT9175 configuration: %w", err)
	}
	if device.firmware == 0 {
		if err := device.loadFirmware(firmwarePath); err != nil {
			return err
		}
	}
	return device.initialize()
}

// Close puts the chip into its power-saving state and releases the USB device.
func (device *Device) Close() error {
	_ = device.stream.Stop()
	_ = device.sleep()
	return device.usb.Close()
}

// ReadStream takes the next transport packets off the stream endpoint. It
// returns usbio.ErrTimeout when the chip had nothing ready.
func (device *Device) ReadStream(buffer []byte, timeoutMS uint32) (int, error) {
	return device.stream.Read(buffer, timeoutMS)
}

// StartReading puts the reads on the stream endpoint in flight.
func (device *Device) StartReading() error { return device.stream.Start() }

// StopReading withdraws the reads still outstanding on the stream endpoint, so
// the next stream does not begin with what the last channel sent.
func (device *Device) StopReading() error { return device.stream.Stop() }

// sleep parks the demodulator and tuner and powers the analogue front end down.
func (device *Device) sleep() error {
	steps := []regMask{
		{0x80fbb9, 0, 0x20},
	}
	if err := device.writeMasks(steps); err != nil {
		return err
	}
	if err := device.WriteRegister(0xe00c, 0x01); err != nil {
		return err
	}
	if err := device.WriteRegisterMask(0x80fbb9, 0x20, 0x20); err != nil {
		return err
	}
	// Suspend the demodulator and wait for it to acknowledge, so the analogue
	// supply is not pulled while it is still running.
	if err := device.writeValues([]regValue{{0x80004c, 0x01}, {0x800000, 0}}); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	for attempt := 0; attempt < 20; attempt++ {
		state, err := device.ReadRegister(0x80004c)
		if err != nil {
			return err
		}
		if state == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := device.writeValues([]regValue{
		{0x80fb24, 0x08},
		// Stop the tuner clock, then drop the tuner's supply.
		{0x80fba8, 0},
		{0x80ec40, 0},
	}); err != nil {
		return err
	}
	// Clearing the tuner's register banks leaves no stale calibration for the
	// next open to reapply.
	zeros := make([]byte, 15)
	zeros[1] = 0x0c
	if err := device.WriteRegisters(0x80ec02, zeros); err != nil {
		return err
	}
	zeros[1] = 0
	for _, bank := range []struct {
		register uint32
		length   int
	}{{0x80ec12, 4}, {0x80ec17, 9}, {0x80ec22, 10}} {
		if err := device.WriteRegisters(bank.register, zeros[:bank.length]); err != nil {
			return err
		}
	}
	return device.writeValues([]regValue{{0x80ec20, 0}, {0x80ec3f, 0x01}})
}

// errChipConfig reports that the demodulator stopped accepting configuration,
// which means the sequence has to start again from a fresh open.
var errChipConfig = errors.New("the IT9175 demodulator rejected its configuration")

// checkConfigured reads the demodulator's configuration-valid flag, tested after
// each group of writes.
func (device *Device) checkConfigured() error {
	state, err := device.ReadRegister(0x8001dc)
	if err != nil {
		return err
	}
	if state != 1 {
		return fmt.Errorf("%w (state %#02x)", errChipConfig, state)
	}
	return nil
}
