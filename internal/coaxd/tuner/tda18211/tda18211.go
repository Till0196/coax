// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// Package tda18211 drives the NXP TDA18211HD terrestrial tuner over any i2c.Bus,
// typically the gated bus behind an MB86A20S. The chip is programmed through a
// 39-byte register image: the driver keeps that image, edits the fields a step
// needs and writes back only the affected span.
package tda18211

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// address is the tuner's I2C slave address on the gated bus, already shifted.
const address byte = 0xc0

// Register names as NXP spells them.
const (
	regThermo      = 0x01
	regEasyProg1   = 0x03 // bandpass filter
	regEasyProg2   = 0x04 // RF band and gain taper
	regEasyProg3   = 0x05 // power state and standard
	regEasyProg4   = 0x06 // IF level and calibration mode
	regEasyProg5   = 0x07 // image-rejection band
	regCalPostDiv  = 0x08
	regCalDiv1     = 0x09
	regCalDiv2     = 0x0a
	regCalDiv3     = 0x0b
	regMainPostDiv = 0x0c
	regMainDiv1    = 0x0d
	regMainDiv2    = 0x0e
	regMainDiv3    = 0x0f
	regExtended1   = 0x10
	regExtended4   = 0x13
	regExtended7   = 0x16
	regExtended12  = 0x1b
	regExtended13  = 0x1c // K and M bits
	regExtended14  = 0x1d
	regExtended17  = 0x20 // AGC1 gain
	regExtended20  = 0x23 // AGC2 gain
	regExtended22  = 0x25
)

const registerCount = 39

// tableEntry maps an upper frequency bound in kHz to the value at or below it.
type tableEntry struct {
	upperKHz uint32
	value    uint32
}

// lookup falls back to the last entry above every bound.
func lookup(table []tableEntry, frequencyKHz uint32) uint32 {
	for _, entry := range table {
		if frequencyKHz <= entry.upperKHz {
			return entry.value
		}
	}
	return table[len(table)-1].value
}

// Tuner is a TDA18211HD reached over an I2C bus.
type Tuner struct {
	bus       i2c.Bus
	registers [registerCount]byte
}

// New addresses a TDA18211HD on the given bus.
func New(bus i2c.Bus) *Tuner { return &Tuner{bus: bus} }

func (tuner *Tuner) writeRegisters(offset, length int) error {
	if length <= 0 || offset < 0 || offset+length > registerCount {
		return fmt.Errorf("TDA18211 register span %d..%d is out of range", offset, offset+length)
	}
	data := make([]byte, 1+length)
	data[0] = byte(offset)
	copy(data[1:], tuner.registers[offset:offset+length])
	return tuner.bus.Write(address, data)
}

// Initialize is NXP's tmInitTuner: the power-up script, then the register
// touches that follow it.
func (tuner *Tuner) Initialize() error {
	if err := tuner.powerUp(); err != nil {
		return err
	}
	tuner.registers[regEasyProg3] = 0x19 // power state normal, STD = 19
	tuner.registers[regEasyProg4] = 0x60 // IF level 0, calibration mode 0
	tuner.registers[regMainPostDiv] |= 0x80
	tuner.registers[regExtended22] = 0x37
	if err := tuner.writeRegisters(regEasyProg3, 2); err != nil {
		return err
	}
	if err := tuner.writeRegisters(regMainPostDiv, 1); err != nil {
		return err
	}
	if err := tuner.writeRegisters(regExtended22, 1); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return nil
}

