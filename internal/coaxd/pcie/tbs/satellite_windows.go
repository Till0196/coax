// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/m88rs6060"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// Some of these cards carry two demodulators per tuner: a Sony part for the
// terrestrial and cable standards and a Montage part beside it for satellite,
// each on its own address of the same bus, the card's mode settling which is
// listening. The rest of this backend is written against the Sony chip's
// shape, so the Montage part is wrapped here to answer the same calls; it
// takes its channel in one go, so Tune is what reaches the chip.
type satelliteDemod struct {
	demod   *m88rs6060.Demod
	pending sony.WakeupParams
	system  sony.System
}

func newSatelliteDemod(bus i2c.Bus, firmwareDir string, config m88rs6060.Config) *satelliteDemod {
	return &satelliteDemod{demod: m88rs6060.New(bus, firmwareDir, config)}
}

func (satellite *satelliteDemod) Initialize() error { return satellite.demod.Initialize() }

// Wakeup only records: the chip wants the standard, symbol rate and frequency
// together, so nothing reaches it until Tune.
func (satellite *satelliteDemod) Wakeup(system sony.System, params sony.WakeupParams) error {
	if !system.IsSatellite() {
		return fmt.Errorf("this demodulator receives satellite only, not %s", system)
	}
	satellite.system, satellite.pending = system, params
	return nil
}

func (satellite *satelliteDemod) Tune(frequencyKHz uint32) error {
	delivery := "DVBS2"
	if satellite.system == sony.SystemDVBS {
		delivery = "DVBS"
	}
	// The Sony side's stream identifier means something else on each standard;
	// here it is the DVB multistream selector, -1 taking the first stream.
	streamID := int32(-1)
	if satellite.pending.StreamID != 0 {
		streamID = int32(satellite.pending.StreamID)
	}
	return satellite.demod.Tune(m88rs6060.Params{
		DeliverySystem: delivery,
		FrequencyKHz:   frequencyKHz,
		SymbolRate:     satellite.pending.SymbolRateSPS,
		StreamID:       streamID,
	})
}

func (satellite *satelliteDemod) Lock() (sony.Lock, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: status.Locked, Locked: status.Locked}, nil
}

func (satellite *satelliteDemod) CNR() (float64, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return 0, err
	}
	if !status.HasCNR {
		return 0, fmt.Errorf("the demodulator has no carrier-to-noise reading yet")
	}
	return status.CNRdB, nil
}

func (satellite *satelliteDemod) SignalStrength(frequencyKHz uint32) (float64, error) {
	return satellite.demod.SignalStrength(frequencyKHz)
}

// SetVoltage and SetTone are the chip's own, reached by type assertion.
func (satellite *satelliteDemod) SetVoltage(high, off bool) error {
	return satellite.demod.SetVoltage(high, off)
}

func (satellite *satelliteDemod) SetTone(on bool) error { return satellite.demod.SetTone(on) }
