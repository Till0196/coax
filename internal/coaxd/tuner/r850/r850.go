// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (r850.c) by nns779.

// Package r850 drives the RafaelMicro R850 terrestrial tuner over any i2c.Bus,
// typically the gated tuner bus behind a TC90522.
//
// The chip has no readable register map: the driver shadows registers 0x08 to
// 0x2f, edits the image and writes the range back. Before the first tune it runs
// the image-rejection and low-pass filter calibrations, measured with the chip's
// own ADC.
package r850

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Till0196/coax/pkg/i2c"
)

const registerCount = 0x30

// Registers below firstWritableRegister are read-only status.
const firstWritableRegister = 0x08

// System is a broadcast system the tuner can be configured for.
type System int

// Systems the chip supports. Only ISDB-T is reachable on the boards this tree
// drives; the tables cover the rest as ported.
const (
	SystemUndefined System = iota
	SystemDVBT
	SystemDVBT2
	SystemDVBT21
	SystemDVBC
	SystemJ83B
	SystemISDBT
	SystemDTMB
	SystemATSC
	SystemFM
	systemCount
)

// Bandwidth selects the channel width.
type Bandwidth int

const (
	Bandwidth6M Bandwidth = iota
	Bandwidth7M
	Bandwidth8M
)

// Config describes how the tuner is wired on the board.
type Config struct {
	// XtalKHz is the crystal frequency in kHz; every known board uses 24000.
	XtalKHz uint32
	// LoopThrough keeps the RF loop-through output alive.
	LoopThrough bool
	// ClockOut enables the clock output pin.
	ClockOut bool
	// Skipping the slow start-up calibrations trades sensitivity for a faster
	// first tune.
	NoIMRCalibration bool
	NoLPFCalibration bool
}

// SystemConfig is the system, width and IF the demodulator wants.
type SystemConfig struct {
	System      System
	Bandwidth   Bandwidth
	IFFrequency uint32
}

// ISDBTSystemConfig is the system every board in this tree drives the R850
// with: ISDB-T in a 6 MHz channel at the TC90522's 4063 kHz IF.
var ISDBTSystemConfig = SystemConfig{
	System:      SystemISDBT,
	Bandwidth:   Bandwidth6M,
	IFFrequency: 4063,
}

// lpfParams is a low-pass filter setting, calibrated or taken from the table.
type lpfParams struct {
	code      byte
	bandwidth byte
	lsb       byte
}

type systemParams struct {
	bandwidth           int
	ifFrequency         uint32
	filterCalibrationIF uint32
	bw                  byte
	filterExtend        byte
	hpfNotch            byte
	hpfCorner           byte
	filterCompensation  byte
	imageGain           byte
	agcClock            byte
	lpf                 lpfParams
}

type frequencyParams struct {
	ifFrequency uint32
	rfMin       uint32
	rfMax       uint32

	lnaTop              byte
	lnaVTLH             byte
	lnaNRBDetect        byte
	lnaRFDisableMode    byte
	lnaRFChargeCurrent  byte
	lnaRFDisableCurrent byte
	lnaDisableSlowFast  byte

	rfTop             byte
	rfVTLH            byte
	rfGainLimit       byte
	rfDisableSlowFast byte
	rfLTEPSG          byte

	nrbTop          byte
	nrbBandwidthHPF byte
	nrbBandwidthLPF byte

	mixerTop                byte
	mixerVTH                byte
	mixerVTL                byte
	mixerAmpLPF             byte
	mixerGainLimit          byte
	mixerDetectBandwidthLPF byte
	mixerFilterDisable      byte

	filterTop           byte
	filterVTH           byte
	filterVTL           byte
	filter3rdLPFCurrent byte
	filter3rdLPFGain    byte

	basebandDisableCurrent byte
	basebandDetectMode     byte
	naPowerDetect          byte
	enablePolyGain         byte
	imageNRBAdder          byte
	hpfCompensation        byte
	feedbackResistor1st    byte
}

type imr struct {
	gain  byte
	phase byte
	iqcap byte
	value byte
}

// imrDirection is which of the two axes a calibration step walks.
type imrDirection int

const (
	imrGain imrDirection = iota
	imrPhase
)

// calibrationState holds the image-rejection results for one mixer mode.
type calibrationState struct {
	points      [5]imr
	done        bool
	usable      [5]bool
	mixerAmpLPF byte
}

// Tuner is an R850 reached over an I2C bus.
type Tuner struct {
	bus     i2c.Bus
	address byte
	config  Config

	mu          sync.Mutex
	initialized bool
	// chip is 0 for the original revision, 1 for the newer; it selects the
	// system table column.
	chip        int
	xtalPower   byte
	registers   [registerCount]byte
	system      SystemConfig
	current     SystemConfig
	mixerMode   byte
	mixerAmpCal byte
	calibration [2]calibrationState
}

