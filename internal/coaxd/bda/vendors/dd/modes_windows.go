// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package dd is what this host knows about Digital Devices' private interface:
// two property sets on a tuner filter, one that says what the tuner is and can
// be made to be, and one that says what its demodulator is measuring.
package dd

import (
	"fmt"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// DemodMode is the standard a card is wired for, as named next door.
type DemodMode = vendors.DemodMode

// Tuner is a card's tuner, as named next door.
type Tuner = vendors.Tuner

// ModeUnknown is the value for a standard this package does not recognise.
const ModeUnknown = vendors.ModeUnknown

// The Digital Devices standard values the KS property takes. The driver accepts
// anything below 26 its per-tuner capability mask allows; the gaps are real (7
// and 12..15 name no standard). The satellite ones are 4, 5, 6, 10 and 11.
//
// This is NOT the MX_SUPPORT_* numbering of the same vendor's capability word,
// where DVB-S is bit 0; mixing the two writes a wrong standard. Annex C has no
// value of its own -- the driver demodulates it with ISDB-C -- so both names
// map to the ISDB-C value.
const (
	ModeDVBT   DemodMode = 0
	ModeDVBT2  DemodMode = 1
	ModeDVBC   DemodMode = 2
	ModeDVBC2  DemodMode = 3
	ModeDVBS   DemodMode = 4
	ModeDVBS2  DemodMode = 5
	ModeDVBS2X DemodMode = 6
	ModeISDBT  DemodMode = 8
	ModeISDBC  DemodMode = 9
	ModeISDBS  DemodMode = 10
	ModeISDBS3 DemodMode = 11
	ModeJ83B   DemodMode = 16
	ModeATSC   DemodMode = 17
	ModeATSC3  DemodMode = 18
)

func ModeFor(system string) (DemodMode, bool) {
	switch system {
	case "DVBT":
		return ModeDVBT, true
	case "DVBT2":
		return ModeDVBT2, true
	case "DVBC", "DVBCANNEXA", "J83A":
		return ModeDVBC, true
	case "DVBC2":
		return ModeDVBC2, true
	case "DVBS":
		return ModeDVBS, true
	case "DVBS2":
		return ModeDVBS2, true
	case "ISDBT":
		return ModeISDBT, true
	case "ISDBC", "DVBCANNEXC", "J83C":
		return ModeISDBC, true
	case "ISDBS":
		return ModeISDBS, true
	case "ISDBS3":
		return ModeISDBS3, true
	case "DVBCANNEXB", "J83B":
		return ModeJ83B, true
	case "ATSC":
		return ModeATSC, true
	}
	return ModeUnknown, false
}

func ModeName(mode DemodMode) string {
	switch mode {
	case ModeDVBT:
		return "DVB-T"
	case ModeDVBT2:
		return "DVB-T2"
	case ModeDVBC:
		return "DVB-C"
	case ModeDVBC2:
		return "DVB-C2"
	case ModeDVBS:
		return "DVB-S"
	case ModeDVBS2:
		return "DVB-S2"
	case ModeDVBS2X:
		return "DVB-S2X"
	case ModeISDBT:
		return "ISDB-T"
	case ModeISDBC:
		return "ISDB-C"
	case ModeISDBS:
		return "ISDB-S"
	case ModeISDBS3:
		return "ISDB-S3"
	case ModeJ83B:
		return "J83B"
	case ModeATSC:
		return "ATSC"
	case ModeATSC3:
		return "ATSC3"
	}
	return fmt.Sprintf("mode %d", byte(mode))
}

// Is recognises a Digital Devices tuner. Their cards enumerate on a synthetic
// bus of the driver's own (DD_DVB rather than PCI), so the device path names the
// vendor directly and there is no PCI vendor id to match on.
func Is(tuner Tuner) bool {
	path := strings.ToLower(tuner.Path)
	return strings.Contains(path, "dd_dvb") || strings.Contains(path, "ven_dd01")
}
