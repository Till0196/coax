// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"errors"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/mxl58x"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// The 6909 is the odd card of this vendor's satellite range: one MaxLinear
// MxL584 carries all eight frontends behind one I2C address and one firmware,
// the frontend told apart by an index in the command payload. Re-downloading
// that firmware holds the chip's processor in reset and would drop all eight
// streams, not just this tuner's, so Config.AlreadyBooted is set: a chip found
// cold is reported rather than written to.
type mxl58xDemod struct {
	demod   *mxl58x.Demod
	pending sony.WakeupParams
	system  sony.System
}

func mxl58xConfigFor(config mxl58x.Config, slot int) mxl58x.Config {
	if slot >= 0 {
		config.DemodIndex = uint32(slot)
	}
	return config
}

func newMXL58xDemod(bus i2c.Bus, firmwareDir string, config mxl58x.Config, slot int) *mxl58xDemod {
	return &mxl58xDemod{demod: mxl58x.New(bus, firmwareDir, mxl58xConfigFor(config, slot))}
}

func (satellite *mxl58xDemod) Initialize() error { return satellite.demod.Initialize() }

// Wakeup only records: the chip takes the standard, symbol rate and frequency
// in one command, so nothing reaches it until Tune.
func (satellite *mxl58xDemod) Wakeup(system sony.System, params sony.WakeupParams) error {
	if system != sony.SystemDVBS && system != sony.SystemDVBS2 {
		return fmt.Errorf("an MxL58x receives DVB-S and DVB-S2, not %s", system)
	}
	satellite.system, satellite.pending = system, params
	return nil
}

func (satellite *mxl58xDemod) Tune(frequencyKHz uint32) error {
	delivery := "DVBS2"
	if satellite.system == sony.SystemDVBS {
		delivery = "DVBS"
	}
	// This firmware has no multistream filter, and the driver refuses a
	// channel that names a stream rather than locking onto the wrong one.
	// -1 means no filtering.
	streamID := int32(-1)
	if satellite.pending.StreamID != 0 {
		streamID = int32(satellite.pending.StreamID)
	}
	return satellite.demod.Tune(mxl58x.Params{
		DeliverySystem: delivery,
		FrequencyKHz:   frequencyKHz,
		SymbolRate:     satellite.pending.SymbolRateSPS,
		StreamID:       streamID,
	})
}

func (satellite *mxl58xDemod) Lock() (sony.Lock, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: status.Locked, Locked: status.Locked}, nil
}

func (satellite *mxl58xDemod) CNR() (float64, error) {
	status, err := satellite.demod.Status()
	if err != nil {
		return 0, err
	}
	if !status.HasCNR {
		return 0, errors.New("the demodulator has no carrier-to-noise reading yet")
	}
	return status.CNRdB, nil
}

// SignalStrength ignores the frequency: the chip reports input power in dBm.
func (satellite *mxl58xDemod) SignalStrength(uint32) (float64, error) {
	return satellite.demod.SignalStrength()
}

// SetVoltage and SetTone do not switch the lead in this card's default wiring:
// a quattro block holds four leads at their own band, so these pick which lead
// the frontend listens to. Same choice, same way round, so the tune path need
// not know.
func (satellite *mxl58xDemod) SetVoltage(high, off bool) error {
	return satellite.demod.SetVoltage(high, off)
}

func (satellite *mxl58xDemod) SetTone(on bool) error { return satellite.demod.SetTone(on) }
