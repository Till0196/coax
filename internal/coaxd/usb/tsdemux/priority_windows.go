// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tsdemux

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procCurrentThread = kernel32.NewProc("GetCurrentThread")
	procSetPriority   = kernel32.NewProc("SetThreadPriority")

	// The multimedia class scheduler, which is how a thread says what it is
	// for. It is not there on every installation, so nothing here insists.
	avrt             = syscall.NewLazyDLL("avrt.dll")
	procJoinTask     = avrt.NewProc("AvSetMmThreadCharacteristicsW")
	procTaskPriority = avrt.NewProc("AvSetMmThreadPriority")
)

// THREAD_PRIORITY_ABOVE_NORMAL, and AVRT_PRIORITY_HIGH.
const (
	abovePriority    = 1
	taskPriorityHigh = 2
)

// prioritise gives the calling goroutine a thread of its own, says what the
// thread is for, and asks for it to be scheduled ahead of ordinary work.
//
// What this goroutine does is take bytes off a card that will not wait: an
// endpoint sending isochronously is served only while transfers are
// outstanding, and it has to be given more five hundred times a second. A video
// renderer registers its own threads as multimedia work and is scheduled ahead
// of anything that has not: measured on a two-processor machine playing the
// stream back, two runs in four lost packets without this and none with it.
func prioritise() {
	runtime.LockOSThread()
	thread, _, _ := procCurrentThread.Call()
	procSetPriority.Call(thread, uintptr(abovePriority))

	task, err := syscall.UTF16PtrFromString("Capture")
	if err != nil {
		return
	}
	var index uint32
	handle, _, _ := procJoinTask.Call(uintptr(unsafe.Pointer(task)), uintptr(unsafe.Pointer(&index)))
	if handle != 0 {
		procTaskPriority.Call(handle, uintptr(taskPriorityHigh))
	}
}
