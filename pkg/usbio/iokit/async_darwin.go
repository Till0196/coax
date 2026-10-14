// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Asynchronous completions through IOKit, read straight off the mach port.
//
// IOKit reports an asynchronous transfer by sending a message to the
// interface's async port, and the usual way to hear it is to put that port on a
// run loop and have IOKit call a C function the message names. Nothing about
// the message needs C, though: it carries the reference the transfer was
// submitted with, the IOReturn, and the arguments the callback would have been
// given (IODispatchCalloutFromCFMessage in IOKitUser does no more than read
// them out and make the call). Receiving it here with mach_msg and reading
// those out leaves nothing for the kernel to call back into, so the package
// needs neither a run-loop thread nor a single instruction of assembly.

package iokit

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// slotInterfaceAsyncPort is CreateInterfaceAsyncPort on IOUSBInterfaceInterface.
// It hands back the interface's one async port, making it on the first call.
const slotInterfaceAsyncPort = 6

// asyncCompletion is what a transfer's completion message said. It lives on the
// Go heap: the kernel only echoes the reference a transfer was submitted with,
// and never writes here.
type asyncCompletion struct {
	done   atomic.Int32
	status int32
	arg0   uint64
}

func (completion *asyncCompletion) reset() {
	completion.status = 0
	completion.arg0 = 0
	completion.done.Store(0)
}

func (completion *asyncCompletion) finished() bool { return completion.done.Load() != 0 }

// The completions in flight, by the reference each transfer was submitted with.
// A reference is a number rather than an address, so a message that arrives for
// a completion already let go of is recognised and dropped instead of written
// through.
var (
	completionsMu sync.Mutex
	completions   = map[uint64]*asyncCompletion{}
	nextReference uint64
)

func registerCompletion(completion *asyncCompletion) uint64 {
	completionsMu.Lock()
	defer completionsMu.Unlock()
	nextReference++
	completions[nextReference] = completion
	return nextReference
}

func unregisterCompletion(reference uint64) {
	completionsMu.Lock()
	delete(completions, reference)
	completionsMu.Unlock()
}

// asyncCallout stands where IOKit expects the callback's address. Nothing calls
// it -- the message is read here and never dispatched -- but IOKit wants a
// function there, so it is one that would do no harm if something did.
func asyncCallout() uintptr { return symbol(&libc_getpid) }

// The mach_msg options for a receive that gives up after a while, so the
// receiver can notice it has been asked to stop. A receive a signal interrupts
// is restarted inside mach_msg itself unless MACH_RCV_INTERRUPT is asked for,
// so that needs nothing here.
const (
	machReceiveMessage  = 0x2
	machReceiveTimeout  = 0x100
	machReceiveTimedOut = 0x10004003
	machReceiveTooLarge = 0x10004004
	// machMessageComplex is the header bit saying descriptors follow the body.
	machMessageComplex = 0x80000000
)

// The layout of an IOKit async completion message, after mach_msg_header_t
// (and the body and port descriptors, in a complex message):
// OSNotificationHeader64 -- size, type, then the eight-word OSAsyncReference64 --
// and then IOAsyncCompletionContent, an IOReturn and the callback's arguments
// packed against it. See _sendAsyncResult64 in xnu's IOUserClient.cpp.
const (
	machHeaderSize           = 24
	machBodySize             = 4
	machPortDescriptorSize   = 12
	notificationHeaderSize   = 8 + 8*8
	asyncCompletionType      = 150 // kIOAsyncCompletionNotificationType
	asyncCalloutFuncIndex    = 1   // kIOAsyncCalloutFuncIndex
	asyncCalloutRefconIndex  = 2   // kIOAsyncCalloutRefconIndex
	asyncReceiveBufferLength = 1024
	asyncReceiveTimeoutMS    = 50
)

// asyncReceiver reads one interface's completion messages for as long as a ring
// on it is open.
type asyncReceiver struct {
	port    uint32
	stopped atomic.Bool
	exited  chan struct{}
}

