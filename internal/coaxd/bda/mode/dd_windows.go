// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package mode

import (
	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/dd"
)

// ddSwitcher is a Digital Devices tuner, which has no mode to switch: these
// cards offer one BDA filter per standard, so the choice was made when the
// filter was opened. Which standards have a filter at all is a separate,
// persistent setting (bda.ReadDDStandards, bda.WriteDDFilterMask) that a tune
// may not change.
type ddSwitcher struct {
	tuner filters.Moniker
}

func (mode *ddSwitcher) Family() string                         { return "Digital Devices" }
func (mode *ddSwitcher) NameFor(value vendors.DemodMode) string { return dd.ModeName(value) }

func (mode *ddSwitcher) BeforeGraph(tuning.TuneProfile) (vendors.DemodMode, error) {
	return vendors.ModeUnknown, nil
}

// Current is the standard this filter is for, which the driver puts in its
// name. It needs no graph and does not change.
func (mode *ddSwitcher) Current(*graph.Session) (vendors.DemodMode, error) {
	standard, named := dd.StandardOf(mode.tuner.Vendor())
	if !named {
		return vendors.ModeUnknown, nil
	}
	return standard, nil
}
