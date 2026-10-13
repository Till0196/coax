// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import "github.com/Till0196/coax/internal/coaxd/bda/vendors"

// DemodMode is the standard a multi-standard card is wired for. Which values a
// card takes and what they mean is vendor knowledge, kept in the vendors
// package; how a card is told to change is in the mode package.
type DemodMode = vendors.DemodMode

const (
	ModeUnknown = vendors.ModeUnknown
	ModeDVBT    = vendors.ModeDVBT
	ModeDVBS    = vendors.ModeDVBS
	ModeATSC    = vendors.ModeATSC
	ModeDVBC    = vendors.ModeDVBC
	ModeISDBT   = vendors.ModeISDBT
	ModeQAMB    = vendors.ModeQAMB
	ModeDVBC2   = vendors.ModeDVBC2
)

// ParseDemodMode reads a mode by name or by number.
func ParseDemodMode(value string) (DemodMode, error) { return vendors.ParseDemodMode(value) }
