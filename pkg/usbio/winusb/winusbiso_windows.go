// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winusb

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// isoPacket is USBD_ISO_PACKET_DESCRIPTOR, which WinUSB fills in per frame:
// where in the registered buffer the frame landed and how much of it arrived.
type isoPacket struct {
	Offset uint32
	Length uint32
	Status uint32
}

// isoSlot is one transfer of the ring, with its own region of the registered
// buffer so the transfers behind it can be filling theirs meanwhile.
type isoSlot struct {
	offset   uint32
	packets  []isoPacket
	wait     *waiter
	inFlight bool
	// frame is the start frame a transfer beginning a stream is booked at. The
	// call takes it by address and hands back the frame after the transfer
	// through it, so it lives as long as the transfer does rather than on the
	// submitting goroutine's stack. start keeps the frame asked for, which the
	// call has overwritten by the time it returns.
	frame uint32
	start uint32
}

// winusbIsoRing keeps several isochronous transfers outstanding on one pipe. An
// isochronous endpoint is serviced only while a transfer is outstanding, with
// no queue and no retry, so reading one transfer at a time leaves a gap on
// every lap. WinUSB registers one buffer with the driver and takes transfers
// into successive regions of it, each continuing the stream the last left off
// at.
type winusbIsoRing struct {
	opened     *winusbDevice
	pipe       byte
	packetSize int
	packets    int
	buffer     []byte
	isoch      uintptr
	slots      []*isoSlot
	head       int
	stopped    bool
	// restarts counts the ring restarts, telling a starved stream from a quiet one.
	restarts uint64
}

// leastTransfers is the shortest ring worth streaming from: a tenth of a second
// of a high-speed endpoint.
const leastTransfers = 64

// isoStartLead is how many frames ahead of the current one a stream is begun:
// far enough that the frame has not gone by before the controller is handed the
// transfer.
const isoStartLead = 8

func (opened *winusbDevice) OpenIsoStream(endpoint byte, packetSize, packets, depth int,
	timeoutMS uint32) (backend.IsoStream, error) {
	ring := &winusbIsoRing{
		opened: opened, pipe: endpoint,
		packetSize: packetSize, packets: packets,
		buffer: make([]byte, packetSize*packets*depth),
	}
	// A pipe that was aborted stays aborted until reset, and it is aborted every
	// time a stream is closed. Without this the second stream of a session, and
	// every one after, is accepted and then simply carries nothing.
	procResetPipe.Call(opened.handle, uintptr(endpoint))
	if ret, _, err := procRegisterIsoch.Call(opened.handle, uintptr(endpoint),
		uintptr(unsafe.Pointer(&ring.buffer[0])), uintptr(len(ring.buffer)),
		uintptr(unsafe.Pointer(&ring.isoch))); ret == 0 {
		return nil, windowsError(fmt.Sprintf("register isochronous buffer on endpoint %#02x", endpoint), err)
	}
	for index := 0; index < depth; index++ {
		wait, err := newWaiter()
		if err != nil {
			_ = ring.Close()
			return nil, err
		}
		ring.slots = append(ring.slots, &isoSlot{
			offset:  uint32(index * packetSize * packets),
			packets: make([]isoPacket, packets),
			wait:    wait,
		})
	}
	// The first transfer starts the stream and the rest continue it, which is what
	// makes the ring gapless.
	for index, slot := range ring.slots {
		if err := ring.submit(slot, index != 0); err != nil {
			// What the ring holds is runway, not correctness: a shorter one covers a
			// shorter silence from the host and streams just the same, which is better
			// than refusing a stream because the system would not take the whole of it.
			if index >= leastTransfers {
				for _, dropped := range ring.slots[index:] {
					dropped.wait.Close()
				}
				ring.slots = ring.slots[:index]
				break
			}
			_ = ring.Close()
			return nil, err
		}
	}
	ring.awaitStart()
	return ring, nil
}

// startWaitLimit bounds how long opening a ring waits for its first frame,
// which is isoStartLead frames off when the ring is opened.
const startWaitLimit = 100 * time.Millisecond

