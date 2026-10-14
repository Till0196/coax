// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TBS linux_media (cxd2878.c). The package is named after that file
// rather than the part, which is why this one is cxd2878 and the package beside
// it is cxd2857er.
//
// Package cxd2878 drives the terrestrial and cable members of the family:
// CXD2878ER, CXD6801GL, CXD6802GL, CXD6822GL and CXD2878A. Which tuner sits
// behind the demodulator -- the older ASCOT3 or the newer Freia -- follows from
// the chip ID at runtime.
//
// None of these reaches satellite, which is the tuner's doing rather than the
// demodulator's. The satellite-capable CXD2857ER has its own package; what they
// share lives in internal/coaxd/demod/sony.
package cxd2878

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/pkg/i2c"
)

// Demod is one of these chips reached over an I2C bus. It is not safe for
// concurrent use: there is no internal lock, so a caller that shares one
// between goroutines has to serialise access itself.
type Demod struct {
	*sony.Core
}

// New addresses a chip on the given bus.
func New(bus i2c.Bus, config sony.Config) *Demod {
	return &Demod{Core: sony.NewCore(bus, config)}
}

// usesASCOT3 reports whether this chip revision embeds the older ASCOT3 tuner
// core. Only CXD6802 does; every other revision this package drives uses
// Freia.
func (demod *Demod) usesASCOT3() bool { return demod.Chip == sony.ChipCXD6802 }

// Initialize powers the chip up, identifies the silicon revision and tuner, and
// brings the tuner up, leaving the chip asleep.
func (demod *Demod) Initialize() error {
	if err := demod.Write(sony.SLVX, 0x02, 0x00); err != nil {
		return fmt.Errorf("reset CXD2878ER: %w", err)
	}
	time.Sleep(4 * time.Millisecond)
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x1d, Value: 0x00},
		{Where: sony.SLVX, Register: 0x14, Value: demod.Config().XtalCode},
	}); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x50, Value: 0x00},
	}); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := demod.Write(sony.SLVX, 0x10, 0x00); err != nil {
		return err
	}
	time.Sleep(21 * time.Millisecond)

	if err := demod.ReadChipID(); err != nil {
		return err
	}

	if demod.usesASCOT3() {
		if err := demod.Run([]sony.Op{
			{Where: sony.SLVT, Register: 0x00, Value: 0x9c},
			{Where: sony.SLVT, Register: 0x10, Data: []byte{0x00, 0x00, 0x00, 0x00}},
		}); err != nil {
			return err
		}
	} else {
		if err := demod.Run([]sony.Op{
			{Where: sony.SLVT, Register: 0x00, Value: 0x95},
			{Where: sony.SLVT, Register: 0x23, Value: 0x03},
		}); err != nil {
			return err
		}
	}
	demod.State = sony.StateSleep

	// Enable the tuner I2C bridge, then bring the tuner up.
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x1a, Value: 0x01, Mask: 0xff},
	}); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	var tunerErr error
	if demod.usesASCOT3() {
		tunerErr = demod.ascot3Init()
	} else {
		tunerErr = demod.freiaInit()
	}
	if tunerErr != nil {
		return fmt.Errorf("initialize the tuner: %w", tunerErr)
	}

	return demod.ConfigureTS()
}

// Tune points the tuner at a frequency. It has to run after Wakeup, which
// tells the tuner code which system and bandwidth table to use.
func (demod *Demod) Tune(frequencyKHz uint32) error {
	if demod.usesASCOT3() {
		return demod.ascot3Tune(frequencyKHz)
	}
	return demod.freiaTune(frequencyKHz)
}

// Wakeup brings the chip up in a system, or restarts acquisition if it is
// already in that one.
func (demod *Demod) Wakeup(system sony.System, params sony.WakeupParams) error {
	bandwidth := params.BandwidthMHz
	demod.Bandwidth = bandwidth
	if demod.State == sony.StateActive {
		if demod.System == system {
			if err := demod.SetStreamOutput(false); err != nil {
				return err
			}
			return demod.retuneBand(system, params)
		}
		if err := demod.Sleep(); err != nil {
			return err
		}
	}
	// The TS clock has to be set up before the system's own wake-up sequence,
	// because one of its bits depends on which system that is. The retune
	// path above deliberately skips it.
	if !system.IsSatellite() {
		if err := demod.SetTSClockMode(system); err != nil {
			return err
		}
	}
	var err error
	switch system {
	case sony.SystemDVBT:
		err = demod.WakeupDVBT(bandwidth)
	case sony.SystemDVBT2:
		err = demod.WakeupDVBT2(bandwidth, sony.PLPOrZero(params.PLPID))
	case sony.SystemDVBC:
		err = demod.WakeupCable(sony.SystemDVBC, bandwidth, 0)
	case sony.SystemISDBT:
		err = demod.WakeupISDBT(bandwidth)
	case sony.SystemJ83B:
		err = demod.WakeupCable(sony.SystemJ83B, bandwidth, params.SymbolRateSPS)
	case sony.SystemATSC:
		err = demod.WakeupATSC()
	default:
		return sony.ErrNoSystem
	}
	if err != nil {
		return err
	}
	demod.System = system
	demod.State = sony.StateActive
	if system == sony.SystemATSC {
		// The ATSC core is retriggered rather than soft-reset, so tuneEnd is
		// skipped for it entirely.
		return nil
	}
	return demod.TuneEnd()
}

// retuneBand reloads only the channel filter for a re-acquisition on the same
// system, which is all the "Active, same system" case needs.
func (demod *Demod) retuneBand(system sony.System, params sony.WakeupParams) error {
	switch system {
	case sony.SystemDVBT:
		return demod.SetBandwidthDVBT(params.BandwidthMHz)
	case sony.SystemDVBT2:
		return demod.SetBandwidthDVBT2(params.BandwidthMHz)
	case sony.SystemISDBT:
		return demod.RetuneISDBT(params.BandwidthMHz)
	case sony.SystemDVBC, sony.SystemJ83B:
		return demod.SetCableBand(system, params.BandwidthMHz, params.SymbolRateSPS)
	case sony.SystemATSC:
		return demod.SetStreamOutput(false)
	default:
		return sony.ErrNoSystem
	}
}

// ChipID reports which member of the family answered, as Initialize read it
// through sony.Core.ReadChipID.
func (demod *Demod) ChipID() sony.Chip { return demod.Chip }

// TunedKHz reads back where the tuner is actually pointed, which is not always
// where Tune put it: on a card whose transport is a vendor driver's graph, the
// vendor tunes the hardware itself and its tune can land after this one. Both
// tuner cores keep the frequency in the same three registers, at the top of
// the tuning packet written from 0x68; only the top byte's width differs.
func (demod *Demod) TunedKHz() (uint32, error) {
	top := byte(0x1f)
	if demod.usesASCOT3() {
		top = 0x0f
	}
	value, err := demod.Tuner().Read(0x70, 3)
	if err != nil {
		return 0, err
	}
	return uint32(value[0]) | uint32(value[1])<<8 | uint32(value[2]&top)<<16, nil
}
