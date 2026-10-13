// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// Package em287x drives the eMPIA EM287x USB bridge used by ISDB boards such as
// the US-3POUT and the KTV-FSUSB2: vendor control transfers carry register
// access and I2C, and the transport stream arrives on endpoint 0x84, bulk or
// isochronous depending on how the bridge enumerated. Only the power-up
// sequence and the I2C clock differ between boards, so each board has its own
// constructor and shares the rest.
package em287x

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

const (
	registerI2CReturn  = 0x05
	registerI2CMode    = 0x06
	registerChipID     = 0x0a
	registerChipConfig = 0x0c
	registerTSEnable   = 0x5f
	registerGPIOP0     = 0x80
)

// registerCASMode1 enables the bridge's smart-card block: 1 brings it up, 0
// stops it. Scanning never reads a card, so only the stopping side is used.
const registerCASMode1 = 0x72

// TS enable bits of register 0x5f.
const (
	tsCaptureEnable = 0x01
	tsNullDiscard   = 0x04
)

// controlTimeoutMS bounds every register and I2C control transfer.
const controlTimeoutMS = 400

// The shape of one isochronous transfer on the stream endpoint: sixteen frames
// of five transport packets, which is the alternate setting's frame size.
const (
	isoPacketSize = 188 * 5
	isoPackets    = 16
	// isoDepth is how many transfers are kept outstanding. Each covers 2 ms of the
	// endpoint, so this is a second of it.
	//
	// A second is not for a slow reader; it is for the host. An isochronous
	// endpoint is served only while a transfer is outstanding, and a continued
	// transfer is only valid behind another still running, so once the outstanding
	// transfers are used up the chain comes apart. What is outstanding has to
	// cover the longest the host can go without completing one -- measured, half a
	// second of silence with nothing of ours outstanding, against a quarter of a
	// second of ring.
	isoDepth = 512
)

// Bridge is an opened EM287x. Concurrent register and I2C access is serialised.
type Bridge struct {
	usb *usbio.Transport
	mu  sync.Mutex
	// primaryClock and secondaryClock are the register 0x06 values that put the
	// I2C master on each bus at the rate the board's ICs answer at.
	primaryClock   byte
	secondaryClock byte
	i2cMode        byte
	iso            bool
	// stream is the isochronous ring, held between Stream(true) and
	// Stream(false). It is nil on a bulk bridge and where the platform backend
	// has no ring, in which case ReadStream falls back to one transfer at a time.
	stream   *usbio.IsoStream
	streamMu sync.Mutex
	// fsusb2 selects the KTV-FSUSB2 stream control, which drives the TS enable
	// register directly instead of through the US-3POUT's vendor request.
	fsusb2 bool

	// card is the slot the bridge carries beside the tuner. It has its own
	// lock because a T=1 conversation spans several register writes and window
	// transfers that must not be interleaved with another.
	card cardState
}

// New probes and initialises the bridge on a US-3POUT; Close releases the USB
// transport too.
func New(device *usbio.Transport) (*Bridge, error) {
	bridge := &Bridge{usb: device, primaryClock: 0x42, secondaryClock: 0x45}
	chip, err := bridge.readRegister(registerChipID)
	if err != nil {
		return nil, err
	}
	if chip != 0x41 && chip != 0x72 {
		return nil, fmt.Errorf("unsupported EM287x chip ID %#02x", chip)
	}
	if err := bridge.writeRegister(registerI2CMode, bridge.primaryClock); err != nil {
		return nil, err
	}
	bridge.i2cMode = bridge.primaryClock
	if err := bridge.initialize(); err != nil {
		return nil, err
	}
	mode, err := bridge.readRegister(0x0b)
	if err != nil {
		return nil, err
	}
	if mode&0x02 != 0 {
		if err := bridge.writeRegister(0x5e, 5); err != nil {
			return nil, err
		}
		bridge.iso = true
		return bridge, nil
	}
	if err := bridge.writeRegister(0x5d, 244); err != nil {
		return nil, err
	}
	return bridge, nil
}

