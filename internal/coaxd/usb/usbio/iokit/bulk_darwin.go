// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

package iokit

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// slotInterfaceReadAsync is ReadPipeAsync on IOUSBInterfaceInterface.
const slotInterfaceReadAsync = 33

// iokitBulkEntry is one read of a bulk ring, with the memory IOKit fills.
type iokitBulkEntry struct {
	payload    unsafe.Pointer
	completion asyncCompletion
	reference  uint64
	inFlight   bool
}

func (entry *iokitBulkEntry) done() bool { return entry.completion.finished() }

// iokitBulkRing keeps several reads outstanding on one bulk pipe, so the device's
// FIFO is drained while the reader is away with what it took last. ReadPipeTO
// asks for one transfer and waits for it, which leaves the pipe unserviced
// between every two reads. The completions arrive on the same async port as the
// isochronous ring's; for a bulk read the first argument is the count of bytes
// that arrived.
type iokitBulkRing struct {
	opened   *iokitDevice
	receiver *asyncReceiver
	pipe     uint8
	size     int
	entries  []*iokitBulkEntry
	head     int
	closed   bool
}

func (opened *iokitDevice) OpenBulkStream(endpoint byte, size, depth int, timeoutMS uint32) (backend.Stream, error) {
	pipe, err := opened.pipeFor(endpoint)
	if err != nil {
		return nil, err
	}
	receiver, err := startReceiver(opened.iface)
	if err != nil {
		return nil, err
	}
	ring := &iokitBulkRing{opened: opened, receiver: receiver, pipe: pipe, size: size}
	for index := 0; index < depth; index++ {
		entry := &iokitBulkEntry{}
		if entry.payload = allocate(size); entry.payload == nil {
			ring.Close()
			return nil, errors.New("allocate a bulk buffer")
		}
		entry.reference = registerCompletion(&entry.completion)
		ring.entries = append(ring.entries, entry)
	}
	for _, entry := range ring.entries {
		if err := ring.submit(entry); err != nil {
			ring.Close()
			return nil, err
		}
	}
	return ring, nil
}

func (ring *iokitBulkRing) submit(entry *iokitBulkEntry) error {
	entry.completion.reset()
	result := vtable(ring.opened.iface, slotInterfaceReadAsync,
		uintptr(ring.pipe), uintptr(entry.payload), uintptr(ring.size),
		asyncCallout(), uintptr(entry.reference))
	if result != 0 {
		return ioKitError("submit a bulk read", uint32(result))
	}
	entry.inFlight = true
	return nil
}

// Read hands back the oldest read once it has come back, then whatever else has
// already come back behind it while there is room, putting each back in flight.
func (ring *iokitBulkRing) Read(buffer []byte, timeoutMS uint32) (int, error) {
	if ring.closed {
		return 0, backend.ErrGone
	}
	if len(buffer) < ring.size {
		return 0, errors.New("bulk stream read buffer is smaller than one transfer")
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	total := 0
	for len(buffer)-total >= ring.size {
		entry := ring.entries[ring.head]
		if !entry.inFlight {
			if err := ring.submit(entry); err != nil {
				return total, err
			}
		}
		if !entry.done() {
			if total != 0 {
				break
			}
			if time.Now().After(deadline) {
				return 0, backend.ErrTimeout
			}
			// The receiver does the waiting; this only comes back and looks.
			time.Sleep(time.Millisecond)
			continue
		}
		entry.inFlight = false
		status := uint32(entry.completion.status)
		carried := int(uint32(entry.completion.arg0))
		if carried > ring.size {
			carried = ring.size
		}
		outOfForeign(buffer[total:], entry.payload, carried)
		total += carried
		// Back in flight before anything is reported, so the pipe keeps a full
		// ring while the caller is away with the data.
		resubmit := ring.submit(entry)
		ring.head = (ring.head + 1) % len(ring.entries)
		switch status {
		case 0:
		case 0xe00002c0, 0xe00002d9: // kIOReturnNoDevice, kIOReturnNotAttached
			return total, backend.ErrGone
		default:
			return total, ioKitError(fmt.Sprintf("bulk endpoint pipe %d", ring.pipe), status)
		}
		if resubmit != nil {
			return total, resubmit
		}
	}
	return total, nil
}

// Close aborts what is outstanding, waits for the completions to have arrived,
// and only then frees anything: IOKit writes into a read's buffer until it has
// completed.
func (ring *iokitBulkRing) Close() error {
	if ring.closed {
		return nil
	}
	ring.closed = true
	if ring.opened.iface != 0 {
		vtable(ring.opened.iface, slotInterfaceAbortPipe, uintptr(ring.pipe))
	}
	settle(len(ring.entries), func(index int) bool {
		entry := ring.entries[index]
		return !entry.inFlight || entry.done()
	})
	if ring.receiver != nil {
		ring.receiver.Close()
		ring.receiver = nil
	}
	for _, entry := range ring.entries {
		unregisterCompletion(entry.reference)
		if entry.inFlight && !entry.done() {
			// Never came back, so IOKit may still write into it: left allocated.
			continue
		}
		if entry.payload != nil {
			release(entry.payload)
		}
	}
	ring.entries = nil
	return nil
}
