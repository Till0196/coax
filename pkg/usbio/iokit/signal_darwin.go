// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Keeping the scheduler out of a transfer.

package iokit

import (
	"runtime"
	"unsafe"
)

//go:cgo_import_dynamic libc_pthread_sigmask pthread_sigmask "/usr/lib/libSystem.B.dylib"

//go:linkname libc_pthread_sigmask libc_pthread_sigmask
var libc_pthread_sigmask byte

// The two ways of setting a thread's mask, and the signals a transfer is not
// made under.
const (
	sigBlock   = 1
	sigSetMask = 3
)

// heldDuringTransfers is every signal held off for the length of one transfer,
// which is every signal that may be held off at all. Blocking only SIGURG is
// too narrow: a Mach receive is cut short by any signal pending against the
// thread waiting in it. Excluded are the signals raised by the running
// instruction and the two that cannot be blocked.
const heldDuringTransfers = ^uint32(0) &^ (1<<(4-1) | // SIGILL
	1<<(5-1) | // SIGTRAP
	1<<(6-1) | // SIGABRT
	1<<(7-1) | // SIGEMT
	1<<(8-1) | // SIGFPE
	1<<(9-1) | // SIGKILL
	1<<(10-1) | // SIGBUS
	1<<(11-1) | // SIGSEGV
	1<<(12-1) | // SIGSYS
	1<<(17-1)) // SIGSTOP

// callUninterrupted makes a C call with signals blocked on the thread making
// it.
//
// The IOKit entry points used here are synchronous Mach messages underneath,
// and a Mach receive a signal interrupts does not resume: it returns, and IOKit
// reports kIOReturnAborted. Measured against a MyGica T230C, ten opens of the
// board produced five or six aborted bulk reads with preemption on and none at
// all with it off.
//
// Retrying is not an answer: an aborted transfer was cut short, not skipped, so
// a control transfer writing a bridge's I2C register may already have reached
// the device. A US-3POUT in that state stops answering the bus and needs
// unplugging.
func callUninterrupted(fn uintptr, args ...uintptr) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// A Darwin sigset_t is a single word, and a signal is one bit of it,
	// counted from zero for signal one.
	blocked := heldDuringTransfers
	var previous uint32
	defer pin(&blocked, &previous).Unpin()
	_, _, errno := syscallDarwin(symbol(&libc_pthread_sigmask), sigBlock,
		uintptr(unsafe.Pointer(&blocked)), uintptr(unsafe.Pointer(&previous)))
	if errno != 0 {
		// Nothing was blocked, so there is nothing to put back: restoring from a
		// word that was never written would unblock whatever the runtime had
		// blocked on this thread for its own reasons.
		return call(fn, args...)
	}
	result := call(fn, args...)
	syscallDarwin(symbol(&libc_pthread_sigmask), sigSetMask,
		uintptr(unsafe.Pointer(&previous)), 0)
	return result
}

// vtableUninterrupted calls a plug-in method with preemption blocked, for the
// methods that carry a transfer. See callUninterrupted.
func vtableUninterrupted(object uintptr, slot int, args ...uintptr) uintptr {
	table := *(*uintptr)(foreign(object))
	method := *(*uintptr)(foreign(table + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	return callUninterrupted(method, append([]uintptr{object}, args...)...)
}
