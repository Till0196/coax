// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TurboSight's driver for the TBS 6812 (a.k.a. PT4K).
//
// Package cxd2857er drives the satellite-capable member of the Sony CXD2878ER
// family. It is separate from its terrestrial siblings because its tuner has a
// satellite side as well as a terrestrial one and is powered up differently,
// its cable half is arranged differently, and it has the MMT/TLV output ISDB-S3
// needs. What the drivers share lives in internal/coaxd/demod/sony.
//
// The tuner is a chip of its own here: a TBS 6812 carries a CXD2857ER and a
// CXD2868ER side by side. The CXD2868ER is a Freia, which is where the
// satellite side comes from. Everything below reaches it through the
// demodulator's I2C repeater.
package cxd2857er

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// Demod is a CXD2857ER reached over an I2C bus. Not safe for concurrent use: a
// caller sharing one between goroutines must serialise access itself.
type Demod struct {
	*sony.Core
	// side is which half of the two-sided tuner is in use. Crossing between
	// them takes a handover that must not be repeated on every tune.
	side tunerSide
}

// New addresses a CXD2857ER on the given bus.
func New(bus i2c.Bus, config sony.Config) *Demod {
	return &Demod{Core: sony.NewCore(bus, config)}
}

// Initialize powers the chip up, checks it really is a CXD2857ER, brings the
// tuner up and configures the output, leaving the chip asleep.
func (demod *Demod) Initialize() error {
	if err := demod.Write(sony.SLVX, 0x02, 0x00); err != nil {
		return fmt.Errorf("reset CXD2857ER: %w", err)
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
	if err := demod.Write(sony.SLVX, 0x50, 0x00); err != nil {
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
	if demod.Chip != sony.ChipCXD2857 {
		return fmt.Errorf("this driver is for the CXD2857ER, but the chip answered %s", demod.Chip)
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x95},
		{Where: sony.SLVT, Register: 0x23, Value: 0x03},
	}); err != nil {
		return err
	}
	demod.State = sony.StateSleep

	// Enable the tuner I2C bridge, then bring the tuner up behind it.
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x1a, Value: 0x01, Mask: 0xff},
	}); err != nil {
		return err
	}
	time.Sleep(2 * time.Millisecond)
	if err := demod.initTuner(); err != nil {
		return fmt.Errorf("initialize the tuner: %w", err)
	}
	if demod.Config().TLVMode {
		if err := demod.setTLVOutput(); err != nil {
			return fmt.Errorf("configure the MMT/TLV output: %w", err)
		}
	}
	return demod.ConfigureTS()
}

// Tune points the tuner at a frequency in kHz. It has to run after Wakeup,
// which tells the tuner which side and which table to use.
func (demod *Demod) Tune(frequencyKHz uint32) error {
	if demod.System.IsSatellite() {
		return demod.tuneSatellite(frequencyKHz, demod.System)
	}
	return demod.tuneTerrestrial(frequencyKHz)
}

// Wakeup brings the chip up in a system, or restarts acquisition if it is
// already in that one.
func (demod *Demod) Wakeup(system sony.System, params sony.WakeupParams) error {
	demod.Bandwidth = params.BandwidthMHz
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
	// The transport stream clock is set up before the system's own wake-up:
	// one of its bits depends on which system that is. The satellite sequences
	// carry their own.
	if !system.IsSatellite() {
		if err := demod.SetTSClockMode(system); err != nil {
			return err
		}
	}
	var err error
	switch system {
	case sony.SystemDVBT:
		err = demod.WakeupDVBT(params.BandwidthMHz)
	case sony.SystemDVBT2:
		err = demod.WakeupDVBT2(params.BandwidthMHz, sony.PLPOrZero(params.PLPID))
	case sony.SystemISDBT:
		err = demod.WakeupISDBT(params.BandwidthMHz)
	case sony.SystemDVBC:
		err = demod.WakeupCable(sony.SystemDVBC, params.BandwidthMHz, params.SymbolRateSPS)
	case sony.SystemJ83B:
		err = demod.WakeupCable(sony.SystemJ83B, params.BandwidthMHz, params.SymbolRateSPS)
	case sony.SystemISDBS:
		if err = demod.setISDBSStreamID(params.StreamID); err == nil {
			err = demod.wakeupISDBS()
		}
	case sony.SystemISDBS3:
		if err = demod.setISDBS3StreamID(params.StreamID); err == nil {
			err = demod.wakeupISDBS3()
		}
	case sony.SystemDVBS, sony.SystemDVBS2:
		err = demod.wakeupDVBS(system, params.SymbolRateSPS)
	default:
		return sony.ErrNoSystem
	}
	if err != nil {
		return err
	}
	demod.System = system
	demod.State = sony.StateActive
	return demod.TuneEnd()
}

