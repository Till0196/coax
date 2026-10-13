// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package comutil holds the small conversions every caller of a COM interface
// in this backend needs, so no one carries their own "read a wide string until
// the NUL".
package comutil

import (
	"unicode/utf16"
	"unsafe"
)

// UTF16PointerString reads a NUL-terminated wide string COM handed back.
//
// It walks one unit at a time rather than viewing the pointer as a generously
// sized slice: that would claim memory that may not be mapped and would cap the
// string length for no reason.
func UTF16PointerString(value *uint16) string {
	if value == nil {
		return ""
	}
	var units []uint16
	for offset := uintptr(0); ; offset += unsafe.Sizeof(uint16(0)) {
		unit := *(*uint16)(unsafe.Add(unsafe.Pointer(value), offset))
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units))
}

// UTF16BufferString decodes a wide-string buffer up to its first NUL.
func UTF16BufferString(buffer []uint16) string {
	for index, value := range buffer {
		if value == 0 {
			buffer = buffer[:index]
			break
		}
	}
	return string(utf16.Decode(buffer))
}
