// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (cxd2858er.c) by nns779.

// Package cxd2858er drives the Sony CXD2858ER, a combined terrestrial and
// satellite tuner sitting behind a CXD2856ER's I2C gate. Switching between
// ISDB-T and ISDB-S stops the other path first.
//
// The gate is held open across a whole sequence rather than per transfer, which
// is why this driver takes the bus and the gate separately instead of an already
// gated bus.
package cxd2858er

import (
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/pkg/i2c"
)

// Delivery systems are the sony package's, shared with the CXD2856ER this tuner
// sits behind. ISDB-S3 and ATSC have no sequence here and J.83B has no row in
// the terrestrial table; each is refused rather than tuned as something else.

// TuneParams carries what a tune needs beyond the frequency; each field applies
// to some systems and is ignored by the rest.
type TuneParams struct {
	// FrequencyKHz is the channel's centre frequency.
	FrequencyKHz uint32
	// BandwidthMHz is the channel width, for the terrestrial and J.83 systems.
	BandwidthMHz uint32
	// SymbolRateKS sets the satellite low-pass filter, in thousands of symbols
	// per second. It is ignored by ISDB-S, whose filter is fixed.
	SymbolRateKS uint32
}

// Tune points the tuner at a channel in the given system, stopping the other
// path first when it has to.
func (tuner *Tuner) Tune(system sony.System, params TuneParams) error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	return tuner.gated(func() error {
		switch {
		case system == sony.SystemISDBT:
			return tuner.tuneISDBT(params.FrequencyKHz)
		case system.IsSatellite():
			return tuner.tuneSatellite(system, params.FrequencyKHz, params.SymbolRateKS)
		default:
			return tuner.tuneDVBTerrestrial(system, params.FrequencyKHz, params.BandwidthMHz)
		}
	})
}

// Config describes how the tuner is wired on the board.
type Config struct {
	// Xtal is the tuner's crystal; the chip's register tables are keyed by it.
	// The zero value of sony.TunerXtal is 16 MHz, which is what every board
	// here fits.
	Xtal sony.TunerXtal
	// TerrestrialLNA and SatelliteLNA enable the external low-noise amplifiers.
	TerrestrialLNA bool
	SatelliteLNA   bool
}

// Tuner is a CXD2858ER behind a demodulator's I2C gate.
type Tuner struct {
	gate   i2c.Gate
	regs   sony.TunerWriter
	config Config

	mu     sync.Mutex
	system sony.System
}

// New addresses a CXD2858ER. bus is the ungated bus the demodulator is on, gate
// opens its pass-through, and address is the tuner's 8-bit I2C address.
func New(bus i2c.Bus, gate i2c.Gate, address byte, config Config) *Tuner {
	return &Tuner{gate: gate, regs: sony.NewTunerWriter(bus, address), config: config}
}

// SetLNA turns the external low-noise amplifiers on or off -- the registers say
// only whether one is in circuit, so this is a switch, not a level. It takes
// effect at the next tune, which is where the register carrying it is written.
func (tuner *Tuner) SetLNA(on bool) {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	tuner.config.TerrestrialLNA = on
	tuner.config.SatelliteLNA = on
}

func (tuner *Tuner) System() sony.System {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	return tuner.system
}

// xtalCode is the chip's table index for the crystal frequency; only two of the
// four crystals sony.TunerXtal names have one. It is encoded in the power-up
// block before anything else, so a 20.5 or 41 MHz board is refused, not guessed.
func (tuner *Tuner) xtalCode(sixteen, twentyFour byte) (byte, error) {
	switch tuner.config.Xtal {
	case sony.TunerXtal16000KHz:
		return sixteen, nil
	case sony.TunerXtal24000KHz:
		return twentyFour, nil
	default:
		return 0, fmt.Errorf("CXD2858ER does not support a %s crystal", tuner.config.Xtal)
	}
}

