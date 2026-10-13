// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (cxd2856er.c) by nns779.

// Package cxd2856er drives the Sony CXD2856ER, a combined ISDB-T and ISDB-S
// demodulator. One chip does either system, switched by putting it to sleep and
// waking it into the other.
//
// The chip answers on two I2C addresses: a system address (SLV-X) for power and
// clocking, and a demodulator address (SLV-T) whose register space is banked --
// sony.SLVX and sony.SLVT here. The tuner sits behind this chip's I2C gate,
// which TunerBus exposes as an ordinary bus.
package cxd2856er

import (
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// Which member of the family is fitted is read from the chip rather than declared by the
// board: Initialize asks, and what it finds decides which systems Supports will admit.
// Sony's published standards, quoted as written:
//
//	CXD2854ER  DVB-T2/T/C2/C/S2/S, ISDB-T/SBTVD-T, ISDB-S
//	CXD2856ER  ISDB-T/S, J.83A/B/C, J.382, DVB-T2/T/C2/C/S2/S
//	CXD2857ER  ISDB-T/C/S/S3, J.83A/B/C, J.382, DVB-T/T2/C/C2/S/S2
//
// The lists are less far apart than they look: DVB-C is J.83 Annex A and DVB-C2 is J.382,
// and Annex C is the Annex A core at another roll-off -- so ISDB-C needs nothing the family
// lacks, and this driver receives it as sony.SystemDVBC. What the CXD2854ER's list lacks
// is Annex B, the trellis-coded core the mode register reaches with 0x08; the CXD2856ER
// lists it, so the system is offered.
//
// The adjacent numbers are not all demodulators: the CXD2858ER and CXD2868ER are tuners.

// Demod is a CXD2856ER reached over an I2C bus, embedding the family's shared
// core; this package holds this chip's own wake-up and teardown sequences.
//
// Unlike the CXD2857ER and CXD2878 ports it carries a lock: its boards reach
// the tuner through this chip's I2C gate from a bus shared with the other
// tuners on the same bridge. Exported methods serialise against that; the
// unexported ones assume the caller has.
type Demod struct {
	*sony.Core

	mu sync.Mutex
}

// New addresses a CXD2856ER. Both I2C addresses are given rather than derived:
// this board's bridge uses eight-bit addresses, where the step between them is
// four rather than the two sony.Config derives.
func New(bus i2c.Bus, systemAddress, demodAddress byte, config sony.Config) *Demod {
	config.DemodAddress = demodAddress
	config.SystemAddress = systemAddress
	return &Demod{Core: sony.NewCore(bus, config)}
}

// Supports reports whether the fitted part can demodulate a system.
func (demod *Demod) Supports(system sony.System) bool {
	if _, err := demodMode(system); err != nil {
		return false
	}
	return true
}

// ChipID asks the chip which member of the family it is. The identifier is ten
// bits across two registers on the system address.
func (demod *Demod) ChipID() (sony.Chip, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if err := demod.readChipID(); err != nil {
		return 0, err
	}
	return demod.Chip, nil
}

// readChipID does the read; the caller holds the lock. Not
// sony.Core.ReadChipID: the shared one takes a single banked read on the
// demodulator address, while this part answers across two registers on the
// system address with the bank selected first.
func (demod *Demod) readChipID() error {
	if err := demod.Bank(0x00); err != nil {
		return err
	}
	high, err := demod.Read(sony.SLVX, 0xfb, 1)
	if err != nil {
		return err
	}
	low, err := demod.Read(sony.SLVX, 0xfd, 1)
	if err != nil {
		return err
	}
	demod.Chip = sony.Chip(uint16(high[0]&0x03)<<8 | uint16(low[0]))
	return nil
}

// CurrentSystem reports which system the chip is woken into. The embedded
// core's System field says the same, but this takes the lock, for a caller
// looking at a chip another goroutine may be retuning.
func (demod *Demod) CurrentSystem() sony.System {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.System
}

// Initialize powers the chip up into its sleep state.
func (demod *Demod) Initialize() error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if demod.Config().XtalCode != sony.Xtal24000KHz {
		return fmt.Errorf("CXD2856ER needs a 24 MHz crystal, but the board says %d kHz (clock-mode code %d)",
			demod.Config().XtalKHz(), demod.Config().XtalCode)
	}
	demod.State = sony.StateUnknown
	demod.System = sony.SystemUnspecified
	demod.Chip = 0

	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x10, Value: 0x01},
		{Where: sony.SLVX, Register: 0x18, Value: 0x01},
		{Where: sony.SLVX, Register: 0x28, Value: 0x13},
		{Where: sony.SLVX, Register: 0x17, Value: 0x01},
		{Where: sony.SLVX, Register: 0x1d, Value: 0x00},
		{Where: sony.SLVX, Register: 0x14, Value: demod.Config().XtalCode},
		{Where: sony.SLVX, Register: 0x1c, Value: 0x03},
	}); err != nil {
		return err
	}
	time.Sleep(4 * time.Millisecond)
	if err := demod.Write(sony.SLVX, 0x50, 0x00); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	if err := demod.Write(sony.SLVX, 0x10, 0x00); err != nil {
		return err
	}
	time.Sleep(time.Millisecond)
	// What answers decides which systems Supports will admit.
	if err := demod.readChipID(); err != nil {
		return err
	}

	demod.State = sony.StateSleep

	tunerBridge := byte(0x00)
	if demod.Config().TunerI2C {
		tunerBridge = 0x01
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x1a, Value: tunerBridge},
	})
}

