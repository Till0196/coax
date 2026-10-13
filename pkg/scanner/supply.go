// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"maps"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/tuner"
)

// supplied is a device that asks for the LNB supply on every satellite channel
// that does not say otherwise. A walk builds its channels from a plan and a
// network's tables, neither of which knows what the dish needs, so --lnb-power
// has to reach them this way.
type supplied struct {
	tuner.Device
	voltage string
}

// withSatelliteSupply wraps the options' device when a supply was asked for.
func withSatelliteSupply(options Options) Options {
	if options.SatelliteVoltage == "" || options.Device == nil {
		return options
	}
	if _, already := options.Device.(supplied); already {
		return options
	}
	options.Device = supplied{Device: options.Device, voltage: options.SatelliteVoltage}
	return options
}

func (device supplied) Tune(properties map[string]string) error {
	if _, said := properties["VOLTAGE"]; said || !isSatellite(properties["DELIVERY_SYSTEM"]) {
		return device.Device.Tune(properties)
	}
	with := maps.Clone(properties)
	with["VOLTAGE"] = device.voltage
	return device.Device.Tune(with)
}

func isSatellite(system string) bool {
	switch channelconfig.NormalizeDeliverySystem(system) {
	case "ISDBS", "ISDBS3", "DVBS", "DVBS2":
		return true
	}
	return false
}
