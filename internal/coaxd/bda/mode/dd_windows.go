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

// ddSwitcher describes the standard selected for a DD graph. Publishing filters
// is a separate persistent setting which a tune never changes.
type ddSwitcher struct {
	tuner filters.Moniker
}

func (mode *ddSwitcher) Family() string                         { return "Digital Devices" }
func (mode *ddSwitcher) NameFor(value vendors.DemodMode) string { return dd.ModeName(value) }

func (mode *ddSwitcher) BeforeGraph(tuning.TuneProfile) (vendors.DemodMode, error) {
	return vendors.ModeUnknown, nil
}

// Current follows the running graph, which may override the filter's advertised
// standard. Without a graph only the filter's name is available.
func (mode *ddSwitcher) Current(session *graph.Session) (vendors.DemodMode, error) {
	if session != nil {
		if standard, known := dd.ModeFor(session.System()); known {
			return standard, nil
		}
	}
	standard, named := dd.StandardOf(mode.tuner.Vendor())
	if !named {
		return vendors.ModeUnknown, nil
	}
	return standard, nil
}
