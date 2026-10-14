// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package filters

import (
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"

	"github.com/Till0196/coax/pkg/win32/comutil"
)

// The two names a device has: the path Windows knows it by, and the name it
// calls itself. Both are best-effort; a device answering with neither is still
// listed.

func displayName(moniker *win32.IMoniker) string {
	var value win32.PWSTR
	if err := moniker.GetDisplayName(nil, nil, &value); err != nil || value == nil {
		return ""
	}
	defer win32.CoTaskMemFree(unsafe.Pointer(value))
	return comutil.UTF16PointerString(value)
}

func friendlyName(moniker *win32.IMoniker) string {
	var unknown *win32.IUnknown
	if err := moniker.BindToStorage(nil, nil, &win32.IID_IPropertyBag, &unknown); err != nil {
		return ""
	}
	bag := (*win32.IPropertyBag)(unsafe.Pointer(unknown))
	defer bag.Release()
	var value win32.VARIANT
	win32.VariantInit(&value)
	defer win32.VariantClear(&value)
	if err := bag.Read("FriendlyName", &value, nil); err != nil {
		return ""
	}
	buffer := make([]uint16, 1024)
	if err := win32.VariantToString(&value, win32.PWSTR(&buffer[0]), uint32(len(buffer))); err != nil {
		return ""
	}
	return comutil.UTF16BufferString(buffer)
}
