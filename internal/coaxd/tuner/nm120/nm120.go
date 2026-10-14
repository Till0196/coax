// Copyright (c) 2014 Budi Rachmanto; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

// Package nm120 drives the Newport Media NM120/NM131 ISDB-T tuner.
//
// Of the five PLEX products the px3 backend claims, only the PX-W3U3 V2 has
// this part; the other four carry a Fitipower FC0012 at another address and
// register width.
//
// Four parts share this sixteen-bit protocol and are told apart by a chip
// identity in register 0x3fc, each wanting a different closing table. All four
// are handled here.
package nm120

import (
	"encoding/binary"
	"fmt"

	"github.com/Till0196/coax/pkg/i2c"
)

// Tuner is an NM120 reached through the TC90522 demodulator core in front of
// it: register access rides on the demodulator's own I2C address.
type Tuner struct {
	bus     i2c.Bus
	address byte
}

// New addresses an NM120 on the given bus. address is the 8-bit I2C address of
// the demodulator core the tuner sits behind.
func New(bus i2c.Bus, address byte) *Tuner { return &Tuner{bus: bus, address: address} }

func (tuner *Tuner) write8(register, value byte) error {
	return tuner.bus.Write(tuner.address, []byte{register, value})
}

func (tuner *Tuner) write(register uint16, value uint32, size int) error {
	if size < 0 || size > 4 {
		return fmt.Errorf("NM120 write size %d is invalid", size)
	}
	data := make([]byte, 4+size)
	data[0], data[1] = 0xfe, 0xce
	data[2], data[3] = byte(register>>8), byte(register)
	for index := 0; index < size; index++ {
		data[4+index] = byte(value >> (8 * index))
	}
	if register == 0x36 && size != 0 {
		data[4] &= 0x7f
	}
	return tuner.bus.Write(tuner.address, data)
}

func (tuner *Tuner) read(register uint16, size int) ([]byte, error) {
	if size <= 0 || size > 4 {
		return nil, fmt.Errorf("NM120 read size %d is invalid", size)
	}
	if err := tuner.write(register, 0, 0); err != nil {
		return nil, err
	}
	return i2c.WriteRead(tuner.bus, tuner.address, []byte{0xfe, 0xcf}, size)
}

