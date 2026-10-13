// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package bda

import (
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	coaxtuning "github.com/Till0196/coax/internal/coaxd/tuning"
)

// Channels do not depend on Windows, so they live in the tuning package and are
// aliased here. A profile built anywhere is the same profile this package tunes.

type (
	// Delivery is the broad family a delivery system belongs to.
	Delivery = tuning.Delivery
	// TuneProfile is one channel, in the terms a BDA graph is built from.
	TuneProfile = tuning.TuneProfile
)

const (
	DeliveryTerrestrial = tuning.DeliveryTerrestrial
	DeliveryCable       = tuning.DeliveryCable
	DeliverySatellite   = tuning.DeliverySatellite
)

// BuildTuneProfile turns DVBv5-style channel properties into a tune profile.
func BuildTuneProfile(properties map[string]string) (TuneProfile, error) {
	// Neither BDA nor the TBS vendor property sets expose an amplifier setting,
	// so a channel asking for one is refused rather than silently tuned without.
	if err := coaxtuning.RefuseGain(properties, "a Windows BDA driver"); err != nil {
		return TuneProfile{}, err
	}
	return tuning.BuildTuneProfile(properties)
}

// BuildDrivenProfile is BuildTuneProfile for a backend that drives the supply on
// the aerial lead itself -- the TBS vendor property, a Digital Devices card's
// register -- and has already done what the channel asked of it. The supply is
// taken out before the properties reach the BDA side, which would otherwise
// refuse a satellite channel asking for none (a BDA driver's supply being its
// own), and refuse it after the card had already switched the supply off.
func BuildDrivenProfile(properties map[string]string) (TuneProfile, error) {
	return BuildTuneProfile(WithoutSupply(properties))
}

// WithoutSupply is the properties less the LNB supply, for a backend that sets
// the supply itself before handing the rest to a BDA graph.
func WithoutSupply(properties map[string]string) map[string]string {
	if _, ok := properties["VOLTAGE"]; !ok {
		return properties
	}
	rest := make(map[string]string, len(properties))
	for key, value := range properties {
		if key != "VOLTAGE" {
			rest[key] = value
		}
	}
	return rest
}

// Supports reports whether this backend receives a delivery system at all.
func Supports(deliverySystem string) bool { return tuning.Supports(deliverySystem) }
