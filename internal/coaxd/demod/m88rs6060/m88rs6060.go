// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TBS linux_media (m88rs6060.c), TurboSight's driver for TBS5530's
// satellite half. Registers are flat 8-bit -- no banked SLV-style map like
// CXD2878ER -- and the embedded tuner sits behind a single gate register (0x03)
// written before every tuner transfer, the shape internal/coaxd/usb/i2c.Gate
// models. Tuner access stays inline.
//
// ts_autoclock is not ported: TBS5530 has no CI slot.
package m88rs6060

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/i2c"
)

// TSMode is enum m88rs6060_ts_mode minus its unused zero value.
type TSMode int

const (
	TSModeSerial TSMode = iota
	TSModeParallel
	TSModeCommon
)

// Config describes how the board wired the chip.
type Config struct {
	// DemodAddress is the chip's seven-bit I2C address -- 0x69 here.
	DemodAddress byte
	// TunerAddress is the embedded tuner's seven-bit address, reached through
	// the demodulator's gate register.
	TunerAddress byte
	// ClockHz is the tuner crystal; every board checked so far uses 27 MHz.
	ClockHz uint32
	TSMode  TSMode
	// TSPinSwitch reverses the transport stream data pin order.
	TSPinSwitch bool
	// NoTone says this chip's tone pin is not wired out -- a board can put two
	// of these on one bus and bring out only one. Tone requests are then ignored
	// rather than refused, which would make the tuner useless for every channel
	// rather than only the ones needing a tone.
	NoTone bool
}

const firmwareName = "dvb-demod-m88rs6060.fw"

// tunerGateValue is written to register 0x03 before every tuner transfer.
// Reads would use the board's repeater value; every board here leaves it 0x11.
const tunerGateValue = 0x11

// Demod is an M88RS6060 reached over an I2C bus.
type Demod struct {
	bus         i2c.Bus
	firmwareDir string
	config      Config

	warm     bool
	mclkKHz  uint32
	system   string
	streamID int32
}

// New addresses an M88RS6060 on bus. firmwareDir holds the firmware blob
// Initialize loads.
func New(bus i2c.Bus, firmwareDir string, config Config) *Demod {
	if config.ClockHz == 0 {
		config.ClockHz = 27_000_000
	}
	return &Demod{bus: bus, firmwareDir: firmwareDir, config: config}
}

func (demod *Demod) regRead(register byte) (byte, error) {
	data, err := i2c.WriteRead(demod.bus, demod.config.DemodAddress, []byte{register}, 1)
	if err != nil {
		return 0, fmt.Errorf("M88RS6060 read %#02x: %w", register, err)
	}
	return data[0], nil
}

func (demod *Demod) regReadMulti(register byte, length int) ([]byte, error) {
	data, err := i2c.WriteRead(demod.bus, demod.config.DemodAddress, []byte{register}, length)
	if err != nil {
		return nil, fmt.Errorf("M88RS6060 read %#02x: %w", register, err)
	}
	return data, nil
}

func (demod *Demod) regWrite(register, value byte) error {
	if err := demod.bus.Write(demod.config.DemodAddress, []byte{register, value}); err != nil {
		return fmt.Errorf("M88RS6060 write %#02x: %w", register, err)
	}
	return nil
}

func (demod *Demod) regUpdateBits(register, mask, value byte) error {
	if mask != 0xff {
		current, err := demod.regRead(register)
		if err != nil {
			return err
		}
		value = i2c.Mask(current, value, mask)
	}
	return demod.regWrite(register, value)
}

// OpenTunerGate satisfies i2c.Gate. The gate is never closed, only rewritten
// before the next tuner transfer, so this acts only on open.
func (demod *Demod) OpenTunerGate(open bool) error {
	if !open {
		return nil
	}
	return demod.regWrite(0x03, tunerGateValue)
}

func (demod *Demod) tunerWrite(register, value byte) error {
	if err := demod.OpenTunerGate(true); err != nil {
		return err
	}
	if err := demod.bus.Write(demod.config.TunerAddress, []byte{register, value}); err != nil {
		return fmt.Errorf("M88RS6060 tuner write %#02x: %w", register, err)
	}
	return nil
}

func (demod *Demod) tunerRead(register byte) (byte, error) {
	if err := demod.OpenTunerGate(true); err != nil {
		return 0, err
	}
	data, err := i2c.WriteRead(demod.bus, demod.config.TunerAddress, []byte{register}, 1)
	if err != nil {
		return 0, fmt.Errorf("M88RS6060 tuner read %#02x: %w", register, err)
	}
	return data[0], nil
}

// hardReset is m88rs6060_hard_rest: tuner reset pulse, tuner wakeup, then the
// demodulator-side reset.
func (demod *Demod) hardReset() error {
	if err := demod.tunerWrite(0x04, 0x01); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x04, 0x00); err != nil {
		return err
	}
	value, err := demod.tunerRead(0x04)
	if err != nil {
		return err
	}
	if err := demod.tunerWrite(0x04, value&^0x01); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	if err := demod.tunerWakeup(); err != nil {
		return err
	}

	value, err = demod.regRead(0x08)
	if err != nil {
		return err
	}
	if err := demod.regWrite(0x08, value|0x01); err != nil {
		return err
	}
	value, err = demod.regRead(0x0b)
	if err != nil {
		return err
	}
	if err := demod.regWrite(0x0b, value|0x01); err != nil {
		return err
	}
	value, err = demod.regRead(0xb2)
	if err != nil {
		return err
	}
	if value == 0x01 {
		if err := demod.regWrite(0x00, 0x00); err != nil {
			return err
		}
		if err := demod.regWrite(0xb2, 0x00); err != nil {
			return err
		}
	}
	if err := demod.regWrite(0x07, 0x80); err != nil {
		return err
	}
	if err := demod.regWrite(0x07, 0x00); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	value, err = demod.regRead(0x08)
	if err != nil {
		return err
	}
	return demod.regWrite(0x08, value|0x01)
}

