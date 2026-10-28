// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (tc90522.c) by nns779.

// Package tc90522 drives the Toshiba TC90522 ISDB-T/ISDB-S demodulator.
//
// The chip carries two independent demodulator cores, each on its own I2C
// address, so one Demod is one core. Each repeats I2C to the tuner behind it,
// which TunerBus exposes as an ordinary bus.
package tc90522

import (
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// InitTerrestrial is the register sequence a terrestrial core needs at power-up
// on the boards this chip appears on; boards needing extra pairs append to a
// copy.
var InitTerrestrial = []byte{
	0xb0, 0xa0, 0xb2, 0x3d, 0xb3, 0x25, 0xb4, 0x8b,
	0xb5, 0x4b, 0xb6, 0x3f, 0xb7, 0xff, 0xb8, 0xc0,
}

// Demod is one demodulator core of a TC90522.
type Demod struct {
	bus     i2c.Bus
	address byte
	// secondary marks the second core of a dual-tuner board, which needs a
	// different AGC reference.
	secondary bool
	mu        sync.Mutex
}

// New addresses one core. address is the 8-bit I2C address; secondary marks the
// second core of a board that fits two.
func New(bus i2c.Bus, address byte, secondary bool) *Demod {
	return &Demod{bus: bus, address: address, secondary: secondary}
}

// ReadRegisters reads a run of registers.
func (demod *Demod) ReadRegisters(register byte, length int) ([]byte, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.readRegisters(register, length)
}

func (demod *Demod) readRegisters(register byte, length int) ([]byte, error) {
	if length <= 0 {
		return nil, fmt.Errorf("TC90522 register read length %d is invalid", length)
	}
	data, err := i2c.WriteRead(demod.bus, demod.address, []byte{register}, length)
	if err != nil {
		return nil, err
	}
	if len(data) != length {
		return nil, fmt.Errorf("TC90522 short read at %#02x: %d of %d", register, len(data), length)
	}
	return data, nil
}

// ReadRegister reads one register.
func (demod *Demod) ReadRegister(register byte) (byte, error) {
	data, err := demod.ReadRegisters(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// WriteRegisters writes a run of registers.
func (demod *Demod) WriteRegisters(register byte, data []byte) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.writeRegisters(register, data)
}

func (demod *Demod) writeRegisters(register byte, data []byte) error {
	if len(data) == 0 || len(data) > 254 {
		return fmt.Errorf("TC90522 register write length %d is invalid", len(data))
	}
	return demod.bus.Write(demod.address, append([]byte{register}, data...))
}

// WriteRegister writes one register.
func (demod *Demod) WriteRegister(register, value byte) error {
	return demod.WriteRegisters(register, []byte{value})
}

// WritePairs writes a flat list of register and value pairs.
func (demod *Demod) WritePairs(pairs []byte) error {
	if len(pairs)%2 != 0 {
		return fmt.Errorf("TC90522 register pair list has an odd length %d", len(pairs))
	}
	demod.mu.Lock()
	defer demod.mu.Unlock()
	for offset := 0; offset < len(pairs); offset += 2 {
		if err := demod.writeRegisters(pairs[offset], pairs[offset+1:offset+2]); err != nil {
			return err
		}
	}
	return nil
}

// WriteRegisterMask writes the masked bits of a register.
func (demod *Demod) WriteRegisterMask(register, value, mask byte) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if mask != 0 && mask != 0xff {
		current, err := demod.readRegisters(register, 1)
		if err != nil {
			return err
		}
		value = i2c.Mask(current[0], value, mask)
	}
	return demod.writeRegisters(register, []byte{value})
}

// TunerBus exposes the core's I2C repeater as an ordinary bus, so a tuner
// driver need not know it sits behind a demodulator.
func (demod *Demod) TunerBus() i2c.Bus { return tunerBus{demod: demod} }

type tunerBus struct{ demod *Demod }

// repeaterRegister is the register that forwards a transfer to the tuner.
const repeaterRegister = 0xfe

func (bus tunerBus) Write(address byte, data []byte) error {
	bus.demod.mu.Lock()
	defer bus.demod.mu.Unlock()
	command := append([]byte{repeaterRegister, address &^ 0x01}, data...)
	return bus.demod.bus.Write(bus.demod.address, command)
}

func (bus tunerBus) Read(address byte, length int) ([]byte, error) {
	bus.demod.mu.Lock()
	defer bus.demod.mu.Unlock()
	data, err := i2c.WriteRead(bus.demod.bus, bus.demod.address, []byte{repeaterRegister, address | 0x01}, length)
	if err != nil {
		return nil, err
	}
	if len(data) != length {
		return nil, fmt.Errorf("TC90522 short tuner read: %d of %d", len(data), length)
	}
	return data, nil
}

// ---- ISDB-S core ----

// SleepS powers the satellite core down or brings it back.
func (demod *Demod) SleepS(sleep bool) error {
	pairs := []byte{0x13, 0x00, 0x17, 0x00}
	if sleep {
		pairs = []byte{0x13, 0x80, 0x17, 0xff}
	}
	return demod.WritePairs(pairs)
}

// SetAGCS enables or disables the satellite core's automatic gain control.
func (demod *Demod) SetAGCS(on bool) error {
	reference := byte(0xb0)
	if demod.secondary {
		reference = 0x30
	}
	pairs := []byte{0x0a, 0x00, 0x10, reference, 0x11, 0x02, 0x03, 0x01}
	if on {
		pairs[1] = 0xff
		pairs[3] = reference | 0x02
		pairs[5] = 0x00
	}
	return demod.WritePairs(pairs)
}

// TMCCTSIDS reads one of the twelve TSIDs the TMCC carries.
func (demod *Demod) TMCCTSIDS(index int) (uint16, error) {
	if index < 0 || index >= 12 {
		return 0, fmt.Errorf("TC90522 TMCC slot %d is outside 0..11", index)
	}
	data, err := demod.ReadRegisters(byte(0xce+index*2), 2)
	if err != nil {
		return 0, err
	}
	return uint16(data[0])<<8 | uint16(data[1]), nil
}

// TSIDS reports the TSID the satellite core is currently demultiplexing.
func (demod *Demod) TSIDS() (uint16, error) {
	data, err := demod.ReadRegisters(0xe6, 2)
	if err != nil {
		return 0, err
	}
	return uint16(data[0])<<8 | uint16(data[1]), nil
}

// SetTSIDS selects the TSID to demultiplex.
func (demod *Demod) SetTSIDS(tsid uint16) error {
	return demod.WriteRegisters(0x8f, []byte{byte(tsid >> 8), byte(tsid)})
}

// SelectStreamS tells the satellite core which stream to demultiplex, waiting
// until it has settled. Values below 12 name a TMCC slot rather than a TSID and
// are resolved once the TMCC block has been received.
func (demod *Demod) SelectStreamS(streamID uint16) error {
	tsid := streamID
	if streamID < 12 {
		var err error
		for attempt := 0; attempt < 100; attempt++ {
			tsid, err = demod.TMCCTSIDS(int(streamID))
			if err == nil && tsid != 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("read TMCC slot %d: %w", streamID, err)
		}
		if tsid == 0 {
			return fmt.Errorf("TMCC slot %d carries no stream", streamID)
		}
	}
	if err := demod.SetTSIDS(tsid); err != nil {
		return err
	}
	for attempt := 0; attempt < 100; attempt++ {
		current, err := demod.TSIDS()
		if err != nil {
			return err
		}
		if current == tsid {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the demodulator did not settle on stream %#04x", tsid)
}

// CNS reports the satellite core's raw carrier-to-noise measurement.
func (demod *Demod) CNS() (uint16, error) {
	data, err := demod.ReadRegisters(0xbc, 2)
	if err != nil {
		return 0, err
	}
	return uint16(data[0])<<8 | uint16(data[1]), nil
}

// EnableTSPinsS starts or stops the satellite core's TS output.
func (demod *Demod) EnableTSPinsS(enable bool) error {
	pairs := []byte{0x1c, 0x00, 0x1f, 0x00}
	if !enable {
		pairs[1] = 0x80
		pairs[3] = 0x22
	}
	return demod.WritePairs(pairs)
}

// LockedS reports whether the satellite core reached lock.
func (demod *Demod) LockedS() (bool, error) {
	value, err := demod.ReadRegister(0xc3)
	if err != nil {
		return false, err
	}
	return value&0x10 == 0, nil
}

// ---- ISDB-T core ----

// SleepT powers the terrestrial core down or brings it back.
func (demod *Demod) SleepT(sleep bool) error {
	value := byte(0x00)
	if sleep {
		value = 0xf0
	}
	return demod.WriteRegister(0x03, value)
}

// SetAGCT enables or disables the terrestrial core's automatic gain control.
func (demod *Demod) SetAGCT(on bool) error {
	pairs := []byte{0x25, 0x00, 0x20, 0x00, 0x23, 0x4d, 0x01, 0x50}
	if on {
		pairs[5] &^= 0x01
	}
	return demod.WritePairs(pairs)
}

// CNDatT reports the terrestrial core's raw carrier-to-noise measurement.
func (demod *Demod) CNDatT() (uint32, error) {
	data, err := demod.ReadRegisters(0x8b, 3)
	if err != nil {
		return 0, err
	}
	return uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2]), nil
}

// EnableTSPinsT starts or stops the terrestrial core's TS output.
func (demod *Demod) EnableTSPinsT(enable bool) error {
	value := byte(0xa8)
	if enable {
		value = 0x00
	}
	return demod.WriteRegister(0x1d, value)
}

// LockedT reports whether the terrestrial core reached lock. The lock flag,
// the synchronisation flag and the Viterbi lock count all have to agree: the
// last two come up first, while the packets the core puts out still carry
// transport errors.
func (demod *Demod) LockedT() (bool, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	sync, err := demod.readRegisters(0x80, 1)
	if err != nil {
		return false, err
	}
	if sync[0]&0x2a != 0 {
		return false, nil
	}
	viterbi, err := demod.readRegisters(0xb0, 1)
	if err != nil {
		return false, err
	}
	return viterbi[0]&0x0f >= 8, nil
}