// New addresses an R850 on the given bus. address is the 8-bit I2C address.
func New(bus i2c.Bus, address byte, config Config) *Tuner {
	if config.XtalKHz == 0 {
		config.XtalKHz = 24000
	}
	return &Tuner{bus: bus, address: address, config: config}
}

// reverseBits mirrors a byte; status registers come back bit-reversed.
func reverseBits(value byte) byte {
	value = value&0x55<<1 | value&0xaa>>1
	value = value&0x33<<2 | value&0xcc>>2
	return value&0x0f<<4 | value&0xf0>>4
}

// readRegisters reads len(into) registers starting at register. The chip always
// answers from register 0, so the transfer covers everything up to the range.
func (tuner *Tuner) readRegisters(register byte, into []byte) error {
	length := int(register) + len(into)
	if len(into) == 0 || length > registerCount {
		return fmt.Errorf("R850 register read %#02x+%d is out of range", register, len(into))
	}
	if err := tuner.bus.Write(tuner.address, []byte{0x00}); err != nil {
		return err
	}
	data, err := tuner.bus.Read(tuner.address, length)
	if err != nil {
		return err
	}
	if len(data) < length {
		return fmt.Errorf("R850 short register read: %d of %d", len(data), length)
	}
	for index := range into {
		into[index] = reverseBits(data[int(register)+index])
	}
	return nil
}

func (tuner *Tuner) writeRegisters(register byte, length int) error {
	if length == 0 || int(register)+length > registerCount {
		return fmt.Errorf("R850 register write %#02x+%d is out of range", register, length)
	}
	command := make([]byte, 1+length)
	command[0] = register
	copy(command[1:], tuner.registers[register:int(register)+length])
	return tuner.bus.Write(tuner.address, command)
}

func (tuner *Tuner) resetRegisters() { tuner.registers = initRegisters }

func (tuner *Tuner) setXtalCap(cap byte) {
	value := cap
	gain := false
	if value > 0x1f {
		value -= 10
		gain = true
	}
	tuner.registers[0x21] = tuner.registers[0x21]&0x07 | value<<2&0x78
	if gain {
		tuner.registers[0x21] |= 0x80
	}
	tuner.registers[0x22] = tuner.registers[0x22]&0xf7 | value<<3&0x08
}

// Initialize probes the chip revision and measures its crystal drive level.
func (tuner *Tuner) Initialize() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	tuner.initialized = false
	tuner.chip = 0
	tuner.system = SystemConfig{}
	tuner.current = SystemConfig{}
	tuner.calibration[0].done = false
	tuner.calibration[1].done = false

	var err error
	for attempt := 0; attempt < 4; attempt++ {
		var status [1]byte
		if err = tuner.readRegisters(0x00, status[:]); err != nil {
			continue
		}
		if status[0]&0x98 != 0 {
			tuner.chip = 1
			break
		}
	}
	if err != nil {
		return fmt.Errorf("R850 did not respond: %w", err)
	}

	// The crystal probe overwrites the writable range, so put back whatever the
	// chip powered up with.
	var saved [registerCount]byte
	if err := tuner.readRegisters(firstWritableRegister, saved[firstWritableRegister:]); err != nil {
		return fmt.Errorf("R850 register read: %w", err)
	}
	if err := tuner.checkXtalPower(); err != nil {
		return fmt.Errorf("R850 crystal probe: %w", err)
	}
	tuner.registers = saved
	if err := tuner.writeRegisters(firstWritableRegister, registerCount-firstWritableRegister); err != nil {
		return err
	}
	tuner.resetRegisters()
	tuner.initialized = true
	return nil
}

// PLLLocked reports whether the synthesiser reached lock.
func (tuner *Tuner) PLLLocked() (bool, error) {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return false, errors.New("R850 is not initialised")
	}
	var status [1]byte
	if err := tuner.readRegisters(0x02, status[:]); err != nil {
		return false, err
	}
	return status[0]&0x40 != 0, nil
}

// Sleep powers the tuner down.
func (tuner *Tuner) Sleep() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return errors.New("R850 is not initialised")
	}
	tuner.registers = sleepRegisters
	if !tuner.config.LoopThrough {
		tuner.registers[0x08] |= 0x40
	}
	if err := tuner.writeRegisters(firstWritableRegister, registerCount-firstWritableRegister); err != nil {
		return err
	}
	tuner.current = SystemConfig{}
	return nil
}

// Wake brings the tuner back from Sleep.
func (tuner *Tuner) Wake() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return errors.New("R850 is not initialised")
	}
	tuner.registers = wakeupRegisters
	if err := tuner.writeRegisters(firstWritableRegister, registerCount-firstWritableRegister); err != nil {
		return err
	}
	tuner.resetRegisters()
	return tuner.writeRegisters(firstWritableRegister, registerCount-firstWritableRegister)
}
