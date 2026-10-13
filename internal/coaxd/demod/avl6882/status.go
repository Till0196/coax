// Copyright (c) 2015 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package avl6882

import (
	"errors"
	"fmt"
	"time"
)

// Status is one status query's result.
type Status struct {
	Locked bool
	// CNRdB is in dB. HasCNR is false for DVB-C, which has no readable value.
	CNRdB  float64
	HasCNR bool
}

// Status reports lock state and, where the chip measures one, the CNR.
func (demod *Demod) Status() (Status, error) {
	if demod.system == SystemUnspecified {
		return Status{}, errors.New("no AVL6882 delivery system has been selected")
	}
	locked, err := demod.locked()
	if err != nil {
		return Status{}, err
	}
	status := Status{Locked: locked}
	if !locked {
		return status, nil
	}
	cnr, known, err := demod.cnr()
	if err != nil {
		return status, err
	}
	status.CNRdB, status.HasCNR = cnr, known
	return status, nil
}

func (demod *Demod) locked() (bool, error) {
	switch demod.system {
	case SystemDVBC:
		// DVB-C has no lock bit; mode-status byte 0x15 means locked.
		value, err := demod.read32(0x400 + rsDVBCModeStatus)
		if err != nil {
			return false, err
		}
		return value&0xff == 0x15, nil
	case SystemDVBS:
		value, err := demod.read16(0xc00 + rsDVBSxFecLock)
		if err != nil {
			return false, err
		}
		return value != 0, nil
	default:
		value, err := demod.read8(0x800 + rsDVBTxFecLock)
		if err != nil {
			return false, err
		}
		return value != 0, nil
	}
}

// cnr is avl6882fe_snr. A reading above 10000 is invalid and reported as
// unmeasured, so callers can tell "no reading" from "measured 0 dB".
func (demod *Demod) cnr() (float64, bool, error) {
	var raw uint32
	var err error
	switch demod.system {
	case SystemDVBC:
		// The DVB-C core keeps no CNR register up to date, and a reading left
		// by another system would be stale.
		return 0, false, nil
	case SystemDVBS:
		raw, err = demod.read32(0xc00 + rsDVBSxIntSNRdB)
	default:
		raw, err = demod.read16(0x800 + rsDVBTxSNRdBx100)
	}
	if err != nil {
		return 0, false, err
	}
	if raw > 10000 {
		return 0, false, nil
	}
	// Both readings are hundredths of a decibel.
	return float64(raw) / 100, true, nil
}

// The entry points from here on serve the chip's DVB-S side; no board in this
// tree calls them yet.

// SignalStrength is the raw RF AGC register, rising with signal level. Not dBm:
// unlike the CXD2878ER and M88RS6060 ports, this chip has no conversion curve.
func (demod *Demod) SignalStrength() (uint16, error) {
	value, err := demod.read16(0x0a4 + rsRFAGC)
	if err != nil {
		return 0, err
	}
	return uint16(value), nil
}

// SetVoltage drives the LNB supply through the chip's GPIOs: high selects 18 V
// (horizontal/left), false 13 V (vertical/right); off cuts power and wins.
func (demod *Demod) SetVoltage(high, off bool) error {
	power, voltage := uint32(gpioLevel0), uint32(gpioLevel0)
	switch {
	case off:
		power, voltage = gpioLevel1, gpioLevel0
	case high:
		// 18 V is selected by floating the voltage pin, not driving it high.
		power, voltage = gpioLevel0, gpioHighZ
	}
	if err := demod.gpioSet(gpioLNBPower, power); err != nil {
		return err
	}
	return demod.gpioSet(gpioLNBVoltage, voltage)
}

// SetTone enables or disables the continuous 22 kHz DiSEqC tone.
func (demod *Demod) SetTone(on bool) error {
	control, err := demod.read32(0x16c000 + diseqcTxControl)
	if err != nil {
		return err
	}
	if on {
		control &^= 0x07
		control |= 0x03    // continuous mode
		control |= 1 << 10 // tone on
	} else {
		control &^= 0x0c00
	}
	return demod.write32(0x16c000+diseqcTxControl, control)
}

// ErrDiSEqCTimeout reports that the chip never finished transmitting.
var ErrDiSEqCTimeout = errors.New("AVL6882 DiSEqC transmission timed out")

// SendDiSEqCMasterCommand transmits a DiSEqC command (up to 8 bytes), turning a
// running continuous tone off for the transmission and back on afterwards.
func (demod *Demod) SendDiSEqCMasterCommand(msg []byte) error {
	if len(msg) > 8 {
		return fmt.Errorf("an AVL6882 DiSEqC message may be at most 8 bytes, got %d", len(msg))
	}
	// Reset the receive FIFO.
	rxControl, err := demod.read32(0x16c000 + diseqcRxControl)
	if err != nil {
		return err
	}
	if err := demod.write32(0x16c000+diseqcRxControl, rxControl|1); err != nil {
		return err
	}
	if err := demod.write32(0x16c000+diseqcRxControl, rxControl&^1); err != nil {
		return err
	}

	control, err := demod.read32(0x16c000 + diseqcTxControl)
	if err != nil {
		return err
	}
	toneWasOn := control&0x400 != 0
	if toneWasOn {
		control &^= 0x0c00
	}

	// Modulation mode, then load the FIFO.
	control &^= 0x07
	if err := demod.write32(0x16c000+diseqcTxControl, control); err != nil {
		return err
	}
	for _, b := range msg {
		if err := demod.write32(0x16c000+diseqcTxFIFO, uint32(b)); err != nil {
			return err
		}
	}
	time.Sleep(20 * time.Millisecond)

	// Start transmitting and wait for the done bit.
	control |= 4
	if err := demod.write32(0x16c000+diseqcTxControl, control); err != nil {
		return err
	}
	done := false
	for range 25 {
		status, err := demod.read32(0x16c000 + diseqcTxStatus)
		if err != nil {
			return err
		}
		if status&0x40 != 0 {
			done = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if toneWasOn {
		control &^= 0x07
		control |= 0x403
		if err := demod.write32(0x16c000+diseqcTxControl, control); err != nil {
			return err
		}
	}
	if !done {
		return ErrDiSEqCTimeout
	}
	return nil
}
