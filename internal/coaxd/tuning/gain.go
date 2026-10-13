// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// GainProperty is the channel-file key setting the front end's gain.
//
// One key and not one per amplifier: every front end this program drives has a
// single setting (the Linux DVB API's DTV_LNA, the PX-MLT's external
// amplifiers, the FC0012's twenty-four step ladder, the RT710's fine gain), and
// the one known to have more, the PX-W3U3 V2's satellite side, is not ported.
//
// The loop-through gain some of these tuners carry is deliberately not here: it
// is the level handed to the next receiver down the lead.
const GainProperty = "GAIN"

// GainKind is which of the ways of asking a channel used.
type GainKind int

const (
	// GainUnset is a channel that did not say: the hardware is left as it
	// starts.
	GainUnset GainKind = iota
	// GainAuto asks whoever is driving the front end to decide.
	GainAuto
	// GainMin and GainMax are the ends of whatever range the hardware has.
	GainMin
	GainMax
	// GainDecibels is a level, which is matched to the nearest setting the
	// hardware actually has.
	GainDecibels
)

// Gain is what a channel asked the front end for.
type Gain struct {
	Kind GainKind
	// Decibels is meaningful only for GainDecibels.
	Decibels float64
}

// Given reports whether the channel asked for anything.
func (gain Gain) Given() bool { return gain.Kind != GainUnset }

// String is the setting spelled the way a channel file writes it.
func (gain Gain) String() string {
	switch gain.Kind {
	case GainAuto:
		return "auto"
	case GainMin:
		return "min"
	case GainMax:
		return "max"
	case GainDecibels:
		return DecibelString(gain.Decibels)
	default:
		return ""
	}
}

// DecibelString writes a level the way this program accepts one, so that what
// a tuner reports back can be pasted into a channel file unchanged.
func DecibelString(decibels float64) string {
	text := strconv.FormatFloat(decibels, 'f', -1, 64)
	if decibels >= 0 {
		text = "+" + text
	}
	return text + "dB"
}

// ParseGainValue reads one written setting; an empty value means the channel did
// not say, which is not an error. A level must carry its unit, decibels being
// the only one that means the same thing on two chips: a bare "13" is more
// likely a step number meant for some other receiver.
func ParseGainValue(value string) (Gain, error) {
	trimmed := strings.Trim(strings.TrimSpace(value), "\"'")
	if trimmed == "" {
		return Gain{}, nil
	}
	switch Normalize(trimmed) {
	case "AUTO":
		return Gain{Kind: GainAuto}, nil
	// off and on are here because the LNA property spells the ends that way,
	// and a file may reasonably write either key with either word.
	case "MIN", "OFF", "LOW":
		return Gain{Kind: GainMin}, nil
	case "MAX", "ON", "HIGH":
		return Gain{Kind: GainMax}, nil
	}
	number := strings.TrimSpace(trimmed)
	if !strings.HasSuffix(strings.ToUpper(number), "DB") {
		return Gain{}, fmt.Errorf("%q is not a gain setting; write auto, min, max, or a level with its unit such as +13dB", value)
	}
	number = strings.TrimSpace(number[:len(number)-2])
	decibels, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return Gain{}, fmt.Errorf("%q is not a gain setting: %q is not a number of decibels", value, number)
	}
	if math.IsNaN(decibels) || math.IsInf(decibels, 0) {
		return Gain{}, fmt.Errorf("%q is not a gain setting", value)
	}
	return Gain{Kind: GainDecibels, Decibels: decibels}, nil
}