// retuneBand reapplies only what a move within the same system needs.
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
	case sony.SystemISDBS:
		if err := demod.setISDBSStreamID(params.StreamID); err != nil {
			return err
		}
		return demod.Run([]sony.Op{
			{Where: sony.SLVT, Register: 0x00, Value: 0x00},
			{Where: sony.SLVT, Register: 0xc3, Value: 0x01},
		})
	case sony.SystemISDBS3:
		if err := demod.setISDBS3StreamID(params.StreamID); err != nil {
			return err
		}
		return demod.Run([]sony.Op{
			{Where: sony.SLVT, Register: 0x00, Value: 0x01},
			{Where: sony.SLVT, Register: 0xc0, Value: 0x01},
		})
	case sony.SystemDVBS, sony.SystemDVBS2:
		return demod.retuneDVBS(system, params.SymbolRateSPS)
	default:
		return sony.ErrNoSystem
	}
}

// Lock reports how far acquisition has got in the system the chip is in.
func (demod *Demod) Lock() (sony.Lock, error) {
	switch demod.System {
	case sony.SystemDVBT:
		return demod.LockDVBT()
	case sony.SystemDVBT2:
		return demod.LockDVBT2()
	case sony.SystemISDBT:
		return demod.LockISDBT()
	case sony.SystemDVBC, sony.SystemJ83B:
		return demod.LockCable()
	case sony.SystemISDBS, sony.SystemDVBS, sony.SystemDVBS2:
		// These three read their lock the same way.
		return demod.lockSatellite(3)
	case sony.SystemISDBS3:
		return demod.lockSatellite(2)
	default:
		return sony.Lock{}, sony.ErrNoSystem
	}
}

// CNR reports the carrier-to-noise ratio in dB.
func (demod *Demod) CNR() (float64, error) {
	switch demod.System {
	case sony.SystemISDBS, sony.SystemDVBS, sony.SystemDVBS2:
		// Also shared: the satellite curve is picked by symbol rate rather
		// than by standard, and everything but ISDB-S3 falls on this one.
		mdB, err := demod.cnrSatellite()
		if err != nil {
			return 0, err
		}
		return float64(mdB) / 1000, nil
	case sony.SystemISDBS3:
		mdB, err := demod.cnrISDBS3()
		if err != nil {
			return 0, err
		}
		return float64(mdB) / 1000, nil
	}
	var mdB int32
	var err error
	switch demod.System {
	case sony.SystemDVBT:
		mdB, err = demod.CNRDVBT(0x10, 0x1a0, 0, 0)
	case sony.SystemISDBT:
		mdB, err = demod.CNRISDBT()
	case sony.SystemDVBC, sony.SystemJ83B:
		mdB, err = demod.CNRCable()
	default:
		return 0, sony.ErrNoSystem
	}
	if err != nil {
		return 0, err
	}
	return float64(mdB)/1000 - 1.5, nil
}

// SignalStrength reports the input level in dBm.
func (demod *Demod) SignalStrength(frequencyKHz uint32) (float64, error) {
	// The satellite side does not use the tuner's reading: the level comes from
	// the demodulator's IF AGC, on a scale fixed to the chip's -97 dBm
	// reference point.
	if demod.System.IsSatellite() {
		return demod.satelliteLevel()
	}
	rssi, err := demod.readRSSI(frequencyKHz)
	if err != nil {
		return 0, err
	}
	// This chip's own scale, not the family's: a flat 4.5 dB comes off whatever
	// the system, where the others subtract a per-system IF level and add a
	// calibration offset.
	return float64(rssi)/100 - 4.5, nil
}

// ChipID reports which member of the family answered, as Initialize read and
// checked before anything else ran (via sony.Core.ReadChipID), so a board's
// claim about what it fits can be checked against the part.
func (demod *Demod) ChipID() sony.Chip { return demod.Chip }
