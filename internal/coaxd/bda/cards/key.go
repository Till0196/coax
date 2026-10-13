// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package cards names the physical card a BDA tuner sits on and tracks which
// cards have a graph running. Writing a card's demodulator mode restarts every
// tuner on that card, so a mode switch must be able to see what else on it is
// streaming -- and for that the card needs a name.
package cards

import "strings"

// KeyOf reduces a tuner's device path to the card it is on. Tuners on one card
// share a device instance in their moniker and differ only after the interface
// class, so cutting at "#{" names the card and tells two cards apart.
func KeyOf(selector string) string {
	lowered := strings.ToLower(selector)
	if index := strings.Index(lowered, "#{"); index >= 0 {
		return lowered[:index]
	}
	return lowered
}
