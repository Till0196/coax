// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"sync/atomic"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"
)

// --- IBaseFilter -----------------------------------------------------------

func sinkOf(this uintptr) *Sink { return (*Sink)(comPointer(this)) }

func sinkQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv == 0 {
		return hrPointer
	}
	if guidIs(riid, &win32.IID_IUnknown) || guidIs(riid, &win32.IID_IPersist) ||
		guidIs(riid, &directshow.IID_IMediaFilter) || guidIs(riid, &directshow.IID_IBaseFilter) {
		writePointer(ppv, this)
		sinkAddRef(this)
		return hrOK
	}
	writePointer(ppv, 0)
	return hrNoInterface
}

func sinkAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&sinkOf(this).refs, 1))
}

func sinkRelease(this uintptr) uintptr {
	sink := sinkOf(this)
	left := atomic.AddInt32(&sink.refs, -1)
	if left > 0 {
		return uintptr(left)
	}
	sinkLive.Delete(uintptr(unsafe.Pointer(sink.pin)))
	sinkLive.Delete(this)
	return 0
}

func sinkGetClassID(this, pClassID uintptr) uintptr {
	if pClassID == 0 {
		return hrPointer
	}
	*(*win32.GUID)(comPointer(pClassID)) = clsidStreamSink
	return hrOK
}

func sinkStop(this uintptr) uintptr {
	atomic.StoreInt32(&sinkOf(this).state, sinkStateStopped)
	return hrOK
}

func sinkPause(this uintptr) uintptr {
	atomic.StoreInt32(&sinkOf(this).state, sinkStatePaused)
	return hrOK
}

func sinkRun(this uintptr) uintptr {
	atomic.StoreInt32(&sinkOf(this).state, sinkStateRunning)
	return hrOK
}

func sinkGetState(this, msTimeout, pState uintptr) uintptr {
	if pState == 0 {
		return hrPointer
	}
	*(*int32)(comPointer(pState)) = atomic.LoadInt32(&sinkOf(this).state)
	return hrOK
}

// sinkSetSyncSource accepts whatever clock it is offered, including none. A
// filter that refuses here is a filter that stops the graph from running.
func sinkSetSyncSource(this, pClock uintptr) uintptr { return hrOK }

func sinkGetSyncSource(this, ppClock uintptr) uintptr {
	if ppClock == 0 {
		return hrPointer
	}
	writePointer(ppClock, 0)
	return hrOK
}

func sinkEnumPins(this, ppEnum uintptr) uintptr {
	if ppEnum == 0 {
		return hrPointer
	}
	enumerator := &sinkEnum{vtbl: &enumPinsVtbl, refs: 1, sink: sinkOf(this)}
	sinkLive.Store(uintptr(unsafe.Pointer(enumerator)), enumerator)
	sinkAddRef(this)
	writePointer(ppEnum, uintptr(unsafe.Pointer(enumerator)))
	return hrOK
}

func sinkFindPin(this, id, ppPin uintptr) uintptr {
	if ppPin == 0 {
		return hrPointer
	}
	pin := uintptr(unsafe.Pointer(sinkOf(this).pin))
	pinAddRef(pin)
	writePointer(ppPin, pin)
	return hrOK
}

func sinkQueryFilterInfo(this, pInfo uintptr) uintptr {
	if pInfo == 0 {
		return hrPointer
	}
	sink := sinkOf(this)
	info := (*directshow.FILTER_INFO)(comPointer(pInfo))
	*info = directshow.FILTER_INFO{}
	putUTF16(info.AchName[:], sinkFilterName)
	if sink.graph != 0 {
		comAddRef(sink.graph)
		info.PGraph = (*directshow.IFilterGraph)(comPointer(sink.graph))
	}
	return hrOK
}

func sinkJoinFilterGraph(this, pGraph, pName uintptr) uintptr {
	sinkOf(this).graph = pGraph
	return hrOK
}

func sinkQueryVendorInfo(this, pVendorInfo uintptr) uintptr { return hrNotImpl }
