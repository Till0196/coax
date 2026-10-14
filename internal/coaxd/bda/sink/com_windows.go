// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"unicode/utf16"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
)

// The plumbing a COM server written in Go needs: turning callback addresses back
// into objects, and handing strings and pointers back out as COM expects.

// comPointer reinterprets an address COM passed in as a pointer. Every such
// address is either an object this package handed to DirectShow -- kept alive by
// sinkLive for as long as DirectShow holds it -- or memory COM owns for the
// duration of the call. Vet's unsafe-pointer check can see neither fact, so the
// conversion is made here once rather than at forty call sites.
func comPointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

func guidIs(riid uintptr, want *win32.GUID) bool {
	if riid == 0 {
		return false
	}
	return *(*win32.GUID)(comPointer(riid)) == *want
}

func comAddRef(pointer uintptr) {
	if pointer != 0 {
		(*win32.IUnknown)(comPointer(pointer)).AddRef()
	}
}

func comRelease(pointer uintptr) {
	if pointer != 0 {
		(*win32.IUnknown)(comPointer(pointer)).Release()
	}
}

func writePointer(out uintptr, value uintptr) {
	*(*uintptr)(comPointer(out)) = value
}

func putUTF16(destination []uint16, value string) {
	encoded := utf16.Encode([]rune(value))
	if len(encoded) > len(destination)-1 {
		encoded = encoded[:len(destination)-1]
	}
	copy(destination, encoded)
	destination[len(encoded)] = 0
}

// coTaskMemUTF16 copies a string into memory the caller frees, which is how
// COM hands strings back.
func coTaskMemUTF16(value string) uintptr {
	encoded := append(utf16.Encode([]rune(value)), 0)
	memory := win32.CoTaskMemAlloc(uintptr(len(encoded)) * 2)
	if memory == nil {
		return 0
	}
	copy(unsafe.Slice((*uint16)(memory), len(encoded)), encoded)
	return uintptr(memory)
}