// NewFSUSB2 initialises the bridge on a KTV-FSUSB2, whose EM287x needs a
// different power-up sequence and answers its demodulator and tuner on the
// secondary I2C bus one clock step slower than the US-3POUT does. The card
// reader is left parked.
func NewFSUSB2(device *usbio.Transport) (*Bridge, error) {
	bridge := &Bridge{usb: device, primaryClock: 0x42, secondaryClock: 0x44, fsusb2: true}
	// Confirm the bridge answers at all before anything is driven.
	for _, register := range []uint16{registerChipID, registerChipConfig, 0x0b} {
		if _, err := bridge.readRegister(register); err != nil {
			return nil, fmt.Errorf("probe EM287x register %#02x: %w", register, err)
		}
	}
	steps := [][2]byte{
		{registerGPIOP0, 0xff},
		{registerChipConfig, 0x10}, {0x12, 0x27}, {registerGPIOP0, 0xfe},
		// Park the smart-card interface rather than bringing it up.
		{registerCASMode1, 0x00},
		{0x13, 0x10}, {0x10, 0x00}, {0x11, 0x11},
		{0x28, 0x01}, {0x29, 0xff}, {0x2a, 0x01}, {0x2b, 0xff},
		{0x1c, 0x00}, {0x1d, 0x00}, {0x1e, 0x00}, {0x1f, 0x00}, {0x1b, 0x00},
		{0x5e, 0x80}, {registerTSEnable, 0x00},
	}
	for _, step := range steps {
		if err := bridge.writeRegister(uint16(step[0]), step[1]); err != nil {
			return nil, err
		}
	}
	// The tuner and demodulator only come out of reset once the supply GPIO is
	// driven, which happens around the card-reader reset.
	if err := bridge.writeRegister(registerGPIOP0, 0x3e); err != nil {
		return nil, err
	}
	time.Sleep(100 * time.Millisecond)
	if err := bridge.writeRegister(registerGPIOP0, 0x7e); err != nil {
		return nil, err
	}
	time.Sleep(50 * time.Millisecond)
	if err := bridge.writeRegister(registerI2CMode, bridge.secondaryClock); err != nil {
		return nil, err
	}
	bridge.i2cMode = bridge.secondaryClock
	return bridge, nil
}

func (bridge *Bridge) control(index uint16, request byte, output bool, data []byte) error {
	requestType := byte(0xc0)
	if output {
		requestType = 0x40
	}
	read, err := bridge.usb.Control(requestType, request, 0, index, data, controlTimeoutMS)
	if err != nil {
		return err
	}
	if read != len(data) {
		return fmt.Errorf("%w: %d bytes, want %d", errShortTransfer, read, len(data))
	}
	return nil
}

// errShortTransfer is a control transfer answered with fewer bytes than asked.
var errShortTransfer = errors.New("EM287x short control transfer")

// A register read that comes back empty is asked again.
//
// The bridge occasionally answers one with a zero-length data stage and no
// error -- about one open in 150, on the status register the I2C engine is
// polled through. Nothing came back, so asking again asks the same question.
// Only an empty answer is retried: a short answer to a longer read means part
// of something arrived.
const registerReadAttempts = 3

func (bridge *Bridge) readRegister(register uint16) (byte, error) {
	data := []byte{0}
	var err error
	for attempt := 0; attempt < registerReadAttempts; attempt++ {
		if err = bridge.control(register, 0, false, data); err == nil {
			return data[0], nil
		}
		if !errors.Is(err, errShortTransfer) {
			return 0, err
		}
	}
	return 0, err
}

func (bridge *Bridge) writeRegister(register uint16, value byte) error {
	return bridge.control(register, 0, true, []byte{value})
}

func (bridge *Bridge) writeRegisterMask(register uint16, value, mask byte) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if mask != 0 && mask != 0xff {
		current, err := bridge.readRegister(register)
		if err != nil {
			return err
		}
		value = i2c.Mask(current, value, mask)
	}
	return bridge.writeRegister(register, value)
}

func (bridge *Bridge) initialize() error {
	if err := bridge.writeRegister(0x0d, 0xff); err != nil {
		return err
	}
	if err := bridge.writeRegister(registerGPIOP0, 0xfe); err != nil {
		return err
	}
	if err := bridge.writeRegisterMask(registerGPIOP0, 0, 0x40); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	if err := bridge.writeRegisterMask(registerGPIOP0, 0x40, 0x40); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	registers := [][2]byte{{0x12, 0x27}, {0x0d, 0x42}, {0x15, 0x20}, {0x16, 0x20}, {0x17, 0x20}, {0x18, 0}, {0x19, 0}, {0x1a, 0}, {0x23, 0}, {0x24, 0}, {0x26, 0}, {0x13, 0x08}, {registerChipConfig, 0x10}}
	for _, register := range registers {
		if err := bridge.writeRegister(uint16(register[0]), register[1]); err != nil {
			return err
		}
	}
	if err := bridge.usb.SetAlternate(0, 1); err != nil {
		return err
	}
	return bridge.writeRegister(0x5f, 0x80)
}

