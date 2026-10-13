// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"errors"
	"fmt"
	"strings"
)

// LNB is what a channel asks of the supply on the aerial lead, in the only
// terms every board here can answer. Named rather than numbered because the
// voltages differ per board: a PT3 or px4_drv tuner offers 11 V and 15 V; a DVB
// frontend and the TBS boards 13 V and 18 V, where the choice also selects the
// polarization; a PLEX enclosure gates one supply with a GPIO. The numbers are
// accepted as names too, since a channel file and recpt1 write them.
type LNB int

const (
	// LNBUnset is a channel that said nothing, leaving the backend's default.
	LNBUnset LNB = iota
	// LNBOff asks for no supply at all, as an installation with its own power
	// inserter wants.
	LNBOff
	// LNBLow and LNBHigh are the two levels a board may offer.
	LNBLow
	LNBHigh
)

func (lnb LNB) String() string {
	switch lnb {
	case LNBOff:
		return "off"
	case LNBLow:
		return "low"
	case LNBHigh:
		return "high"
	default:
		return ""
	}
}

// lnbNames are every spelling accepted for a level. Both pairs of voltages mean
// the level rather than the number: 15 on a DVB frontend asks for its higher
// level, which is 18 V.
var lnbNames = map[string]LNB{
	"":     LNBUnset,
	"OFF":  LNBOff,
	"0":    LNBOff,
	"NONE": LNBOff,
	"LOW":  LNBLow,
	"11":   LNBLow,
	"13":   LNBLow,
	"HIGH": LNBHigh,
	"ON":   LNBHigh,
	"15":   LNBHigh,
	"18":   LNBHigh,
}

// LNBNames are the spellings a caller may write, for help text and errors.
var LNBNames = []string{"off", "low", "high", "0", "11", "13", "15", "18", "on"}

// ParseLNBValue reads one written setting. Quotes come off first, as
// dvbv5-scan writes some values that way.
func ParseLNBValue(value string) (LNB, error) {
	lnb, ok := lnbNames[Normalize(strings.Trim(strings.TrimSpace(value), "\"'"))]
	if !ok {
		return LNBUnset, fmt.Errorf("%q is not an LNB setting; write one of off, low, high (11 and 13 mean low, 15 and 18 mean high)", value)
	}
	return lnb, nil
}

// ParseLNB reads the supply setting out of a channel's properties. The property
// is VOLTAGE, what a channel file writes and what the host's lnb parameter
// becomes; absence means the channel did not say.
func ParseLNB(properties map[string]string) (LNB, error) {
	return ParseLNBValue(properties["VOLTAGE"])
}

// ErrNoLNBSwitch reports that a board cannot cut the supply on its aerial lead:
// it is wired on and the only choice is which voltage. A channel asking for
// none is told so rather than fed 13 V, since the reason to ask is usually that
// something else already feeds the line.
var ErrNoLNBSwitch = fmt.Errorf("this tuner cannot switch its LNB supply off")

// PolarizationHigh reports whether a channel's polarization calls for the higher
// of the two voltages, and whether it said. Where the voltage is the
// polarization, horizontal and left-hand circular are the higher one.
func PolarizationHigh(properties map[string]string) (high, known bool) {
	switch Normalize(strings.Trim(strings.TrimSpace(properties["POLARIZATION"]), "\"'")) {
	case "H", "HORIZONTAL", "L", "LEFT", "CIRCULARLEFT":
		return true, true
	case "V", "VERTICAL", "R", "RIGHT", "CIRCULARRIGHT":
		return false, true
	default:
		// "off" is a DVBv5 file saying it has no polarization to give, as
		// is an absent key. Neither decides a voltage.
		return false, false
	}
}

// Supply is what a satellite tune should do with the supply on the aerial
// lead.
type Supply struct {
	// High selects the higher of the two voltages. Where the voltage is the
	// polarization, that is the horizontal or left-hand one.
	High bool
	// Off asks for no supply at all.
	Off bool
	// Explicit is whether the channel asked for this rather than it being the
	// default. It matters to a board that cannot cut its supply: refusing a
	// channel that asked for none is honest, refusing one that never mentioned it
	// would break the board.
	Explicit bool
}

