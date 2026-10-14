// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (it930x.c, itedtv_bus.c) by nns779.

// Package it930x drives the ITE IT930x USB bridge every PLEX PX-series board is
// built around. It speaks a framed command protocol on bulk endpoints 0x02 and
// 0x81, carrying register access, three I2C masters, GPIO and a firmware
// loader. All of the board's tuners feed one bulk stream endpoint, 0x84, each
// packet marked with the source input's sync byte; ../../tsdemux splits that
// apart.
package it930x

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

// Command codes carried in the frame header.
const (
	commandRegisterRead  = 0x0000
	commandRegisterWrite = 0x0001
	commandQueryInfo     = 0x0022
	commandBoot          = 0x0023
	commandScatterWrite  = 0x0029
	commandI2CRead       = 0x002a
	commandI2CWrite      = 0x002b
)

// Bulk endpoints. Control runs on its own pair so it stays usable while the
// stream endpoint is busy.
const (
	endpointCommandOut = 0x02
	endpointCommandIn  = 0x81
	endpointStreamIn   = 0x84
)

const (
	controlTimeoutMS = 3000
	frameSize        = 256
	// Inputs is how many TS input ports the bridge multiplexes.
	Inputs    = 5
	gpioCount = 16
)

// GPIOMode is the direction of a bridge GPIO pin.
type GPIOMode int

// GPIO directions.
const (
	GPIOUndefined GPIOMode = iota
	GPIOIn
	GPIOOut
)

// Input configures one TS input port of the bridge.
type Input struct {
	Enable bool
	// IsParallel selects a parallel rather than serial TS interface. Only ports
	// 0 and 1 support it.
	IsParallel bool
	// PortNumber is the bridge's internal port, not the index in Config.Inputs.
	PortNumber byte
	// SlaveNumber selects which of the five I2C address slots the port uses.
	SlaveNumber byte
	// I2CBus is the bridge I2C master the demodulator sits on, 1 to 3.
	I2CBus byte
	// I2CAddr is the demodulator's 7-bit I2C address.
	I2CAddr byte
	// PacketLength is the TS packet size, normally 188.
	PacketLength byte
	// SyncByte is written into every packet from this port so the host can tell
	// the inputs apart. Boards with one input leave it at 0x47.
	SyncByte byte
}

// Config describes the board the bridge is on.
type Config struct {
	// XferSize is the stream transfer threshold in bytes.
	XferSize uint32
	// I2CSpeed is the bridge's I2C clock divider, 0x07 on every known board.
	I2CSpeed byte
	// MaxBulkSize is the stream endpoint's maximum packet size: 512 at full
	// speed, 1024 at high speed.
	MaxBulkSize int
	// Inputs configures the TS input ports, in board order.
	Inputs [Inputs]Input
}

type gpioState struct {
	enable bool
	mode   GPIOMode
}

// Bridge is an opened IT930x.
type Bridge struct {
	usb    *usbio.Transport
	config Config
	stream *usbio.BulkReader

	// commandMu serialises the framed command protocol: the sequence number and
	// the shared frame buffer are not re-entrant. Stream reads bypass it.
	commandMu sync.Mutex
	frame     [frameSize]byte
	sequence  byte

	gpioMu sync.Mutex
	gpio   [gpioCount]gpioState
}

// New wraps an opened USB device. Nothing is sent until Raise or LoadFirmware.
func New(transport *usbio.Transport, config Config) *Bridge {
	if config.I2CSpeed == 0 {
		config.I2CSpeed = 0x07
	}
	if config.MaxBulkSize == 0 {
		config.MaxBulkSize = 512
	}
	if config.XferSize == 0 {
		config.XferSize = 188 * 816
	}
	return &Bridge{usb: transport, config: config, stream: transport.BulkReader(endpointStreamIn)}
}

// Name reports the bus:address of the underlying USB device.
func (bridge *Bridge) Name() string { return bridge.usb.Name() }

// ProductID reports the USB product number the board enumerated with.
func (bridge *Bridge) ProductID() uint16 { return bridge.usb.ProductID() }