// powerUp loads the cold-start register image, walks the two AGC ramps and
// runs the image-rejection calibration once per band. The delays are the part's
// own settling times: it is measuring, and a write that arrives early is taken
// against the previous state.
func (tuner *Tuner) powerUp() error {
	if err := tuner.load(0x01, powerUpImage); err != nil {
		return err
	}
	for _, level := range agc1Ramp {
		if err := tuner.load(regExtended17, []byte{level}); err != nil {
			return err
		}
	}
	for _, level := range agc2Ramp {
		if err := tuner.load(regExtended20, []byte{level}); err != nil {
			return err
		}
	}
	for index, band := range irBands {
		if err := tuner.load(regEasyProg3, band.measure[:]); err != nil {
			return err
		}
		// The low band alone is walked into the measurement rather than
		// dropped into it.
		if index == 0 {
			if err := tuner.load(regExtended4, []byte{0x61}); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
			if err := tuner.load(regExtended4, []byte{0x41}); err != nil {
				return err
			}
		}
		time.Sleep(5 * time.Millisecond)
		if err := tuner.load(regEasyProg1, []byte{0xc6}); err != nil { // launch the detector
			return err
		}
		time.Sleep(5 * time.Millisecond)
		if err := tuner.load(regEasyProg3, band.calPLL[:]); err != nil {
			return err
		}
		time.Sleep(5 * time.Millisecond)
		if err := tuner.load(regEasyProg2, []byte{0xdf}); err != nil { // launch the optimisation
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err := tuner.load(regEasyProg4, []byte{0x64}); err != nil { // back to normal mode
		return err
	}
	return tuner.load(regEasyProg1, []byte{0xc6}) // and resynchronise
}

// load puts values into the register image starting at offset and sends that
// span, so the shadow copy and the part stay in step.
func (tuner *Tuner) load(offset int, values []byte) error {
	if offset < 0 || offset+len(values) > registerCount {
		return fmt.Errorf("TDA18211 register span %d..%d is out of range", offset, offset+len(values))
	}
	copy(tuner.registers[offset:], values)
	return tuner.writeRegisters(offset, len(values))
}

// Tune moves the tuner to a terrestrial frequency given in kHz.
func (tuner *Tuner) Tune(frequencyKHz uint32) error {
	tuner.registers[regEasyProg3] = 0x19 // power state normal, STD = 19
	tuner.registers[regEasyProg4] = 0x64 // IF level 1, calibration mode 0
	tuner.registers[regMainPostDiv] |= 0x80
	tuner.registers[regExtended22] = 0x37
	for _, span := range [][2]int{{regEasyProg3, 2}, {regMainPostDiv, 1}, {regExtended22, 1}} {
		if err := tuner.writeRegisters(span[0], span[1]); err != nil {
			return err
		}
	}
	tuner.registers[regExtended12] &^= 0x30
	if err := tuner.writeRegisters(regExtended12, 1); err != nil {
		return err
	}

	tuner.registers[regEasyProg1] = tuner.registers[regEasyProg1]&0xc0 | byte(lookup(bandpassTable, frequencyKHz))
	tuner.registers[regEasyProg2] = byte(lookup(rfBandTable, frequencyKHz)<<5) | byte(lookup(gainTaperTable, frequencyKHz))
	if err := tuner.writeRegisters(regEasyProg1, 1); err != nil {
		return err
	}
	tuner.registers[regExtended13] = byte(lookup(kmTable, frequencyKHz)) | tuner.registers[regExtended13]&0x83
	if err := tuner.writeRegisters(regExtended13, 1); err != nil {
		return err
	}

	// Force the local-oscillator and calibration sources while the synthesiser is
	// programmed, then hand control back once it has settled.
	tuner.registers[regExtended4] = tuner.registers[regExtended4]&0x07 | 0x60
	tuner.registers[regExtended7] |= 0x20
	tuner.registers[regExtended14] = 0
	tuner.registers[regExtended20] &^= 0x20
	for _, span := range [][2]int{{regExtended4, 1}, {regExtended7, 1}, {regExtended14, 1}, {regExtended20, 1}} {
		if err := tuner.writeRegisters(span[0], span[1]); err != nil {
			return err
		}
	}
	tuner.registers[regEasyProg4] |= 0x03 // calibration mode 3
	tuner.setCalibrationPLL(frequencyKHz + 3000)
	tuner.setMainPLL(frequencyKHz + 4000)
	if err := tuner.writeRegisters(regEasyProg3, 11); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	// Writing the band registers back twice latches the new synthesiser setting.
	for repeat := 0; repeat < 2; repeat++ {
		if err := tuner.writeRegisters(regEasyProg2, 1); err != nil {
			return err
		}
		if err := tuner.writeRegisters(regEasyProg1, 1); err != nil {
			return err
		}
	}

	tuner.registers[regExtended4] &^= 0x20
	tuner.registers[regExtended7] &^= 0x20
	if err := tuner.writeRegisters(regExtended4, 1); err != nil {
		return err
	}
	if err := tuner.writeRegisters(regExtended7, 1); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)

	tuner.registers[regExtended20] |= 0x20 // hold the lock
	if err := tuner.writeRegisters(regExtended20, 1); err != nil {
		return err
	}
	time.Sleep(60 * time.Millisecond)

	tuner.registers[regEasyProg4] &^= 0x03 // calibration mode 0
	if err := tuner.writeRegisters(regEasyProg4, 1); err != nil {
		return err
	}
	if err := tuner.writeRegisters(regEasyProg1, 1); err != nil {
		return err
	}

	tuner.registers[regEasyProg5] = tuner.registers[regEasyProg5]&^0x07 | byte(lookup(irMeasureTable, frequencyKHz))
	tuner.registers[regExtended1] &^= 0x03
	if err := tuner.writeRegisters(regExtended1, 1); err != nil {
		return err
	}

	// Reprogram the main synthesiser now the front end is configured, and pulse
	// the forced source so it reacquires against the final settings.
	tuner.setMainPLL(frequencyKHz + 4000)
	if err := tuner.writeRegisters(regThermo, 15); err != nil {
		return err
	}
	tuner.registers[regExtended4] |= 0x20
	if err := tuner.writeRegisters(regExtended4, 1); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	tuner.registers[regExtended4] &^= 0x20
	if err := tuner.writeRegisters(regExtended4, 1); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)

	tuner.registers[regEasyProg3] |= 0x04 // switch the RF AGC to its normal speed
	if err := tuner.writeRegisters(regEasyProg3, 1); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return nil
}

