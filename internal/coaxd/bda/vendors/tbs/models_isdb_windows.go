// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"fmt"
	"strings"
)

// Model6812 is the TBS 6812, the one card whose mode values are its own
// vocabulary. Its two tuners each carry their own mode: the property is two
// bytes, byte zero for Tuner 0 and byte one for Tuner 1. One tuner on ISDB-T and
// the other on ISDB-S is supported.
var Model6812 = Model{
	Name: "TBS 6812", Width: 2, Reply: 8,
	ModeFor: isdbModeFor,
	NameFor: isdbModeName,
	SlotFor: slot6812,
}

// The TBS 6812's mode values, which are not the DVB cards'. Only 1 and 2 are
// taken -- 1 terrestrial, 2 satellite -- so the byte that means DVB-T on a
// 6205SE means ISDB-T here.
//
// ISDB-S and ISDB-S3 share the satellite value: the driver picks between them by
// symbol rate at tune time, ISDB-S below 33,750,001 sym/s and ISDB-S3 at or
// above.
const (
	isdbModeTerrestrial DemodMode = 1
	isdbModeSatellite   DemodMode = 2
)

func isdbModeFor(system string) (DemodMode, bool) {
	switch system {
	case "ISDBT":
		return isdbModeTerrestrial, true
	case "ISDBS", "ISDBS3":
		return isdbModeSatellite, true
	}
	return ModeUnknown, false
}

func isdbModeName(mode DemodMode) string {
	switch mode {
	case isdbModeTerrestrial:
		return "ISDB-T"
	case isdbModeSatellite:
		return "ISDB-S/S3"
	}
	return fmt.Sprintf("mode %d", byte(mode))
}

// The TBS 6812's identifiers: one PCI device, and one device interface reference
// string per tuner. The reference string is what tells the two tuners apart in a
// moniker, and unlike the friendly name a driver update cannot reword it.
const (
	tbs6812Device = "dev_6178"
	tbs6812Subsys = "subsys_00016812"
	tbs6812TunerA = "{1f7c3bfb-f6dd-438e-ba22-5e42d7f28fea}"
	tbs6812TunerB = "{8aaa8912-3af3-448c-b78d-1a84379c0ca3}"
)

func slot6812(tuner Tuner) int {
	path := strings.ToLower(tuner.Path)
	switch {
	case strings.Contains(path, tbs6812TunerB):
		return 1
	case strings.Contains(path, tbs6812TunerA):
		return 0
	}
	// Without the reference string, fall back to the friendly name, numbered the
	// same way the reference strings are ordered.
	if strings.Contains(strings.ToLower(tuner.Name), "tuner 1") {
		return 1
	}
	return 0
}
