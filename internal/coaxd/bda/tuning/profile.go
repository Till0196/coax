// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package tuning turns the DVBv5-style channel properties this program passes
// around into the one description a BDA graph is built from. It is deliberately
// free of Windows, so it can be tested anywhere.
package tuning

import (
	"fmt"
	"strconv"
	"strings"

	channels "github.com/Till0196/coax/internal/coaxd/tuning"
)

type TuneProfile struct {
	Delivery     Delivery
	System       string
	FrequencyKHz int32
	BandwidthMHz int32
	SymbolRate   int32
	StreamID     int32
	Modulation   string
	Polarization string
	// LNB describes the block on the dish, when the channel named one.
	//
	// Windows works the opposite way round from every other backend: the others
	// are given the intermediate frequency and tune it, while a BDA driver is
	// given the downlink plus the block and does the subtraction itself -- which
	// is also how it decides whether the tone belongs on the lead. So the
	// daemon's conversion is undone here and the two halves handed over
	// separately. Zero means no block was named.
	LNB LNBInfo
	// Diseqc is the message to put on the lead before the channel is asked for,
	// when the channel named a position on a switch. See channels.DiseqcFor.
	Diseqc       channels.Diseqc
	DiseqcWanted bool
}

// SatNumberPropertyName is the channel-file key naming the DiSEqC position, so
// that a message about it here spells it the same way the file does.
const SatNumberPropertyName = channels.SatNumberProperty

// LNBInfo is what a BDA tuning space is told about the block on the dish. All
// three are in kilohertz, which is what IDVBSTuningSpace takes.
type LNBInfo struct {
	LowOscillatorKHz  int32
	HighOscillatorKHz int32
	SwitchKHz         int32
}

// Known reports whether the channel named a block.
func (info LNBInfo) Known() bool { return info.LowOscillatorKHz != 0 }

func BuildTuneProfile(properties map[string]string) (TuneProfile, error) {
	system := normalize(properties["DELIVERY_SYSTEM"])
	delivery, err := deliveryFor(system)
	if err != nil {
		return TuneProfile{}, err
	}
	frequency, err := requiredInt64(properties, "FREQUENCY")
	if err != nil {
		return TuneProfile{}, err
	}
	// DVBv5 files normally use Hz. Satellite IF tables in common use kHz.
	if frequency >= 50_000_000 {
		frequency /= 1000
	}
	if frequency <= 0 || frequency > int64(^uint32(0)>>1) {
		return TuneProfile{}, fmt.Errorf("FREQUENCY is outside the BDA range: %d kHz", frequency)
	}
	// BDA has no LNB supply control: the tune request carries the polarization
	// and the voltage is the driver's business. A satellite channel asking for no
	// supply is refused rather than ignored, because the reason to ask is usually
	// that something else is feeding the line. A terrestrial channel carrying the
	// setting is left alone; there is no supply there.
	lnb, err := channels.ParseLNB(properties)
	if err != nil {
		return TuneProfile{}, err
	}
	if lnb == channels.LNBOff && delivery == DeliverySatellite {
		return TuneProfile{}, fmt.Errorf("%w: a BDA tuner's LNB supply is the driver's to switch, not this program's", channels.ErrNoLNBSwitch)
	}
	profile := TuneProfile{
		Delivery: delivery, System: system, FrequencyKHz: int32(frequency),
		BandwidthMHz: -1, SymbolRate: -1, StreamID: -1,
		Modulation:   normalize(properties["MODULATION"]),
		Polarization: normalize(properties["POLARIZATION"]),
	}
	// A channel naming a block is handed over as the downlink with the block
	// beside it, which is the shape this interface takes.
	if lnbf, ok := channels.LNBfOf(properties); ok && delivery == DeliverySatellite {
		tone := channels.ToneWanted(properties)
		profile.FrequencyKHz = int32(lnbf.DownlinkKHz(uint64(profile.FrequencyKHz), tone))
		profile.LNB = LNBInfo{
			LowOscillatorKHz:  int32(lnbf.LowLO) * 1000,
			HighOscillatorKHz: int32(lnbf.HighLO) * 1000,
			SwitchKHz:         int32(lnbf.SwitchMHz) * 1000,
		}
		// A single-oscillator block says so by putting it on both, which a driver
		// reads as "there is no band to switch".
		if profile.LNB.HighOscillatorKHz == 0 {
			profile.LNB.HighOscillatorKHz = profile.LNB.LowOscillatorKHz
		}
	}
	// The message is built the same way for both backends -- the standard's own
	// bytes -- and only the sending differs.
	if command, wanted, diseqcErr := channels.DiseqcFor(properties); diseqcErr != nil {
		return TuneProfile{}, diseqcErr
	} else if wanted {
		profile.Diseqc, profile.DiseqcWanted = command, true
	}
	if value := properties["BANDWIDTH_HZ"]; value != "" && normalize(value) != "AUTO" {
		bandwidth, parseErr := strconv.ParseInt(strings.TrimSpace(value), 0, 32)
		if parseErr != nil || bandwidth <= 0 {
			return TuneProfile{}, fmt.Errorf("invalid BANDWIDTH_HZ %q", value)
		}
		profile.BandwidthMHz = int32((bandwidth + 500_000) / 1_000_000)
	}
	if value := properties["SYMBOL_RATE"]; value != "" && normalize(value) != "AUTO" {
		symbolRate, parseErr := strconv.ParseInt(strings.TrimSpace(value), 0, 32)
		if parseErr != nil || symbolRate <= 0 {
			return TuneProfile{}, fmt.Errorf("invalid SYMBOL_RATE %q", value)
		}
		profile.SymbolRate = int32(symbolRate)
	}
	if value := first(properties, "STREAM_ID", "TS_ID"); value != "" && normalize(value) != "AUTO" {
		streamID, parseErr := strconv.ParseInt(strings.TrimSpace(value), 0, 32)
		if parseErr != nil || streamID < 0 {
			return TuneProfile{}, fmt.Errorf("invalid STREAM_ID %q", value)
		}
		profile.StreamID = int32(streamID)
	}
	return profile, nil
}

func requiredInt64(properties map[string]string, key string) (int64, error) {
	value := strings.TrimSpace(properties[key])
	if value == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	parsed, err := strconv.ParseInt(value, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", key, value)
	}
	return parsed, nil
}

func first(properties map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := properties[key]; value != "" {
			return value
		}
	}
	return ""
}
