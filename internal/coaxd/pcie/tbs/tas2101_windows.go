// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/demod/tas2101"
	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/tuner/av201x"
)

// The vendor's older satellite cards carry a Tmax TAS2101 with an Airoha
// AV201x beside it rather than the single-package M88RS6060, wrapped here as
// satelliteDemod wraps the Montage part. The TAS2101 needs no firmware, which
// is why these cards can be driven at all: it is picked up from a running
// vendor driver without a reset, where the Silicon Labs parts elsewhere in the
// range cannot without dropping the stream that driver is carrying.
type tas2101Demod struct {
	demod   *tas2101.Demod
	pending sony.WakeupParams
	system  sony.System
}

// tas2101Card is how a card wired this way is described.
type tas2101Card struct {
	demod tas2101.Config
	tuner av201x.Config
	// addresses is the demodulator's I2C address per tuner, cycled the way
	// demodAddresses is: every card here puts two demodulators on a bus.
	addresses []byte
	// inits is the board's seven bytes per tuner, cycled alongside the
	// addresses; one entry serves a card whose tuners all take the same bytes.
	inits [][7]byte
}

func (card tas2101Card) configFor(slot int) (tas2101.Config, av201x.Config) {
	config := card.demod
	if slot >= 0 && len(card.addresses) > 0 {
		config.DemodAddress = card.addresses[slot%len(card.addresses)]
	}
	if slot >= 0 && len(card.inits) > 0 {
		config.Init = card.inits[slot%len(card.inits)]
	}
	return config, card.tuner
}

func newTAS2101Demod(bus i2c.Bus, card tas2101Card, slot int) *tas2101Demod {
	demodConfig, tunerConfig := card.configFor(slot)
	// This order because the tuner is reached through the demodulator: the
	// gate it holds is opened around every transfer the tuner makes.
	demod := tas2101.New(bus, demodConfig)
	demod.AttachTuner(av201x.New(i2c.Gated(bus, demod), tunerConfig))
	return &tas2101Demod{demod: demod}
}

func (satellite *tas2101Demod) Initialize() error {
	return satellite.demod.Initialize()
}

// Wakeup only records: the chips want the frequency and symbol rate together,
// so nothing reaches them until Tune.
func (satellite *tas2101Demod) Wakeup(system sony.System, params sony.WakeupParams) error {
	if !system.IsSatellite() {
		return fmt.Errorf("this demodulator receives satellite only, not %s", system)
	}
	if system != sony.SystemDVBS && system != sony.SystemDVBS2 {
		return fmt.Errorf("a TAS2101 receives DVB-S and DVB-S2, not %s", system)
	}
	satellite.system, satellite.pending = system, params
	return nil
}

func (satellite *tas2101Demod) Tune(frequencyKHz uint32) error {
	delivery := "DVBS2"
	if satellite.system == sony.SystemDVBS {
		delivery = "DVBS"
	}
	return satellite.demod.Tune(tas2101.Params{
		DeliverySystem: delivery,
		FrequencyKHz:   frequencyKHz,
		SymbolRate:     satellite.pending.SymbolRateSPS,
	})
}

func (satellite *tas2101Demod) Lock() (sony.Lock, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: status.Locked, Locked: status.Locked}, nil
}

func (satellite *tas2101Demod) CNR() (float64, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return 0, err
	}
	if !status.HasCNR {
		return 0, fmt.Errorf("the demodulator has no carrier-to-noise reading yet")
	}
	return status.CNRdB, nil
}

// SignalStrength ignores the frequency: the chip's curve is calibrated once
// for the whole band.
func (satellite *tas2101Demod) SignalStrength(uint32) (float64, error) {
	return satellite.demod.SignalStrength()
}

// SetVoltage and SetTone are the chip's own, reached by type assertion.
func (satellite *tas2101Demod) SetVoltage(high, off bool) error {
	return satellite.demod.SetVoltage(high, off)
}

func (satellite *tas2101Demod) SetTone(on bool) error { return satellite.demod.SetTone(on) }
