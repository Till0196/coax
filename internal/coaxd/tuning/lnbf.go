// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// An LNBf is a low-noise block downconverter: the thing on the dish that mixes
// the broadcast frequency down to what a coaxial cable and a tuner can carry.
//
// A channel file may name either frequency: a DVB-tools file carries the
// downlink and expects the oscillator to be subtracted, a Japanese-tools file
// the intermediate frequency and expects nobody to touch it. Naming an LNBf,
// spelled as libdvbv5 spells it, says which.
type LNBf struct {
	// Alias is the name a channel file or a caller writes.
	Alias string
	// Also are older names for the same block: looked up but not listed.
	Also []string
	// Name says what it is, for a listing.
	Name string
	// MinMHz and MaxMHz are the downlink frequencies it covers. A frequency
	// outside them is refused rather than downconverted: it is almost always an
	// intermediate frequency given by mistake, and subtracting an oscillator from
	// one produces a plausible number.
	MinMHz, MaxMHz uint32
	// LowLO is the local oscillator, in MHz.
	LowLO uint32
	// HighLO and SwitchMHz describe a second oscillator, selected for
	// frequencies at or above the switch point by putting a 22 kHz tone on
	// the lead. Both are zero on a block with one oscillator.
	HighLO, SwitchMHz uint32
	// Tone forces the tone rather than letting the band decide: the Japanese
	// premium service combines two satellites onto one lead with the same
	// oscillator, and the tone picks between them.
	Tone ToneSetting
}

// ToneSetting is what an LNBf asks of the 22 kHz tone.
type ToneSetting int

const (
	// ToneFromBand leaves the tone to the band, as a universal block wants.
	ToneFromBand ToneSetting = iota
	ToneOff
	ToneOn
)

// lnbfs are the blocks this program knows: the first six from libdvbv5's table,
// the last two from szap-s2j, which libdvbv5 has no entry for. Blocks that
// stack two polarizations onto one lead with a different oscillator for each
// are deliberately absent: they need the polarization to pick an oscillator,
// and none of the hardware here is wired to one.
var lnbfs = []LNBf{
	{
		Alias: "UNIVERSAL", Name: "Universal Ku (Europe)",
		MinMHz: 10800, MaxMHz: 12700,
		LowLO: 9750, HighLO: 10600, SwitchMHz: 11700,
	},
	{
		Alias: "EXTENDED", Name: "Extended universal Ku (Astra 19.2E)",
		MinMHz: 10700, MaxMHz: 12750,
		LowLO: 9750, HighLO: 10600, SwitchMHz: 11700,
	},
	{
		Alias: "ENHANCED", Name: "Enhanced Ku (Astra)",
		MinMHz: 10700, MaxMHz: 11700, LowLO: 9750,
	},
	{
		Alias: "STANDARD", Name: "Standard Ku",
		MinMHz: 10945, MaxMHz: 11450, LowLO: 10000,
	},
	{
		Alias: "DBS", Name: "DBS (North America)",
		MinMHz: 12200, MaxMHz: 12700, LowLO: 11250,
	},
	{
		Alias: "C-BAND", Name: "C band",
		MinMHz: 3700, MaxMHz: 4200, LowLO: 5150,
	},
	{
		Alias: "110BS", Name: "Japan BS / CS-110",
		MinMHz: 11710, MaxMHz: 12751, LowLO: 10678,
	},
	{
		// The DVB-S2 communications satellite at 128 degrees east: tone off here, on
		// for the other, same oscillator. The band is the block's 12.2 to 12.75 GHz
		// rather than szap-s2j's span of transponders in use.
		Alias: "JCSAT3A", Name: "Japan 128E CS (DVB-S2)",
		MinMHz: 12200, MaxMHz: 12750, LowLO: 11200, Tone: ToneOff,
	},
	{
		// The same dish and lead at 124 degrees east. JCSAT-4A held this slot
		// until 2012 and JCSAT-4B holds it now, but szap-s2j still writes the
		// older name.
		Alias: "JCSAT4B", Also: []string{"JCSAT4A"},
		Name:   "Japan 124E CS (DVB-S2)",
		MinMHz: 12200, MaxMHz: 12750, LowLO: 11200, Tone: ToneOn,
	},
}

// LookupLNBf finds a block by the name a channel file or a caller wrote.
func LookupLNBf(alias string) (LNBf, bool) {
	wanted := Normalize(strings.Trim(strings.TrimSpace(alias), "\"'"))
	for _, lnbf := range lnbfs {
		if Normalize(lnbf.Alias) == wanted {
			return lnbf, true
		}
		for _, also := range lnbf.Also {
			if Normalize(also) == wanted {
				return lnbf, true
			}
		}
	}
	return LNBf{}, false
}

// LNBfAliases names every accepted spelling, older ones included.
func LNBfAliases() []string {
	aliases := make([]string, 0, len(lnbfs))
	for _, lnbf := range lnbfs {
		aliases = append(aliases, lnbf.Alias)
		aliases = append(aliases, lnbf.Also...)
	}
	sort.Strings(aliases)
	return aliases
}

// LNBfs lists the blocks with their descriptions, for a listing.
func LNBfs() []LNBf { return append([]LNBf(nil), lnbfs...) }

