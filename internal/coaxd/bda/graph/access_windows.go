// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/win32/directshow"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// Access is a graph built only to reach the demod-mode property, plus the pin it
// is read and written through. The property is an IKsPropertySet on the tuner
// filter's input pin, and a KS pin has no handle until connected, so reaching it
// needs a graph even though nothing streams through this one. Only the network
// provider is added: no tuning space, no capture chain, no running graph -- the
// capture chain is exactly what will not build while the card is in the mode
// being left.
type Access struct {
	graph    *directshow.IGraphBuilder
	provider *directshow.IBaseFilter
	tuner    *directshow.IBaseFilter
	pin      *directshow.IPin
	property *vendors.PinProperties
}

func OpenAccess(tunerMoniker filters.Moniker) (*Access, error) {
	graph, err := createGraph()
	if err != nil {
		return nil, fmt.Errorf("create DirectShow graph: %w", err)
	}
	access := &Access{graph: graph}
	success := false
	defer func() {
		if !success {
			access.Close()
		}
	}()

	access.provider, err = createBaseFilter(clsidMSNetworkProvider)
	if err != nil {
		return nil, fmt.Errorf("create BDA network provider: %w", err)
	}
	if err := graph.AddFilter(access.provider, "BDA Network Provider"); err != nil {
		return nil, fmt.Errorf("add BDA network provider: %w", err)
	}
	access.tuner, err = bindBaseFilter(tunerMoniker.Moniker)
	if err != nil {
		return nil, fmt.Errorf("bind BDA tuner %q: %w", tunerMoniker.Name, err)
	}
	if err := graph.AddFilter(access.tuner, tunerMoniker.Name); err != nil {
		return nil, fmt.Errorf("add BDA tuner %q: %w", tunerMoniker.Name, err)
	}
	if err := connectFilters(graph, access.provider, access.tuner); err != nil {
		return nil, fmt.Errorf("connect network provider to tuner: %w", err)
	}

	access.pin = findPin(access.tuner, directshow.PINDIR_INPUT)
	if access.pin == nil {
		return nil, fmt.Errorf("the BDA tuner has no input pin")
	}
	property, err := vendors.Pin(access.pin)
	if err != nil {
		return nil, err
	}
	access.property = property

	success = true
	return access, nil
}

func (access *Access) Close() {
	if access.property != nil {
		access.property.Release()
		access.property = nil
	}
	if access.pin != nil {
		access.pin.Release()
		access.pin = nil
	}
	if access.tuner != nil {
		access.tuner.Release()
		access.tuner = nil
	}
	if access.provider != nil {
		access.provider.Release()
		access.provider = nil
	}
	if access.graph != nil {
		access.graph.Release()
		access.graph = nil
	}
}

// findPin returns the first pin of a filter facing one way, or nil.
func findPin(filter *directshow.IBaseFilter, direction directshow.PIN_DIRECTION) *directshow.IPin {
	var enumerator *directshow.IEnumPins
	if err := filter.EnumPins(&enumerator); err != nil {
		return nil
	}
	defer enumerator.Release()
	for {
		pins := make([]*directshow.IPin, 1)
		var fetched uint32
		if _, err := enumerator.Next(pins, &fetched); err != nil || fetched == 0 || pins[0] == nil {
			return nil
		}
		var found directshow.PIN_DIRECTION
		if err := pins[0].QueryDirection(&found); err == nil && found == direction {
			return pins[0]
		}
		pins[0].Release()
	}
}

// Tuner is the tuner filter this graph was built on, through which a vendor's
// filter-level properties are asked.
func (access *Access) Tuner() *directshow.IBaseFilter { return access.tuner }

// Properties is the vendor property set on the tuner's input pin.
func (access *Access) Properties() *vendors.PinProperties { return access.property }