// OpenTunerGate opens or closes the I2C pass-through to the tuner, satisfying
// i2c.Gate.
func (demod *Demod) OpenTunerGate(open bool) error {
	value := byte(0x00)
	if open {
		value = 0x01
	}
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.Write(sony.SLVX, 0x08, value)
}

// TunerBus exposes the tuner behind the chip's I2C gate as an ordinary bus.
func (demod *Demod) TunerBus() i2c.Bus { return i2c.Gated(demod.Bus(), demod) }

// setTSClock configures the transport stream output clock for a system.
func (demod *Demod) setTSClock(system sony.System) error {
	if err := demod.Bank(0x00); err != nil {
		return err
	}
	// The chip latches its output configuration from this register, so read it
	// before touching the rest.
	if _, err := demod.Read(sony.SLVT, 0xc4, 1); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0xd3, Value: 0x01, Mask: 0x01},
		{Where: sony.SLVT, Register: 0xde, Value: 0x00, Mask: 0x01},
		{Where: sony.SLVT, Register: 0xda, Value: 0x00, Mask: 0x01},
		{Where: sony.SLVT, Register: 0xc4, Value: 0x00, Mask: 0x03},
		{Where: sony.SLVT, Register: 0xd1, Value: 0x02, Mask: 0x03},
		{Where: sony.SLVT, Register: 0xd9, Value: 0x10},
		{Where: sony.SLVT, Register: 0x32, Value: 0x00, Mask: 0x01},
	}); err != nil {
		return err
	}
	var rate byte
	switch system {
	// ISDB-T and ISDB-S each take a rate of their own, known to work on the
	// existing hardware; every other system shares the single rate the
	// CXD2857ER uses throughout.
	case sony.SystemISDBT:
		rate = 0x02
	case sony.SystemISDBS:
		rate = 0x00
	case sony.SystemDVBC, sony.SystemJ83B, sony.SystemDVBT, sony.SystemDVBT2, sony.SystemDVBS, sony.SystemDVBS2:
		rate = 0x01
	default:
		return sony.ErrNoSystem
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x33, Value: rate, Mask: 0x03},
		{Where: sony.SLVT, Register: 0x32, Value: 0x01, Mask: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0x66, Value: 0x01, Mask: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x40},
		{Where: sony.SLVT, Register: 0x66, Value: 0x01, Mask: 0x01},
	})
}

