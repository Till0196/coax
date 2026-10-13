// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package mxl136 drives the MaxLinear MxL136 ISDB-T tuner over any i2c.Bus,
// typically the terrestrial tuner bus behind a TC90522.
package mxl136

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// address is the MxL136's I2C slave address on the tuner bus.
const address byte = 0xc0

// Tuner is an MxL136 reached over an I2C bus.
type Tuner struct{ bus i2c.Bus }

// New addresses an MxL136 on the given bus and loads its initial register set.
func New(bus i2c.Bus) (*Tuner, error) {
	tuner := &Tuner{bus: bus}
	if err := tuner.write([]byte{0xff}); err != nil {
		return nil, err
	}
	init := []byte{0x02, 0, 0x03, 0x40, 0x05, 0x04, 0x06, 0x10, 0x2e, 0x15, 0x30, 0x10, 0x45, 0x58, 0x48, 0x19, 0x52, 0x03, 0x53, 0x44, 0x6a, 0x4b, 0x76, 0, 0x78, 0x18, 0x7a, 0x17, 0x85, 0x06, 0x01, 0x01}
	if err := tuner.write(init); err != nil {
		return nil, err
	}
	return tuner, nil
}

func (tuner *Tuner) write(data []byte) error {
	return tuner.bus.Write(address, data)
}

// Sleep powers the tuner down, freeing the shared antenna path.
func (tuner *Tuner) Sleep() error { return tuner.write([]byte{0x01, 0, 0x0f, 0}) }

// Wake powers the tuner back up.
func (tuner *Tuner) Wake() error { return tuner.write([]byte{0x01, 1}) }

// Tune moves the tuner to a terrestrial frequency given in kHz.
func (tuner *Tuner) Tune(frequencyKHz uint64) error {
	data, err := tuneData(frequencyKHz)
	if err != nil {
		return err
	}
	if err := tuner.write(data); err != nil {
		return err
	}
	time.Sleep(3 * time.Millisecond)
	return nil
}

func tuneData(frequencyKHz uint64) ([]byte, error) {
	if frequencyKHz < 60000 || frequencyKHz >= 860000 {
		return nil, fmt.Errorf("MxL136 terrestrial frequency %d kHz is out of range", frequencyKHz)
	}
	digital := (frequencyKHz << 3) / 125
	band := byte(0x41)
	if frequencyKHz < 333000 {
		band = 0x01
	}
	data := []byte{0x0f, 0, 0x0c, 0x15, 0x0d, byte(digital), 0x0e, byte(digital >> 8), 0x1f, 0x87, 0x20, 0x1f, 0x21, 0x87, 0x22, 0x1f, 0x80, band, 0x0f, 1}
	return data, nil
}
