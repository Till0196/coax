// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package mode

import (
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// noSwitcher is a tuner whose standard is not a setting, and what an
// unrecognised card gets. The point is the writing: reading another vendor's
// property is merely useless, writing one reaches hardware that never agreed to
// the meaning.
type noSwitcher struct{}

func (*noSwitcher) Family() string                         { return "" }
func (*noSwitcher) NameFor(value vendors.DemodMode) string { return value.String() }

func (*noSwitcher) BeforeGraph(tuning.TuneProfile) (vendors.DemodMode, error) {
	return vendors.ModeUnknown, nil
}

func (*noSwitcher) Current(*graph.Session) (vendors.DemodMode, error) {
	return vendors.ModeUnknown, nil
}
