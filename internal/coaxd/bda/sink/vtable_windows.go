// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"sync"
	"syscall"
)

// The vtables DirectShow reaches these objects through, built once on the first
// sink because syscall.NewCallback allocates a trampoline that is never freed.

var (
	filterVtbl   [15]uintptr
	pinVtbl      [18]uintptr
	memInputVtbl [9]uintptr
	enumPinsVtbl [7]uintptr
	sinkVtblOnce sync.Once
)

// The slot order in each vtable below is the interface's own and must not be
// rearranged: DirectShow calls by index, not by name.
func buildSinkVtables() {
	filterVtbl = [15]uintptr{
		syscall.NewCallback(sinkQueryInterface),
		syscall.NewCallback(sinkAddRef),
		syscall.NewCallback(sinkRelease),
		syscall.NewCallback(sinkGetClassID),
		syscall.NewCallback(sinkStop),
		syscall.NewCallback(sinkPause),
		syscall.NewCallback(sinkRunEntry),
		syscall.NewCallback(sinkGetState),
		syscall.NewCallback(sinkSetSyncSource),
		syscall.NewCallback(sinkGetSyncSource),
		syscall.NewCallback(sinkEnumPins),
		syscall.NewCallback(sinkFindPin),
		syscall.NewCallback(sinkQueryFilterInfo),
		syscall.NewCallback(sinkJoinFilterGraph),
		syscall.NewCallback(sinkQueryVendorInfo),
	}
	pinVtbl = [18]uintptr{
		syscall.NewCallback(pinQueryInterface),
		syscall.NewCallback(pinAddRef),
		syscall.NewCallback(pinRelease),
		syscall.NewCallback(pinConnect),
		syscall.NewCallback(pinReceiveConnection),
		syscall.NewCallback(pinDisconnect),
		syscall.NewCallback(pinConnectedTo),
		syscall.NewCallback(pinConnectionMediaType),
		syscall.NewCallback(pinQueryPinInfo),
		syscall.NewCallback(pinQueryDirection),
		syscall.NewCallback(pinQueryId),
		syscall.NewCallback(pinQueryAccept),
		syscall.NewCallback(pinEnumMediaTypes),
		syscall.NewCallback(pinQueryInternalConnections),
		syscall.NewCallback(pinEndOfStream),
		syscall.NewCallback(pinBeginFlush),
		syscall.NewCallback(pinEndFlush),
		syscall.NewCallback(pinNewSegmentEntry),
	}
	memInputVtbl = [9]uintptr{
		syscall.NewCallback(memQueryInterface),
		syscall.NewCallback(memAddRef),
		syscall.NewCallback(memRelease),
		syscall.NewCallback(memGetAllocator),
		syscall.NewCallback(memNotifyAllocator),
		syscall.NewCallback(memGetAllocatorRequirements),
		syscall.NewCallback(memReceive),
		syscall.NewCallback(memReceiveMultiple),
		syscall.NewCallback(memReceiveCanBlock),
	}
	enumPinsVtbl = [7]uintptr{
		syscall.NewCallback(enumQueryInterface),
		syscall.NewCallback(enumAddRef),
		syscall.NewCallback(enumRelease),
		syscall.NewCallback(enumNext),
		syscall.NewCallback(enumSkip),
		syscall.NewCallback(enumReset),
		syscall.NewCallback(enumClone),
	}
}
