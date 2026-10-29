// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"errors"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/demod/stid135"
	"github.com/Till0196/coax/pkg/i2c"
)

// The vendor's S2X cards are built round an STMicroelectronics STiD135: eight
// demodulators and their front ends in one package behind a single I2C address,
// a demodulator chosen by the base address of its register block, four aerial
// inputs shared between the eight.
//
// The chip driver is a partial port that does not bring the chip up -- that is
// thousands of register defaults plus analogue self-calibration, neither
// checkable without one of these cards. It takes over a chip the vendor driver
// has already started.
type stid135Demod struct {
	demod *stid135.Demod
	// quattro says the four inputs are fed by a quattro block, so the supply
	// and the tone choose an input rather than switching anything.
	quattro bool
	pending sony.WakeupParams
	system  sony.System
}

type stid135Card struct {
	demod stid135.Config
	// demodIndexes and rfInputs are the chip demodulator and the card's aerial
	// input each tuner uses, cycled the way other cards' addresses are. Not
	// the tuner number: a 6903X uses two of the eight, not the first two.
	demodIndexes []int
	rfInputs     []int
}

func (card stid135Card) configFor(slot int) stid135.Config {
	config := card.demod
	if slot >= 0 && len(card.demodIndexes) > 0 {
		config.Demod = card.demodIndexes[slot%len(card.demodIndexes)]
	}
	if slot >= 0 && len(card.rfInputs) > 0 {
		config.RFInput = card.rfInputs[slot%len(card.rfInputs)]
	}
	return config
}

func newSTID135Demod(bus i2c.Bus, card stid135Card, slot int) *stid135Demod {
	config := card.configFor(slot)
	return &stid135Demod{
		demod:   stid135.New(bus, config),
		quattro: config.Multiswitch == stid135.Quattro,
	}
}

func (satellite *stid135Demod) Initialize() error { return satellite.demod.Initialize() }

// Wakeup only records: the chip takes the standard, symbol rate and frequency
// together, so nothing reaches it until Tune.
func (satellite *stid135Demod) Wakeup(system sony.System, params sony.WakeupParams) error {
	if system != sony.SystemDVBS && system != sony.SystemDVBS2 {
		return fmt.Errorf("an STiD135 receives DVB-S and DVB-S2, not %s", system)
	}
	satellite.system, satellite.pending = system, params
	return nil
}

func (satellite *stid135Demod) Tune(frequencyKHz uint32) error {
	delivery := "DVBS2"
	if satellite.system == sony.SystemDVBS {
		delivery = "DVBS"
	}
	streamID := int32(-1)
	if satellite.pending.StreamID != 0 {
		streamID = int32(satellite.pending.StreamID)
	}
	return satellite.demod.Tune(stid135.Params{
		DeliverySystem: delivery,
		FrequencyKHz:   frequencyKHz,
		SymbolRate:     satellite.pending.SymbolRateSPS,
		StreamID:       streamID,
	})
}

func (satellite *stid135Demod) Lock() (sony.Lock, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: status.Locked, Locked: status.Locked}, nil
}

func (satellite *stid135Demod) CNR() (float64, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return 0, err
	}
	if !status.HasCNR {
		return 0, errors.New("the demodulator has no carrier-to-noise reading yet")
	}
	return status.CNRdB, nil
}

// SignalStrength has nothing to report: the chip's gain reading is a raw
// integrator, and turning it into decibels needs the baseband filter
// calibration the chip driver deliberately does not run.
func (satellite *stid135Demod) SignalStrength(uint32) (float64, error) {
	return 0, errors.New("this demodulator reports no calibrated signal level")
}

// SetVoltage reaches the chip only on a quattro-fed card, and then switches
// nothing: the four leads are held at their own band and polarization, and
// this chooses which one the demodulator listens to. Elsewhere the supply is a
// separate regulator, already set through the vendor's property by the time
// this is called and out of the chip driver's reach.
func (satellite *stid135Demod) SetVoltage(high, off bool) error {
	if !satellite.quattro {
		return nil
	}
	return satellite.demod.SetVoltage(high, off)
}

func (satellite *stid135Demod) SetTone(on bool) error { return satellite.demod.SetTone(on) }

func (satellite *stid135Demod) SendDiSEqC(message []byte) error {
	return satellite.demod.SendDiSEqC(message)
}
