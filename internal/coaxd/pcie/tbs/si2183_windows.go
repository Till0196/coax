// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"errors"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/si2183"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/tuner/av201x"
	"github.com/Till0196/coax/internal/coaxd/tuner/rda5816"
	"github.com/Till0196/coax/internal/coaxd/tuner/si2157"
	"github.com/Till0196/coax/pkg/i2c"
)

// The third chip these cards are built around is a Silicon Labs Si2183: one
// demodulator that receives everything, with two tuners beside it -- an Si2157
// for terrestrial and cable, an AV201x for satellite -- both behind the gate
// the demodulator holds. Which tuner is used follows the channel's standard,
// not the tuner number.
//
// Config.AlreadyBooted matters here: the vendor driver loaded this chip's
// firmware long before this program starts, and the cold sequence would drop a
// running chip back into its bootloader, taking that driver's stream with it.

// si2183Card is how a card built this way is described. The per-tuner lists
// cycle the way the Sony cards' addresses do: a card puts two demodulators on
// a bus and alternates everything between them.
type si2183Card struct {
	demod     si2183.Config
	addresses []byte
	// agcModes is the chip's AGC configuration byte per tuner. It alternates
	// down the card, and not the same way round on every model.
	agcModes []byte
	// terrestrial is the Si2157 beside it; nil on a satellite-only card.
	terrestrial          *si2157.Config
	terrestrialAddresses []byte
	// satellite is the AV201x beside it; nil on a card that fitted the other.
	satellite *av201x.Config
	// satelliteRDA is the RDA5816 the 6508 fitted instead; never both.
	satelliteRDA       *rda5816.Config
	satelliteAddresses []byte
}

// satelliteTuner is whichever part the board fitted; the two are used
// interchangeably.
type satelliteTuner interface {
	Initialize() error
	Tune(frequencyHz, symbolRateSPS uint32) error
}

// satelliteFitting is the satellite tuner a card fitted, carrying the address
// this tuner uses. At most one of the two is set.
type satelliteFitting struct {
	av201x  *av201x.Config
	rda5816 *rda5816.Config
}

func (fitting satelliteFitting) fitted() bool {
	return fitting.av201x != nil || fitting.rda5816 != nil
}

func (fitting satelliteFitting) Address() byte {
	switch {
	case fitting.av201x != nil:
		return fitting.av201x.Address
	case fitting.rda5816 != nil:
		return fitting.rda5816.Address
	}
	return 0
}

func (fitting satelliteFitting) tuner(bus i2c.Bus) satelliteTuner {
	switch {
	case fitting.av201x != nil:
		return av201x.New(bus, *fitting.av201x)
	case fitting.rda5816 != nil:
		return rda5816.New(bus, *fitting.rda5816)
	}
	return nil
}

func (card si2183Card) configFor(slot int) (si2183.Config, *si2157.Config, satelliteFitting) {
	demod := card.demod
	if slot >= 0 && len(card.addresses) > 0 {
		demod.DemodAddress = card.addresses[slot%len(card.addresses)]
	}
	if slot >= 0 && len(card.agcModes) > 0 {
		demod.AGCMode = card.agcModes[slot%len(card.agcModes)]
	}
	var terrestrial *si2157.Config
	if card.terrestrial != nil {
		config := *card.terrestrial
		if slot >= 0 && len(card.terrestrialAddresses) > 0 {
			config.Address = card.terrestrialAddresses[slot%len(card.terrestrialAddresses)]
		}
		terrestrial = &config
	}
	address := byte(0)
	if slot >= 0 && len(card.satelliteAddresses) > 0 {
		address = card.satelliteAddresses[slot%len(card.satelliteAddresses)]
	}
	var satellite satelliteFitting
	switch {
	case card.satellite != nil:
		config := *card.satellite
		if address != 0 {
			config.Address = address
		}
		satellite.av201x = &config
	case card.satelliteRDA != nil:
		config := *card.satelliteRDA
		if address != 0 {
			config.Address = address
		}
		satellite.rda5816 = &config
	}
	return demod, terrestrial, satellite
}

type si2183Demod struct {
	demod       *si2183.Demod
	terrestrial *si2157.Tuner
	satellite   satelliteTuner

	pending sony.WakeupParams
	system  sony.System
}

func newSi2183Demod(bus i2c.Bus, firmwareDir string, card si2183Card, slot int) *si2183Demod {
	demodConfig, terrestrialConfig, satelliteFitted := card.configFor(slot)
	demod := si2183.New(bus, firmwareDir, demodConfig)
	// Both tuners are on the far side of the demodulator's gate, which is
	// opened around every transfer they make.
	gated := i2c.Gated(bus, demod)
	front := &si2183Demod{demod: demod}
	if terrestrialConfig != nil {
		front.terrestrial = si2157.New(gated, firmwareDir, *terrestrialConfig)
	}
	if satelliteFitted.fitted() {
		front.satellite = satelliteFitted.tuner(gated)
	}
	return front
}