// startReceiver takes the interface's async port and starts reading it.
func startReceiver(iface uintptr) (*asyncReceiver, error) {
	port := new(uint32)
	defer pin(port).Unpin()
	if result := vtable(iface, slotInterfaceAsyncPort, uintptr(unsafe.Pointer(port))); result != 0 || *port == 0 {
		return nil, ioKitError("create the interface's async port", uint32(result))
	}
	receiver := &asyncReceiver{port: *port, exited: make(chan struct{})}
	go receiver.run()
	return receiver, nil
}

func (receiver *asyncReceiver) run() {
	defer close(receiver.exited)
	// Outside the Go heap and every goroutine stack, for the kernel to copy each
	// message into: see pin for why nothing of Go's is handed over.
	raw := allocate(asyncReceiveBufferLength)
	defer release(raw)
	buffer := unsafe.Slice((*byte)(raw), asyncReceiveBufferLength)
	for !receiver.stopped.Load() {
		result := call(symbol(&libc_mach_msg), uintptr(raw),
			machReceiveMessage|machReceiveTimeout, 0, asyncReceiveBufferLength,
			uintptr(receiver.port), asyncReceiveTimeoutMS, 0)
		switch uint32(result) {
		case 0:
			deliver(buffer)
		case machReceiveTimedOut:
		case machReceiveTooLarge:
			// Not one of ours: a completion is a few dozen bytes. Without
			// MACH_RCV_LARGE the kernel has already thrown it away.
		default:
			// The port has gone, which happens only once the interface is closed.
			return
		}
	}
}

// settleTimeout is how long closing a ring waits, in all, for the transfers it
// aborted to come back. An aborted transfer comes back as soon as the driver
// lets go of it; this only bounds one that never does.
const settleTimeout = time.Second

// settle waits until every one of count transfers reports itself back, or the
// time is up, polling them all together so that the wait is one settleTimeout
// however deep the ring. A transfer still out afterwards keeps its buffers:
// IOKit may yet write into them.
func settle(count int, back func(index int) bool) {
	deadline := time.Now().Add(settleTimeout)
	for {
		waiting := false
		for index := 0; index < count; index++ {
			if !back(index) {
				waiting = true
				break
			}
		}
		if !waiting || time.Now().After(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// Close stops the receiver and waits for it, which takes at most one receive
// timeout. Every transfer has to have come back first: a completion that
// arrives after this is never read.
func (receiver *asyncReceiver) Close() {
	receiver.stopped.Store(true)
	<-receiver.exited
}

// deliver reads one received message and records it against the completion it
// names. Anything not shaped like an IOKit async completion of ours is ignored.
func deliver(message []byte) {
	order := binary.LittleEndian
	bits := order.Uint32(message[0:])
	size := int(order.Uint32(message[4:]))
	if size > len(message) || size < machHeaderSize+machBodySize {
		return
	}
	at := machHeaderSize
	if bits&machMessageComplex != 0 {
		descriptors := int(order.Uint32(message[at:]))
		at += machBodySize + descriptors*machPortDescriptorSize
	}
	if size < at+notificationHeaderSize+4 {
		return
	}
	if order.Uint32(message[at+4:]) != asyncCompletionType {
		return
	}
	references := message[at+8:]
	if uintptr(order.Uint64(references[asyncCalloutFuncIndex*8:])) != asyncCallout() {
		return
	}
	reference := order.Uint64(references[asyncCalloutRefconIndex*8:])
	content := at + notificationHeaderSize
	status := int32(order.Uint32(message[content:]))
	var arg0 uint64
	if size >= content+4+8 {
		arg0 = order.Uint64(message[content+4:])
	}

	completionsMu.Lock()
	completion := completions[reference]
	completionsMu.Unlock()
	if completion == nil {
		return
	}
	completion.status = status
	completion.arg0 = arg0
	completion.done.Store(1)
}