// Serial reports the enclosure's serial number, which the boards that
// enumerate as two USB devices share.
func (bridge *Bridge) Serial() (string, error) { return bridge.usb.Serial() }

// Close releases the USB device.
func (bridge *Bridge) Close() error {
	_ = bridge.stream.Stop()
	return bridge.usb.Close()
}

// checksum is the bridge's ones-complement sum over big-endian 16-bit words.
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

// command runs one framed exchange. readLength is how many response bytes the
// caller wants, or zero for none.
func (bridge *Bridge) command(code uint16, write []byte, readLength int) ([]byte, error) {
	if len(write) > frameSize-1-3-2 {
		return nil, fmt.Errorf("IT930x command %#04x payload is too large: %d", code, len(write))
	}
	bridge.commandMu.Lock()
	defer bridge.commandMu.Unlock()

	length := 4 + 2 + len(write)
	sequence := bridge.sequence
	bridge.sequence++
	frame := bridge.frame[:length]
	frame[0] = byte(length - 1)
	frame[1] = byte(code >> 8)
	frame[2] = byte(code)
	frame[3] = sequence
	copy(frame[4:], write)
	sum := checksum(frame[1 : length-2])
	frame[length-2] = byte(sum >> 8)
	frame[length-1] = byte(sum)

	if err := bridge.send(frame); err != nil {
		return nil, fmt.Errorf("IT930x command %#04x: %w", code, err)
	}

	response := bridge.frame[:frameSize]
	read, err := bridge.usb.Bulk(endpointCommandIn, response, controlTimeoutMS)
	// The bridge needs a moment between frames.
	time.Sleep(time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("IT930x command %#04x response: %w", code, err)
	}
	if read < 5 {
		return nil, fmt.Errorf("IT930x command %#04x: short response of %d bytes", code, read)
	}
	response = response[:read]
	want := checksum(response[1 : read-2])
	got := uint16(response[read-2])<<8 | uint16(response[read-1])
	if want != got {
		return nil, fmt.Errorf("IT930x command %#04x: checksum %#04x, want %#04x", code, got, want)
	}
	if response[1] != sequence {
		return nil, fmt.Errorf("IT930x command %#04x: sequence %#02x, want %#02x", code, response[1], sequence)
	}
	if response[2] != 0 {
		return nil, fmt.Errorf("IT930x command %#04x rejected with result %d", code, response[2])
	}
	if readLength == 0 {
		return nil, nil
	}
	// read is at least 5, so this cannot go negative.
	available := read - 3 - 2
	if available > readLength {
		available = readLength
	}
	result := make([]byte, available)
	copy(result, response[3:])
	return result, nil
}

// send writes a command frame, which for firmware blocks may exceed one bulk
// transfer.
func (bridge *Bridge) send(frame []byte) error {
	written, err := bridge.usb.Bulk(endpointCommandOut, frame, controlTimeoutMS)
	time.Sleep(time.Millisecond)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("short write: %d of %d", written, len(frame))
	}
	return nil
}

// registerLength is how many address bytes a register needs on the wire.
func registerLength(register uint32) byte {
	switch {
	case register&0xff000000 != 0:
		return 4
	case register&0x00ff0000 != 0:
		return 3
	case register&0x0000ff00 != 0:
		return 2
	default:
		return 1
	}
}

func registerHeader(register uint32, length byte) []byte {
	return []byte{
		length,
		registerLength(register),
		byte(register >> 24), byte(register >> 16), byte(register >> 8), byte(register),
	}
}

// ReadRegisters reads consecutive registers.
func (bridge *Bridge) ReadRegisters(register uint32, length int) ([]byte, error) {
	if length <= 0 || length > 251 {
		return nil, fmt.Errorf("IT930x register read of %d bytes is outside the frame's 1..251-byte range", length)
	}
	return bridge.command(commandRegisterRead, registerHeader(register, byte(length)), length)
}

