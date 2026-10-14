// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Isochronous endpoints through IOKit: the same ring the other backends use,
// because such an endpoint is serviced only while a transfer is outstanding.
//
// IOKit has no synchronous isochronous call. A transfer is scheduled against a
// bus frame number in the future and reported by a message on the interface's
// async port, which async_darwin.go reads.

package iokit

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// isoFrame is IOUSBIsocFrame: what was asked of one frame and what came back.
type isoFrame struct {
	status   int32
	reqCount uint16
	actCount uint16
}

// iokitIsoEntry is one transfer of the ring, with the memory IOKit fills.
type iokitIsoEntry struct {
	payload    unsafe.Pointer
	frames     unsafe.Pointer
	completion asyncCompletion
	// reference is what the transfer is submitted with, and what its completion
	// message names it by.
	reference uint64
	inFlight  bool
	length    int
	// start is the bus frame the transfer was last booked against.
	start uint64
}

func (entry *iokitIsoEntry) done() bool { return entry.completion.finished() }

// iokitIsoRing keeps several isochronous transfers outstanding on one pipe.
type iokitIsoRing struct {
	opened     *iokitDevice
	receiver   *asyncReceiver
	pipe       uint8
	packets    int
	packetSize int
	// nextFrame is the bus frame the next transfer is scheduled against.
	// Isochronous transfers are placed in time rather than queued, so each takes
	// the frames after the last: a gap is airtime nobody hears, an overlap is
	// refused.
	nextFrame uint64
	// framesPerTransfer is how far nextFrame moves per submission: the frames
	// `packets` frame-list entries occupy. See the note above microFramesPerFrame.
	framesPerTransfer uint64
	entries           []*iokitIsoEntry
	head              int
	closed            bool
}

// isoSlackFrames is how far ahead of the bus the first transfer is scheduled.
// A frame is a millisecond, and the controller will refuse anything already in
// the past by the time it reads it.
//
// It is kept short because it is not free: opening the ring waits for the first
// booked frame before the device is told to send (see awaitStart), so this is
// time added to every start. That wait is what keeps the head of the stream
// whole. Before it, the device sent into a FIFO nobody read until the first
// frame came round, which overflowed and left a few stale packets and a gap:
// measured on a US-3POUT on a host where a ring fills in 11 to 20 ms, ten frames
// gave a clean start in ten opens of ten, 32 in seven, 64 in four.
//
// A short slack can be overtaken instead. The ring is filled one submission at
// a time, each booking two frames, so the lead grows as it fills -- but only
// from this, and any submission can stall: each is a round trip into the
// kernel, and a garbage collection, a preempted thread or a busy machine holds
// the next one up. A stall longer than the lead built so far lets the bus
// overtake the booking; book then catches up, and the rest is booked from where
// the bus has got to. On one host no submission stalled for more than half a
// millisecond; on another they stalled for 4 ms every few dozen and now and
// then for 15 to 65 ms, and one fill in seven was overtaken.
const isoSlackFrames = 10

// The two arguments that place a transfer in time do not count the same unit on
// a high-speed endpoint. Apple's rule: on a high-speed device the frame list
// and its length mean "transfer opportunities", one entry per microframe and
// eight to the millisecond, while frameStart still means a frame. So a
// submission asks for `packets` microframes but is placed at a frame number,
// and the next belongs `packets/8` frames later.
//
// Getting that wrong does not fail: it books one eighth of the airtime.
// Measured on a US-3POUT carrying ISDB-T, advancing by whole frames held the
// endpoint to 460 KB/s against the 2.15 MB/s the same board delivers on
// Windows.
const (
	// microFramesPerFrame is what a high-speed frame is divided into.
	microFramesPerFrame = 8
	// fullSpeedFrameMicroseconds is what GetFrameListTime answers for a full-speed
	// device, where a frame-list entry is a whole frame.
	fullSpeedFrameMicroseconds = 1000
)

// leastTransfers is the shortest ring worth streaming from: a tenth of a second
// of a high-speed endpoint.
const leastTransfers = 64

// isoMostFramesAhead bounds how far ahead of the bus the ring is booked, which
// cuts the ring short of the depth asked for where the two disagree.
//
// Every controller has a horizon past which it will not book, and they differ.
// One measured refuses a transfer about 900 frames ahead with
// kIOReturnIsoTooNew; the ring is then cut short where it was refused, but every
// transfer put back afterwards is booked that same distance ahead and refused
// again, and the stream stops after one lap. Another accepts anything and comes
// apart instead once a ring reaches past about 700 frames: the transfers are
// served one or two at a time against frames already gone, and almost nothing
// arrives. Measured there on a US-3POUT carrying ISDB-T, 320 transfers of 2 ms
// streamed 1.76 MB/s with no continuity errors, 352 and more lost most of the
// multiplex, and the 512 asked for kept under a twentieth of it. Half a second
// of ring is inside both, with a margin.
const isoMostFramesAhead = 512

