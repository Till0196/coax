// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package vendors holds what this host knows about the private interfaces of
// the cards it drives on Windows: the property sets, item numbers, model tables
// and what the values mean. The bda package beside it knows how Windows tunes
// and streams, which is the same for every card.
//
// Nothing here calls COM: a caller passes in something that can get and set a
// property, and which set, item, width and meaning is decided here.
package vendors

import (
	"fmt"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/win32"
)

// Tuner names a card's tuner to these packages. Both fields are needed: the path
// is what a vendor is reliably recognised from, and the name is all that is left
// when a moniker carries no path.
type Tuner struct {
	Name string
	Path string
}

// GUID is how a property set is named, spelled the way the Windows bindings
// spell it.
type GUID = win32.GUID

// Properties is a filter's private property interface, however the caller
// reaches it.
type Properties interface {
	// Get reads an item into data and reports how much came back.
	Get(set win32.GUID, id uint32, data []byte) (uint32, error)
	// Set writes an item.
	Set(set win32.GUID, id uint32, data []byte) error
}

// DemodMode is the broadcast standard a multi-standard card is currently wired
// for. Such a card does several standards but only one at a time, and on
// Windows the choice is not part of tuning -- the BDA driver advertises one
// network type, so the card must be told out of band. (On Linux it is part of
// the tune.)
//
// The value is a raw byte whose meaning belongs to the card that answered it: 1
// is DVB-T on a TBS DVB card, ISDB-T on a TBS 6812, DVB-T2 on a Digital Devices
// card.
type DemodMode byte

const (
	ModeUnknown DemodMode = 0
	ModeDVBT    DemodMode = 1 // DVB-T and DVB-T2
	ModeDVBS    DemodMode = 2 // DVB-S and DVB-S2
	ModeATSC    DemodMode = 3
	ModeDVBC    DemodMode = 4 // DVB-C, ITU J.83 Annex A
	ModeISDBT   DemodMode = 5
	ModeQAMB    DemodMode = 6 // MCNS, ITU J.83 Annex B
	ModeDVBC2   DemodMode = 7
)

func (mode DemodMode) String() string {
	switch mode {
	case ModeDVBT:
		return "DVB-T/T2"
	case ModeDVBS:
		return "DVB-S/S2"
	case ModeATSC:
		return "ATSC"
	case ModeDVBC:
		return "DVB-C"
	case ModeISDBT:
		return "ISDB-T"
	case ModeQAMB:
		return "MCNS/QAM-B"
	case ModeDVBC2:
		return "DVB-C2"
	}
	return fmt.Sprintf("mode %d", byte(mode))
}

// ParseDemodMode reads a mode by name or by number.
func ParseDemodMode(value string) (DemodMode, error) {
	switch normalize(value) {
	case "DVBT", "DVBT2", "1":
		return ModeDVBT, nil
	case "DVBS", "DVBS2", "2":
		return ModeDVBS, nil
	case "ATSC", "3":
		return ModeATSC, nil
	case "DVBC", "DVBCANNEXA", "J83A", "4":
		return ModeDVBC, nil
	case "ISDBT", "5":
		return ModeISDBT, nil
	case "MCNS", "QAMB", "DVBCANNEXB", "J83B", "6":
		return ModeQAMB, nil
	case "DVBC2", "7":
		return ModeDVBC2, nil
	}
	return ModeUnknown, fmt.Errorf("unknown demodulator mode %q", value)
}

// normalize folds a name to the form the tables here are written in.
func normalize(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '-' || character == '_' || character == '/' || character == ' ' {
			return -1
		}
		return character
	}, strings.ToUpper(value))
}
