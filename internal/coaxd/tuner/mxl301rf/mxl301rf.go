// Copyright (c) 2014 Akihiro Tsukada; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from the Linux kernel (drivers/media/tuners/mxl301rf.c)
// by Akihiro Tsukada.

// Package mxl301rf drives the MaxLinear MxL301RF terrestrial tuner over any
// i2c.Bus, which on the boards that carry it is the repeater behind a TC90522.
//
// The chip's initialisation sequence is not published and is not here: on the
// PT3 it lives in the card's own ROM and is run by the card. This driver assumes
// that has happened and only wakes, tunes, measures and sleeps.
package mxl301rf

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// Tuner is one MxL301RF reached over an I2C bus.
type Tuner struct {
	bus     i2c.Bus
	address byte
}

// New addresses one chip. address is the 8-bit (already shifted) I2C address.
func New(bus i2c.Bus, address byte) *Tuner { return &Tuner{bus: bus, address: address} }

// What the chip will tune, in Hz: the band's first and last channel centres,
// on the odd 1/7 MHz offset ISDB-T carriers use. See bandEdgeSlack for why they
// are not compared exactly.
const (
	FrequencyMin = 93000000
	FrequencyMax = 803142857
)

// bandEdgeSlack is how far outside the band a request may sit and still count as
// the edge channel: 803142857 Hz folds to 803143 kHz and comes back 143 Hz above
// the limit, and band membership should not turn on that rounding. The slack is
// half the chip's 1/64 MHz tuning step, so nothing landing on a different code
// gets through.
const bandEdgeSlack = 1000000 / 64 / 2

func (tuner *Tuner) write(data ...byte) error { return tuner.bus.Write(tuner.address, data) }

func (tuner *Tuner) writeRegister(register, value byte) error {
	return tuner.write(register, value)
}

// readRegister has no repeated-start form on this chip: writing 0xfb and the
// register selects it, and the next read returns it. Two transfers, with nothing
// else on the bus in between.
func (tuner *Tuner) readRegister(register byte) (byte, error) {
	if err := tuner.write(0xfb, register); err != nil {
		return 0, err
	}
	data, err := tuner.bus.Read(tuner.address, 1)
	if err != nil {
		return 0, err
	}
	if len(data) != 1 {
		return 0, fmt.Errorf("MxL301RF short read: %d bytes", len(data))
	}
	return data[0], nil
}

// Wake brings the chip out of standby. Nothing else here works before it, and
// the board's own initialisation has to have run first.
func (tuner *Tuner) Wake() error { return tuner.writeRegister(0x01, 0x01) }

// Sleep puts the chip in standby, stopping any tune in progress.
func (tuner *Tuner) Sleep() error { return tuner.write(0x01, 0x00, 0x13, 0x00) }

// Where the chip's own spurs land, and how to move them. The centres are the
// analogue channel frequencies the chip was designed around; a digital channel
// near one is spoiled by the same spur. threshold is how far either side of the
// centre the shift applies.
var spurShifts = [...]struct {
	centre    uint32 // kHz
	threshold uint32 // kHz
	value     byte
	direction byte
}{
	{64500, 500, 0x92, 0x07},
	{191500, 300, 0xe2, 0x07},
	{205500, 500, 0x2c, 0x04},
	{212500, 500, 0x1e, 0x04},
	{226500, 500, 0xd4, 0x07},
	{99143, 500, 0x9c, 0x07},
	{173143, 500, 0xd4, 0x07},
	{191143, 300, 0xd4, 0x07},
	{207143, 500, 0xce, 0x07},
	{225143, 500, 0xce, 0x07},
	{243143, 500, 0xd4, 0x07},
	{261143, 500, 0xd4, 0x07},
	{291143, 500, 0xd4, 0x07},
	{339143, 500, 0x2c, 0x04},
	{117143, 500, 0x7a, 0x07},
	{135143, 300, 0x7a, 0x07},
	{153143, 500, 0x01, 0x07},
}

// setIDAC is written after every tune. What it does is not published, so it is
// kept as an opaque sequence.
var setIDAC = []byte{
	0x0d, 0x00,
	0x0c, 0x67,
	0x6f, 0x89,
	0x70, 0x0c,
	0x6f, 0x8a,
	0x70, 0x0e,
	0x6f, 0x8b,
	0x70, 0x1c,
}

