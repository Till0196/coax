// Copyright (c) 2014 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TurboSight's av201x driver by Luis Alves, which is out of tree.

// Package av201x drives the Airoha Technology AV201x satellite tuner over any
// i2c.Bus. It is a single-die tuner: no firmware, no separate demodulator gate
// to open, just a fixed register table and a PLL calculation.
package av201x

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// Model selects which register table follows the fixed init sequence. The AV2011
// and AV2012 differ only in the pair (0x0e, 0x0f) Initialize writes between the
// common tables.
type Model int

const (
	AV2011 Model = iota
	AV2012
)

// Config describes how the tuner is wired on the board.
type Config struct {
	// Address is the tuner's 8-bit I2C address.
	Address byte
	// Model selects the AV2011 or AV2012 register table.
	Model Model
	// XtalKHz is the reference crystal frequency, typically 27000.
	XtalKHz uint32
}

// Registers written verbatim during Initialize; the split into three groups is
// because the model-specific pair sits in the middle of a fixed sequence.
var (
	commonBefore = []byte{
		0x00, 0x38, 0x01, 0x00, 0x02, 0x00, 0x03, 0x50,
		0x04, 0x1f, 0x05, 0xa3, 0x06, 0xfd, 0x07, 0x58,
		0x08, 0x36, 0x09, 0xc2, 0x0a, 0x88, 0x0b, 0xb4,
	}
	modelAV2011 = []byte{0x0e, 0x94, 0x0f, 0x9a}
	modelAV2012 = []byte{0x0e, 0x5b, 0x0f, 0x6a}
	commonAfter = []byte{
		0x0d, 0x40,
		0x10, 0x66, 0x11, 0x40, 0x12, 0x80, 0x13, 0x2b,
		0x14, 0x6a, 0x15, 0x50, 0x16, 0x91, 0x17, 0x27,
		0x18, 0x8f, 0x19, 0xcc, 0x1a, 0x21, 0x1b, 0x10,
		0x1c, 0x80, 0x1d, 0x02, 0x1e, 0xf5, 0x1f, 0x7f,
		0x20, 0x4a, 0x21, 0x9b, 0x22, 0xe0, 0x23, 0xe0,
		0x24, 0x36, 0x25, 0x00, 0x26, 0xab, 0x27, 0x97,
		0x28, 0xc5, 0x29, 0xa8,
	}
)

const (
	registerBandwidth = 0x05
	registerFineTune  = 0x25
	registerControl   = 0x0c
)

// registerFineTune bits.
const (
	fineTuneEnable = 0x02
	fineTuneBlock  = 0x04
)

// Tuner is an AV201x reached over an I2C bus.
type Tuner struct {
	bus    i2c.Bus
	config Config
}

// New addresses an AV201x on the given bus.
func New(bus i2c.Bus, config Config) *Tuner {
	if config.XtalKHz == 0 {
		config.XtalKHz = 27000
	}
	return &Tuner{bus: bus, config: config}
}

func (tuner *Tuner) write(register, value byte) error {
	return tuner.bus.Write(tuner.config.Address, []byte{register, value})
}

func (tuner *Tuner) writePairs(pairs []byte) error {
	for offset := 0; offset < len(pairs); offset += 2 {
		if err := tuner.write(pairs[offset], pairs[offset+1]); err != nil {
			return fmt.Errorf("AV201x register %#02x: %w", pairs[offset], err)
		}
	}
	return nil
}

// Initialize loads the tuner's fixed register table.
func (tuner *Tuner) Initialize() error {
	if err := tuner.writePairs(commonBefore); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	model := modelAV2012
	if tuner.config.Model == AV2011 {
		model = modelAV2011
	}
	if err := tuner.writePairs(model); err != nil {
		return err
	}
	if err := tuner.writePairs(commonAfter); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	return nil
}

// Tune moves the tuner to a frequency in Hz, with a channel filter sized from
// the symbol rate.
func (tuner *Tuner) Tune(frequencyHz, symbolRateSPS uint32) error {
	xtalHz := tuner.config.XtalKHz * 1000
	// RF = (n * xtal) / 2^16, the PLL output halved. n is split across the low
	// byte of the divider and the 17-bit fraction that follows it.
	coarse := min((frequencyHz+xtalHz/2)/xtalHz, 0xff)
	fraction := (uint64(frequencyHz/1000) << 17) + uint64(xtalHz/1000)/2
	fraction /= uint64(xtalHz / 1000)
	pll := []byte{
		0x00,
		byte(coarse),
		byte(fraction >> 9),
		byte(fraction >> 1),
		byte(fraction<<7&0x80) | 0x50,
	}
	if err := tuner.bus.Write(tuner.config.Address, pll); err != nil {
		return fmt.Errorf("AV201x PLL: %w", err)
	}
	time.Sleep(20 * time.Millisecond)

	bandwidthKHz := symbolRateSPS / 1000 * 135 / 200
	if symbolRateSPS < 6_500_000 {
		bandwidthKHz += 6000
	}
	bandwidthKHz += 2000
	bandwidthKHz = bandwidthKHz * 108 / 100
	switch {
	case bandwidthKHz > 40000:
		bandwidthKHz = 40000
	case bandwidthKHz < 4000:
		bandwidthKHz = 4000
	}
	// The filter steps in units of 211 kHz.
	filter := (uint64(bandwidthKHz)*127 + 21100/2) / 21100
	if err := tuner.write(registerBandwidth, byte(filter)); err != nil {
		return fmt.Errorf("AV201x bandwidth: %w", err)
	}
	if err := tuner.write(registerFineTune, fineTuneEnable|fineTuneBlock); err != nil {
		return err
	}
	if err := tuner.write(registerControl, 0x96); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	return nil
}

// agcPoints is the piecewise-linear AGC-to-dBm calibration curve, in
// (AGC reading, tenths of a dBm) pairs.
var agcPoints = [][2]int32{
	{0, 90}, {82, -50}, {100, -263}, {116, -361}, {140, -463},
	{162, -563}, {173, -661}, {187, -761}, {210, -861},
	{223, -891}, {254, -904}, {255, -910},
}

// SignalStrengthFromAGC converts a raw AGC reading (0..255) to dBm along the
// calibration curve.
func SignalStrengthFromAGC(agc byte) float64 {
	reading := int32(agc)
	index := len(agcPoints) - 1
	for candidate := range agcPoints {
		if agcPoints[candidate][0] > reading {
			index = candidate
			break
		}
	}
	if index == 0 {
		index = 1
	}
	lower, upper := agcPoints[index-1], agcPoints[index]
	slope := (upper[1] - lower[1]) * 1000 / (upper[0] - lower[0])
	tenthsDBm := lower[1] + ((reading-lower[0])*slope+500)/1000
	return float64(tenthsDBm) / 10
}