// Sleep leaves only the crystal running, which is how the tuner parks.
func (tuner *Tuner) Sleep() error {
	tuner.registers[regEasyProg3] = tuner.registers[regEasyProg3]&^0xe0 | 0xc0
	return tuner.writeRegisters(regEasyProg3, 1)
}

// setCalibrationPLL programs the calibration synthesiser for a frequency in
// kHz. The divider bytes follow NXP's arithmetic, div = (d * kHz << 7) / 125.
func (tuner *Tuner) setCalibrationPLL(frequencyKHz uint32) {
	entry := lookup(calPLLTable, frequencyKHz)
	tuner.registers[regCalPostDiv] = byte(entry >> 8)
	value := (uint64(frequencyKHz) * uint64(entry&0xff) << 7) / 125
	tuner.registers[regCalDiv1] = byte(value>>16) & 0x7f
	tuner.registers[regCalDiv2] = byte(value >> 8)
	tuner.registers[regCalDiv3] = byte(value)
}

// setMainPLL is the same for the main synthesiser, except that the post divider
// keeps the register's top bit: the IF notch, set by the caller not the table.
func (tuner *Tuner) setMainPLL(frequencyKHz uint32) {
	entry := lookup(mainPLLTable, frequencyKHz)
	divider := uint64(entry & 0xff)
	tuner.registers[regMainPostDiv] = tuner.registers[regMainPostDiv]&^0x7f | byte(entry>>8)&0x7f

	var value uint64
	if frequencyKHz%1000 == 143 {
		// Japanese terrestrial channels sit on a 1/7 MHz offset; splitting the
		// multiply keeps that third of a kHz from being rounded away.
		value = uint64(frequencyKHz-143) * divider << 7
		value += divider * 128000 / 7
	} else {
		value = uint64(frequencyKHz) * divider << 7
	}
	value /= 125

	tuner.registers[regMainDiv1] = byte(value>>16) & 0x7f
	tuner.registers[regMainDiv2] = byte(value >> 8)
	tuner.registers[regMainDiv3] = byte(value)
}
