// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tbs

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// channelFrequency keeps the already-downconverted IF. The BDA profile undoes
// LNBf conversion for a vendor tuner; a directly driven chip must not use it.
func channelFrequency(properties map[string]string) (uint32, error) {
	if _, err := bda.BuildDrivenProfile(properties); err != nil {
		return 0, err
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return 0, err
	}
	frequency = tuning.FrequencyKHz(frequency)
	if frequency == 0 || frequency > uint64(^uint32(0)) {
		return 0, fmt.Errorf("frequency is outside the chip's range")
	}
	return uint32(frequency), nil
}

type toneSetter interface{ SetTone(bool) error }
type voltageSetter interface{ SetVoltage(bool, bool) error }
type diseqcSender interface{ SendDiSEqC([]byte) error }

// satelliteEquipment runs after wake-up, when the chip's SEC block is live,
// and before acquisition. Quattro requests select an input without touching
// its shared supply. A message is never silently dropped on an unsupported chip.
func satelliteEquipment(front any, properties map[string]string, bridge func(bool, bool) error, quattro bool) (err error) {
	supply, err := tuning.SatelliteSupply(properties)
	if err != nil {
		return err
	}
	command, wanted, err := tuning.DiseqcFor(properties)
	if err != nil {
		return err
	}
	sender, sends := front.(diseqcSender)
	if wanted && (quattro || !sends) {
		return fmt.Errorf("SAT_NUMBER cannot switch this chip's aerial lead")
	}
	if wanted && supply.Off {
		return fmt.Errorf("SAT_NUMBER needs an enabled LNB supply")
	}
	toner, tones := front.(toneSetter)
	if !tones && (wanted || tuning.ToneWanted(properties)) {
		return fmt.Errorf("this chip cannot set the requested 22 kHz tone")
	}
	if bridge != nil && !quattro {
		if err := tuning.ApplySupply(supply, bridge); err != nil {
			return err
		}
	}
	if regulator, ok := front.(voltageSetter); ok {
		if err := tuning.ApplySupply(supply, regulator.SetVoltage); err != nil {
			return err
		}
	}
	if !tones {
		return nil
	}
	tone := tuning.ToneWanted(properties)
	if !wanted {
		return toner.SetTone(tone)
	}
	if err := toner.SetTone(false); err != nil {
		return err
	}
	defer func() {
		if restore := toner.SetTone(tone); err == nil {
			err = restore
		}
	}()
	return sender.SendDiSEqC(command.Message[:command.Length])
}
