// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package siano

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// These are the METHOD_NEITHER requests used by SmsCtrlLib, not KS properties.
// The read buffer is passed as input and filled by the driver through an MDL.
const (
	registerClient   = 0x8800000b
	unregisterClient = 0x8800000f
	readMessage      = 0x88000013
	writeMessage     = 0x88000017
	requestTimeoutMS = 500
)

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	createEvent         = kernel32.NewProc("CreateEventW")
	getOverlappedResult = kernel32.NewProc("GetOverlappedResult")
)

// registration is 12 bytes for a 32-bit caller and 16 for a 64-bit caller. The
// driver uses IoIs32bitProcess to unpack the former on 64-bit Windows.
type registration struct {
	event    syscall.Handle
	client   uint32
	dataType uint32
}

// Read reads one fresh statistics response through its own short-lived
// control handle. It neither changes the running firmware's mode nor registers
// for stream data.
func Read(path string) (Reading, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return Reading{}, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return Reading{}, err
	}
	defer syscall.CloseHandle(handle)
	event, err := newEvent()
	if err != nil {
		return Reading{}, err
	}
	defer syscall.CloseHandle(event)
	channel := control{handle: handle}
	registration := registration{event: event, client: statisticsClient}
	data := unsafe.Slice((*byte)(unsafe.Pointer(&registration)), int(unsafe.Sizeof(registration)))
	if _, err := channel.call(registerClient, data); err != nil {
		return Reading{}, err
	}
	defer channel.call(unregisterClient, nil)
	return readStatistics(&channel)
}

type control struct{ handle syscall.Handle }

func (channel *control) Exchange(request []byte) ([]byte, error) {
	// The driver drops a response when no read is queued. Post the read before
	// sending the request, including when the firmware answers immediately.
	reply := make([]byte, 512)
	read, err := channel.start(readMessage, reply)
	if err != nil {
		return nil, err
	}
	defer read.close()
	if _, err := channel.call(writeMessage, request); err != nil {
		return nil, err
	}
	count, err := read.wait(requestTimeoutMS)
	if err != nil {
		return nil, err
	}
	if count > uint32(len(reply)) {
		return nil, fmt.Errorf("oversized Siano response: %d", count)
	}
	return reply[:count], nil
}

func (channel *control) call(code uint32, data []byte) (uint32, error) {
	operation, err := channel.start(code, data)
	if err != nil {
		return 0, err
	}
	defer operation.close()
	return operation.wait(requestTimeoutMS)
}

// operation pins all memory the driver retains until completion or cancellation.
// In particular, METHOD_NEITHER input can remain locked after DeviceIoControl
// returns ERROR_IO_PENDING.
type operation struct {
	handle   syscall.Handle
	overlap  syscall.Overlapped
	returned uint32
	pin      runtime.Pinner
	pending  bool
}

func newEvent() (syscall.Handle, error) {
	handle, _, err := createEvent.Call(0, 1, 0, 0)
	if handle == 0 {
		return 0, err
	}
	return syscall.Handle(handle), nil
}

func (channel *control) start(code uint32, data []byte) (*operation, error) {
	return startIO(channel.handle, data, func(buffer *byte, returned *uint32, overlap *syscall.Overlapped) error {
		return syscall.DeviceIoControl(channel.handle, code, buffer, uint32(len(data)), nil, 0, returned, overlap)
	})
}

func startIO(handle syscall.Handle, data []byte, issue func(*byte, *uint32, *syscall.Overlapped) error) (*operation, error) {
	event, err := newEvent()
	if err != nil {
		return nil, err
	}
	operation := &operation{handle: handle, overlap: syscall.Overlapped{HEvent: event}}
	operation.pin.Pin(&operation.overlap)
	operation.pin.Pin(&operation.returned)
	var buffer *byte
	if len(data) != 0 {
		buffer = &data[0]
		operation.pin.Pin(buffer)
	}
	err = issue(buffer, &operation.returned, &operation.overlap)
	operation.pending = errors.Is(err, syscall.ERROR_IO_PENDING)
	if err != nil && !operation.pending {
		operation.close()
		return nil, err
	}
	return operation, nil
}

func (operation *operation) wait(milliseconds uint32) (uint32, error) {
	if operation.pending {
		result, err := syscall.WaitForSingleObject(operation.overlap.HEvent, milliseconds)
		if err != nil {
			return 0, err
		}
		if result != syscall.WAIT_OBJECT_0 {
			return 0, fmt.Errorf("Siano control request timed out")
		}
		return operation.complete()
	}
	return operation.complete()
}

func (operation *operation) complete() (uint32, error) {
	result, _, err := getOverlappedResult.Call(uintptr(operation.handle),
		uintptr(unsafe.Pointer(&operation.overlap)), uintptr(unsafe.Pointer(&operation.returned)), 1)
	operation.pending = false
	if result == 0 {
		return 0, err
	}
	return operation.returned, nil
}

func (operation *operation) close() {
	if operation.overlap.HEvent == 0 {
		return
	}
	if operation.pending {
		_ = syscall.CancelIoEx(operation.handle, &operation.overlap)
		// Cancellation is asynchronous too. Collect it before unpinning or
		// closing the event, so the kernel cannot access released Go memory.
		_, _ = operation.complete()
	}
	operation.pin.Unpin()
	_ = syscall.CloseHandle(operation.overlap.HEvent)
	operation.overlap.HEvent = 0
}