// I2C runs a transfer on the bridge's I2C master. Bit 0x1000 of the address
// selects the first bus; the low byte is the slave address.
func (bridge *Bridge) I2C(address uint16, write []byte, readLength int) ([]byte, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	// Nothing else writes the clock register, so the cached value is what the
	// bridge is on and switching costs a transfer only when the bus changes.
	wanted := bridge.secondaryClock
	if address&0x1000 != 0 {
		wanted = bridge.primaryClock
	}
	if bridge.i2cMode != wanted {
		if err := bridge.writeRegister(registerI2CMode, wanted); err != nil {
			return nil, err
		}
		bridge.i2cMode = wanted
	}
	index := address & 0xff
	if len(write) != 0 {
		request := byte(2)
		if readLength != 0 {
			request = 3
		}
		if err := bridge.writeI2C(index, request, write); err != nil {
			return nil, err
		}
	}
	if readLength == 0 {
		return nil, nil
	}
	response := make([]byte, readLength)
	request := byte(3)
	if len(write) != 0 {
		request = 2
	}
	if err := bridge.control(index, request, false, response); err != nil {
		return nil, err
	}
	status, err := bridge.readRegister(registerI2CReturn)
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return nil, fmt.Errorf("EM287x I2C read from %#02x failed (%#02x)", index, status)
	}
	return response, nil
}

// An address that does not acknowledge is asked again.
//
// A busy slave may refuse its address: the demodulator on a US-3POUT declines
// the first word of the tuner's bring-up about one open in 15. A refused
// address is a transaction that did not happen, so trying again is right. An
// aborted USB transfer is the opposite: it may already have reached the device.
const (
	i2cWriteAttempts = 4
	i2cRetryDelay    = 2 * time.Millisecond
)

// writeI2C runs the write half and waits for the engine, asking again if the
// slave refused its address.
func (bridge *Bridge) writeI2C(index uint16, request byte, write []byte) error {
	for attempt := 0; ; attempt++ {
		if err := bridge.control(index, request, true, write); err != nil {
			return err
		}
		status, err := bridge.awaitI2C()
		if err != nil {
			return err
		}
		switch {
		case status == 0:
			return nil
		case status != 0x10:
			return fmt.Errorf("EM287x I2C address %#02x remained busy (%#02x)", index, status)
		case attempt+1 >= i2cWriteAttempts:
			return fmt.Errorf("EM287x I2C address %#02x did not acknowledge, after %d attempts",
				index, i2cWriteAttempts)
		}
		time.Sleep(i2cRetryDelay)
	}
}

// awaitI2C waits for the I2C engine to finish and reports how it went: zero
// for done, 0x10 for an address that was refused, anything else for an engine
// still working.
func (bridge *Bridge) awaitI2C() (byte, error) {
	var status byte
	for attempt := 0; attempt < 5; attempt++ {
		var err error
		if status, err = bridge.readRegister(registerI2CReturn); err != nil {
			return 0, err
		}
		if status == 0 || status == 0x10 {
			return status, nil
		}
		time.Sleep(3 * time.Millisecond)
	}
	return status, nil
}

// I2CBus exposes the I2C master as an ordinary bus with 8-bit addresses, for IC
// drivers that do not know about the bridge.
func (bridge *Bridge) I2CBus() i2c.Bus { return i2cBus{bridge: bridge} }

type i2cBus struct{ bridge *Bridge }

func (bus i2cBus) Write(address byte, data []byte) error {
	_, err := bus.bridge.I2C(uint16(address), data, 0)
	return err
}

func (bus i2cBus) Read(address byte, length int) ([]byte, error) {
	return bus.bridge.I2C(uint16(address), nil, length)
}

// WriteRead keeps a register read in one transaction, so the bridge emits a
// repeated start rather than a stop. Any other way leaves the slave's pointer
// unlatched.
func (bus i2cBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	return bus.bridge.I2C(uint16(address), write, readLength)
}

// WriteRegister writes one register, taking the lock the private helpers leave
// to their callers.
func (bridge *Bridge) WriteRegister(register uint16, value byte) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.writeRegister(register, value)
}