// ReadRegister reads one register.
func (bridge *Bridge) ReadRegister(register uint32) (byte, error) {
	data, err := bridge.ReadRegisters(register, 1)
	if err != nil {
		return 0, err
	}
	if len(data) != 1 {
		return 0, fmt.Errorf("IT930x register %#06x: short read", register)
	}
	return data[0], nil
}

// WriteRegisters writes consecutive registers.
func (bridge *Bridge) WriteRegisters(register uint32, data []byte) error {
	if len(data) == 0 || len(data) > 250-6 {
		return fmt.Errorf("IT930x register write of %d bytes is outside the frame's 1..%d-byte range", len(data), 250-6)
	}
	_, err := bridge.command(commandRegisterWrite, append(registerHeader(register, byte(len(data))), data...), 0)
	return err
}

// WriteRegister writes one register.
func (bridge *Bridge) WriteRegister(register uint32, value byte) error {
	return bridge.WriteRegisters(register, []byte{value})
}

// WriteRegisterMask writes the masked bits, reading the register first when only
// some of them change.
func (bridge *Bridge) WriteRegisterMask(register uint32, value, mask byte) error {
	if mask == 0 {
		return errors.New("IT930x masked write with an empty mask")
	}
	if mask != 0xff {
		current, err := bridge.ReadRegister(register)
		if err != nil {
			return err
		}
		value = i2c.Mask(current, value, mask)
	}
	return bridge.WriteRegister(register, value)
}

// I2C returns one of the bridge's I2C masters as a bus, numbered 1 to 3, with
// 8-bit addresses.
func (bridge *Bridge) I2C(bus byte) i2c.Bus { return i2cBus{bridge: bridge, bus: bus} }

type i2cBus struct {
	bridge *Bridge
	bus    byte
}

func (bus i2cBus) Write(address byte, data []byte) error {
	if len(data) == 0 || len(data) > 250-3 {
		return fmt.Errorf("IT930x I2C write of %d bytes is outside the frame's 1..%d-byte range", len(data), 250-3)
	}
	command := append([]byte{byte(len(data)), bus.bus, address}, data...)
	_, err := bus.bridge.command(commandI2CWrite, command, 0)
	return err
}

func (bus i2cBus) Read(address byte, length int) ([]byte, error) {
	if length <= 0 || length > 251 {
		return nil, fmt.Errorf("IT930x I2C read of %d bytes is outside the frame's 1..251-byte range", length)
	}
	return bus.bridge.command(commandI2CRead, []byte{byte(length), bus.bus, address}, length)
}

// FirmwareVersion reports the running firmware, zero when the bridge is still
// cold.
func (bridge *Bridge) FirmwareVersion() (uint32, error) {
	data, err := bridge.command(commandQueryInfo, []byte{1}, 4)
	if err != nil {
		return 0, err
	}
	if len(data) != 4 {
		return 0, errors.New("IT930x: short firmware version response")
	}
	return uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3]), nil
}

// Raise wakes the bridge. A cold device ignores the first few frames, so the
// query is retried.
func (bridge *Bridge) Raise() error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if _, err = bridge.FirmwareVersion(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("IT930x did not respond: %w", err)
}

// LoadFirmware uploads the bridge firmware unless it is already running.
func (bridge *Bridge) LoadFirmware(path string) error {
	version, err := bridge.FirmwareVersion()
	if err != nil {
		return err
	}
	if version != 0 {
		return nil
	}
	if err := bridge.WriteRegister(0xf103, bridge.config.I2CSpeed); err != nil {
		return err
	}
	firmware, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read required IT930x firmware %s: %w", path, err)
	}
	if err := bridge.uploadFirmware(firmware); err != nil {
		return err
	}
	if _, err := bridge.command(commandBoot, nil, 0); err != nil {
		return fmt.Errorf("boot IT930x: %w", err)
	}
	version, err = bridge.FirmwareVersion()
	if err != nil {
		return err
	}
	if version == 0 {
		return errors.New("IT930x firmware did not start")
	}
	return nil
}

