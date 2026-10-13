// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (r850.c) by nns779.

package r850

import (
	"errors"
	"time"
)

// calibrateLPF measures the low-pass filter code for a system's IF.
func (tuner *Tuner) calibrateLPF(ifFrequency uint32, bw, gap byte) (lpfParams, error) {
	var result lpfParams
	tuner.registers = lpfCalibrationRegisters
	if err := tuner.setPLL(72000-ifFrequency, ifFrequency, SystemUndefined); err != nil {
		return result, err
	}

	var value, reference byte
	for level := byte(5); level < 16; level++ {
		tuner.registers[0x29] = tuner.registers[0x29]&0x0f | level<<4&0xf0
		if err := tuner.writeRegisters(0x29, 1); err != nil {
			return result, err
		}
		time.Sleep(5 * time.Millisecond)
		sample, err := tuner.readADC()
		if err != nil {
			return result, err
		}
		value = sample
		if value > 0x28 {
			break
		}
	}

	if ifFrequency > 9999 {
		if err := tuner.setPLL(63500, 8500, SystemUndefined); err != nil {
			return result, err
		}
		time.Sleep(5 * time.Millisecond)
		sample, err := tuner.readADC()
		if err != nil {
			return result, err
		}
		reference = sample
		if reference > value+8 {
			return result, errors.New("R850 low-pass filter calibration did not settle")
		}
		if err := tuner.setPLL(72000-ifFrequency, ifFrequency, SystemUndefined); err != nil {
			return result, err
		}
	}

	bandwidth := byte(0)
	start := 0
	if bw == 2 {
		start = 1
	}
	for index := start; index < 3; index++ {
		bandwidth = 0
		if index != 0 {
			bandwidth = byte(index + 1)
		}
		tuner.registers[0x17] = tuner.registers[0x17]&0x9f&0xe1 | bandwidth<<5&0x60
		if err := tuner.writeRegisters(0x17, 1); err != nil {
			return result, err
		}
		time.Sleep(5 * time.Millisecond)
		wide, err := tuner.readADC()
		if err != nil {
			return result, err
		}
		tuner.registers[0x17] = tuner.registers[0x17]&0xe1 | 0x1a
		if err := tuner.writeRegisters(0x17, 1); err != nil {
			return result, err
		}
		time.Sleep(5 * time.Millisecond)
		narrow, err := tuner.readADC()
		if err != nil {
			return result, err
		}
		if narrow+16 < wide {
			break
		}
	}
	result.bandwidth = bandwidth

	code := 0
	for ; code < 16; code++ {
		tuner.registers[0x17] = tuner.registers[0x17]&0xe1 | byte(code)<<1&0x1e
		if err := tuner.writeRegisters(0x17, 1); err != nil {
			return result, err
		}
		time.Sleep(5 * time.Millisecond)
		sample, err := tuner.readADC()
		if err != nil {
			return result, err
		}
		if code == 0 {
			if ifFrequency <= 9999 {
				value = sample
			} else {
				value = reference
			}
		}
		if sample+gap < value {
			if code == 0 {
				return result, errors.New("R850 low-pass filter calibration fell off its range")
			}
			tuner.registers[0x17] = tuner.registers[0x17]&0xe0 | 1 | byte(code-1)<<1&0x1e
			if err := tuner.writeRegisters(0x17, 1); err != nil {
				return result, err
			}
			time.Sleep(5 * time.Millisecond)
			sample, err = tuner.readADC()
			if err != nil {
				return result, err
			}
			if sample+gap < value {
				code--
				result.lsb = 1
			}
			break
		}
	}
	result.code = byte(code)
	return result, nil
}

// checkXtalPower finds the lowest crystal drive level the chip still starts at.
func (tuner *Tuner) checkXtalPower() error {
	const bank = 55 // for a 24 MHz crystal
	power := byte(3)

	tuner.resetRegisters()
	if tuner.chip != 0 {
		tuner.registers[0x2f] &= 0xfd
	} else {
		tuner.registers[0x2f] &= 0xfc
	}
	tuner.registers[0x1b] = tuner.registers[0x1b]&0x80 | 0x12
	tuner.registers[0x1e] = tuner.registers[0x1e]&0xe0 | 0x08
	tuner.registers[0x22] &= 0x27
	tuner.registers[0x1d] &= 0x0f
	tuner.registers[0x21] |= 0xf8
	tuner.registers[0x22] = tuner.registers[0x22]&0x77 | 0x80
	tuner.registers[0x1f] = tuner.registers[0x1f]&0x80 | 0x40
	tuner.registers[0x1f] &= 0xbf

	if err := tuner.writeRegisters(firstWritableRegister, registerCount-firstWritableRegister); err != nil {
		return err
	}
	for level := byte(0); level <= 3; level++ {
		tuner.registers[0x22] = tuner.registers[0x22]&0xcf | level<<4
		if err := tuner.writeRegisters(0x22, 1); err != nil {
			return err
		}
		var status [1]byte
		if err := tuner.readRegisters(0x02, status[:]); err != nil {
			return err
		}
		if status[0]&0x40 != 0 && status[0]&0x3f-(bank-6) <= 12 {
			power = level
			break
		}
	}
	if power < 3 {
		power++
	}
	tuner.xtalPower = power
	return nil
}
