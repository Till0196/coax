// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winusb

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// The pipe policies a bulk stream sets, from winusbio.h.
const (
	// policyRawIO hands reads straight to the USB stack instead of queueing them
	// inside WinUSB, which otherwise sends the next one down only once the last
	// has come back. That is the whole point of keeping several outstanding.
	policyRawIO = 0x07
	// policyMaximumTransferSize is the most one read may ask for under raw I/O.
	policyMaximumTransferSize = 0x08
)

// bulkSlot is one read of the ring, with a buffer of its own.
type bulkSlot struct {
	buffer   []byte
	wait     *waiter
	inFlight bool
}

// winusbBulkRing keeps several reads outstanding on one bulk pipe, so the
// device's FIFO is drained while the reader is away with what it took last.
type winusbBulkRing struct {
	opened  *winusbDevice
	pipe    byte
	size    int
	slots   []*bulkSlot
	head    int
	raw     bool
	stopped bool
}

func (opened *winusbDevice) OpenBulkStream(endpoint byte, size, depth int, timeoutMS uint32) (backend.Stream, error) {
	ring := &winusbBulkRing{opened: opened, pipe: endpoint, size: size}
	// Left over from the last stream, which was closed by aborting the pipe.
	procResetPipe.Call(opened.handle, uintptr(endpoint))
	ring.raw = ring.rawIO(true)
	for index := 0; index < depth; index++ {
		wait, err := newWaiter()
		if err != nil {
			_ = ring.Close()
			return nil, err
		}
		ring.slots = append(ring.slots, &bulkSlot{buffer: make([]byte, ring.size), wait: wait})
	}
	for _, slot := range ring.slots {
		if err := ring.submit(slot); err != nil {
			_ = ring.Close()
			return nil, err
		}
	}
	return ring, nil
}

// rawIO turns raw I/O on or off for the pipe, reporting whether it is on. Raw
// I/O wants every read a whole number of packets and no larger than the
// driver's limit, so the ring's reads are cut to fit; where the policy cannot be
// set the reads still queue inside WinUSB, which is better than one at a time.
func (ring *winusbBulkRing) rawIO(on bool) bool {
	value := byte(0)
	size := ring.size
	if on {
		var limit uint32
		length := uint32(unsafe.Sizeof(limit))
		if ret, _, _ := procGetPipePolicy.Call(ring.opened.handle, uintptr(ring.pipe), policyMaximumTransferSize,
			uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&limit))); ret != 0 && limit != 0 && int(limit) < size {
			size = int(limit)
		}
		// 1024 is a whole number of packets at every speed a tuner runs at.
		size -= size % 1024
		if size == 0 {
			return false
		}
		value = 1
	}
	ret, _, _ := procSetPipePolicy.Call(ring.opened.handle, uintptr(ring.pipe), policyRawIO,
		uintptr(unsafe.Sizeof(value)), uintptr(unsafe.Pointer(&value)))
	if ret == 0 || !on {
		return false
	}
	ring.size = size
	return true
}

func (ring *winusbBulkRing) submit(slot *bulkSlot) error {
	if ret, _, err := procResetEvent.Call(uintptr(slot.wait.event)); ret == 0 {
		return windowsError("reset completion event", err)
	}
	slot.wait.overlapped = syscall.Overlapped{HEvent: slot.wait.event}
	ret, _, err := procReadPipe.Call(ring.opened.handle, uintptr(ring.pipe),
		uintptr(unsafe.Pointer(&slot.buffer[0])), uintptr(len(slot.buffer)),
		0, uintptr(unsafe.Pointer(&slot.wait.overlapped)))
	if ret == 0 && !errors.Is(err, syscall.ERROR_IO_PENDING) {
		return windowsError(fmt.Sprintf("read bulk endpoint %#02x", ring.pipe), err)
	}
	slot.inFlight = true
	return nil
}

// Read hands back the oldest read once it has come back, then whatever else has
// already come back behind it while there is room, putting each one back in
// flight as it goes. Reads on one pipe complete in the order they were made.
func (ring *winusbBulkRing) Read(buffer []byte, timeoutMS uint32) (int, error) {
	if ring.stopped {
		return 0, backend.ErrGone
	}
	if len(buffer) < ring.size {
		return 0, errors.New("bulk stream read buffer is smaller than one transfer")
	}
	total, err := ring.take(buffer, timeoutMS)
	if err != nil {
		return total, err
	}
	for len(buffer)-total >= ring.size {
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
			return total, err
		}
	}
	return total, nil
}

// take collects the oldest read into the buffer and puts the slot back. A read
// that has not come back in time is left outstanding: withdrawing it would be
// the gap the ring is there to prevent.
func (ring *winusbBulkRing) take(buffer []byte, timeoutMS uint32) (int, error) {
	slot := ring.slots[ring.head]
	if !slot.inFlight {
		if err := ring.submit(slot); err != nil {
			return 0, err
		}
	}
	// Asked for first without blocking; see the isochronous ring.
	code, err := syscall.WaitForSingleObject(slot.wait.event, 0)
	if err == nil && code == waitTimeout && timeoutMS != 0 {
		code, err = syscall.WaitForSingleObject(slot.wait.event, timeoutMS)
	}
	if err != nil {
		return 0, windowsError("wait for bulk transfer", err)
	}
	if code == waitTimeout {
		return 0, backend.ErrTimeout
	}
	slot.inFlight = false

	var moved uint32
	// Through the file handle, not WinUSB, so a control transfer in flight does
	// not hold the collection up; see the isochronous ring.
	completed, _, completedErr := procFileOverlapped.Call(uintptr(ring.opened.file),
		uintptr(unsafe.Pointer(&slot.wait.overlapped)), uintptr(unsafe.Pointer(&moved)), 1)
	transferred := copy(buffer, slot.buffer[:moved])
	if completed == 0 {
		if isGone(completedErr) {
			return transferred, backend.ErrGone
		}
		// Put back all the same: a stream that failed once, a stall cleared by the
		// stack say, carries on, and one that is really broken fails again.
		_ = ring.submit(slot)
		ring.head = (ring.head + 1) % len(ring.slots)
		return transferred, windowsError(fmt.Sprintf("bulk endpoint %#02x", ring.pipe), completedErr)
	}
	resubmit := ring.submit(slot)
	ring.head = (ring.head + 1) % len(ring.slots)
	return transferred, resubmit
}

// Close withdraws whatever is still in flight, waits for each read to come back
// so no buffer is freed under the driver, and leaves the pipe reset and in its
// ordinary policy for whoever reads it next.
//
// The wait is on each read's event, which works whether or not the device is
// still open: closing the device first cancels the reads itself, but they still
// complete into these buffers afterwards. A read that does not come back is
// stranded rather than let go.
func (ring *winusbBulkRing) Close() error {
	if ring.stopped {
		return nil
	}
	ring.stopped = true
	if ring.opened.handle != 0 {
		procAbortPipe.Call(ring.opened.handle, uintptr(ring.pipe))
	}
	for _, slot := range ring.slots {
		if slot.inFlight {
			slot.inFlight = false
			if !slot.wait.settle() {
				strand(slot)
				continue
			}
		}
		slot.wait.Close()
	}
	// A device closed first has no pipe left to put back.
	if ring.opened.handle != 0 {
		if ring.raw {
			ring.rawIO(false)
		}
		procResetPipe.Call(ring.opened.handle, uintptr(ring.pipe))
	}
	ring.slots = nil
	return nil
}