// uploadFirmware walks the blob's scatter-write blocks: each starts with 0x03,
// its fourth byte counts the segments, and every segment's length is the third
// byte of its descriptor.
func (bridge *Bridge) uploadFirmware(firmware []byte) error {
	for offset := 0; offset < len(firmware); {
		if offset+4 > len(firmware) || firmware[offset] != 0x03 {
			return fmt.Errorf("invalid IT930x firmware block at offset %#x", offset)
		}
		segments := int(firmware[offset+3])
		header := 4 + segments*3
		if offset+header > len(firmware) {
			return fmt.Errorf("truncated IT930x firmware block at offset %#x", offset)
		}
		length := 0
		for segment := 0; segment < segments; segment++ {
			length += int(firmware[offset+6+segment*3])
		}
		if length == 0 {
			// A block carrying no data still advances past its header.
			offset += header
			continue
		}
		length += header
		if offset+length > len(firmware) {
			return fmt.Errorf("truncated IT930x firmware block at offset %#x", offset)
		}
		if _, err := bridge.command(commandScatterWrite, firmware[offset:offset+length], 0); err != nil {
			return fmt.Errorf("IT930x firmware block at offset %#x: %w", offset, err)
		}
		offset += length
	}
	return nil
}

// InitWarm configures a bridge whose firmware is running: stream output, the I2C
// masters and the TS input ports.
func (bridge *Bridge) InitWarm() error {
	for _, register := range []uint32{0x4976, 0x4bfb, 0x4978, 0x4977} {
		if err := bridge.WriteRegister(register, 0); err != nil {
			return err
		}
	}
	// Keep the sync byte rather than dropping it: the host tells the inputs
	// apart by it.
	if err := bridge.WriteRegister(0xda1a, 0); err != nil {
		return err
	}
	for _, step := range []struct {
		register    uint32
		value, mask byte
	}{
		{0xf41f, 0x04, 0x04}, // DVB-T interrupt: enable
		{0xda10, 0x00, 0x01}, // MPEG full speed
		{0xf41a, 0x01, 0x01}, // DVB-T mode: enable
	} {
		if err := bridge.WriteRegisterMask(step.register, step.value, step.mask); err != nil {
			return err
		}
	}
	if err := bridge.configureStreamOutput(); err != nil {
		return fmt.Errorf("configure IT930x stream output: %w", err)
	}
	for _, step := range []struct {
		register uint32
		value    byte
	}{{0xd833, 1}, {0xd830, 0}, {0xd831, 1}, {0xd832, 0}} {
		if err := bridge.WriteRegister(step.register, step.value); err != nil {
			return err
		}
	}
	if err := bridge.configureI2C(); err != nil {
		return fmt.Errorf("configure IT930x I2C: %w", err)
	}
	if err := bridge.configureStreamInput(); err != nil {
		return fmt.Errorf("configure IT930x stream input: %w", err)
	}
	return nil
}

// i2cAddressRegisters is the address and bus register pair of each of the five
// demodulator slots.
var i2cAddressRegisters = [Inputs][2]uint32{
	{0x4975, 0x4971},
	{0x4974, 0x4970},
	{0x4973, 0x496f},
	{0x4972, 0x496e},
	{0x4964, 0x4963},
}

func (bridge *Bridge) configureI2C() error {
	if err := bridge.WriteRegister(0xf6a7, bridge.config.I2CSpeed); err != nil {
		return err
	}
	if err := bridge.WriteRegister(0xf103, bridge.config.I2CSpeed); err != nil {
		return err
	}
	for _, input := range bridge.config.Inputs {
		if !input.Enable {
			continue
		}
		if int(input.SlaveNumber) >= len(i2cAddressRegisters) {
			return fmt.Errorf("IT930x slave number %d is out of range", input.SlaveNumber)
		}
		registers := i2cAddressRegisters[input.SlaveNumber]
		if err := bridge.WriteRegister(registers[0], input.I2CAddr<<1); err != nil {
			return err
		}
		if err := bridge.WriteRegister(registers[1], input.I2CBus); err != nil {
			return err
		}
	}
	return nil
}

