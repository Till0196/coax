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

// usbfsIsoEntry is one URB of a ring, with the buffer the kernel fills.
type usbfsIsoEntry struct {
	block    []byte
	request  *urb
	frames   []isoPacketDescriptor
	buffer   []byte
	inFlight bool
	reaped   bool
}

// usbfsIsoRing keeps several isochronous URBs outstanding on one endpoint: such
// an endpoint is serviced only while a transfer is outstanding, with no queue
// and no retry, so submitting one URB at a time leaves a gap on every lap. URBs
// on one endpoint complete in submission order, so the ring is read in that
// order too; one that comes back out of turn is still matched by address.
type usbfsIsoRing struct {
	opened     *usbfsDevice
	endpoint   byte
	packetSize int
	packets    int
	timeoutMS  uint32
	entries    []*usbfsIsoEntry
	head       int
	// pinner is the ring's own, not the device's: the device's is unpinned
	// wholesale by every call that uses it, which would pull these URBs out from
	// under the kernel.
	pinner runtime.Pinner
	closed bool
}

// leastTransfers is the shortest ring worth streaming from: a tenth of a second
// of a high-speed endpoint. Below that the reader would have to be given its
// turn more often than an ordinary machine ever manages.
const leastTransfers = 64

func (opened *usbfsDevice) OpenIsoStream(endpoint byte, packetSize, packets, depth int, timeoutMS uint32) (backend.IsoStream, error) {
	// The ring the last stream left running, if it was on this endpoint: it is
	// already being read, so it needs neither filling nor waiting for.
	if ring := opened.unpark(endpoint, packetSize, packets); ring != nil {
		ring.timeoutMS = timeoutMS
		return ring, nil
	}
	ring := &usbfsIsoRing{
		opened: opened, endpoint: endpoint,
		packetSize: packetSize, packets: packets, timeoutMS: timeoutMS,
	}
	headerSize := int(unsafe.Sizeof(urb{}))
	frameSize := int(unsafe.Sizeof(isoPacketDescriptor{}))
	for index := 0; index < depth; index++ {
		entry := &usbfsIsoEntry{
			// The URB header and its frame descriptors must be one allocation,
			// the way the kernel's variable-length struct is laid out.
			block:  make([]byte, headerSize+packets*frameSize),
			buffer: make([]byte, packetSize*packets),
		}
		entry.request = (*urb)(unsafe.Pointer(&entry.block[0]))
		entry.frames = unsafe.Slice((*isoPacketDescriptor)(unsafe.Pointer(&entry.block[headerSize])), packets)
		ring.pinner.Pin(&entry.block[0])
		ring.pinner.Pin(&entry.buffer[0])
		ring.entries = append(ring.entries, entry)
	}
	for index, entry := range ring.entries {
		if err := ring.submit(entry); err != nil {
			// What the ring holds is runway, not correctness: a shorter one
			// covers a shorter silence from the host and streams just the same.
			// The kernel bounds what one process may have pinned for transfers
			// at once, so a second stream on the same machine can be refused
			// part of a ring that the first one was given whole -- and refusing
			// to stream at all over that would be worse than covering less.
			if index >= leastTransfers {
				ring.entries = ring.entries[:index]
				break
			}
			_ = ring.withdraw()
			return nil, err
		}
	}
	ring.awaitStart()
	return ring, nil
}

// startWaitLimit bounds how long opening a ring waits for its first URB.
const startWaitLimit = 200 * time.Millisecond

