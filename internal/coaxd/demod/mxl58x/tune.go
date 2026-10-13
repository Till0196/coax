// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices; MaxLinear; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package mxl58x

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

const (
	minFrequencyKHz = 950_000
	maxFrequencyKHz = 2_150_000
	minSymbolRate   = 1_000_000
	maxSymbolRate   = 45_000_000
)

// Values from the firmware's own enumerations.
const (
	standardDSS   = 0
	standardDVBS  = 1
	standardDVBS2 = 2

	fecAuto = 0

	modulationAuto = 0
	modulationQPSK = 1

	spectrumAuto = 0

	rolloffAuto = 0

	pilotsOff  = 0
	pilotsAuto = 2

	// maxCarrierOffsetMHz is the search span either side of the frequency.
	maxCarrierOffsetMHz = 10
)

// Params is a satellite channel to lock onto.
type Params struct {
	// DeliverySystem is "DVBS", "DVBS2" or "DSS".
	DeliverySystem string
	FrequencyKHz   uint32
	SymbolRate     uint32 // symbols per second

	// StreamID carries the DVB API's packed multistream selector. Only a
	// negative value -- no filtering -- can be honoured: see Tune.
	StreamID int32
}

// Tune posts one message to the firmware describing the channel. It does not
// wait for a lock -- the firmware searches on its own and the caller polls
// Status -- so a tune that will fail still returns nil.
func (demod *Demod) Tune(params Params) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if params.StreamID >= 0 {
		// The firmware has a scramble-code command carrying a gold code, but
		// nothing selects an input stream identifier.
		return fmt.Errorf("MxL58x cannot select multistream %d: this chip's driver has no stream filter",
			params.StreamID)
	}
	if params.FrequencyKHz < minFrequencyKHz || params.FrequencyKHz > maxFrequencyKHz {
		return fmt.Errorf("MxL58x takes %d to %d kHz, not %d",
			minFrequencyKHz, maxFrequencyKHz, params.FrequencyKHz)
	}
	if params.SymbolRate < minSymbolRate || params.SymbolRate > maxSymbolRate {
		return fmt.Errorf("MxL58x takes %d to %d symbols per second, not %d",
			minSymbolRate, maxSymbolRate, params.SymbolRate)
	}

	standard, modulation, pilots, err := demodulation(params.DeliverySystem)
	if err != nil {
		return err
	}
	// The firmware's demodulator parameter structure, field for field.
	// Modulation, roll-off and pilots take its "automatic" values where they
	// are not needed.
	payload := words(
		demod.rfInput,
		demod.config.DemodIndex,
		params.FrequencyKHz*1000,
		standard,
		spectrumAuto,
		rolloffAuto,
		params.SymbolRate,
		pilots,
		modulation,
		fecAuto,
		maxCarrierOffsetMHz,
	)
	return demod.sendCommand(commandDemodSetParam, payload)
}

// demodulation maps a delivery system onto the firmware's standard,
// modulation and pilot settings.
func demodulation(system string) (standard, modulation, pilots uint32, err error) {
	switch tuning.Normalize(system) {
	case "DSS":
		return standardDSS, modulationAuto, pilotsOff, nil
	case "DVBS":
		// DVB-S is pinned to QPSK, the only constellation the standard has.
		return standardDVBS, modulationQPSK, pilotsOff, nil
	case "DVBS2":
		return standardDVBS2, modulationAuto, pilotsAuto, nil
	}
	return 0, 0, 0, fmt.Errorf("MxL58x does not support delivery system %q", system)
}