// setTSPinState starts or stops the transport stream output pins. Which pin
// mask applies depends on how the board wired the output.
func (demod *Demod) setTSPinState(enable bool) error {
	if err := demod.Bank(0x00); err != nil {
		return err
	}
	current, err := demod.Read(sony.SLVT, 0xc4, 1)
	if err != nil {
		return err
	}
	var mask byte
	switch current[0] & 0x88 {
	case 0x80:
		mask = 0x01
	case 0x88:
		mask = 0x80
	default:
		mask = 0xff
	}
	if err := demod.Bank(0x00); err != nil {
		return err
	}
	value := byte(0xff)
	if enable {
		value = 0x00
	}
	return demod.WriteMasked(sony.SLVT, 0x81, value, mask)
}

// sleepISDBT parks the chip out of ISDB-T mode.
func (demod *Demod) sleepISDBT() error {
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xc3, Value: 0x01},
		{Where: sony.SLVT, Register: 0x80, Value: 0x1f, Mask: 0x1f},
	}); err != nil {
		return err
	}
	if err := demod.setTSPinState(false); err != nil {
		return err
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0x69, Value: 0x05},
		{Where: sony.SLVT, Register: 0x6b, Value: 0x07},
		{Where: sony.SLVT, Register: 0x9d, Value: 0x14},
		{Where: sony.SLVT, Register: 0xd3, Value: 0x00},
		{Where: sony.SLVT, Register: 0xed, Value: 0x01},
		{Where: sony.SLVT, Register: 0xe2, Value: 0x4e},
		{Where: sony.SLVT, Register: 0xf2, Value: 0x03},
		{Where: sony.SLVT, Register: 0xde, Value: 0x32},
		{Where: sony.SLVT, Register: 0x00, Value: 0x15},
		{Where: sony.SLVT, Register: 0xde, Value: 0x03},
		{Where: sony.SLVT, Register: 0x00, Value: 0x17},
		{Where: sony.SLVT, Register: 0x38, Data: []byte{0x01, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x1e},
		{Where: sony.SLVT, Register: 0x73, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x63},
		{Where: sony.SLVT, Register: 0x81, Value: 0x01},
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x49, Value: 0x33},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x21},
		{Where: sony.SLVT, Register: 0xfe, Value: 0x01},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: 0x01},
	})
}

// sleepISDBS parks the chip out of ISDB-S mode.
func (demod *Demod) sleepISDBS() error {
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xc3, Value: 0x01},
		{Where: sony.SLVT, Register: 0x80, Value: 0x1f, Mask: 0x1f},
	}); err != nil {
		return err
	}
	if err := demod.setTSPinState(false); err != nil {
		return err
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x11},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x21},
		{Where: sony.SLVX, Register: 0x28, Value: 0x13},
		{Where: sony.SLVT, Register: 0xfe, Value: 0x01},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2d, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0xa0},
		{Where: sony.SLVT, Register: 0xd7, Value: 0x00},
	})
}

// Sleep parks the chip whichever system it was woken into.
func (demod *Demod) Sleep() error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.sleep()
}

func (demod *Demod) sleep() error {
	if demod.State == sony.StateSleep {
		return nil
	}
	var err error
	switch {
	case demod.System == sony.SystemUnspecified:
	case demod.System.IsSatellite():
		err = demod.sleepSatellite()
	default:
		err = demod.sleepTerrestrial()
	}
	demod.State = sony.StateSleep
	demod.System = sony.SystemUnspecified
	return err
}

// setBandwidthISDBT loads the channel filter. Only 6 MHz, the ISDB-T channel
// width, is defined.
func (demod *Demod) setBandwidthISDBT(bandwidthMHz uint32) error {
	if err := demod.Bank(0x10); err != nil {
		return err
	}
	band, ok := isdbtBands[bandwidthMHz]
	if !ok {
		return fmt.Errorf("CXD2856ER does not support a %d MHz channel", bandwidthMHz)
	}
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x9f, Data: band.nominalRate},
		{Where: sony.SLVT, Register: 0xa6, Data: band.itbCoef},
		// This IF triple is a literal rather than a value computed from an IF
		// frequency.
		{Where: sony.SLVT, Register: 0xb6, Data: []byte{0x12, 0xee, 0xef}},
		{Where: sony.SLVT, Register: 0xd7, Value: band.channelWidth},
		{Where: sony.SLVT, Register: 0xd9, Data: band.trl},
		{Where: sony.SLVT, Register: 0x00, Value: 0x12},
		{Where: sony.SLVT, Register: 0x71, Value: 0x07},
		{Where: sony.SLVT, Register: 0x00, Value: 0x15},
		{Where: sony.SLVT, Register: 0xbe, Value: 0x02},
	})
}