func (bridge *Bridge) configureStreamInput() error {
	for _, input := range bridge.config.Inputs {
		port := uint32(input.PortNumber)
		if !input.Enable {
			if err := bridge.WriteRegister(0xda4c+port, 0); err != nil {
				return err
			}
			continue
		}
		if input.PortNumber < 2 {
			parallel := byte(0)
			if input.IsParallel {
				parallel = 1
			}
			if err := bridge.WriteRegister(0xda58+port, parallel); err != nil {
				return err
			}
		}
		// Aggregation mode: tag packets with the sync byte.
		if err := bridge.WriteRegister(0xda73+port, 1); err != nil {
			return err
		}
		if err := bridge.WriteRegister(0xda78+port, input.SyncByte); err != nil {
			return err
		}
		if err := bridge.WriteRegister(0xda4c+port, 1); err != nil {
			return err
		}
	}
	return nil
}

func (bridge *Bridge) configureStreamOutput() error {
	if err := bridge.WriteRegisterMask(0xda1d, 0x01, 0x01); err != nil {
		return err
	}
	err := bridge.configureStreamEndpoint()
	// The bridge is taken back out of hold whatever happened above.
	if release := bridge.WriteRegisterMask(0xda1d, 0x00, 0x01); err == nil {
		err = release
	}
	if reverse := bridge.WriteRegister(0xd920, 0); err == nil {
		err = reverse
	}
	return err
}

func (bridge *Bridge) configureStreamEndpoint() error {
	// Cycle endpoint 4 with its NAK disabled before setting the sizes.
	for _, step := range []struct {
		register    uint32
		value, mask byte
	}{{0xdd11, 0x00, 0x20}, {0xdd13, 0x00, 0x20}, {0xdd11, 0x20, 0x20}} {
		if err := bridge.WriteRegisterMask(step.register, step.value, step.mask); err != nil {
			return err
		}
	}
	threshold := uint16(bridge.config.XferSize / 4)
	if err := bridge.WriteRegisters(0xdd88, []byte{byte(threshold), byte(threshold >> 8)}); err != nil {
		return err
	}
	if err := bridge.WriteRegister(0xdd0c, byte(bridge.config.MaxBulkSize/4)); err != nil {
		return err
	}
	for _, register := range []uint32{0xda05, 0xda06} {
		if err := bridge.WriteRegisterMask(register, 0x00, 0x01); err != nil {
			return err
		}
	}
	return nil
}

// GPIO register banks, one entry per pin. The mode register is followed by its
// enable register, so only the mode bank needs listing for both.
var (
	gpioModeRegisters = [gpioCount]uint32{
		0xd8b0, 0xd8b8, 0xd8b4, 0xd8c0, 0xd8bc, 0xd8c8, 0xd8c4, 0xd8d0,
		0xd8cc, 0xd8d8, 0xd8d4, 0xd8e0, 0xd8dc, 0xd8e4, 0xd8e8, 0xd8ec,
	}
	gpioEnableRegisters = [gpioCount]uint32{
		0xd8b1, 0xd8b9, 0xd8b5, 0xd8c1, 0xd8bd, 0xd8c9, 0xd8c5, 0xd8d1,
		0xd8cd, 0xd8d9, 0xd8d5, 0xd8e1, 0xd8dd, 0xd8e5, 0xd8e9, 0xd8ed,
	}
	gpioOutputRegisters = [gpioCount]uint32{
		0xd8af, 0xd8b7, 0xd8b3, 0xd8bf, 0xd8bb, 0xd8c7, 0xd8c3, 0xd8cf,
		0xd8cb, 0xd8d7, 0xd8d3, 0xd8df, 0xd8db, 0xd8e3, 0xd8e7, 0xd8eb,
	}
	gpioInputRegisters = [gpioCount]uint32{
		0xd8ae, 0xd8b6, 0xd8b2, 0xd8be, 0xd8ba, 0xd8c6, 0xd8c2, 0xd8ce,
		0xd8ca, 0xd8d6, 0xd8d2, 0xd8de, 0xd8da, 0xd8e2, 0xd8e6, 0xd8ea,
	}
)

