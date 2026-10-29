// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"errors"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/demod/stv091x"
	"github.com/Till0196/coax/internal/coaxd/tuner/stv6120"
	"github.com/Till0196/coax/pkg/i2c"
)

// The vendor's 6903, 6905 and 6908 are built round shared STMicroelectronics
// parts: one STV0910 is two demodulators and one STV6120 is two tuners, so a
// pair of the card's tuners lives on one chip of each. The board wires the
// halves crossed -- the demodulator half is the tuner number's low bit, the
// tuner half its complement.
//
// Sharing is also why both halves are taken over warm: the cold bring-up is
// chip-wide and would reset the other half's transport stream and PLL while it
// may be streaming.
type stv091xDemod struct {
	demod   *stv091x.Demod
	pending sony.WakeupParams
	system  sony.System
}

// stv091xCard is one entry for all three: same chips, differing tuner counts.
type stv091xCard struct {
	demod stv091x.Config
	tuner stv6120.Config
}

func (card stv091xCard) halvesFor(slot int) (stv091x.Config, stv6120.Config) {
	demod, tuner := card.demod, card.tuner
	if slot >= 0 {
		demod.Index = slot & 1
		tuner.Index = 1 - (slot & 1)
	}
	return demod, tuner
}

func newSTV091xDemod(bus i2c.Bus, card stv091xCard, slot int) *stv091xDemod {
	demodConfig, tunerConfig := card.halvesFor(slot)
	demod := stv091x.New(bus, demodConfig)
	// The tuner is behind the demodulator's repeater, and which of the two
	// repeater registers opens it depends on how the board wired the one tuner
	// chip to the two demodulators -- configuration, not derived from the half.
	demod.AttachTuner(stv6120.New(i2c.Gated(bus, demod), tunerConfig))
	return &stv091xDemod{demod: demod}
}

// Initialize brings up this half of each chip. The tuner needs no separate
// call: it comes up as part of the demodulator that holds its repeater.
func (satellite *stv091xDemod) Initialize() error { return satellite.demod.Initialize() }

// Wakeup only records: the chip takes the standard, symbol rate and frequency
// together, so nothing reaches it until Tune.
func (satellite *stv091xDemod) Wakeup(system sony.System, params sony.WakeupParams) error {
	if system != sony.SystemDVBS && system != sony.SystemDVBS2 {
		return fmt.Errorf("an STV0910 receives DVB-S and DVB-S2, not %s", system)
	}
	satellite.system, satellite.pending = system, params
	return nil
}

func (satellite *stv091xDemod) Tune(frequencyKHz uint32) error {
	delivery := "DVBS2"
	if satellite.system == sony.SystemDVBS {
		delivery = "DVBS"
	}
	streamID := int32(-1)
	if satellite.pending.StreamID != 0 {
		streamID = int32(satellite.pending.StreamID)
	}
	return satellite.demod.Tune(stv091x.Params{
		DeliverySystem: delivery,
		FrequencyKHz:   frequencyKHz,
		SymbolRate:     satellite.pending.SymbolRateSPS,
		StreamID:       streamID,
	})
}

func (satellite *stv091xDemod) Lock() (sony.Lock, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: status.Locked, Locked: status.Locked}, nil
}

func (satellite *stv091xDemod) CNR() (float64, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return 0, err
	}
	if !status.HasCNR {
		return 0, errors.New("the demodulator has no carrier-to-noise reading yet")
	}
	return status.CNRdB, nil
}

func (satellite *stv091xDemod) SignalStrength(frequencyKHz uint32) (float64, error) {
	return satellite.demod.SignalStrength(frequencyKHz)
}

// SetTone is the chip's; the supply is not. These cards switch it with two of
// the bridge's own pins, reached through the vendor's LNB property, so there
// is deliberately no SetVoltage here.
func (satellite *stv091xDemod) SetTone(on bool) error { return satellite.demod.SetTone(on) }

func (satellite *stv091xDemod) SendDiSEqC(message []byte) error {
	return satellite.demod.SendDiSEqC(message)
}
