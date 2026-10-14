// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import (
	"fmt"
	"runtime"

	"github.com/Till0196/coax/pkg/win32"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/graph"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/dd"
)

// Reaching a card's private properties. What is in them is in the vendors
// package; this is only how a filter is asked.

// ReadDDStandards asks a Digital Devices tuner what it can receive.
func ReadDDStandards(selector string) (dd.Standards, error) {
	var standards dd.Standards
	err := withVendorAccess(selector, func(access *graph.Access) error {
		control, err := vendors.Filter(access.Tuner())
		if err != nil {
			return err
		}
		defer control.Release()
		standards, err = dd.ReadStandards(control)
		return err
	})
	if err != nil {
		return dd.Standards{}, err
	}
	return standards, nil
}

// WriteDDFilterMask sets which standards a Digital Devices tuner offers, and
// reports what the driver kept.
func WriteDDFilterMask(selector string, mask uint32) (uint32, error) {
	var stored uint32
	err := withVendorAccess(selector, func(access *graph.Access) error {
		control, err := vendors.Filter(access.Tuner())
		if err != nil {
			return err
		}
		defer control.Release()
		stored, err = dd.WriteFilterMask(control, mask)
		return err
	})
	return stored, err
}

// withVendorAccess opens one named tuner far enough to reach its properties,
// and closes everything afterwards.
func withVendorAccess(selector string, work func(*graph.Access) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := win32.CoInitializeEx(uint32(win32.COINIT_MULTITHREADED)); err != nil {
		return fmt.Errorf("initialize COM: %w", err)
	}
	defer win32.CoUninitialize()

	tuners, err := filters.Enumerate(filters.CategoryNetworkTuner)
	if err != nil {
		return fmt.Errorf("enumerate BDA tuners: %w", err)
	}
	defer filters.ReleaseAll(tuners)
	tuner, err := filters.Select(tuners, selector)
	if err != nil {
		return err
	}
	if !dd.Is(tuner.Vendor()) {
		return fmt.Errorf("%q is not a Digital Devices tuner", tuner.Name)
	}
	access, err := graph.OpenAccess(tuner)
	if err != nil {
		return err
	}
	defer access.Close()
	return work(access)
}

// Statistics asks the driver what the demodulator is measuring.
//
// It works whether or not a stream is open, which is the point of it: the
// filter is reached through the graph while one is, and through the tuned
// session while one is not.
func (device *Device) Statistics() (dd.Statistics, bool) {
	filter := device.streamingTuner
	if filter == nil && device.session != nil {
		filter = device.session.Tuner()
	}
	if filter == nil {
		return dd.Statistics{}, false
	}
	control, err := vendors.Filter(filter)
	if err != nil {
		return dd.Statistics{}, false
	}
	defer control.Release()
	return dd.ReadStatistics(control)
}
