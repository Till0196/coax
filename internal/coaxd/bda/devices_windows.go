// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import (
	"fmt"
	"runtime"

	"github.com/Till0196/coax/pkg/win32"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/asicen"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/siano"
)

// Found is one BDA network tuner the system is offering, seen without a graph
// being built on it.
type Found struct {
	// Name is the friendly name, as shown in Device Manager.
	Name string
	// Selector is the moniker display name: the device path Windows knows it
	// by. It is what Open takes, and unlike the friendly name it stays unique
	// when a second identical card is installed.
	Selector string
	// Deliveries are known from vendor filter identities; nil means unknown.
	Deliveries []string
	// Model is the product a PLEX tuner belongs to, such as "PX-W3U3 V2", and
	// USBProduct its USB product ID, 0 for a card.
	Model      string
	USBProduct uint16
	// CardSlot marks the tuner its enclosure's card is reached through.
	CardSlot bool
}

// Devices lists the BDA network tuners without opening one.
//
// A tuner is listed whether or not anything is using it: BDA cannot report that
// without building a graph, which is the expensive, exclusive thing a listing
// must not do.
func Devices() ([]Found, error) {
	// COM must be initialised on the calling thread, and a goroutine may move
	// between threads at any suspension point, so pin it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := win32.CoInitializeEx(uint32(win32.COINIT_MULTITHREADED)); err != nil {
		return nil, fmt.Errorf("initialize COM: %w", err)
	}
	defer win32.CoUninitialize()

	tuners, err := filters.Enumerate(filters.CategoryNetworkTuner)
	if err != nil {
		return nil, fmt.Errorf("enumerate BDA tuners: %w", err)
	}
	defer filters.ReleaseAll(tuners)

	found := make([]Found, 0, len(tuners))
	for _, tuner := range tuners {
		selector := tuner.Display
		if selector == "" {
			// No device path: the friendly name has to serve as both.
			selector = tuner.Name
		}
		vendor := tuner.Vendor()
		deliveries := asicen.Deliveries(vendor)
		if deliveries == nil {
			deliveries = siano.Deliveries(tuner.Display)
		}
		found = append(found, Found{
			Name:       tuner.Name,
			Selector:   selector,
			Deliveries: deliveries,
			Model:      asicen.Model(vendor),
			USBProduct: asicen.USBProduct(vendor),
			CardSlot:   asicen.CarriesCard(vendor),
		})
	}
	return found, nil
}
