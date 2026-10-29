// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package sink is a DirectShow renderer with one input pin, which writes every
// byte handed to it and asks nothing of the samples it is given.
//
// It replaces the stock Microsoft file writer, which cannot be used here. A BDA
// capture graph has no reference clock, so ksproxy delivers transport stream
// samples with no time stamp, and the file writer rejects the first one with
// VFW_E_SAMPLE_TIME_NOT_SET and aborts the run while the graph goes on
// reporting itself as running. A transport stream is timed by the broadcast, so
// there is nothing a sample time could mean here.
//
// The COM side is split by interface: the filter in filter_windows.go, its pin
// in pin_windows.go and meminput_windows.go, the pin enumerator in
// enumpins_windows.go.
package sink

import (
	"io"
	"sync"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"
)

// Sink is the renderer this package's doc describes.
type Sink struct {
	// vtbl must stay first: DirectShow reaches this object's methods by reading
	// a vtable pointer from offset zero.
	vtbl *[15]uintptr

	refs  int32
	state int32
	// graph is held without a reference, as a filter must hold its owning graph:
	// a reference would be a cycle neither side breaks.
	graph uintptr

	pin *sinkPin
}

type sinkPin struct {
	vtbl    *[18]uintptr // IPin, at offset zero
	memVtbl *[9]uintptr  // IMemInputPin, reached at memVtblOffset

	filter *Sink
	peer   uintptr
	haveMT bool
	mt     directshow.AM_MEDIA_TYPE
	format []byte

	mu      sync.Mutex
	out     io.Writer
	written int64
	failure error
}

type sinkEnum struct {
	vtbl  *[7]uintptr
	refs  int32
	sink  *Sink
	index int
}

// memVtblOffset turns an IMemInputPin pointer back into the pin that owns it.
var memVtblOffset = unsafe.Offsetof(sinkPin{}.memVtbl)

// sinkLive keeps every object DirectShow holds a pointer to reachable from Go:
// the graph's reference count is invisible to the garbage collector.
var sinkLive sync.Map

const (
	hrOK             = uintptr(0)
	hrFalse          = uintptr(1)
	hrFail           = uintptr(0x80004005)
	hrNotImpl        = uintptr(0x80004001)
	hrNoInterface    = uintptr(0x80004002)
	hrPointer        = uintptr(0x80004003)
	hrUnexpected     = uintptr(0x8000FFFF)
	hrAlreadyConnect = uintptr(0x80040204) // VFW_E_ALREADY_CONNECTED
	hrNotConnected   = uintptr(0x80040209) // VFW_E_NOT_CONNECTED
	hrNoAllocator    = uintptr(0x8004020F) // VFW_E_NO_ALLOCATOR

	sinkPinName    = "In"
	sinkFilterName = "coax Transport Stream Sink"

	sinkStateStopped = 0
	sinkStatePaused  = 1
	sinkStateRunning = 2
)

// clsidStreamSink names this filter. It is never registered -- the sink is only
// ever handed to a graph directly -- but a filter is still asked for a class id.
var clsidStreamSink = win32.GUID{
	Data1: 0xc0a17f01, Data2: 0x51b4, Data3: 0x4d6a,
	Data4: [8]byte{0x9e, 0x21, 0x77, 0x0c, 0xa1, 0x03, 0x2b, 0x11},
}

// New returns a sink holding one reference, which Release drops.
func New(out io.Writer) *Sink {
	sinkVtblOnce.Do(buildSinkVtables)
	sink := &Sink{vtbl: &filterVtbl, refs: 1}
	sink.pin = &sinkPin{vtbl: &pinVtbl, memVtbl: &memInputVtbl, filter: sink, out: out}
	sinkLive.Store(uintptr(unsafe.Pointer(sink)), sink)
	sinkLive.Store(uintptr(unsafe.Pointer(sink.pin)), sink.pin)
	return sink
}

func (sink *Sink) Filter() *directshow.IBaseFilter {
	return (*directshow.IBaseFilter)(unsafe.Pointer(sink))
}

func (sink *Sink) Release() {
	sinkRelease(uintptr(unsafe.Pointer(sink)))
}

// BytesWritten reports how much has reached the sink, the only direct evidence
// that the graph is carrying data rather than merely claiming to run.
func (sink *Sink) BytesWritten() int64 {
	sink.pin.mu.Lock()
	defer sink.pin.mu.Unlock()
	return sink.pin.written
}

func (sink *Sink) WriteError() error {
	sink.pin.mu.Lock()
	defer sink.pin.mu.Unlock()
	return sink.pin.failure
}

// Attach replaces the byte destination while the graph keeps running. Packet
// boundaries and format detection belong to the reader, not the capture callback.
func (sink *Sink) Attach(out io.Writer) {
	sink.pin.mu.Lock()
	defer sink.pin.mu.Unlock()
	sink.pin.out = out
}

// Detach stops the sink writing, so whatever is behind it can be closed while
// the graph is still being torn down.
func (sink *Sink) Detach() {
	sink.pin.mu.Lock()
	defer sink.pin.mu.Unlock()
	sink.pin.out = nil
}
