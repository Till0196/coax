// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package dvb

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

var numericPropertyCommands = map[string]uint32{
	"FREQUENCY":               dtvFrequency,
	"BANDWIDTH_HZ":            dtvBandwidthHz,
	"SYMBOL_RATE":             dtvSymbolRate,
	"STREAM_ID":               dtvStreamID,
	tuning.ScramblingProperty: dtvScramblingSequenceIndex,
}

func BuildTuneProperties(properties map[string]string) ([]TuneProperty, uint32, error) {
	deliveryName := normalizeValue(properties["DELIVERY_SYSTEM"])
	delivery, ok := deliverySystemValues[deliveryName]
	if !ok {
		return nil, 0, fmt.Errorf("unsupported DELIVERY_SYSTEM %q", properties["DELIVERY_SYSTEM"])
	}

	tuneProperties := []TuneProperty{{Command: dtvDeliverySystem, Value: delivery}}
	orderedKeys := []string{
		"FREQUENCY", "MODULATION", "BANDWIDTH_HZ", "SYMBOL_RATE", "INVERSION",
		"INNER_FEC", "CODE_RATE_HP", "CODE_RATE_LP", "TRANSMISSION_MODE",
		"GUARD_INTERVAL", "HIERARCHY", "VOLTAGE", "POLARIZATION", "TONE",
		"PILOT", "ROLLOFF", "STREAM_ID", tuning.ScramblingProperty,
		tuning.GainProperty, tuning.LNAProperty,
		tuning.SatNumberProperty,
	}
	// A channel asking for no supply is not overruled by its own polarization.
	// Both land on dtvVoltage and the polarization is written second, so
	// without this it would win. See tuning.SatelliteSupply.
	silent := false
	if lnb, err := tuning.ParseLNB(properties); err != nil {
		return nil, 0, err
	} else if lnb == tuning.LNBOff {
		silent = true
	}

	// The block on the dish is not a frontend property; it arrives here only
	// because the Windows backend needs it. Marking it seen keeps the check
	// below from calling it unsupported.
	seen := map[string]bool{"DELIVERY_SYSTEM": true, tuning.LNBProperty: true}
	for _, key := range orderedKeys {
		value, exists := properties[key]
		if !exists {
			continue
		}
		// A polarization of "off" is how a DVBv5 file says it has none to
		// give, not a request for anything, so it is passed over rather than
		// turned into a voltage. Asking for no supply passes it over too.
		if key == "POLARIZATION" && (silent || tuning.Normalize(value) == "OFF") {
			seen[key] = true
			continue
		}
		// The two gain spellings land on the same kernel property; whichever
		// carried it, it is read once below.
		if key == tuning.LNAProperty || key == tuning.GainProperty {
			seen[key] = true
			continue
		}
		seen[key] = true
		property, err := parseTuneProperty(key, value)
		if err != nil {
			return nil, 0, err
		}
		tuneProperties = append(tuneProperties, property)
	}
	// DTV_LNA is the same three-valued setting the shared vocabulary's ends
	// name. A driver whose frontend has no amplifier ignores the property rather
	// than failing the tune, so this is the one backend that does not refuse a
	// gain on hardware that has none. Decibels are refused: the API has no way
	// to ask for a level.
	gain, err := tuning.ParseGain(properties)
	if err != nil {
		return nil, 0, err
	}
	switch gain.Kind {
	case tuning.GainMin:
		tuneProperties = append(tuneProperties, TuneProperty{Command: dtvLNA, Value: 0})
	case tuning.GainMax:
		tuneProperties = append(tuneProperties, TuneProperty{Command: dtvLNA, Value: 1})
	case tuning.GainAuto:
		tuneProperties = append(tuneProperties, TuneProperty{Command: dtvLNA, Value: lnaAuto})
	case tuning.GainDecibels:
		return nil, 0, fmt.Errorf("a Linux frontend's gain is off, on or auto; it has no way to be asked for %s: %w", gain, tuning.ErrNoGain)
	}
	for key := range properties {
		if !seen[key] {
			return nil, 0, fmt.Errorf("unsupported tuning property %q", key)
		}
	}
	// A satellite channel that named neither a voltage nor a polarization gets no
	// supply, said rather than left: the frontend keeps whatever the last tune put
	// on the lead. See tuning.SatelliteSupply.
	if tuning.IsSatellite(properties["DELIVERY_SYSTEM"]) && !carries(tuneProperties, dtvVoltage) {
		tuneProperties = append(tuneProperties, TuneProperty{Command: dtvVoltage, Value: secVoltageOff})
	}
	return tuneProperties, delivery, nil
}