// Initialize resumes the demodulator and its tuners; each driver leaves a part
// a previous process left warm alone.
func (front *si2183Demod) Initialize() error {
	if err := front.demod.Initialize(); err != nil {
		return err
	}
	if front.terrestrial != nil {
		if err := front.terrestrial.Initialize(); err != nil {
			return fmt.Errorf("initialize the Si2157: %w", err)
		}
	}
	if front.satellite != nil {
		if err := front.satellite.Initialize(); err != nil {
			return fmt.Errorf("initialize the AV201x: %w", err)
		}
	}
	return nil
}

// Wakeup only records, and checks the card has a tuner for the side of the
// band the standard is on. The demodulator takes its whole channel in one
// command, so nothing reaches it until Tune.
func (front *si2183Demod) Wakeup(system sony.System, params sony.WakeupParams) error {
	if _, err := si2183DeliveryFor(system); err != nil {
		return err
	}
	if system.IsSatellite() && front.satellite == nil {
		return fmt.Errorf("this card's satellite tuner is not one this backend drives")
	}
	if !system.IsSatellite() && front.terrestrial == nil {
		return fmt.Errorf("this card has no terrestrial tuner")
	}
	front.system, front.pending = system, params
	return nil
}

func (front *si2183Demod) Tune(frequencyKHz uint32) error {
	delivery, err := si2183DeliveryFor(front.system)
	if err != nil {
		return err
	}
	bandwidthHz := front.pending.BandwidthMHz * 1_000_000
	streamID := -1
	if front.system.IsSatellite() {
		if front.pending.StreamID != 0 {
			streamID = int(front.pending.StreamID)
		}
		// The tuner moves first, then the demodulator is told what to expect.
		if err := front.satellite.Tune(frequencyKHz*1000, front.pending.SymbolRateSPS); err != nil {
			return fmt.Errorf("tune the AV201x: %w", err)
		}
	} else {
		if front.pending.PLPID >= 0 {
			streamID = int(front.pending.PLPID)
		}
		if err := front.terrestrial.Tune(frequencyKHz*1000, bandwidthHz, delivery); err != nil {
			return fmt.Errorf("tune the Si2157: %w", err)
		}
	}
	// The modulation is not carried this far; the chip finds it, as it does
	// for cable. The one real limit is J.83B, whose two constellations the
	// chip cannot detect: an unnamed one is taken as 256-QAM.
	return front.demod.Tune(si2183.Params{
		DeliverySystem: delivery,
		BandwidthHz:    bandwidthHz,
		SymbolRate:     front.pending.SymbolRateSPS,
		StreamID:       streamID,
	})
}

func (front *si2183Demod) Lock() (sony.Lock, error) {
	status, err := front.demod.Status()
	if err != nil {
		return sony.Lock{}, err
	}
	return sony.Lock{Synced: status.Locked, Locked: status.Locked}, nil
}

func (front *si2183Demod) CNR() (float64, error) {
	status, err := front.demod.Status()
	if err != nil {
		return 0, err
	}
	if !status.HasCNR {
		return 0, errors.New("the demodulator has no carrier-to-noise reading yet")
	}
	return status.CNRdB, nil
}

// SignalStrength has nothing to report: the status command carries no level.
func (front *si2183Demod) SignalStrength(uint32) (float64, error) {
	return 0, errors.New("this demodulator reports no signal level")
}

// SetTone puts the tone on the lead through the block that sends DiSEqC,
// which is the only way to ask for it.
func (front *si2183Demod) SetTone(on bool) error { return front.demod.SetTone(on) }

func si2183DeliveryFor(system sony.System) (string, error) {
	switch system {
	case sony.SystemDVBT:
		return "DVBT", nil
	case sony.SystemDVBT2:
		return "DVBT2", nil
	case sony.SystemDVBC:
		return "DVBC", nil
	case sony.SystemJ83B:
		return "DVBC/ANNEXB", nil
	case sony.SystemISDBT:
		return "ISDBT", nil
	case sony.SystemDVBS:
		return "DVBS", nil
	case sony.SystemDVBS2:
		return "DVBS2", nil
	}
	return "", fmt.Errorf("an Si2183 does not receive %s", system)
}

func (front *si2183Demod) SendDiSEqC(message []byte) error {
	return front.demod.SendDiSEqC(message, false)
}
