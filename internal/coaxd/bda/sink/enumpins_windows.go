// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"sync/atomic"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/win32"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow"
)

// --- IEnumPins -------------------------------------------------------------

func enumOf(this uintptr) *sinkEnum { return (*sinkEnum)(comPointer(this)) }

func enumQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv == 0 {
		return hrPointer
	}
	if guidIs(riid, &win32.IID_IUnknown) || guidIs(riid, &directshow.IID_IEnumPins) {
		writePointer(ppv, this)
		enumAddRef(this)
		return hrOK
	}
	writePointer(ppv, 0)
	return hrNoInterface
}

func enumAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&enumOf(this).refs, 1))
}

func enumRelease(this uintptr) uintptr {
	enumerator := enumOf(this)
	left := atomic.AddInt32(&enumerator.refs, -1)
	if left > 0 {
		return uintptr(left)
	}
	sinkRelease(uintptr(unsafe.Pointer(enumerator.sink)))
	sinkLive.Delete(this)
	return 0
}

func enumNext(this, cPins, ppPins, pcFetched uintptr) uintptr {
	if ppPins == 0 {
		return hrPointer
	}
	enumerator := enumOf(this)
	var fetched uintptr
	for fetched < cPins && enumerator.index < 1 {
		pin := uintptr(unsafe.Pointer(enumerator.sink.pin))
		pinAddRef(pin)
		*(*uintptr)(comPointer(ppPins + fetched*unsafe.Sizeof(uintptr(0)))) = pin
		enumerator.index++
		fetched++
	}
	if pcFetched != 0 {
		*(*uint32)(comPointer(pcFetched)) = uint32(fetched)
	}
	if fetched == cPins {
		return hrOK
	}
	return hrFalse
}

func enumSkip(this, cPins uintptr) uintptr {
	enumerator := enumOf(this)
	enumerator.index += int(cPins)
	if enumerator.index > 1 {
		enumerator.index = 1
		return hrFalse
	}
	return hrOK
}

func enumReset(this uintptr) uintptr {
	enumOf(this).index = 0
	return hrOK
}

func enumClone(this, ppEnum uintptr) uintptr {
	if ppEnum == 0 {
		return hrPointer
	}
	enumerator := enumOf(this)
	clone := &sinkEnum{vtbl: &enumPinsVtbl, refs: 1, sink: enumerator.sink, index: enumerator.index}
	sinkLive.Store(uintptr(unsafe.Pointer(clone)), clone)
	sinkAddRef(uintptr(unsafe.Pointer(enumerator.sink)))
	writePointer(ppEnum, uintptr(unsafe.Pointer(clone)))
	return hrOK
}