// isoTooOld is kIOReturnIsoTooOld, a transfer booked against a frame the bus has
// already passed, and isoMostCatchUps how many times filling a ring books
// again from where the bus is before it gives up.
const (
	isoTooOld       = 0xe00002ee
	isoMostCatchUps = 8
)

func (opened *iokitDevice) startIsoRing(endpoint byte, packetSize, packets, depth int) (*iokitIsoRing, error) {
	pipe, err := opened.pipeFor(endpoint)
	if err != nil {
		return nil, err
	}
	if endpoint&0x80 == 0 {
		return nil, fmt.Errorf("%w: only reading an isochronous endpoint is ported", backend.ErrUnsupported)
	}
	receiver, err := startReceiver(opened.iface)
	if err != nil {
		return nil, err
	}
	ring := &iokitIsoRing{
		opened: opened, receiver: receiver, pipe: pipe,
		packets: packets, packetSize: packetSize,
	}
	frame, err := ring.busFrame()
	if err != nil {
		ring.Close()
		return nil, err
	}
	ring.nextFrame = frame + isoSlackFrames

	// How much time one frame-list entry stands for, the only thing separating a
	// full-speed endpoint from a high-speed one here.
	var entryMicroseconds uint32
	pinned := pin(&entryMicroseconds)
	result := vtable(opened.iface, slotInterfaceFrameTime, uintptr(unsafe.Pointer(&entryMicroseconds)))
	pinned.Unpin()
	if result != 0 {
		// Older interfaces do not offer it. Treating the endpoint as full speed
		// books more airtime than needed rather than less, which costs bus
		// bandwidth but never drops what the device sent.
		entryMicroseconds = fullSpeedFrameMicroseconds
	}
	ring.framesPerTransfer = uint64(packets)
	if entryMicroseconds != fullSpeedFrameMicroseconds {
		ring.framesPerTransfer = (uint64(packets) + microFramesPerFrame - 1) / microFramesPerFrame
	}
	if ring.framesPerTransfer == 0 {
		ring.framesPerTransfer = 1
	}
	if most := int((isoMostFramesAhead - isoSlackFrames) / ring.framesPerTransfer); depth > most {
		depth = most
	}

	length := packetSize * packets
	for index := 0; index < depth; index++ {
		entry := &iokitIsoEntry{length: length}
		if entry.payload = allocate(length); entry.payload == nil {
			ring.Close()
			return nil, errors.New("allocate an isochronous buffer")
		}
		if entry.frames = allocate(packets * int(unsafe.Sizeof(isoFrame{}))); entry.frames == nil {
			ring.Close()
			return nil, errors.New("allocate an isochronous frame list")
		}
		entry.reference = registerCompletion(&entry.completion)
		ring.entries = append(ring.entries, entry)
	}
	caughtUp := 0
	for index := 0; index < len(ring.entries); index++ {
		result := ring.book(ring.entries[index])
		if result == isoTooOld && caughtUp < isoMostCatchUps {
			// Booking fell behind the bus while the ring was being filled: a
			// submission stalled for longer than the lead built so far (see
			// isoSlackFrames), on any host, after book had looked at the bus. Booked
			// again, it catches up with where the bus has got to. The transfers
			// already booked behind it come back partly or wholly empty, which
			// costs the first few dozen milliseconds of the stream; giving up
			// instead left it to a transfer at a time, which keeps almost nothing.
			caughtUp++
			index--
			continue
		}
		if result != 0 {
			err := ioKitError("submit an isochronous transfer", result)
			// What the ring holds is runway, not correctness: a shorter one covers a
			// shorter silence from the host and streams just the same, which is better
			// than refusing a stream because the system would not take the whole of it.
			if index >= leastTransfers {
				for _, dropped := range ring.entries[index:] {
					unregisterCompletion(dropped.reference)
					dropped.release()
				}
				ring.entries = ring.entries[:index]
				break
			}
			ring.Close()
			return nil, err
		}
	}
	ring.awaitStart()
	return ring, nil
}

// startWaitLimit bounds how long opening a ring waits for its first frame.
const startWaitLimit = 200 * time.Millisecond