// secVoltageOff is SEC_VOLTAGE_OFF, the third of the frontend's three settings
// for the lead: the two below it are its 13 V and its 18 V.
const secVoltageOff = 2

// carries reports whether a command is already in the list, which is how the
// default above learns the channel settled the matter itself.
func carries(properties []TuneProperty, command uint32) bool {
	for _, property := range properties {
		if property.Command == command {
			return true
		}
	}
	return false
}

func parseTuneProperty(key string, value string) (TuneProperty, error) {
	if command, ok := numericPropertyCommands[key]; ok {
		number, err := parseUint32(value)
		if err != nil {
			return TuneProperty{}, fmt.Errorf("%s: %w", key, err)
		}
		return TuneProperty{Command: command, Value: number}, nil
	}

	normalized := normalizeValue(value)
	var command uint32
	var values map[string]uint32
	switch key {
	case "MODULATION":
		// Folded further than the rest: a modulation is written with the
		// separator in one vocabulary and without it in the other, and the
		// count at either end. See modulationValues.
		normalized = tuning.NormalizeModulation(value)
		command, values = dtvModulation, modulationValues
	case "INVERSION":
		command = dtvInversion
		values = map[string]uint32{"OFF": 0, "ON": 1, "AUTO": 2}
	case "INNER_FEC":
		command, values = dtvInnerFEC, fecValues
	case "CODE_RATE_HP":
		command, values = dtvCodeRateHP, fecValues
	case "CODE_RATE_LP":
		command, values = dtvCodeRateLP, fecValues
	case "TRANSMISSION_MODE":
		command, values = dtvTransmitMode, transmissionModeValues
	case "GUARD_INTERVAL":
		command, values = dtvGuardInterval, guardIntervalValues
	case "HIERARCHY":
		command, values = dtvHierarchy, hierarchyValues
	case "VOLTAGE":
		// Read through the shared vocabulary rather than as a number, so one
		// spelling means the same on every backend. This frontend's levels are
		// 13 V and 18 V; a bare "15" is not one of the three SEC_VOLTAGE_*
		// values and must not reach the driver as one.
		lnb, err := tuning.ParseLNBValue(value)
		if err != nil {
			return TuneProperty{}, err
		}
		switch lnb {
		case tuning.LNBLow:
			return TuneProperty{Command: dtvVoltage, Value: 0}, nil
		case tuning.LNBHigh:
			return TuneProperty{Command: dtvVoltage, Value: 1}, nil
		default:
			return TuneProperty{Command: dtvVoltage, Value: secVoltageOff}, nil
		}
	case "POLARIZATION":
		// The four a DVBv5 file may carry plus the single-letter forms other
		// tools write; libdvbv5 accepts only the whole words. Horizontal and
		// left-hand circular are carried at the higher of the two voltages,
		// vertical and right-hand at the lower.
		command = dtvVoltage
		values = map[string]uint32{
			"V": 0, "VERTICAL": 0, "R": 0, "RIGHT": 0, "CIRCULARRIGHT": 0,
			"H": 1, "HORIZONTAL": 1, "L": 1, "LEFT": 1, "CIRCULARLEFT": 1,
		}
	case "TONE":
		command = dtvTone
		values = map[string]uint32{"ON": 0, "OFF": 1}
	case "PILOT":
		command = dtvPilot
		values = map[string]uint32{"ON": 0, "OFF": 1, "AUTO": 2}
	case "ROLLOFF":
		command = dtvRolloff
		values = map[string]uint32{"35": 0, "20": 1, "25": 2, "AUTO": 3}
	default:
		return TuneProperty{}, fmt.Errorf("unsupported tuning property %q", key)
	}
	if number, ok := values[normalized]; ok {
		return TuneProperty{Command: command, Value: number}, nil
	}
	if number, err := parseUint32(value); err == nil {
		return TuneProperty{Command: command, Value: number}, nil
	}
	return TuneProperty{}, fmt.Errorf("%s: unsupported value %q", key, value)
}

func parseUint32(value string) (uint32, error) {
	number, err := strconv.ParseUint(strings.TrimSpace(value), 0, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid unsigned integer %q", value)
	}
	return uint32(number), nil
}

func normalizeValue(value string) string {
	return strings.ToUpper(strings.Trim(strings.TrimSpace(value), "\"'"))
}
