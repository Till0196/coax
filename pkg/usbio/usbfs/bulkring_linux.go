// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbfs

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// usbfsBulkEntry is one URB of a bulk ring, with the buffer the kernel fills.
type usbfsBulkEntry struct {
	request  *urb
	buffer   []byte
	inFlight bool
}

// usbfsBulkRing keeps several bulk URBs outstanding on one endpoint, so the host
// controller goes on draining the device's FIFO while the reader is away with
// what it took last. USBDEVFS_BULK submits one URB and waits for it, which
// leaves the endpoint unserviced between every two reads. URBs on one endpoint
// complete in submission order, so the ring is read in that order.
type usbfsBulkRing struct {
	opened   *usbfsDevice
	endpoint byte
	entries  []*usbfsBulkEntry
	head     int
	// pinner is the ring's own; see the isochronous ring.
	pinner runtime.Pinner
	closed bool
}

func (opened *usbfsDevice) OpenBulkStream(endpoint byte, size, depth int, timeoutMS uint32) (backend.Stream, error) {
	ring := &usbfsBulkRing{opened: opened, endpoint: endpoint}
	for index := 0; index < depth; index++ {
		entry := &usbfsBulkEntry{request: new(urb), buffer: make([]byte, size)}
		ring.pinner.Pin(entry.request)
		ring.pinner.Pin(&entry.buffer[0])
		ring.entries = append(ring.entries, entry)
	}
	for index, entry := range ring.entries {
		if err := ring.submit(entry); err != nil {
			// The kernel bounds what one process may have pinned for transfers, so
			// a later stream can be refused part of its ring; a shorter one still
			// covers more than a single read does.
			if index >= 2 {
				ring.entries = ring.entries[:index]
				break
			}
			_ = ring.Close()
			return nil, err
		}
	}
	return ring, nil
}

func (ring *usbfsBulkRing) submit(entry *usbfsBulkEntry) error {
	*entry.request = urb{
		Type:         urbTypeBulk,
		Endpoint:     ring.endpoint,
		Buffer:       unsafe.Pointer(&entry.buffer[0]),
		BufferLength: int32(len(entry.buffer)),
	}
	ring.opened.forget(entry.request)
	if err := ioctlOf(ring.opened, usbfsSubmitURB, entry.request); err != nil {
		if disconnected(err) {
			return backend.ErrGone
		}
		return fmt.Errorf("submit bulk transfer on endpoint %#02x: %w", ring.endpoint, err)
	}
	entry.inFlight = true
	return nil
}

// Read hands back the oldest URB once the kernel has, then whatever else has
// already come back behind it while there is room, putting each back in flight.
func (ring *usbfsBulkRing) Read(buffer []byte, timeoutMS uint32) (int, error) {
	if ring.closed {
		return 0, backend.ErrGone
	}
	size := len(ring.entries[0].buffer)
	if len(buffer) < size {
		return 0, errors.New("bulk stream read buffer is smaller than one transfer")
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	total := 0
	for len(buffer)-total >= size {
		entry := ring.entries[ring.head]
		if !entry.inFlight {
			if err := ring.submit(entry); err != nil {
				return total, err
			}
		}
		back, err := ring.opened.returned(entry.request)
		if err != nil {
			return total, err
		}
		if !back {
			if total != 0 {
				break
			}
			if time.Now().After(deadline) {
				return 0, backend.ErrTimeout
			}
			// A 64 KB transfer fills in a few milliseconds at the rates a tuner
			// runs at, so a millisecond's sleep costs nothing in latency.
			time.Sleep(time.Millisecond)
			continue
		}
		entry.inFlight = false
		carried := int(entry.request.ActualLength)
		status := entry.request.Status
		total += copy(buffer[total:], entry.buffer[:carried])
		// Back in flight before anything is reported, so the endpoint keeps a full
		// ring while the caller is away with the data.
		resubmit := ring.submit(entry)
		ring.head = (ring.head + 1) % len(ring.entries)
		if status != 0 {
			if disconnected(syscall.Errno(-status)) {
				return total, backend.ErrGone
			}
			return total, fmt.Errorf("bulk endpoint %#02x: transfer status %d", ring.endpoint, status)
		}
		if resubmit != nil {
			return total, resubmit
		}
	}
	return total, nil
}

// Close withdraws whatever is still in flight and waits for the kernel to hand
// it back before the pins are dropped.
func (ring *usbfsBulkRing) Close() error {
	if ring.closed {
		return nil
	}
	ring.closed = true
	for _, entry := range ring.entries {
		if entry.inFlight {
			_ = ioctlOf(ring.opened, usbfsDiscardURB, entry.request)
		}
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		outstanding := false
		for _, entry := range ring.entries {
			if !entry.inFlight {
				continue
			}
			if back, _ := ring.opened.returned(entry.request); back {
				entry.inFlight = false
				continue
			}
			outstanding = true
		}
		if !outstanding || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// What has not come back may still be written into when it does, so it is
	// handed to the device to keep rather than let go; see orphans.
	for _, entry := range ring.entries {
		if entry.inFlight {
			pinner := new(runtime.Pinner)
			pinner.Pin(entry.request)
			pinner.Pin(&entry.buffer[0])
			ring.opened.adopt(entry.request, pinner)
		}
	}
	ring.pinner.Unpin()
	ring.entries = nil
	return nil
}
