// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package mode

import (
	"github.com/Till0196/coax/internal/coaxd/bda/cards"
	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/tbs"
)

// tbsSwitcher is the glue between the packages that build a graph and reach a
// pin, and the tbs package, which knows what the property on that pin means.
type tbsSwitcher struct {
	tuner   filters.Moniker
	card    string
	model   tbs.Model
	slot    int
	switch_ tbs.Switch
}

func newTBSSwitcher(tuner filters.Moniker, model tbs.Model) *tbsSwitcher {
	card := cards.KeyOf(tuner.Display)
	slot := model.SlotFor(tuner.Vendor())
	mode := &tbsSwitcher{tuner: tuner, card: card, model: model, slot: slot}
	mode.switch_ = tbs.Switch{
		Model: model,
		Slot:  slot,
		Open: func() (vendors.Properties, func(), error) {
			access, err := graph.OpenAccess(tuner)
			if err != nil {
				return nil, nil, err
			}
			return access.Properties(), access.Close, nil
		},
		Streaming: func() int { return cards.Streaming(card) },
	}
	return mode
}

func (mode *tbsSwitcher) Family() string                         { return mode.model.Name }
func (mode *tbsSwitcher) NameFor(value vendors.DemodMode) string { return mode.model.NameFor(value) }

func (mode *tbsSwitcher) Current(session *graph.Session) (vendors.DemodMode, error) {
	if session == nil {
		return mode.switch_.Current(nil)
	}
	property, err := session.VendorProperty()
	if err != nil {
		return vendors.ModeUnknown, err
	}
	return mode.switch_.Current(property)
}

func (mode *tbsSwitcher) BeforeGraph(profile tuning.TuneProfile) (vendors.DemodMode, error) {
	return mode.switch_.Ensure(profile.System)
}