// awaitStart returns once the bus has reached the frame the ring begins at.
//
// Whoever opens a stream tells the device to send as soon as this returns, and
// until the first booked frame comes round nothing reads the device's FIFO. A
// ring is filled in a few milliseconds, inside the lead the first transfer is
// booked with, so without this wait the device was sending into a FIFO nobody
// read for the rest of it: measured on a US-3POUT, the first transfer came
// back with three full microframes queued, and half the streams began with a few
// packets from before a gap. Waiting a few frames costs nothing.
func (ring *winusbIsoRing) awaitStart() {
	if len(ring.slots) == 0 || !ring.slots[0].inFlight {
		return
	}
	start := ring.slots[0].start
	deadline := time.Now().Add(startWaitLimit)
	for time.Now().Before(deadline) {
		var frame uint32
		var stamp int64
		if ok, _, _ := procCurrentFrame.Call(ring.opened.handle,
			uintptr(unsafe.Pointer(&frame)), uintptr(unsafe.Pointer(&stamp))); ok == 0 {
			return
		}
		// Compared as a difference, which carries the wrap of the frame counter.
		if int32(frame-start) >= 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func (ring *winusbIsoRing) submit(slot *isoSlot, continueStream bool) error {
	if ret, _, err := procResetEvent.Call(uintptr(slot.wait.event)); ret == 0 {
		return windowsError("reset completion event", err)
	}
	slot.wait.overlapped = syscall.Overlapped{HEvent: slot.wait.event}
	var ret uintptr
	var err error
	if continueStream {
		ret, _, err = procReadIsochAsap.Call(ring.isoch, uintptr(slot.offset),
			uintptr(ring.packetSize*ring.packets), 1, uintptr(ring.packets),
			uintptr(unsafe.Pointer(&slot.packets[0])), uintptr(unsafe.Pointer(&slot.wait.overlapped)))
	} else {
		// A stream is begun at a frame of our own choosing rather than as soon as
		// possible.
		//
		// Once the host's frame number passes 2^31 -- a machine up for 24.9 days --
		// the stack's own choice for an as-soon-as-possible start comes out as frame
		// 5, the controller refuses it as a bad start frame, and every transfer
		// continued behind it is refused with it, so the endpoint never carries a
		// byte. Measured at frame 2179405760. A start frame given
		// explicitly is taken at any frame number, and the transfers continued
		// behind it are scheduled from it as usual.
		var frame uint32
		var stamp int64
		if ok, _, frameErr := procCurrentFrame.Call(ring.opened.handle,
			uintptr(unsafe.Pointer(&frame)), uintptr(unsafe.Pointer(&stamp))); ok == 0 {
			return windowsError("read the current USB frame number", frameErr)
		}
		slot.start = frame + isoStartLead
		slot.frame = slot.start
		ret, _, err = procReadIsoch.Call(ring.isoch, uintptr(slot.offset),
			uintptr(ring.packetSize*ring.packets), uintptr(unsafe.Pointer(&slot.frame)), uintptr(ring.packets),
			uintptr(unsafe.Pointer(&slot.packets[0])), uintptr(unsafe.Pointer(&slot.wait.overlapped)))
	}
	if ret == 0 && !errors.Is(err, syscall.ERROR_IO_PENDING) {
		return windowsError(fmt.Sprintf("read isochronous endpoint %#02x", ring.pipe), err)
	}
	slot.inFlight = true
	return nil
}

// restart puts the whole ring back, beginning a stream rather than continuing
// one. A continued transfer is only valid behind another still running, so once
// the chain has come apart the remaining slots have nothing to continue from
// either.
func (ring *winusbIsoRing) restart() error {
	ring.restarts++
	procAbortPipe.Call(ring.opened.handle, uintptr(ring.pipe))
	for _, slot := range ring.slots {
		if !slot.inFlight {
			continue
		}
		var moved uint32
		procOverlapped.Call(ring.opened.handle, uintptr(unsafe.Pointer(&slot.wait.overlapped)),
			uintptr(unsafe.Pointer(&moved)), 1)
		slot.inFlight = false
	}
	// From here on the stream is opened again from nothing, in the order it was
	// opened in the first place. Anything less leaves the pipe stopped, and a
	// stopped pipe does not refuse the transfers put on it -- it takes them and
	// hands them straight back saying the resource is in use, for ever. The buffer
	// is handed back first: the driver refers to it for as long as it holds a
	// stream against it, and one taken twice is refused.
	if ring.isoch != 0 {
		procUnregisterIso.Call(ring.isoch)
		ring.isoch = 0
	}
	// Setting the alternate setting again is what resets every pipe on the
	// interface. Resetting this one alone is not enough.
	procSetAlt.Call(ring.opened.handle, uintptr(ring.opened.alternate))
	procResetPipe.Call(ring.opened.handle, uintptr(ring.pipe))
	if ret, _, err := procRegisterIsoch.Call(ring.opened.handle, uintptr(ring.pipe),
		uintptr(unsafe.Pointer(&ring.buffer[0])), uintptr(len(ring.buffer)),
		uintptr(unsafe.Pointer(&ring.isoch))); ret == 0 {
		return windowsError("register isochronous buffer again", err)
	}
	ring.head = 0
	for index, slot := range ring.slots {
		if err := ring.submit(slot, index != 0); err != nil {
			return err
		}
	}
	return nil
}

// Read takes the transfers the driver has handed back, packs the frames that
// carried anything into the buffer, and puts each one back as it goes.
//
// As many as the buffer will hold rather than one, because one is not enough to
// keep up with. Each transfer covers two milliseconds of the endpoint, so the
// ring turns over five hundred times a second, and taking them one at a time
// means five hundred trips through four system calls every second. Measured, a
// hundred and ninety-seven transfers piled up uncollected against a ring that
// holds a hundred and twenty-eight, and everything past that was gone. Taking
// whatever has come back in one trip lets a reader catch up.
func (ring *winusbIsoRing) Read(buffer []byte, timeoutMS uint32) (int, error) {
	if ring.stopped {
		return 0, backend.ErrGone
	}
	if len(buffer) < ring.packetSize*ring.packets {
		return 0, errors.New("isochronous read buffer is smaller than one transfer")
	}
	total, err := ring.take(buffer, timeoutMS)
	if err != nil {
		return total, err
	}
	// The rest of what is already waiting, while there is room for it. Nothing
	// here waits: a transfer that has not come back yet is next time's.
	for len(buffer)-total >= ring.packetSize*ring.packets {
		slot := ring.slots[ring.head]
		if !slot.inFlight {
			break
		}
		code, waitErr := syscall.WaitForSingleObject(slot.wait.event, 0)
		if waitErr != nil || code == waitTimeout {
			break
		}
		read, err := ring.take(buffer[total:], 0)
		total += read
		if err != nil {
			break
		}
	}
	return total, nil
}

// take collects the oldest transfer into the buffer and puts the slot back.
func (ring *winusbIsoRing) take(buffer []byte, timeoutMS uint32) (int, error) {
	slot := ring.slots[ring.head]
	if !slot.inFlight {
		if err := ring.submit(slot, true); err != nil {
			return 0, err
		}
	}
	// Asked for first without blocking.
	//
	// A wait that blocks hands the runtime back the processor slot the reader was
	// holding, and getting one again is a queue behind everything else the machine
	// is doing. Measured, a transfer that had already completed took three hundred
	// milliseconds to come back that way, which is longer than the ring covers.
	code, err := syscall.WaitForSingleObject(slot.wait.event, 0)
	if err == nil && code == waitTimeout && timeoutMS != 0 {
		code, err = syscall.WaitForSingleObject(slot.wait.event, timeoutMS)
	}
	if err != nil {
		return 0, windowsError("wait for isochronous transfer", err)
	}
	if code == waitTimeout {
		return 0, backend.ErrTimeout
	}
	slot.inFlight = false

	var moved uint32
	// Collected through the file handle rather than through WinUSB.
	//
	// WinUsb_GetOverlappedResult takes WinUSB's own lock on the interface, and a
	// control transfer holds that lock for as long as it is in flight. Measured on
	// a US-3POUT: a completed transfer took a hundred and thirty-three
	// milliseconds to be collected while the demodulator was being asked for its
	// lock, and seventy-five of the ring's transfers piled up behind it. Where the
	// pile outgrows the ring the chain comes apart and the endpoint stops for
	// good. A transfer is an overlapped operation on the device's file handle like
	// any other, so the plain call collects it just as well.
	completed, _, completedErr := procFileOverlapped.Call(uintptr(ring.opened.file),
		uintptr(unsafe.Pointer(&slot.wait.overlapped)), uintptr(unsafe.Pointer(&moved)), 1)
	if completed == 0 {
		if isGone(completedErr) {
			return 0, backend.ErrGone
		}
		// A continued stream that came apart is not a broken device: the pipe ran on
		// while nothing was queued behind it, which is what happens when the reader
		// is held up. Put the ring back and report the lap as empty.
		if err := ring.restart(); err != nil {
			return 0, err
		}
		return 0, backend.ErrTimeout
	}

	transferred := 0
	for _, packet := range slot.packets {
		if packet.Length == 0 {
			continue
		}
		at := int(slot.offset + packet.Offset)
		transferred += copy(buffer[transferred:], ring.buffer[at:at+int(packet.Length)])
	}

	resubmit := ring.submit(slot, true)
	ring.head = (ring.head + 1) % len(ring.slots)
	return transferred, resubmit
}

// isGone reports whether a Win32 failure means the device has left.
func isGone(err error) bool {
	var code syscall.Errno
	if errors.As(err, &code) {
		switch code {
		case 2, 31, 433, 1167:
			return true
		}
	}
	return false
}

// Restarts reports how many times the stream had to be started again because
// the reader fell behind.
func (ring *winusbIsoRing) Restarts() uint64 { return ring.restarts }

// Close withdraws whatever is still in flight and unregisters the buffer. The
// driver refers to the buffer until every transfer on it has come back.
func (ring *winusbIsoRing) Close() error {
	if ring.stopped {
		return nil
	}
	ring.stopped = true
	if len(ring.slots) != 0 && ring.opened.handle != 0 {
		procAbortPipe.Call(ring.opened.handle, uintptr(ring.pipe))
	}
	// Each transfer is waited out on its event, which works whether or not the
	// device is still open; see the bulk ring. One that does not come back keeps
	// the whole ring alive, since its region is part of the registered buffer.
	for _, slot := range ring.slots {
		if slot.inFlight {
			slot.inFlight = false
			if !slot.wait.settle() {
				strand(ring)
				return nil
			}
		}
		slot.wait.Close()
	}
	if ring.opened.handle == 0 {
		// The device was closed first. The registration went with its handle,
		// or it did not; either way it cannot be unregistered through a handle
		// that has been freed, and the buffer it covers is kept rather than
		// handed back while that is not known.
		strand(ring)
		return nil
	}
	if ring.isoch != 0 {
		procUnregisterIso.Call(ring.isoch)
		ring.isoch = 0
	}
	// Left reset rather than aborted, so the next stream on this pipe will run.
	procResetPipe.Call(ring.opened.handle, uintptr(ring.pipe))
	ring.slots = nil
	return nil
}

// Isochronous reads one transfer's worth without keeping a ring, for a caller
// that has not opened a stream. It exists so the interface is complete.
func (opened *winusbDevice) Isochronous(endpoint byte, buffer []byte,
	packetSize, packets int, timeoutMS uint32) (int, error) {
	stream, err := opened.OpenIsoStream(endpoint, packetSize, packets, 1, timeoutMS)
	if err != nil {
		return 0, err
	}
	defer stream.Close()
	return stream.Read(buffer, timeoutMS)
}