// wakeupISDBT brings the chip up in ISDB-T mode.
func (demod *Demod) wakeupISDBT(bandwidthMHz uint32) error {
	if err := demod.setTSClock(sony.SystemISDBT); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: 0x06},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x01},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x74},
		{Where: sony.SLVT, Register: 0x49, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x50},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0xa5, Value: 0x01},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x10},
		{Where: sony.SLVT, Register: 0x69, Value: 0x04},
		{Where: sony.SLVT, Register: 0x6b, Value: 0x03},
		{Where: sony.SLVT, Register: 0x9d, Value: 0x50},
		{Where: sony.SLVT, Register: 0xd3, Value: 0x06},
		{Where: sony.SLVT, Register: 0xed, Value: 0x00},
		{Where: sony.SLVT, Register: 0xe2, Value: 0xce},
		{Where: sony.SLVT, Register: 0xf2, Value: 0x13},
		{Where: sony.SLVT, Register: 0xde, Value: 0x2e},
		{Where: sony.SLVT, Register: 0x00, Value: 0x15},
		{Where: sony.SLVT, Register: 0xde, Value: 0x02},
		{Where: sony.SLVT, Register: 0x00, Value: 0x17},
		{Where: sony.SLVT, Register: 0x38, Data: []byte{0x00, 0x03}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x1e},
		{Where: sony.SLVT, Register: 0x73, Value: 0x68},
		{Where: sony.SLVT, Register: 0x00, Value: 0x63},
		{Where: sony.SLVT, Register: 0x81, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x11},
		{Where: sony.SLVT, Register: 0x33, Data: []byte{0x00, 0x03, 0x3b}},
		{Where: sony.SLVT, Register: 0x00, Value: 0x60},
		{Where: sony.SLVT, Register: 0xa8, Data: []byte{0xb7, 0x1b}},
	}); err != nil {
		return err
	}
	if err := demod.setBandwidthISDBT(bandwidthMHz); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x08, Mask: 0x1f},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

// wakeupISDBS brings the chip up in ISDB-S mode.
func (demod *Demod) wakeupISDBS() error {
	if err := demod.setTSClock(sony.SystemISDBS); err != nil {
		return err
	}
	if err := demod.Run([]sony.Op{
		{Where: sony.SLVX, Register: 0x00, Value: 0x00},
		{Where: sony.SLVX, Register: 0x17, Value: 0x0c},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2d, Value: 0x00},
		{Where: sony.SLVT, Register: 0xa9, Value: 0x00},
		{Where: sony.SLVT, Register: 0x2c, Value: 0x01},
		{Where: sony.SLVX, Register: 0x28, Value: 0x31},
		{Where: sony.SLVT, Register: 0x4b, Value: 0x31},
		{Where: sony.SLVT, Register: 0x6a, Value: 0x00},
		{Where: sony.SLVX, Register: 0x18, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x20, Value: 0x01},
		{Where: sony.SLVT, Register: 0xce, Data: []byte{0x00, 0x00}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xae},
		{Where: sony.SLVT, Register: 0x20, Data: []byte{0x07, 0x37, 0x0a}},
		{Where: sony.SLVT, Register: 0x00, Value: 0xa0},
		{Where: sony.SLVT, Register: 0xd7, Value: 0x00},
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0x80, Value: 0x10, Mask: 0x1f},
	}); err != nil {
		return err
	}
	return demod.setTSPinState(true)
}