// Stream starts or stops the transport stream endpoint. Starting opens the
// isochronous ring before the device is told to send, so the first packets have
// transfers waiting; stopping closes the ring after, so nothing is left
// outstanding.
func (bridge *Bridge) Stream(enable bool) error {
	if !enable {
		defer bridge.closeStream()
		if bridge.fsusb2 {
			return bridge.WriteRegister(registerTSEnable, 0)
		}
		return bridge.writeRegisterMask(registerTSEnable, 0, 0x0f)
	}
	if bridge.fsusb2 {
		// The alternate setting is what gives the endpoint its bandwidth, so
		// the ring cannot be opened before it is in place.
		if err := bridge.usb.SetAlternate(0, 1); err != nil {
			return err
		}
		bridge.openStream()
		return bridge.WriteRegister(registerTSEnable, tsCaptureEnable|tsNullDiscard)
	}
	// The US-3POUT takes its alternate setting during bring-up and keeps it,
	// so the endpoint is ready by the time anything asks for the stream.
	bridge.openStream()
	value, err := bridge.readRegister(registerTSEnable)
	if err != nil {
		return err
	}
	value = value&0xf0 | (tsCaptureEnable | tsNullDiscard)
	return bridge.control(0x785f, 0x14, true, []byte{value})
}

// Name reports the bus:address of the underlying USB device.
func (bridge *Bridge) Name() string { return bridge.usb.Name() }

// ReadStream reads transport stream packets from endpoint 0x84, using whichever
// transfer type the bridge enumerated with. It returns usbio.ErrTimeout when the
// device had nothing ready.
func (bridge *Bridge) ReadStream(buffer []byte, timeoutMS uint32) (int, error) {
	if bridge.iso {
		bridge.streamMu.Lock()
		stream := bridge.stream
		bridge.streamMu.Unlock()
		if stream != nil {
			return stream.Read(buffer, timeoutMS)
		}
		return bridge.usb.Isochronous(0x84, buffer, isoPacketSize, isoPackets, timeoutMS)
	}
	return bridge.usb.Bulk(0x84, buffer, timeoutMS)
}

// openStream starts the isochronous ring, if this platform has one. A backend
// without one is not an error: ReadStream falls back to a transfer at a time.
func (bridge *Bridge) openStream() {
	bridge.streamMu.Lock()
	defer bridge.streamMu.Unlock()
	if !bridge.iso || bridge.stream != nil {
		return
	}
	// Setting the alternate setting again resets every pipe on the interface,
	// which an isochronous pipe needs before it carries a second stream: a stopped
	// pipe accepts transfers and returns them empty rather than refusing them.
	_ = bridge.usb.SetAlternate(0, 1)
	stream, err := bridge.usb.OpenIsoStream(0x84, isoPacketSize, isoPackets, isoDepth, streamTimeoutMS)
	if err != nil {
		return
	}
	bridge.stream = stream
}

func (bridge *Bridge) closeStream() {
	bridge.streamMu.Lock()
	stream := bridge.stream
	bridge.stream = nil
	bridge.streamMu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
}

// streamTimeoutMS is how long one transfer of the ring waits for the device
// before it comes back empty.
const streamTimeoutMS = 1000

// Close stops the stream, parks the bridge and releases the USB device.
func (bridge *Bridge) Close() error {
	_ = bridge.Stream(false)
	_ = bridge.usb.SetAlternate(0, 0)
	// A board whose reader was woken has it switched off again.
	bridge.parkCard()
	if bridge.fsusb2 {
		// Both teardown stages are needed: the tuner half parks its front end on the
		// GPIO port, then the bridge half releases the port, stops the smart-card
		// block and clears the chip configuration. The brief low pulse on bit 0
		// between the two writes belongs to the sequence.
		_ = bridge.WriteRegister(registerGPIOP0, 0xfe)
		_ = bridge.WriteRegister(registerGPIOP0, 0xff)
		_ = bridge.WriteRegister(registerCASMode1, 0)
		_ = bridge.WriteRegister(registerChipConfig, 0)
		return bridge.usb.Close()
	}
	_ = bridge.writeRegisterMask(registerTSEnable, 0, 0x0f)
	_ = bridge.writeRegister(registerGPIOP0, 0xff)
	_ = bridge.writeRegister(registerChipConfig, 0)
	return bridge.usb.Close()
}
