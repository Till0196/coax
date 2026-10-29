// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/tbs"
)

// PropertyBus is a tuner's own I2C bus, reached through the vendor property the
// TBS driver puts on every tuner's input pin.
//
// The mode property next door is card-wide while the hardware is four
// independent demodulators on four independent buses. This path reaches them
// directly: each tuner filter's pin is wired to its own I2C block in the card's
// FPGA, control at +0, data at +4. Addresses are the seven-bit ones the chip
// drivers use; the property wants them shifted.
type PropertyBus struct {
	*tbs.Bus
	// access is set when this bus owns the graph it reached the pin through,
	// and nil when the bus was borrowed from a session that owns it already.
	access *graph.Access
}

// OpenPropertyBus opens the bus behind one tuner. The caller closes it.
func OpenPropertyBus(selector string) (*PropertyBus, error) {
	tuners, err := filters.Enumerate(filters.CategoryNetworkTuner)
	if err != nil {
		return nil, fmt.Errorf("enumerate BDA tuners: %w", err)
	}
	defer filters.ReleaseAll(tuners)
	selected, err := filters.Select(tuners, selector)
	if err != nil {
		return nil, err
	}
	access, err := graph.OpenAccess(selected)
	if err != nil {
		return nil, err
	}
	return &PropertyBus{
		Bus:    &tbs.Bus{Properties: access.Properties(), Name: selected.Name},
		access: access,
	}, nil
}

func (bus *PropertyBus) Close() error {
	// A nil access means the bus was borrowed from a session that owns the graph.
	if bus.access != nil {
		bus.access.Close()
		bus.access = nil
	}
	return nil
}

// Bus is the tuner's I2C bus, reached through the graph this device already has
// running -- the only way while the tuner is in use, since a second graph
// cannot be built on a held tuner.
//
// It is valid only as long as that graph: retuning, closing the device, or
// closing a stream taken from it invalidates it. Check GraphRunning and take it
// again.
func (device *Device) Bus() (*PropertyBus, error) {
	if device.session == nil {
		return nil, fmt.Errorf("BDA device is not tuned")
	}
	property, err := device.session.VendorProperty()
	if err != nil {
		return nil, err
	}
	return &PropertyBus{Bus: &tbs.Bus{Properties: property, Name: device.name}}, nil
}

// CaptureControl is the receiver component of the graph this device has
// running, for a driver that puts its vendor properties there rather than on
// the tuner. Like Bus, it is valid only as long as that graph. The caller
// releases it.
func (device *Device) CaptureControl() (*vendors.Control, error) {
	if device.session == nil {
		return nil, fmt.Errorf("BDA device is not tuned")
	}
	capture := device.session.CaptureFilter()
	if capture == nil {
		return nil, fmt.Errorf("the graph has no receiver component")
	}
	return vendors.Filter(capture)
}