// readRegister reads one register; the shared sony helper answers with a slice.
func (tuner *Tuner) readRegister(register byte) (byte, error) {
	data, err := tuner.regs.Read(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// gated runs a sequence with the demodulator's I2C pass-through held open.
func (tuner *Tuner) gated(sequence func() error) error {
	if err := tuner.gate.OpenTunerGate(true); err != nil {
		return err
	}
	err := sequence()
	if closeErr := tuner.gate.OpenTunerGate(false); err == nil {
		err = closeErr
	}
	return err
}

// Initialize powers the tuner up in terrestrial mode.
func (tuner *Tuner) Initialize() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	tuner.system = sony.SystemUnspecified
	return tuner.gated(tuner.powerOn)
}

func (tuner *Tuner) powerOn() error {
	terrestrialLNA := byte(0x00)
	if tuner.config.TerrestrialLNA {
		terrestrialLNA = 0x02
	}
	// Terrestrial mode.
	if err := tuner.regs.Write(0x01, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x67, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x43, 0x05|terrestrialLNA); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x5e, []byte{0x15, 0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x0c, 0x14); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x99, []byte{0x7a, 0x01}); err != nil {
		return err
	}
	xtal, err := tuner.xtalCode(0x10, 0x18)
	if err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x81, []byte{
		xtal, 0x80 | 0x04, 0x80 | 0x26, 0x00, 0x00,
		0x00, 0xc4, 0x40, 0x10, 0x00,
		0x45, 0x75, 0x07, 0x1c, 0x3f,
		0x02, 0x10, 0x20, 0x0a, 0x00,
	}); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x9b, 0x00); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	status, err := tuner.readRegister(0x1a)
	if err != nil {
		return err
	}
	if status != 0x00 {
		return fmt.Errorf("CXD2858ER did not start (status %#02x)", status)
	}
	if err := tuner.regs.WriteMulti(0x17, []byte{0x90, 0x06}); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	// The chip's measured crystal trim goes back as the working value.
	trim, err := tuner.readRegister(0x19)
	if err != nil {
		return err
	}
	if err := tuner.regs.Write(0x95, trim&0xf0>>4); err != nil {
		return err
	}
	for _, step := range [][2]byte{{0x74, 0x02}, {0x88, 0x00}, {0x87, 0xc0}, {0x80, 0x01}} {
		if err := tuner.regs.Write(step[0], step[1]); err != nil {
			return err
		}
	}
	return tuner.regs.WriteMulti(0x41, []byte{0x07, 0x00})
}

// TuneTerrestrial moves the tuner to an ISDB-T frequency given in kHz.
func (tuner *Tuner) TuneTerrestrial(frequency uint32) error {
	return tuner.Tune(sony.SystemISDBT, TuneParams{FrequencyKHz: frequency, BandwidthMHz: 6})
}

// tuneISDBT points the terrestrial path at an ISDB-T channel. Its IF filter gain
// and loop filter settings are specific to this system and are not shared with
// the DVB sequences. The caller holds the lock and the gate.
func (tuner *Tuner) tuneISDBT(frequency uint32) error {
	if tuner.system.IsSatellite() {
		if err := tuner.stopSatellite(); err != nil {
			return err
		}
	}
	xtal, err := tuner.xtalCode(0x02, 0x03)
	if err != nil {
		return err
	}
	// Terrestrial mode.
	if err := tuner.regs.Write(0x01, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x74, 0x02); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x87, []byte{0xc4, 0x40}); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x91, []byte{0x10, 0x20}); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x9c, []byte{0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x5e, []byte{
		0xee, 0x02, 0x1e, 0x67, xtal, 0xb4, 0x78, 0x08, 0x30,
	}); err != nil {
		return err
	}
	if err := tuner.regs.WriteMasked(0x67, 0x00, 0x02); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x68, []byte{
		0x00, 0x88, 0x00, 0x0b, 0x22, 0x00, 0x17, 0x1b,
		byte(frequency), byte(frequency >> 8), byte(frequency>>16) & 0x0f,
		0xff, 0x01, 0x99, 0x00, 0x24, 0x87,
	}); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	if err := tuner.regs.Write(0x88, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x87, 0xc0); err != nil {
		return err
	}
	tuner.system = sony.SystemISDBT
	return nil
}

