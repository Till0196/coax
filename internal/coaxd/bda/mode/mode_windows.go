// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package mode is how one card family is told which standard to be.
//
// A TBS card keeps one setting for the whole card in its EEPROM and must be told
// to change it; a Digital Devices card offers a separate filter per standard, so
// opening the right one is the whole of it; most cards have no such setting.
//
// What the values mean is vendor knowledge, in the vendors package. This is only
// the dispatch and the glue that reaches a card's properties through a graph.
package mode

import (
	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/dd"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/tbs"
)

// Switcher is one card family's answer to being asked which standard it is.
type Switcher interface {
	// Family names the card family, for messages.
	Family() string
	// NameFor says what a mode value means on this family.
	NameFor(mode vendors.DemodMode) string
	// BeforeGraph settles the mode, and is called before the capture graph is
	// built. It reports ModeUnknown when this family has nothing to settle.
	BeforeGraph(profile tuning.TuneProfile) (vendors.DemodMode, error)
	// Current reports the mode now. The session is the running graph when
	// there is one and nil when there is not, because a second graph cannot be
	// built on a tuner the first one is holding.
	Current(session *graph.Session) (vendors.DemodMode, error)
}

// For recognises the card a tuner is on. A card must be recognised to be
// touched; anything else gets noSwitcher, which is most of them -- an ordinary
// BDA device where the standard is not a separate setting at all.
func For(tuner filters.Moniker) Switcher {
	if dd.Is(tuner.Vendor()) {
		return &ddSwitcher{tuner: tuner}
	}
	if model, ok := tbs.ModelFor(tuner.Vendor()); ok {
		return newTBSSwitcher(tuner, model)
	}
	return &noSwitcher{}
}