// Tune sets the chip to a frequency in Hz.
func (tuner *Tuner) Tune(frequency uint32) error {
	if int64(frequency) < FrequencyMin-bandEdgeSlack || int64(frequency) > FrequencyMax+bandEdgeSlack {
		return fmt.Errorf("MxL301RF cannot tune %d Hz, which is outside %d to %d",
			frequency, FrequencyMin, FrequencyMax)
	}

	begin := []byte{
		0x13, 0x00, // abort whatever was being tuned
		0x3b, 0xc0,
		0x3b, 0x80,
		0x10, 0x95, // bandwidth
		0x1a, 0x05,
		0x61, 0x00, // spur shift, replaced below when one applies
		0x62, 0xa0,
	}
	for _, spur := range spurShifts {
		if frequency >= (spur.centre-spur.threshold)*1000 &&
			frequency <= (spur.centre+spur.threshold)*1000 {
			begin[11] = spur.value
			begin[13] = 0xa0 | spur.direction
			break
		}
	}
	if err := tuner.write(begin...); err != nil {
		return err
	}
	time.Sleep(3 * time.Millisecond)

	code := frequencyCode(frequency)
	if err := tuner.write(0x11, byte(code), 0x12, byte(code>>8), 0x13, 0x01); err != nil {
		return err
	}
	// The chip's own tuning time; there is nothing to poll, and the demodulator
	// behind it is what says whether the result was any good.
	time.Sleep(31 * time.Millisecond)

	if err := tuner.writeRegister(0x1a, 0x0d); err != nil {
		return err
	}
	return tuner.write(setIDAC...)
}

// frequencyCode converts Hz to the megahertz in 10.6 fixed point the chip
// wants, rounding the last bit.
func frequencyCode(frequency uint32) uint16 {
	code := uint16(frequency / 1000000)
	remainder := frequency % 1000000
	for step := uint32(500000); step >= 15625; step /= 2 {
		code <<= 1
		if remainder > step {
			remainder -= step
			code |= 1
		}
	}
	// Half of the last step, so the nearest code wins rather than the one below.
	if remainder > 7812 {
		code++
	}
	return code
}

// PLLsLocked reports whether both loops have settled -- the reference and the RF
// synthesiser, in one register. It is the only thing on this side of the card
// that says the tuner is where it was asked.
func (tuner *Tuner) PLLsLocked() (bool, error) {
	value, err := tuner.readRegister(0x16)
	if err != nil {
		return false, err
	}
	return value&0x0c == 0x0c && value&0x03 == 0x03, nil
}

// WaitForPLLs waits for both loops to settle, reporting false rather than an
// error on timeout.
func (tuner *Tuner) WaitForPLLs(budget time.Duration) (bool, error) {
	deadline := time.Now().Add(budget)
	for {
		locked, err := tuner.PLLsLocked()
		if err != nil || locked {
			return locked, err
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(time.Millisecond)
	}
}

// SignalStrength reads the chip's own measure of what is arriving, in
// thousandths of a dBm: a gain reading and an offset to subtract from it, both
// in eighths of a dB, against a reference 113 dB below a milliwatt.
func (tuner *Tuner) SignalStrength() (int32, error) {
	raw, offset, err := tuner.rawStrength()
	if err != nil {
		return 0, err
	}
	level := int32(raw) - int32(offset) - 113<<3
	return level * 1000 / 8, nil
}

func (tuner *Tuner) rawStrength() (raw, offset uint16, err error) {
	if err := tuner.writeRegister(0x14, 0x01); err != nil {
		return 0, 0, err
	}
	time.Sleep(time.Millisecond)

	var values [4]byte
	for index, register := range [4]byte{0x18, 0x19, 0xd6, 0xd7} {
		value, err := tuner.readRegister(register)
		if err != nil {
			return 0, 0, err
		}
		values[index] = value
	}
	raw = uint16(values[1]&0x07)<<8 | uint16(values[0])
	offset = uint16(values[3]&0x0f)<<5 | uint16(values[2]>>3)
	return raw, offset, nil
}
