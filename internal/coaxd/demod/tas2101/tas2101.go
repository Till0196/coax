// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TBS linux_media (tas2101.c), Luis Alves's driver for the Tmax
// TAS2101 -- the satellite demodulator on TBS's 6902, 6904 and 6910 cards.
//
// It needs no firmware, so it can be picked up from a driver already running it
// without a reset. Its tuner is a separate chip behind an I2C gate the
// demodulator holds. The card's SPI flash and EEPROM accessors are not ported.
package tas2101

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// Variant selects which part this is; the two differ only in their tables.
type Variant int

const (
	// TAS2101 is the part every card in this tree carries.
	TAS2101 Variant = iota
	TAS2100
)

// Config describes how the board wired the chip.
type Config struct {
	// DemodAddress is the chip's I2C address, 0x60 or 0x68 on the cards here.
	DemodAddress byte
	Variant      Variant
	// Init is the board's own seven bytes: six to the block at 0xe6, the seventh
	// to 0xe0. They settle the transport stream pin order and the tuner's
	// supply, so they differ per card and per tuner, not per chip.
	Init [7]byte
	// Init2 asks for an extra pair of registers some boards want. No card in
	// this tree does.
	Init2 bool
}

// Tuner is the chip behind the demodulator's I2C gate. Tune takes hertz and
// symbols per second, as the AV201x driver does.
type Tuner interface {
	Tune(frequencyHz, symbolRateSPS uint32) error
}

// Register addresses, from enum tas2101_reg_addr.
const (
	registerID0        = 0x00
	registerID1        = 0x01
	register04         = 0x04
	register06         = 0x06
	registerLNBControl = 0x10
	registerLNBStatus  = 0x16
	registerDiSEqC     = 0x20
	register30         = 0x30
	registerStatus     = 0x31
	register34         = 0x34
	registerStrength0  = 0x42
	registerSetSRate0  = 0x73
	registerFreqOffset = 0x75
	registerSNR0       = 0x92
)

const (
	// i2cGate is register 0x06's bit for the pass-through to the tuner.
	i2cGate = 0x80

	// voltageSelect is register 0x10's bit for the higher supply voltage.
	voltageSelect = 0x40

	// Register 0x10's low three bits say what the chip is doing with the aerial
	// lead; the three above carry a DiSEqC message's length less one.
	diseqcCommandMask = 0x07
	diseqcLengthMask  = 0x38

	toneOff        = 0x00
	toneOn         = 0x01
	diseqcBurstA   = 0x02
	diseqcBurstB   = 0x03
	diseqcSendMsg  = 0x04
	diseqcBusy     = 0x10
	statusMask     = 0x75
	statusLocked   = 0x75
	identity0      = 0x44
	identity1      = 0x4c
	tunerStartTune = 0x01
)

type regtable struct {
	register byte
	set      byte
	clear    byte
	sleep    time.Duration
}

// Demod is a TAS2101 reached over an I2C bus.
type Demod struct {
	bus    i2c.Bus
	config Config
	tuner  Tuner

	warm   bool
	system string
}

// New addresses a TAS2101 on the given bus. The tuner is attached separately
// because it is reached through this chip's gate.
func New(bus i2c.Bus, config Config) *Demod {
	return &Demod{bus: bus, config: config}
}

// AttachTuner names the chip behind the demodulator's I2C gate. With none
// attached, Tune goes as far as the demodulator alone can.
func (demod *Demod) AttachTuner(tuner Tuner) { demod.tuner = tuner }

func (demod *Demod) regReadMulti(register byte, length int) ([]byte, error) {
	data, err := i2c.WriteRead(demod.bus, demod.config.DemodAddress, []byte{register}, length)
	if err != nil {
		return nil, fmt.Errorf("TAS2101 read %#02x: %w", register, err)
	}
	return data, nil
}

func (demod *Demod) regRead(register byte) (byte, error) {
	data, err := demod.regReadMulti(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// regWriteMulti writes a run of registers starting at data[0] (tas2101_wrm).
func (demod *Demod) regWriteMulti(data []byte) error {
	if err := demod.bus.Write(demod.config.DemodAddress, data); err != nil {
		return fmt.Errorf("TAS2101 write %#02x: %w", data[0], err)
	}
	return nil
}

func (demod *Demod) regWrite(register, value byte) error {
	return demod.regWriteMulti([]byte{register, value})
}

// regMask is tas2101_regmask. A clear mask of 0xff replaces every bit and skips
// the read-back -- some of this chip's registers do not read back what was
// written.
func (demod *Demod) regMask(register, set, clear byte) error {
	var current byte
	if clear != 0xff {
		value, err := demod.regRead(register)
		if err != nil {
			return err
		}
		current = value &^ clear
	}
	return demod.regWrite(register, current|set)
}

func (demod *Demod) writeTable(table []regtable) error {
	for _, line := range table {
		if err := demod.regMask(line.register, line.set, line.clear); err != nil {
			return err
		}
		if line.sleep > 0 {
			time.Sleep(line.sleep)
		}
	}
	return nil
}

// OpenTunerGate satisfies i2c.Gate: it is tas2101_i2c_gate_ctrl.
func (demod *Demod) OpenTunerGate(open bool) error {
	if open {
		return demod.regMask(register06, i2cGate, 0)
	}
	return demod.regMask(register06, 0, i2cGate)
}

// Initialize identifies the chip and runs tas2101_initfe. No reset is issued
// even where the board offers a reset line: the cards here are picked up from a
// driver that already brought the chip up, and the table below settles every
// register it depends on.
func (demod *Demod) Initialize() error {
	identity, err := demod.regReadMulti(registerID0, 2)
	if err != nil {
		return err
	}
	if identity[0] != identity0 || identity[1] != identity1 {
		return fmt.Errorf("TAS2101 at %#02x answered %#02x%#02x, not a TAS2101",
			demod.config.DemodAddress, identity[0], identity[1])
	}

	first, second := initfe0, initfe1
	if demod.config.Variant == TAS2100 {
		first, second = tas2100initfe0, tas2100initfe1
	}
	if err := demod.writeTable(first); err != nil {
		return err
	}
	// Six as a run at 0xe6, the seventh over the whole of 0xe0.
	block := make([]byte, 0, 7)
	block = append(block, 0xe6)
	block = append(block, demod.config.Init[:6]...)
	if err := demod.regWriteMulti(block); err != nil {
		return err
	}
	if err := demod.regMask(0xe0, demod.config.Init[6], 0xff); err != nil {
		return err
	}
	if err := demod.writeTable(second); err != nil {
		return err
	}
	if demod.config.Init2 {
		if err := demod.writeTable(initfe2); err != nil {
			return err
		}
	}
	demod.warm = true
	return nil
}

// ErrNotInitialized is answered before Initialize has run.
var ErrNotInitialized = errors.New("TAS2101 is not initialized")