// SatelliteSupply settles what a satellite tune should do with the supply on a
// board where the voltage also selects the polarization.
//
// The polarization wins over the level, deciding whether the channel arrives at
// all; asking for no supply wins over both, being about the installation rather
// than the channel.
//
// A channel that says neither gets no supply, as the other programs do: the
// common installation is a communal aerial or cable pass-through already fed by
// something else, and a second supply on such a line damages equipment rather
// than merely failing.
func SatelliteSupply(properties map[string]string) (Supply, error) {
	lnb, err := ParseLNB(properties)
	if err != nil {
		return Supply{}, err
	}
	if lnb == LNBOff {
		return Supply{Off: true, Explicit: true}, nil
	}
	if polarized, known := PolarizationHigh(properties); known {
		return Supply{High: polarized, Explicit: true}, nil
	}
	if lnb == LNBUnset {
		return Supply{Off: true}, nil
	}
	return Supply{High: lnb == LNBHigh, Explicit: true}, nil
}

// ApplySupply drives a board's voltage switch and reports what it said. Boards
// that cannot cut their supply answer ErrNoLNBSwitch; a refusal of a power-off
// nobody asked for is taken as the line being wired live and the tune carries on.
func ApplySupply(supply Supply, set func(high, off bool) error) error {
	err := set(supply.High, supply.Off)
	if err == nil || supply.Explicit || !supply.Off || !errors.Is(err, ErrNoLNBSwitch) {
		return err
	}
	return set(supply.High, false)
}

// WantsSupply reports whether a channel's setting calls for the line to be fed,
// for boards whose supply is a plain on/off switch. A channel that said nothing
// does not want it; see SatelliteSupply.
func WantsSupply(lnb LNB) bool { return lnb == LNBLow || lnb == LNBHigh }

// LinearPolarization reports whether a channel named a linear polarization, and
// whether it named one at all. Horizontal and vertical are selected by the lead
// voltage alone, so a board with one fixed supply cannot reach both; circular
// left and right are separated by intermediate frequency on Japan's satellites.
func LinearPolarization(properties map[string]string) (linear, known bool) {
	switch Normalize(strings.Trim(strings.TrimSpace(properties["POLARIZATION"]), "\"'")) {
	case "H", "HORIZONTAL", "V", "VERTICAL":
		return true, true
	case "L", "LEFT", "CIRCULARLEFT", "R", "RIGHT", "CIRCULARRIGHT":
		return false, true
	default:
		return false, false
	}
}

// ErrNoLNBVoltageChoice reports that a board feeds its aerial lead at one
// fixed voltage, so a channel that needs a particular one cannot be served.
var ErrNoLNBVoltageChoice = errors.New("this tuner's LNB supply is a switch, not a voltage: it cannot select a linear polarization")

// ErrNoTone reports that a board cannot put the 22 kHz tone on its lead.
var ErrNoTone = errors.New("this tuner cannot put the 22 kHz tone on its aerial lead")

// SwitchedSupply is SatelliteSupply's counterpart for a board whose lead is fed
// through a plain switch: on or off, no voltage to choose and no tone to send.
//
// The two questions it cannot answer are refused rather than dropped: a linear
// polarization needs 13 V or 18 V to select it, and the tone reaches for the
// second satellite of a two-satellite feed, so serving either anyway looks like
// an aerial fault. Circular polarization is not refused, nor is a bare VOLTAGE
// of low or high.
func SwitchedSupply(properties map[string]string) (bool, error) {
	lnb, err := ParseLNB(properties)
	if err != nil {
		return false, err
	}
	if linear, known := LinearPolarization(properties); known && linear {
		return false, fmt.Errorf("%w (POLARIZATION=%s)", ErrNoLNBVoltageChoice,
			strings.TrimSpace(properties["POLARIZATION"]))
	}
	if ToneWanted(properties) {
		return false, ErrNoTone
	}
	return WantsSupply(lnb), nil
}
