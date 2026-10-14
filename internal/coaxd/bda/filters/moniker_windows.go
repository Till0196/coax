// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package filters finds, chooses between and names the BDA filters Windows is
// offering. Nothing here opens hardware: a tuner is only ever a moniker, which
// costs nothing to hold. Opening one is the graph package's business.
package filters

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// Moniker is one filter the system is offering, named but not opened.
type Moniker struct {
	// Name is the friendly name, as shown in Device Manager.
	Name string
	// Display is the moniker display name: the device path Windows knows the
	// device by. Unlike the friendly name it stays unique when a second
	// identical card is installed.
	Display string
	// Moniker is the COM name a filter is bound from.
	Moniker *win32.IMoniker
}

// Vendor names this filter to the vendors package, which recognises a card from
// what the driver calls it and where it sits.
func (moniker Moniker) Vendor() vendors.Tuner {
	return vendors.Tuner{Name: moniker.Name, Path: moniker.Display}
}

// Enumerate lists the filters registered in one KS category. The caller
// releases them with ReleaseAll.
func Enumerate(category win32.GUID) ([]Moniker, error) {
	var unknown *win32.IUnknown
	if err := win32.CoCreateInstance(&clsidSystemDeviceEnum, nil, win32.CLSCTX_INPROC_SERVER,
		&directshow.IID_ICreateDevEnum, &unknown); err != nil {
		return nil, err
	}
	deviceEnum := (*directshow.ICreateDevEnum)(unsafe.Pointer(unknown))
	defer deviceEnum.Release()
	var enumerator *win32.IEnumMoniker
	if err := deviceEnum.CreateClassEnumerator(&category, &enumerator, 0); err != nil {
		return nil, err
	}
	if enumerator == nil {
		return nil, nil
	}
	defer enumerator.Release()
	var result []Moniker
	for {
		items := make([]*win32.IMoniker, 1)
		var fetched uint32
		_, err := enumerator.Next(items, &fetched)
		if err != nil {
			ReleaseAll(result)
			return nil, err
		}
		if fetched == 0 || items[0] == nil {
			break
		}
		display := displayName(items[0])
		name := friendlyName(items[0])
		if name == "" {
			name = display
		}
		result = append(result, Moniker{Name: name, Display: display, Moniker: items[0]})
	}
	return result, nil
}

// Select picks one filter out of a listing by index, by a substring of its
// name or device path, or -- given nothing -- the first one.
func Select(monikers []Moniker, selector string) (Moniker, error) {
	if len(monikers) == 0 {
		return Moniker{}, fmt.Errorf("no BDA network tuner was found")
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return monikers[0], nil
	}
	if index, err := strconv.Atoi(selector); err == nil {
		if index < 0 || index >= len(monikers) {
			return Moniker{}, fmt.Errorf("BDA tuner index %d is outside 0..%d", index, len(monikers)-1)
		}
		return monikers[index], nil
	}
	lower := strings.ToLower(selector)
	for _, moniker := range monikers {
		if strings.Contains(strings.ToLower(moniker.Name), lower) || strings.Contains(strings.ToLower(moniker.Display), lower) {
			return moniker, nil
		}
	}
	names := make([]string, 0, len(monikers))
	for index, moniker := range monikers {
		names = append(names, fmt.Sprintf("%d:%s", index, moniker.Name))
	}
	return Moniker{}, fmt.Errorf("BDA tuner matching %q was not found; available tuners: %s", selector, strings.Join(names, ", "))
}

// ReleaseAll drops the COM references a listing holds.
func ReleaseAll(monikers []Moniker) {
	for _, moniker := range monikers {
		if moniker.Moniker != nil {
			moniker.Moniker.Release()
		}
	}
}