// tunerInit writes the channel-independent tuner bring-up register set, once.
func (demod *Demod) tunerInit() error {
	if err := demod.tunerWrite(0x15, 0x6c); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x2b, 0x1e); err != nil {
		return err
	}
	if err := demod.tunerWakeup(); err != nil {
		return err
	}
	for _, step := range [][2]byte{
		{0x24, 0x04}, {0x6e, 0x39}, {0x83, 0x01},
		{0x70, 0x90}, {0x71, 0xf0}, {0x72, 0xb6},
		{0x73, 0xeb}, {0x74, 0x6f}, {0x75, 0xfc},
	} {
		if err := demod.tunerWrite(step[0], step[1]); err != nil {
			return err
		}
	}
	return nil
}

// tunerWakeup is rs6060_wakeup.
func (demod *Demod) tunerWakeup() error {
	if err := demod.tunerWrite(0x10, 0xfb); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x11, 0x01); err != nil {
		return err
	}
	if err := demod.tunerWrite(0x07, 0x7d); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return nil
}

// setTSMode is m88res6060_set_ts_mode.
func (demod *Demod) setTSMode() error {
	value, err := demod.regRead(0x0b)
	if err != nil {
		return err
	}
	if err := demod.regWrite(0x0b, value&^0x01); err != nil {
		return err
	}

	fd, err := demod.regRead(0xfd)
	if err != nil {
		return err
	}
	// Only two bits differ; the clock-divider writes are shared by the two
	// non-serial modes.
	switch demod.config.TSMode {
	case TSModeParallel:
		fd &^= 0x01
		fd &^= 0x04
	case TSModeSerial:
		fd &^= 0x01
		fd |= 0x04
	default: // TSModeCommon
		fd |= 0x01
		fd &^= 0x04
	}
	if demod.config.TSMode != TSModeSerial {
		if err := demod.regWrite(0xfa, 0x01); err != nil {
			return err
		}
		if err := demod.regWrite(0xf1, 0x60); err != nil {
			return err
		}
		if err := demod.regWrite(0xfa, 0x00); err != nil {
			return err
		}
	}
	fd &^= 0xb8
	fd |= 0x42
	fd |= 0x80
	if err := demod.regWrite(0xfd, fd); err != nil {
		return err
	}

	// The parallel-CI pin level enhancement is off on every board here, so the
	// drive-strength byte is zero in every mode.
	if err := demod.regWrite(0x0a, 0x00); err != nil {
		return err
	}

	value, err = demod.regRead(0x0b)
	if err != nil {
		return err
	}
	if demod.config.TSPinSwitch {
		value |= 0x20
	} else {
		value &^= 0x20
	}
	value |= 0x01
	if err := demod.regWrite(0x0b, value); err != nil {
		return err
	}
	value, err = demod.regRead(0x0c)
	if err != nil {
		return err
	}
	if err := demod.regWrite(0xf4, 0x01); err != nil {
		return err
	}
	return demod.regWrite(0x0c, value&^0x80)
}

// Initialize resets the chip, downloads firmware and brings up the transport
// stream output, leaving it ready for Tune.
func (demod *Demod) Initialize() error {
	if err := demod.hardReset(); err != nil {
		return fmt.Errorf("reset M88RS6060: %w", err)
	}
	if err := demod.tunerInit(); err != nil {
		return fmt.Errorf("initialize M88RS6060 tuner: %w", err)
	}
	if err := demod.regWrite(0x07, 0xe0); err != nil {
		return err
	}
	if err := demod.regWrite(0x07, 0x00); err != nil {
		return err
	}

	data, err := device.ReadFirmware(demod.firmwareDir, firmwareName)
	if err != nil {
		return fmt.Errorf("no M88RS6060 firmware found in %s (tried %s): %w", demod.firmwareDirOrDot(), firmwareName, err)
	}
	if err := demod.regWrite(0xb2, 0x01); err != nil {
		return err
	}
	const maxWrite = 32 // TBS5530's 33-byte I2C write limit, minus one
	for offset := 0; offset < len(data); offset += maxWrite {
		end := min(offset+maxWrite, len(data))
		if err := demod.firmwareDownload(data[offset:end]); err != nil {
			return fmt.Errorf("download M88RS6060 firmware: %w", err)
		}
	}
	if err := demod.regWrite(0xb2, 0x00); err != nil {
		return err
	}

	version, err := demod.regRead(0xb9)
	if err != nil {
		return err
	}
	if version == 0 {
		return errors.New("M88RS6060 firmware did not run")
	}
	time.Sleep(5 * time.Millisecond)

	if err := demod.setTSMode(); err != nil {
		return err
	}
	value, err := demod.regRead(0x4d)
	if err != nil {
		return err
	}
	if err := demod.regWrite(0x4d, value&^0x02); err != nil {
		return err
	}
	demod.warm = true
	return nil
}

func (demod *Demod) firmwareDirOrDot() string {
	if demod.firmwareDir == "" {
		return "the current directory"
	}
	return demod.firmwareDir
}

// firmwareDownload is m88rs6060_fireware_download: register 0xB0 followed by
// up to i2c_wr_max-1 payload bytes.
func (demod *Demod) firmwareDownload(chunk []byte) error {
	return demod.bus.Write(demod.config.DemodAddress, append([]byte{0xb0}, chunk...))
}
