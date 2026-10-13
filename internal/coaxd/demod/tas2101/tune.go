// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package tas2101

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Params is a satellite channel to lock onto. There is no stream identifier:
// this chip has no multistream filter.
type Params struct {
	// DeliverySystem is "DVBS" or "DVBS2".
	DeliverySystem string
	FrequencyKHz   uint32
	SymbolRate     uint32 // symbols per second
}

// The wait for a lock after the tuner has moved.
const (
	lockAttempts = 15
	lockInterval = 20 * time.Millisecond
)

// Tune is tas2101_set_frontend: the pre-tune table, the symbol rate, a
// cleared frequency offset, the tuner, then the bit that starts the search.
func (demod *Demod) Tune(params Params) error {
	if !demod.warm {
		return ErrNotInitialized
	}
	system := tuning.Normalize(params.DeliverySystem)
	if system != "DVBS" && system != "DVBS2" {
		return fmt.Errorf("TAS2101 does not support delivery system %q", params.DeliverySystem)
	}
	if params.SymbolRate == 0 {
		return errors.New("TAS2101 needs a symbol rate")
	}
	demod.system = system

	if err := demod.writeTable(setfe); err != nil {
		return err
	}
	// The symbol rate goes in as kilosymbols per second, low byte first.
	rate := params.SymbolRate / 1000
	if err := demod.regWriteMulti([]byte{registerSetSRate0, byte(rate), byte(rate >> 8)}); err != nil {
		return err
	}
	// Whatever the last search settled on is not this channel's offset.
	if err := demod.regWriteMulti([]byte{registerFreqOffset, 0, 0}); err != nil {
		return err
	}
	if demod.tuner != nil {
		if err := demod.tuner.Tune(params.FrequencyKHz*1000, params.SymbolRate); err != nil {
			return err
		}
	}
	if err := demod.regMask(register30, tunerStartTune, 0); err != nil {
		return err
	}
	for attempt := 0; attempt < lockAttempts; attempt++ {
		status, err := demod.Status()
		if err != nil {
			return err
		}
		if status.Locked {
			return nil
		}
		time.Sleep(lockInterval)
	}
	return fmt.Errorf("TAS2101 did not lock onto %d kHz", params.FrequencyKHz)
}