// awaitStart returns once the bus has reached the frame the ring begins at.
//
// Whoever opens a stream tells the device to send as soon as this returns, and
// until the first booked frame comes round nothing reads the device's FIFO,
// which overflows and leaves a few stale packets and a gap at the head of the
// stream (see isoSlackFrames). A ring that fills slowly is past its first frame
// by the time it is filled; one that fills fast, or was booked again from where
// the bus had got to, is not, and waits here the few frames that remain.
func (ring *iokitIsoRing) awaitStart() {
	if len(ring.entries) == 0 || !ring.entries[0].inFlight {
		return
	}
	start := ring.entries[0].start
	deadline := time.Now().Add(startWaitLimit)
	for time.Now().Before(deadline) {
		frame, err := ring.busFrame()
		if err != nil || frame >= start {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// busFrame reads the bus frame number the controller is on.
func (ring *iokitIsoRing) busFrame() (uint64, error) {
	var frame uint64
	var atTime [8]byte
	defer pin(&frame, &atTime).Unpin()
	if result := vtable(ring.opened.iface, slotInterfaceBusFrame,
		uintptr(unsafe.Pointer(&frame)), uintptr(unsafe.Pointer(&atTime[0]))); result != 0 {
		return 0, ioKitError("read the bus frame number", uint32(result))
	}
	return frame, nil
}

// release frees an entry's buffers, which only an entry IOKit is done with may do.
func (entry *iokitIsoEntry) release() {
	if entry.payload != nil {
		release(entry.payload)
		entry.payload = nil
	}
	if entry.frames != nil {
		release(entry.frames)
		entry.frames = nil
	}
}

func (ring *iokitIsoRing) frameList(entry *iokitIsoEntry) []isoFrame {
	return unsafe.Slice((*isoFrame)(entry.frames), ring.packets)
}

// submit schedules one transfer against the next stretch of bus time.
func (ring *iokitIsoRing) submit(entry *iokitIsoEntry) error {
	if result := ring.book(entry); result != 0 {
		return ioKitError("submit an isochronous transfer", result)
	}
	return nil
}

// book is submit without the error made of its IOReturn, for the caller that
// answers some of them.
func (ring *iokitIsoRing) book(entry *iokitIsoEntry) uint32 {
	entry.completion.reset()
	for index := range ring.frameList(entry) {
		ring.frameList(entry)[index] = isoFrame{reqCount: uint16(ring.packetSize)}
	}
	// A reader that fell behind would place this in the past, which the
	// controller refuses. Catch up with the bus instead: the airtime between is
	// lost, but the stream goes on.
	if bus, err := ring.busFrame(); err == nil && ring.nextFrame < bus+isoSlackFrames {
		ring.nextFrame = bus + isoSlackFrames
	}
	frameStart := ring.nextFrame

	result := vtable(ring.opened.iface, slotInterfaceReadIsoch,
		uintptr(ring.pipe), uintptr(entry.payload), uintptr(frameStart), uintptr(ring.packets),
		uintptr(entry.frames), asyncCallout(), uintptr(entry.reference))
	if result != 0 {
		// Not booked, so the schedule does not move: moving it on a refusal is
		// what ran the ring further into the future with every retry until
		// nothing could be booked at all.
		return uint32(result)
	}
	ring.nextFrame += ring.framesPerTransfer
	entry.start = frameStart
	entry.inFlight = true
	return 0
}

// Read hands back the oldest finished transfer and puts it back in flight.
func (ring *iokitIsoRing) Read(buffer []byte, timeoutMS uint32) (int, error) {
	if ring.closed {
		return 0, backend.ErrGone
	}
	if len(buffer) < ring.packetSize*ring.packets {
		return 0, errors.New("isochronous read buffer is smaller than one transfer")
	}
	entry := ring.entries[ring.head]
	if !entry.inFlight {
		if err := ring.submit(entry); err != nil {
			return 0, err
		}
	}
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for !entry.done() {
		if time.Now().After(deadline) {
			return 0, backend.ErrTimeout
		}
		// The receiver does the waiting; this only comes back and looks. A
		// frame is a millisecond, so a shorter sleep would spin for nothing.
		time.Sleep(time.Millisecond)
	}
	entry.inFlight = false
	status := entry.completion.status

	transferred := 0
	for index, frame := range ring.frameList(entry) {
		length := int(frame.actCount)
		if length > ring.packetSize {
			length = ring.packetSize
		}
		if length != 0 {
			outOfForeignAt(buffer[transferred:], entry.payload, index*ring.packetSize, length)
		}
		transferred += length
	}

	// Back in flight before the result is reported, so the endpoint keeps a
	// full ring while the caller is away with the data.
	resubmit := ring.submit(entry)
	ring.head = (ring.head + 1) % len(ring.entries)

	switch uint32(status) {
	case 0:
	case 0xe00002c0, 0xe00002d9: // kIOReturnNoDevice, kIOReturnNotAttached
		return transferred, backend.ErrGone
	default:
		// A transfer that arrived late or partly is still worth what came back;
		// the frame list already says which packets were empty.
		if transferred == 0 {
			if resubmit != nil {
				return 0, resubmit
			}
			return 0, ioKitError(fmt.Sprintf("isochronous endpoint %#02x", ring.pipe), uint32(status))
		}
	}
	return transferred, resubmit
}

// Close aborts what is outstanding, waits for the completions to have arrived,
// and only then frees anything: IOKit writes into a transfer's buffers until it
// has completed, so freeing earlier hands the allocator memory the kernel is
// filling.
func (ring *iokitIsoRing) Close() error {
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
		entry.release()
	}
	ring.entries = nil
	return nil
}