// ParseGain reads the gain setting out of a channel's properties. LNA is
// accepted as the older spelling, being what the Linux DVB API calls its
// property. Writing both is refused rather than resolved.
func ParseGain(properties map[string]string) (Gain, error) {
	gain, err := ParseGainValue(properties[GainProperty])
	if err != nil {
		return Gain{}, err
	}
	lna, err := ParseGainValue(properties[LNAProperty])
	if err != nil {
		return Gain{}, fmt.Errorf("%s: %w", LNAProperty, err)
	}
	if gain.Given() && lna.Given() {
		return Gain{}, fmt.Errorf("%s and %s are two spellings of the same setting; give one of them",
			GainProperty, LNAProperty)
	}
	if lna.Given() {
		return lna, nil
	}
	return gain, nil
}

// ErrNoGain reports that a tuner cannot do what a channel asked of its gain: it
// has no setting to change, or its amplifier is a switch and was asked for a
// level. Like ErrNoLNBSwitch it is a property of the tuner rather than a fault,
// and is answered as such (409 unsupported, reason "gain"), so a caller can
// tell a viewer why and try another tuner.
var ErrNoGain = errors.New("this tuner cannot set its gain as asked")

// RefuseGain is what a backend with no gain setting answers a channel that asked
// for one. Letting the key fall through would silently discard the setting.
func RefuseGain(properties map[string]string, what string) error {
	gain, err := ParseGain(properties)
	if err != nil {
		return err
	}
	if !gain.Given() {
		return nil
	}
	return fmt.Errorf("%s has no gain setting to change, so %s=%s cannot be honoured: %w",
		what, GainProperty, gain, ErrNoGain)
}

// GainLadder is what one front end's amplifier can be set to: a number of
// steps, least gain first, and what each is worth where the hardware says.
type GainLadder struct {
	// Decibels is each step's gain, nil for hardware with settings but no
	// published figures: the PX-MLT's amplifier is in or out of circuit and
	// the chip does not say by how much.
	Decibels []float64
	// Steps is how many settings there are.
	Steps int
}

// NewGainLadder describes an amplifier whose steps are known, in decibels,
// least first.
func NewGainLadder(decibels ...float64) GainLadder {
	return GainLadder{Decibels: decibels, Steps: len(decibels)}
}

// NewGainSwitch describes an amplifier that is only in or out of circuit.
func NewGainSwitch() GainLadder { return GainLadder{Steps: 2} }

// Usable reports whether there is anything here to set.
func (ladder GainLadder) Usable() bool { return ladder.Steps > 1 }

// Choose picks the step a channel's setting asks for. A level is matched to the
// nearest step, an exact tie going to the lower one (not worth relying on: the
// levels are decimal and the arithmetic binary). Asking for a level a switch
// cannot express is refused rather than rounded to an end.
func (ladder GainLadder) Choose(gain Gain) (int, error) {
	if !ladder.Usable() {
		return 0, fmt.Errorf("this front end has no gain setting to change: %w", ErrNoGain)
	}
	switch gain.Kind {
	case GainMin:
		return 0, nil
	case GainMax:
		return ladder.Steps - 1, nil
	case GainDecibels:
		if len(ladder.Decibels) == 0 {
			return 0, fmt.Errorf("this front end's amplifier is a switch rather than a level: write %s=min, max or auto: %w",
				GainProperty, ErrNoGain)
		}
		best, distance := 0, math.Abs(ladder.Decibels[0]-gain.Decibels)
		for step := 1; step < len(ladder.Decibels); step++ {
			if candidate := math.Abs(ladder.Decibels[step] - gain.Decibels); candidate < distance {
				best, distance = step, candidate
			}
		}
		return best, nil
	default:
		return 0, fmt.Errorf("%s=%s does not name a step", GainProperty, gain)
	}
}

// Describe says what a step is, for reporting back what was chosen. Where the
// steps have no published figures it falls back to naming the ends.
func (ladder GainLadder) Describe(step int) string {
	if step < 0 || step >= ladder.Steps {
		return ""
	}
	if step < len(ladder.Decibels) {
		return DecibelString(ladder.Decibels[step])
	}
	switch step {
	case 0:
		return "min"
	case ladder.Steps - 1:
		return "max"
	default:
		return strconv.Itoa(step)
	}
}