// Wakeup brings the chip up in a system, or restarts acquisition if it is
// already in that one.
func (demod *Demod) Wakeup(system sony.System, params sony.WakeupParams) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if _, err := demodMode(system); err != nil {
		return err
	}
	if !demod.Supports(system) {
		return fmt.Errorf("%s does not support %s", demod.Chip, system)
	}
	if demod.State == sony.StateActive {
		if demod.System == system {
			// Already in this system: a reset restarts acquisition without the
			// full wake-up sequence.
			if err := demod.Run([]sony.Op{
				{Where: sony.SLVT, Register: 0x00, Value: 0x00},
				{Where: sony.SLVT, Register: 0xc3, Value: 0x01},
			}); err != nil {
				return err
			}
			return demod.setBandwidth(system, params)
		}
		if err := demod.sleep(); err != nil {
			return err
		}
	}
	var err error
	switch system {
	case sony.SystemISDBT:
		err = demod.wakeupISDBT(params.BandwidthMHz)
	case sony.SystemISDBS:
		err = demod.wakeupISDBS()
	case sony.SystemDVBT:
		err = demod.wakeupDVBT(params.BandwidthMHz)
	case sony.SystemDVBT2:
		err = demod.wakeupDVBT2(params.BandwidthMHz, params.PLPID)
	case sony.SystemDVBC:
		err = demod.wakeupJ83(system)
	case sony.SystemJ83B:
		err = demod.wakeupJ83B(params.SymbolRateSPS)
	case sony.SystemDVBS, sony.SystemDVBS2:
		err = demod.wakeupDVBS(system, params.SymbolRateSPS/1000)
	default:
		return sony.ErrNoSystem
	}
	if err != nil {
		return err
	}
	demod.System = system
	demod.State = sony.StateActive
	return nil
}

// setBandwidth reloads only the channel filter, which is all a re-acquisition
// on the same system needs. The satellite systems have no such filter.
func (demod *Demod) setBandwidth(system sony.System, params sony.WakeupParams) error {
	switch system {
	case sony.SystemISDBT:
		return demod.setBandwidthISDBT(params.BandwidthMHz)
	case sony.SystemDVBT:
		return demod.setBandwidthDVBT(params.BandwidthMHz)
	case sony.SystemDVBT2:
		return demod.setBandwidthDVBT2(params.BandwidthMHz)
	case sony.SystemDVBC:
		return demod.setBandwidthJ83(system)
	case sony.SystemJ83B:
		return demod.setBandwidthJ83B(params.SymbolRateSPS)
	default:
		return nil
	}
}

// PostTune releases the acquisition hold once the tuner has settled.
func (demod *Demod) PostTune() error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return demod.Run([]sony.Op{
		{Where: sony.SLVT, Register: 0x00, Value: 0x00},
		{Where: sony.SLVT, Register: 0xfe, Value: 0x01},
		{Where: sony.SLVT, Register: 0xc3, Value: 0x00},
	})
}

// ISDBSSlots is how many relative stream slots an ISDB-S carrier is read
// through here, and the number to test a stream identifier against to tell a
// relative slot from a TSID. Not the same on every chip: a TC90522 holds twelve.
const ISDBSSlots = 8

// SetSlotISDBS selects an ISDB-S stream by its relative slot, 0 to 7.
func (demod *Demod) SetSlotISDBS(slot uint16) error {
	if slot >= ISDBSSlots {
		return fmt.Errorf("CXD2856ER slot %d is outside 0..%d", slot, ISDBSSlots-1)
	}
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if err := demod.Bank(0xc0); err != nil {
		return err
	}
	return demod.WriteMulti(sony.SLVT, 0xe9, []byte{0x00, byte(slot), 0x01})
}

// SetTSIDISDBS selects an ISDB-S stream by its TSID.
func (demod *Demod) SetTSIDISDBS(tsid uint16) error {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	if err := demod.Bank(0xc0); err != nil {
		return err
	}
	return demod.WriteMulti(sony.SLVT, 0xe9, []byte{byte(tsid >> 8), byte(tsid), 0x00})
}