// Downconvert turns a downlink frequency into the intermediate frequency the
// tuner is given, and says whether the lead needs the 22 kHz tone. Both are in
// kilohertz, as a satellite channel file and the DVB API use.
func (lnbf LNBf) Downconvert(downlinkKHz uint64) (intermediateKHz uint64, tone bool, err error) {
	megahertz := downlinkKHz / 1000
	if megahertz < uint64(lnbf.MinMHz) || megahertz > uint64(lnbf.MaxMHz) {
		return 0, false, fmt.Errorf("%d kHz is outside what a %s block receives, %d..%d MHz; a satellite frequency given with an LNBf is the downlink and not the intermediate frequency",
			downlinkKHz, lnbf.Alias, lnbf.MinMHz, lnbf.MaxMHz)
	}
	oscillator := uint64(lnbf.LowLO)
	high := lnbf.SwitchMHz != 0 && lnbf.HighLO != 0 && megahertz >= uint64(lnbf.SwitchMHz)
	if high {
		oscillator = uint64(lnbf.HighLO)
	}
	switch lnbf.Tone {
	case ToneOff:
		tone = false
	case ToneOn:
		tone = true
	default:
		tone = high
	}
	// C band mixes down from an oscillator above the band, so the difference
	// runs the other way; unsigned subtraction would wrap 3.7 GHz round.
	if oscillatorKHz := oscillator * 1000; downlinkKHz < oscillatorKHz {
		return oscillatorKHz - downlinkKHz, tone, nil
	}
	return downlinkKHz - oscillator*1000, tone, nil
}

// LNBProperty is the channel-file key naming the block, spelled as libdvbv5
// spells it.
const LNBProperty = "LNB"

// ApplyLNBf reads the block a channel names and rewrites the channel to suit
// it: the downlink frequency becomes the intermediate frequency the tuner is
// given, and the tone the lead needs is filled in.
//
// It does nothing unless a block is named -- the Japanese satellite channel
// files already carry the intermediate frequency -- so naming one is a channel
// saying "this frequency is a downlink, convert it". A channel that sets TONE
// itself keeps what it set.
func ApplyLNBf(properties map[string]string) error {
	alias := properties[LNBProperty]
	if strings.TrimSpace(alias) == "" {
		return nil
	}
	lnbf, ok := LookupLNBf(alias)
	if !ok {
		return fmt.Errorf("no LNBf is called %q; this program knows %s", alias, strings.Join(LNBfAliases(), ", "))
	}
	if !IsSatellite(properties["DELIVERY_SYSTEM"]) {
		return fmt.Errorf("an LNBf is a satellite thing, and %q is not a satellite delivery system",
			properties["DELIVERY_SYSTEM"])
	}
	downlink, err := Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	intermediate, tone, err := lnbf.Downconvert(downlinkKHz(downlink))
	if err != nil {
		return err
	}
	properties["FREQUENCY"] = strconv.FormatUint(intermediate, 10)
	// The name stays because Windows BDA works the other way round: its driver is
	// told the block and converts the downlink itself, so that backend has to undo
	// this. The block's own name is written back, settling older spellings,
	// quoting and case.
	properties[LNBProperty] = lnbf.Alias
	if _, given := properties["TONE"]; !given {
		if tone {
			properties["TONE"] = "ON"
		} else {
			properties["TONE"] = "OFF"
		}
	}
	return nil
}

// IsSatellite reports whether a delivery system is carried on a satellite.
func IsSatellite(deliverySystem string) bool {
	switch Normalize(deliverySystem) {
	case "DVBS", "DVBS2", "DVBS2X", "TURBO", "ISDBS", "ISDBS3", "DSS":
		return true
	default:
		return false
	}
}

// downlinkKHz folds a downlink frequency to kilohertz. This package's usual
// rule -- ten million or more can only be hertz -- is wrong here, since a
// Ku-band downlink in kilohertz is twelve million. A billion is the threshold
// instead.
func downlinkKHz(frequency uint64) uint64 {
	if frequency >= 1_000_000_000 {
		return frequency / 1000
	}
	return frequency
}

// ToneWanted reports whether a channel asks for the continuous 22 kHz tone, via
// the TONE property that ApplyLNBf fills in. Absence means no tone.
func ToneWanted(properties map[string]string) bool {
	return Normalize(strings.Trim(strings.TrimSpace(properties["TONE"]), "\"'")) == "ON"
}

// LNBfOf reports the block a channel named, after ApplyLNBf has run.
func LNBfOf(properties map[string]string) (LNBf, bool) {
	alias := properties[LNBProperty]
	if strings.TrimSpace(alias) == "" {
		return LNBf{}, false
	}
	return LookupLNBf(alias)
}

// DownlinkKHz turns an intermediate frequency back into its downlink, for a
// backend whose driver converts for itself. The tone says which oscillator of a
// two-oscillator block was used, having selected it.
func (lnbf LNBf) DownlinkKHz(intermediateKHz uint64, tone bool) uint64 {
	oscillator := uint64(lnbf.LowLO)
	if tone && lnbf.HighLO != 0 {
		oscillator = uint64(lnbf.HighLO)
	}
	// C band mixes down from above, so the difference ran the other way.
	if uint64(lnbf.MinMHz)*1000 < oscillator*1000 && intermediateKHz < oscillator*1000 {
		return oscillator*1000 - intermediateKHz
	}
	return intermediateKHz + oscillator*1000
}
