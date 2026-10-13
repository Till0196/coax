// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/win32"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow"
)

// --- IPin ------------------------------------------------------------------

func pinOf(this uintptr) *sinkPin { return (*sinkPin)(comPointer(this)) }

func pinFromMem(this uintptr) *sinkPin { return pinOf(this - memVtblOffset) }

func pinQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv == 0 {
		return hrPointer
	}
	switch {
	case guidIs(riid, &win32.IID_IUnknown), guidIs(riid, &directshow.IID_IPin):
		writePointer(ppv, this)
		pinAddRef(this)
		return hrOK
	case guidIs(riid, &directshow.IID_IMemInputPin):
		writePointer(ppv, this+memVtblOffset)
		pinAddRef(this)
		return hrOK
	}
	writePointer(ppv, 0)
	return hrNoInterface
}

// pinAddRef and pinRelease count against the filter: a pin does not outlive its
// filter, and a separate count would only be a second lifetime to get wrong.
func pinAddRef(this uintptr) uintptr {
	return sinkAddRef(uintptr(unsafe.Pointer(pinOf(this).filter)))
}

func pinRelease(this uintptr) uintptr {
	return sinkRelease(uintptr(unsafe.Pointer(pinOf(this).filter)))
}

func pinConnect(this, pReceivePin, pmt uintptr) uintptr { return hrUnexpected }

func pinReceiveConnection(this, pConnector, pmt uintptr) uintptr {
	pin := pinOf(this)
	if pin.peer != 0 {
		return hrAlreadyConnect
	}
	if pmt == 0 {
		return hrPointer
	}
	proposed := (*directshow.AM_MEDIA_TYPE)(comPointer(pmt))
	pin.mt = *proposed
	// The format block belongs to the caller, so it is copied rather than
	// pointed at, and their IUnknown is dropped rather than held.
	pin.format = nil
	if proposed.CbFormat > 0 && proposed.PbFormat != nil {
		pin.format = append([]byte(nil), unsafe.Slice(proposed.PbFormat, proposed.CbFormat)...)
	}
	pin.mt.PUnk = nil
	pin.mt.PbFormat = nil
	pin.mt.CbFormat = 0
	pin.haveMT = true
	pin.peer = pConnector
	comAddRef(pConnector)
	return hrOK
}

func pinDisconnect(this uintptr) uintptr {
	pin := pinOf(this)
	comRelease(pin.peer)
	pin.peer = 0
	pin.haveMT = false
	return hrOK
}

func pinConnectedTo(this, ppPin uintptr) uintptr {
	if ppPin == 0 {
		return hrPointer
	}
	pin := pinOf(this)
	if pin.peer == 0 {
		writePointer(ppPin, 0)
		return hrNotConnected
	}
	comAddRef(pin.peer)
	writePointer(ppPin, pin.peer)
	return hrOK
}

func pinConnectionMediaType(this, pmt uintptr) uintptr {
	if pmt == 0 {
		return hrPointer
	}
	pin := pinOf(this)
	target := (*directshow.AM_MEDIA_TYPE)(comPointer(pmt))
	if !pin.haveMT {
		*target = directshow.AM_MEDIA_TYPE{}
		return hrNotConnected
	}
	copied := pin.mt
	if len(pin.format) > 0 {
		if memory := win32.CoTaskMemAlloc(uintptr(len(pin.format))); memory != nil {
			copy(unsafe.Slice((*byte)(memory), len(pin.format)), pin.format)
			copied.CbFormat = uint32(len(pin.format))
			copied.PbFormat = (*byte)(memory)
		}
	}
	*target = copied
	return hrOK
}

func pinQueryPinInfo(this, pInfo uintptr) uintptr {
	if pInfo == 0 {
		return hrPointer
	}
	pin := pinOf(this)
	info := (*directshow.PIN_INFO)(comPointer(pInfo))
	*info = directshow.PIN_INFO{}
	info.Dir = directshow.PINDIR_INPUT
	putUTF16(info.AchName[:], sinkPinName)
	sinkAddRef(uintptr(unsafe.Pointer(pin.filter)))
	info.PFilter = pin.filter.Filter()
	return hrOK
}

func pinQueryDirection(this, pPinDir uintptr) uintptr {
	if pPinDir == 0 {
		return hrPointer
	}
	*(*int32)(comPointer(pPinDir)) = int32(directshow.PINDIR_INPUT)
	return hrOK
}

func pinQueryId(this, id uintptr) uintptr {
	if id == 0 {
		return hrPointer
	}
	memory := coTaskMemUTF16(sinkPinName)
	if memory == 0 {
		return hrFail
	}
	writePointer(id, memory)
	return hrOK
}

// pinQueryAccept takes every media type: whatever the connection calls it, what
// arrives is a transport stream written out untouched.
func pinQueryAccept(this, pmt uintptr) uintptr { return hrOK }

func pinEnumMediaTypes(this, ppEnum uintptr) uintptr { return hrNotImpl }

func pinQueryInternalConnections(this, apPin, nPin uintptr) uintptr { return hrNotImpl }

func pinEndOfStream(this uintptr) uintptr { return hrOK }

func pinBeginFlush(this uintptr) uintptr { return hrOK }

func pinEndFlush(this uintptr) uintptr { return hrOK }

func pinNewSegment(this uintptr) uintptr { return hrOK }