func (tuner *Tuner) read8(register uint16) (byte, error) {
	data, err := tuner.read(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (tuner *Tuner) read32(register uint16) (uint32, error) {
	data, err := tuner.read(register, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data), nil
}

// Initialize loads the NM120 RF and baseband defaults.
func (tuner *Tuner) Initialize() error {
	initial := [][2]byte{{0xb0, 0xa0}, {0xb2, 0x3d}, {0xb3, 0x25}, {0xb4, 0x8b}, {0xb5, 0x4b}, {0xb6, 0x3f}, {0xb7, 0xff}, {0xb8, 0xc0}, {0x03, 0}, {0x1d, 0}, {0x1f, 0}, {0x0e, 0x77}, {0x0f, 0x13}, {0x75, 0x02}}
	for _, pair := range initial {
		if err := tuner.write8(pair[0], pair[1]); err != nil {
			return err
		}
	}
	rf := [][2]byte{{6, 72}, {7, 64}, {10, 235}, {11, 17}, {12, 16}, {13, 136}, {16, 4}, {17, 48}, {18, 48}, {21, 170}, {22, 3}, {23, 128}, {24, 103}, {25, 212}, {26, 68}, {28, 16}, {29, 238}, {30, 153}, {33, 197}, {34, 145}, {36, 1}, {43, 145}, {45, 1}, {47, 128}, {49, 0}, {51, 0}, {56, 0}, {57, 47}, {58, 0}, {59, 0}}
	for _, pair := range rf {
		if err := tuner.write(uint16(pair[0]), uint32(pair[1]), 1); err != nil {
			return err
		}
	}
	value, err := tuner.read8(0x36)
	if err != nil {
		return err
	}
	if err := tuner.write(0x36, uint32(value&0x7f), 1); err != nil {
		return err
	}
	// The two thirty-two-bit writes. The registers are 0x0164 and 0x01c0, not
	// 0x28 and 0x800 as a misreading of where the pairs begin suggests, and the
	// second value really is as arbitrary as it looks.
	for _, item := range []struct {
		register uint16
		value    uint32
	}{{0x0164, 0x0800}, {0x01c0, 0x2d8c19c7}} {
		if err := tuner.write(item.register, item.value, 4); err != nil {
			return err
		}
	}
	if err := tuner.finish(); err != nil {
		return err
	}
	// The loop-through gain, which the initialization table leaves at 0xeb: 0xf8
	// with the gain's code in the low two bits, 3 being the maximum. It is the
	// level handed to whatever is chained on the aerial lead after this board,
	// not this tuner's own gain, so it is not offered as a setting.
	return tuner.write(loopThroughRegister, loopThroughMaximum, 1)
}

// The loop-through gain register and the value it is left at.
const (
	loopThroughRegister = 0x0a
	loopThroughMaximum  = 0xf8 | 3
)

// The four parts this driver recognises, and what their chip identity reads as.
// The masks are not all the same width, so each part carries its own.
type variant struct {
	name string
	mask uint32
	id   uint32
	// tail is what the part wants written once the common table is in.
	tail [][2]byte
}

var variants = []variant{
	{
		name: "NM120", mask: 0x000fff00, id: 0x00012000,
		tail: [][2]byte{
			{0x0e, 0x45}, {0x1b, 0x0e}, {0x23, 0xff}, {0x26, 0x82}, {0x28, 0x00},
			{0x30, 0xdf}, {0x32, 0xdf}, {0x34, 0x68}, {0x35, 0x18},
		},
	},
	{
		name: "NM130", mask: 0x00ffff00, id: 0x00013000,
		tail: [][2]byte{
			{0x26, 0x80}, {0x27, 0x5f}, {0x28, 0x00}, {0x29, 0x5f},
			{0x34, 0x68}, {0x35, 0x54}, {0x36, 0x7c},
		},
	},
	{
		name: "NM130V", mask: 0x00ffff00, id: 0x00813000,
		tail: [][2]byte{{0x28, 0x00}, {0x2e, 0x56}, {0x34, 0x78}},
	},
	{
		// The NM131 wants only the one write the other three share.
		name: "NM131", mask: 0x000fff00, id: 0x00013100,
		tail: [][2]byte{{0x28, 0x00}},
	},
}

// identityRegister holds the chip identity, thirty-two bits of it.
const identityRegister = 0x3fc

// finish reads the chip's identity and writes the closing table that part wants;
// writing the NM120's table unconditionally leaves registers at values meant for
// a different part. Register 0x0a with 0xfb is in none of the four tables: that
// is the loop-through gain at maximum, which Initialize writes as 0xf8 | 3.
func (tuner *Tuner) finish() error {
	identity, err := tuner.read32(identityRegister)
	if err != nil {
		return err
	}
	for _, part := range variants {
		if identity&part.mask != part.id {
			continue
		}
		for _, pair := range part.tail {
			if err := tuner.write(uint16(pair[0]), uint32(pair[1]), 1); err != nil {
				return fmt.Errorf("%s register %#02x: %w", part.name, pair[0], err)
			}
		}
		return nil
	}
	return fmt.Errorf("the tuner at this address answers with identity %#08x, which is none of the parts this driver knows", identity)
}

// Tune tunes an ISDB-T RF frequency in Hz.
func (tuner *Tuner) Tune(frequency uint32) error {
	cutoffs := []struct {
		hz     uint32
		r8, r9 byte
	}{{45000000, 167, 58}, {55000000, 151, 57}, {65000000, 100, 54}, {75000000, 83, 53}, {85000000, 82, 53}, {95000000, 65, 52}, {105000000, 64, 52}, {115000000, 64, 52}, {125000000, 0, 0}}
	v45 := []uint32{0, 1, 2, 3, 4, 6, 9, 12}
	loTable := []uint32{0, 0, 434000000, 237000000, 214000000, 118000000, 79000000, 53000000}
	for _, pair := range [][2]byte{{1, 0x50}, {0x47, 0x30}, {0x25, 0}, {0x20, 0}, {0x23, 0x4d}} {
		if err := tuner.write8(pair[0], pair[1]); err != nil {
			return err
		}
	}
	clockFrequency := frequency
	var loFrequency, v14, integer, fraction uint32
	var band byte
	for adjusted := false; ; adjusted = true {
		loFrequency = clockFrequency
		band = 6
		if loFrequency <= 53000000 {
			band = 7
		} else {
			for band > 1 && loTable[band] < loFrequency {
				band--
			}
		}
		cutoff := 0
		for cutoff < 8 && !(loFrequency > cutoffs[cutoff].hz && loFrequency <= cutoffs[cutoff+1].hz) {
			cutoff++
		}
		mixer := uint32(5)
		if loFrequency > 120400000 {
			mixer = 0x85
		}
		if err := tuner.write(5, mixer, 1); err != nil {
			return err
		}
		if err := tuner.write(8, uint32(cutoffs[cutoff].r8), 1); err != nil {
			return err
		}
		if err := tuner.write(9, uint32(cutoffs[cutoff].r9), 1); err != nil {
			return err
		}
		v14 = loFrequency / 1000 * 8 * v45[band]
		xo := uint32(24000)
		mode, err := tuner.read8(0x21)
		if err != nil {
			return err
		}
		switch mode & 3 {
		case 2:
			xo *= 2
		case 3:
			xo >>= 1
		}
		integer = v14 / xo
		fraction = (v14 % xo * (0x80000000 / xo) >> 12) & 0x7ffff
		if fraction == 0 || adjusted {
			break
		}
		clockFrequency += 1000
	}
	divisor := v14 / 216000
	if divisor > 31 {
		divisor = 31
	}
	if divisor < 16 {
		divisor = 16
	}
	for _, pair := range [][2]uint32{{1, integer >> 1}, {2, (integer & 1) | 2*fraction}, {3, fraction >> 7}, {4, (fraction >> 15) | 16*divisor}} {
		if err := tuner.write(uint16(pair[0]), pair[1], 1); err != nil {
			return err
		}
	}
	v1d, err := tuner.read8(0x1d)
	if err != nil {
		return err
	}
	if err := tuner.write(0x1d, uint32(32*band|v1d&0x1f), 1); err != nil {
		return err
	}
	settings := [][2]uint32{{0x25, 0xf4}, {0x27, 0xef}, {0x29, 0x4f}, {0x2e, 0x34}}
	if loFrequency < 300000000 {
		settings = [][2]uint32{{0x25, 0x78}, {0x27, 0x7f}, {0x29, 0x7f}, {0x2e, 0x12}}
	}
	for _, pair := range settings {
		if err := tuner.write(uint16(pair[0]), pair[1], 1); err != nil {
			return err
		}
	}
	r36 := uint32(0x7c)
	if loFrequency < 150000000 {
		r36 = 0x54
	}
	if err := tuner.write(0x36, r36, 1); err != nil {
		return err
	}
	r37 := uint32(0x84)
	if loFrequency >= 155000000 && loFrequency < 300000000 {
		r37 = 0x9c
	}
	if err := tuner.write(0x37, r37, 1); err != nil {
		return err
	}
	clockFrequency = (v14<<9)/divisor - 110592000
	rf := clockFrequency/6750 + 16384
	r164 := uint32(0x500)
	if frequency < 300000000 {
		r164 = 0x600
	}
	writes := []struct {
		r uint16
		v uint32
	}{{0x164, r164}, {0x230, (126217<<15)/rf | 0x80000}, {0x250, 0}, {0x27c, 0x1010}, {0x1bc, 0}}
	for _, item := range writes {
		if err := tuner.write(item.r, item.v, 4); err != nil {
			return err
		}
	}
	v, err := tuner.read32(0x21c)
	if err != nil {
		return err
	}
	v = (((v & 0xffc00000) | 524288000/((clockFrequency>>14)+6750)) & 0xc7bffffe) | 0x08000000
	if err := tuner.write(0x21c, v, 4); err != nil {
		return err
	}
	v, err = tuner.read32(0x234)
	if err != nil {
		return err
	}
	if err = tuner.write(0x234, v&0xcfc00000, 4); err != nil {
		return err
	}
	if err = tuner.write(0x210, ((864*(clockFrequency>>5)-1308983296)/216000&0xfffffff0)|3, 4); err != nil {
		return err
	}
	v, err = tuner.read32(0x104)
	if err != nil {
		return err
	}
	rangeCode := uint32(0)
	if clockFrequency > 3686396 {
		rangeCode = 2
	} else if clockFrequency >= 1843193 {
		rangeCode = 1
	}
	v = (((((v&0x87ffffc0)|0x10000011)&0xffff87ff)|1<<11)&0xffff7fff | 0x8000) & 0xfff0ffff
	v = (v & 0xfc0fffff) | 0xa00000 | rangeCode<<16
	if err = tuner.write(0x104, v, 4); err != nil {
		return err
	}
	v = (v & 0xffffffef) | 0x20000020
	if err = tuner.write(0x104, v, 4); err != nil {
		return err
	}
	cal, err := tuner.read32(0x328)
	if err != nil {
		return err
	}
	if cal != 0 {
		v &= 0xffffffdf
		if err = tuner.write(0x104, v, 4); err != nil {
			return err
		}
		if err = tuner.write(0x104, v|0x20, 4); err != nil {
			return err
		}
	}
	for _, pair := range [][2]byte{{0x23, 0x4c}, {1, 0x50}, {0x71, 1}, {0x72, 0x24}} {
		if err := tuner.write8(pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}