// gpioIndex converts the one-based pin number the boards use into a bank index.
func gpioIndex(gpio int) (int, error) {
	if gpio <= 0 || gpio > gpioCount {
		return 0, fmt.Errorf("IT930x GPIO %d is outside the range 1..%d", gpio, gpioCount)
	}
	return gpio - 1, nil
}

// SetGPIOMode sets a pin's direction, optionally enabling it at the same time.
func (bridge *Bridge) SetGPIOMode(gpio int, mode GPIOMode, enable bool) error {
	index, err := gpioIndex(gpio)
	if err != nil {
		return err
	}
	var value byte
	switch mode {
	case GPIOIn:
		value = 0
	case GPIOOut:
		value = 1
	default:
		return fmt.Errorf("IT930x GPIO %d: undefined mode", gpio)
	}
	bridge.gpioMu.Lock()
	defer bridge.gpioMu.Unlock()
	if bridge.gpio[index].mode == mode {
		return nil
	}
	bridge.gpio[index].mode = mode
	if err := bridge.WriteRegister(gpioModeRegisters[index], value); err != nil {
		return err
	}
	if !enable || bridge.gpio[index].enable {
		return nil
	}
	bridge.gpio[index].enable = true
	return bridge.WriteRegister(gpioEnableRegisters[index], 1)
}

// WriteGPIO drives an output pin.
func (bridge *Bridge) WriteGPIO(gpio int, high bool) error {
	index, err := gpioIndex(gpio)
	if err != nil {
		return err
	}
	bridge.gpioMu.Lock()
	defer bridge.gpioMu.Unlock()
	if bridge.gpio[index].mode != GPIOOut {
		return fmt.Errorf("IT930x GPIO %d is not an output", gpio)
	}
	value := byte(0)
	if high {
		value = 1
	}
	return bridge.WriteRegister(gpioOutputRegisters[index], value)
}

// ReadGPIO samples an input pin.
func (bridge *Bridge) ReadGPIO(gpio int) (bool, error) {
	index, err := gpioIndex(gpio)
	if err != nil {
		return false, err
	}
	bridge.gpioMu.Lock()
	if bridge.gpio[index].mode != GPIOIn {
		bridge.gpioMu.Unlock()
		return false, fmt.Errorf("IT930x GPIO %d is not an input", gpio)
	}
	bridge.gpioMu.Unlock()
	value, err := bridge.ReadRegister(gpioInputRegisters[index])
	if err != nil {
		return false, err
	}
	return value != 0, nil
}

// PurgePSB drains whatever the bridge buffered before a capture starts, so the
// first packets belong to the channel just tuned.
func (bridge *Bridge) PurgePSB(timeoutMS uint32) error {
	if err := bridge.WriteRegisterMask(0xda1d, 0x01, 0x01); err != nil {
		return err
	}
	buffer := make([]byte, 1024)
	read, err := bridge.usb.Bulk(endpointStreamIn, buffer, timeoutMS)
	_ = bridge.WriteRegisterMask(0xda1d, 0x00, 0x01)
	// A short transfer that stopped at the 512-byte pending block is the
	// expected outcome, not a failure.
	if read == 512 {
		return nil
	}
	if errors.Is(err, usbio.ErrTimeout) {
		// Nothing was buffered, which is exactly what the purge wants.
		return nil
	}
	return err
}

// ReadStream reads the multiplexed transport stream of every enabled input. It
// returns usbio.ErrTimeout when the board had nothing ready.
func (bridge *Bridge) ReadStream(buffer []byte, timeoutMS uint32) (int, error) {
	return bridge.stream.Read(buffer, timeoutMS)
}

// StartReading puts the reads on the stream endpoint in flight.
func (bridge *Bridge) StartReading() error { return bridge.stream.Start() }

// StopReading withdraws the reads still outstanding on the stream endpoint, so
// the next capture does not begin with what the last one was sent.
func (bridge *Bridge) StopReading() error { return bridge.stream.Stop() }
