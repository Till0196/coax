// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

// Package sigmask runs a system call with signals held off its thread, for
// drivers that go wrong when a signal is pending while they work.
//
// The Go runtime sends SIGURG to a thread running Go code to preempt its
// goroutine: its monitor does when one goroutine has run for ten milliseconds,
// and the garbage collector does whenever it stops goroutines. A thread that
// enters a system call just as one is sent takes it into the kernel as a pending
// signal, so a Go program has a signal pending far more often than a C program
// does.
package sigmask

import (
	"runtime"
	"syscall"
	"unsafe"
)

// Hold runs a call with every signal that may be held off blocked on its
// thread, and reports what the call reported.
//
// A blocked signal does not count as pending to the kernel, and is delivered
// once the mask is put back. The thread is locked because the mask belongs to
// the thread rather than the goroutine.
func Hold(call func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	held := Held
	var previous uint64
	if err := sigprocmask(sigBlock, &held, &previous); err != nil {
		// Nothing was masked, so there is nothing to put back.
		return call()
	}
	defer func() { _ = sigprocmask(sigSetMask, &previous, nil) }()
	return call()
}

// Held is every signal Hold blocks, which is every signal that may be held off
// at all.
//
// SIGURG is the one that matters most, but a window resize, a SIGHUP asking the
// daemon to reload or a profile being taken is just as pending.
//
// The synchronous ones are left alone: SIGSEGV, SIGBUS, SIGFPE, SIGILL,
// SIGTRAP, SIGABRT and SIGSYS are raised by the running instruction, and
// blocking one makes the kernel reset the handler and kill the process. SIGKILL
// and SIGSTOP cannot be blocked and are left out to say so.
//
// The mask belongs to this thread, so a SIGTERM arriving meanwhile is still
// taken by one of the others, and new threads start from the process's original
// mask.
var Held = ^uint64(0) &^ (Bit(syscall.SIGSEGV) |
	Bit(syscall.SIGBUS) |
	Bit(syscall.SIGFPE) |
	Bit(syscall.SIGILL) |
	Bit(syscall.SIGTRAP) |
	Bit(syscall.SIGABRT) |
	Bit(syscall.SIGSYS) |
	Bit(syscall.SIGKILL) |
	Bit(syscall.SIGSTOP))

// Bit is a signal's place in the kernel's 64-bit signal set.
func Bit(signal syscall.Signal) uint64 { return 1 << (uint(signal) - 1) }

// Current is the calling thread's signal mask.
func Current() (uint64, error) {
	var mask uint64
	err := sigprocmask(sigBlock, nil, &mask)
	return mask, err
}

// The two ways of changing a thread's signal mask that are wanted here, as the
// kernel's numbers rather than the C library's names.
const (
	sigBlock   = 0
	sigSetMask = 2
)

// sigprocmask changes the calling thread's signal mask, as the raw system call
// rather than pthread_sigmask. RawSyscall is right because changing a mask does
// not block.
func sigprocmask(how int, set, previous *uint64) error {
	var setArgument, previousArgument unsafe.Pointer
	if set != nil {
		setArgument = unsafe.Pointer(set)
	}
	if previous != nil {
		previousArgument = unsafe.Pointer(previous)
	}
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK,
		uintptr(how), uintptr(setArgument), uintptr(previousArgument),
		unsafe.Sizeof(uint64(0)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