// awaitStart returns once the first URB of the ring has come back, which is
// when the endpoint is known to be being read.
//
// Whoever opens a stream tells the device to send as soon as this returns, and
// until the host controller reaches the first URB nothing reads the device's
// FIFO, which overflows and leaves a few stale packets and a gap at the head of
// the stream. The controller places a URB starting a stream a few frames out --
// some milliseconds on EHCI -- so a ring filled faster than that would have the
// device sending before anything listened, as was measured on Windows. usbfs has
// no call for the current frame, so the wait is for the first URB itself: sent
// before the device sends anything, it comes back empty two frames after it
// starts, and stays reaped for Read to take in its turn.
func (ring *usbfsIsoRing) awaitStart() {
	if len(ring.entries) == 0 || !ring.entries[0].inFlight {
		return
	}
	first := ring.entries[0]
	deadline := time.Now().Add(startWaitLimit)
	for time.Now().Before(deadline) {
		back, err := ring.opened.returned(first.request)
		if err != nil {
			return
		}
		if back {
			first.reaped = true
			first.inFlight = false
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (ring *usbfsIsoRing) submit(entry *usbfsIsoEntry) error {
	request := entry.request
	request.Type = urbTypeISO
	request.Endpoint = ring.endpoint
	request.Buffer = unsafe.Pointer(&entry.buffer[0])
	request.BufferLength = int32(ring.packetSize * ring.packets)
	request.NumberOfPackets = int32(ring.packets)
	request.Status = 0
	request.ActualLength = 0
	request.ErrorCount = 0
	for index := range entry.frames {
		entry.frames[index].Length = uint32(ring.packetSize)
		entry.frames[index].ActualLength = 0
		entry.frames[index].Status = 0
	}
	ring.opened.forget(request)
	if err := ioctlOf(ring.opened, usbfsSubmitURB, request); err != nil {
		return fmt.Errorf("submit isochronous transfer on endpoint %#02x: %w", ring.endpoint, err)
	}
	entry.inFlight = true
	entry.reaped = false
	return nil
}

// Read takes the oldest URB once the kernel has handed it back, packs what it
// carried into buffer, and puts it back in flight.
func (ring *usbfsIsoRing) Read(buffer []byte, timeoutMS uint32) (int, error) {
	if ring.closed {
		return 0, backend.ErrGone
	}
	if len(buffer) < ring.packetSize*ring.packets {
		return 0, errors.New("isochronous read buffer is smaller than one transfer")
	}
	entry := ring.entries[ring.head]
	if !entry.inFlight && !entry.reaped {
		if err := ring.submit(entry); err != nil {
			return 0, err
		}
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for !entry.reaped {
		back, err := ring.opened.returned(entry.request)
		if err != nil {
			return 0, err
		}
		if back {
			entry.reaped = true
			entry.inFlight = false
			break
		}
		if time.Now().After(deadline) {
			return 0, backend.ErrTimeout
		}
		// The frames of one URB take a millisecond apiece to arrive, so there
		// is nothing to gain from spinning tighter than that.
		time.Sleep(time.Millisecond)
	}
	entry.reaped = false
	transferred := packFrames(buffer, entry.buffer, entry.frames, ring.packetSize)
	status := entry.request.Status

	// Back in flight before the result is reported, so the endpoint keeps a
	// full ring while the caller is away with the data.
	resubmit := ring.submit(entry)
	ring.head = (ring.head + 1) % len(ring.entries)

	if status != 0 {
		if disconnected(syscall.Errno(-status)) {
			return transferred, backend.ErrGone
		}
		return transferred, fmt.Errorf("isochronous endpoint %#02x: transfer status %d", ring.endpoint, status)
	}
	return transferred, resubmit
}

// Close ends the stream, leaving the ring running for the next one; see park.
func (ring *usbfsIsoRing) Close() error {
	if ring.closed {
		return nil
	}
	ring.opened.park(ring)
	return nil
}

// withdraw takes the ring down for good: it withdraws whatever is still in
// flight and waits for the kernel to hand it back before the pins are dropped,
// so nothing is left referring to a buffer this program has stopped holding
// still.
func (ring *usbfsIsoRing) withdraw() error {
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
			if entry.inFlight {
				outstanding = true
			}
		}
		if !outstanding || time.Now().After(deadline) {
			break
		}
		for _, entry := range ring.entries {
			if !entry.inFlight {
				continue
			}
			if back, _ := ring.opened.returned(entry.request); back {
				entry.inFlight = false
			}
		}
		time.Sleep(time.Millisecond)
	}
	// What has not come back may still be written into when it does, so it is
	// handed to the device to keep rather than let go; see orphans.
	for _, entry := range ring.entries {
		if entry.inFlight {
			pinner := new(runtime.Pinner)
			pinner.Pin(&entry.block[0])
			pinner.Pin(&entry.buffer[0])
			ring.opened.adopt(entry.request, pinner)
		}
	}
	ring.pinner.Unpin()
	ring.entries = nil
	return nil
}