// TuneSatellite moves the tuner to an ISDB-S frequency given in kHz.
func (tuner *Tuner) TuneSatellite(frequency uint32) error {
	return tuner.Tune(sony.SystemISDBS, TuneParams{FrequencyKHz: frequency})
}

// tuneSatellite points the satellite path at a channel; the systems differ only
// in the low-pass filter cutoff. The caller holds the lock and the gate.
func (tuner *Tuner) tuneSatellite(system sony.System, frequency, symbolRateKS uint32) error {
	if tuner.system != sony.SystemUnspecified && !tuner.system.IsSatellite() {
		if err := tuner.stopTerrestrial(); err != nil {
			return err
		}
	}
	xtal, err := tuner.xtalCode(0x02, 0x03)
	if err != nil {
		return err
	}
	for _, step := range [][2]byte{{0x15, 0x02}, {0x43, 0x06}} {
		if err := tuner.regs.Write(step[0], step[1]); err != nil {
			return err
		}
	}
	if err := tuner.regs.WriteMulti(0x6a, []byte{0x00, 0x00}); err != nil {
		return err
	}
	for _, step := range [][2]byte{{0x75, 0x99}, {0x9d, 0x00}, {0x61, 0x07}, {0x01, 0x01}} {
		if err := tuner.regs.Write(step[0], step[1]); err != nil {
			return err
		}
	}
	cutoff, err := satelliteCutoffMHz(system, symbolRateKS)
	if err != nil {
		return err
	}
	satelliteLNA := byte(0x00)
	if tuner.config.SatelliteLNA {
		satelliteLNA = 0x01
	}
	// The synthesiser counts in 4 kHz steps; the +2 rounds to the nearest.
	divided := (frequency + 2) / 4
	if err := tuner.regs.WriteMulti(0x04, []byte{
		0xc4, 0x40, xtal, 0x00, 0xb4, 0x78, 0x08, 0x30,
		0xfe | satelliteLNA, 0x02, 0x1e, cutoff,
		byte(divided), byte(divided >> 8), byte(divided>>16) & 0x0f,
		0xff, 0x00, 0x01,
	}); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	if err := tuner.regs.Write(0x05, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x04, 0xc0); err != nil {
		return err
	}
	tuner.system = system
	return nil
}

// stopTerrestrial parks the terrestrial path. The gate must already be open.
func (tuner *Tuner) stopTerrestrial() error {
	if err := tuner.regs.Write(0x74, 0x02); err != nil {
		return err
	}
	if err := tuner.regs.WriteMasked(0x67, 0x00, 0xfe); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x5e, []byte{0x15, 0x00, 0x00}); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x88, 0x00); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x87, 0xc0); err != nil {
		return err
	}
	tuner.system = sony.SystemUnspecified
	return nil
}

// stopSatellite parks the satellite path. The gate must already be open.
func (tuner *Tuner) stopSatellite() error {
	terrestrialLNA := byte(0x00)
	if tuner.config.TerrestrialLNA {
		terrestrialLNA = 0x02
	}
	if err := tuner.regs.Write(0x15, 0x02); err != nil {
		return err
	}
	if err := tuner.regs.Write(0x43, 0x05|terrestrialLNA); err != nil {
		return err
	}
	if err := tuner.regs.WriteMulti(0x0c, []byte{0x14, 0x00, 0x00}); err != nil {
		return err
	}
	for _, step := range [][2]byte{{0x01, 0x00}, {0x05, 0x00}, {0x04, 0xc0}} {
		if err := tuner.regs.Write(step[0], step[1]); err != nil {
			return err
		}
	}
	tuner.system = sony.SystemUnspecified
	return nil
}

// Stop parks whichever path the tuner was using.
func (tuner *Tuner) Stop() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if tuner.system == sony.SystemUnspecified {
		return nil
	}
	return tuner.gated(func() error {
		switch tuner.system {
		case sony.SystemISDBT:
			return tuner.stopTerrestrial()
		case sony.SystemISDBS:
			return tuner.stopSatellite()
		}
		return nil
	})
}
