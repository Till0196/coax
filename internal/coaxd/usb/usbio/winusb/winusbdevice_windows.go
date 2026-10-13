// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winusb

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// winusbDevice is one opened device, with WinUSB's handle on interface 0.
type winusbDevice struct {
	file   syscall.Handle
	handle uintptr
	// mu guards the pipe policy, which is per pipe rather than per transfer:
	// two transfers on the same pipe wanting different timeouts must not race.
	mu sync.Mutex
	// alternate is the setting the interface was last put into. Setting it
	// again resets every pipe on the interface, which is what an isochronous
	// pipe needs before it will carry a stream again, so it is remembered here
	// for whoever has to put one back.
	alternate int
}

// setupPacket is WINUSB_SETUP_PACKET: eight bytes, passed by value.
type setupPacket struct {
	RequestType uint8
	Request     uint8
	Value       uint16
	Index       uint16
	Length      uint16
}

// words packs the packet into the machine words it travels in: eight bytes by
// value is one register on 64-bit and two on 32-bit, and a call takes its
// arguments a word at a time. Laid out little-endian explicitly rather than by
// reinterpreting the structure's address.
func (packet setupPacket) words() []uintptr {
	low := uint32(packet.RequestType) |
		uint32(packet.Request)<<8 |
		uint32(packet.Value)<<16
	high := uint32(packet.Index) | uint32(packet.Length)<<16
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return []uintptr{uintptr(low) | uintptr(high)<<32}
	}
	return []uintptr{uintptr(low), uintptr(high)}
}

// openWinUSB claims interface 0 of the device at a device interface path.
func openWinUSB(path string) (backend.Device, error) {
	wide, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Opened without sharing, so a second attempt on the same device fails. One
	// board is several tuners and a card slot behind a single endpoint that only
	// one process can drive, but WinUSB will hand out a second handle and the two
	// then undo each other: the second's bring-up resets the first's alternate
	// setting, the first's teardown takes the second's stream down.
	file, err := syscall.CreateFile(wide, genericRead|genericWrite, 0,
		nil, openExisting, fileFlagOverlapped, 0)
	if err != nil {
		return nil, windowsError("open USB device", err)
	}
	opened := &winusbDevice{file: file}
	if ret, _, err := procInitialize.Call(uintptr(file), uintptr(unsafe.Pointer(&opened.handle))); ret == 0 {
		syscall.CloseHandle(file)
		// A present device bound to something else fails here, the ordinary case
		// for a tuner still held by its vendor driver.
		return nil, fmt.Errorf("claim USB interface (is the device bound to WinUSB?): %w", err)
	}
	return opened, nil
}

// waiter is one transfer's completion: an event and the overlapped structure
// Windows signals it through, and the word a transfer reports its length into.
// It is always on the heap, and whoever submits a transfer through it keeps it
// reachable until the transfer has come back, because Windows writes into it
// until then.
type waiter struct {
	overlapped syscall.Overlapped
	event      syscall.Handle
	moved      uint32
}

// newWaiter makes a manual-reset event to wait a transfer out on.
func newWaiter() (*waiter, error) {
	handle, _, err := procCreateEvent.Call(0, 1, 0, 0)
	if handle == 0 {
		return nil, windowsError("create completion event", err)
	}
	wait := &waiter{event: syscall.Handle(handle)}
	wait.overlapped.HEvent = wait.event
	return wait, nil
}

func (wait *waiter) Close() { syscall.CloseHandle(wait.event) }

// settleTimeoutMS is how long a withdrawn transfer is given to come back. A
// cancelled request completes as soon as the driver lets go of it; this only
// bounds a driver that never does.
const settleTimeoutMS = 5000

// settle waits for a transfer that has been withdrawn, or whose device has been
// closed under it, to come back, and reports whether it did. Windows writes the
// transfer's status into the overlapped structure, and the data into its
// buffer, until then, so neither may be let go before.
//
// It waits on the event rather than through GetOverlappedResult, which needs
// the WinUSB handle: the event is signalled when the request completes whatever
// has become of the handle, including when closing the handle is what cancelled
// it.
func (wait *waiter) settle() bool {
	code, err := syscall.WaitForSingleObject(wait.event, settleTimeoutMS)
	return err == nil && code == waitObject0
}

// stranded keeps alive, for the life of the process, memory a transfer that
// never came back may still be written into. Leaking it is the lesser harm: let
// go, the collector hands it out again and Windows completes the transfer into
// whatever it has become.
var (
	strandedMu sync.Mutex
	stranded   []any
)

func strand(memory ...any) {
	strandedMu.Lock()
	stranded = append(stranded, memory...)
	strandedMu.Unlock()
}

// release lets a single transfer's waiter go, or strands it with the buffer it
// was reading into when the transfer is still in flight. finish has already
// withdrawn such a transfer and waited it out, so it is not waited for again.
func (wait *waiter) release(inFlight bool, buffer []byte) {
	if inFlight {
		strand(wait, buffer)
		return
	}
	wait.Close()
}

// withdraw takes back a transfer still with the driver. A bulk or interrupt
// pipe is aborted, as it has always been, so the pipe is left clear for the
// next transfer. The default pipe cannot be aborted, so a control transfer is
// cancelled by its own overlapped structure instead.
func (opened *winusbDevice) withdraw(wait *waiter, pipe byte) {
	if pipe != 0 {
		procAbortPipe.Call(opened.handle, uintptr(pipe))
		return
	}
	procCancelIoEx.Call(uintptr(opened.file), uintptr(unsafe.Pointer(&wait.overlapped)))
}

// finish waits for the transfer to complete, withdrawing it from the pipe given
// if it does not do so in time. It reports whether the transfer may still be in
// flight, which is only so when even withdrawing it did not bring it back.
func (opened *winusbDevice) finish(wait *waiter, pipe byte, timeoutMS uint32) (int, bool, error) {
	// Asked for first without blocking, because a wait that blocks hands the
	// runtime back the processor slot the caller was holding and getting one
	// again is a queue joined behind everything else the machine is doing.
	// Measured on a two-processor machine with the stream being played back,
	// that queue kept a transfer that had already completed waiting three
	// hundred milliseconds, which is longer than a card streaming will wait.
	code, err := syscall.WaitForSingleObject(wait.event, 0)
	if err == nil && code == waitTimeout && timeoutMS != 0 {
		code, err = syscall.WaitForSingleObject(wait.event, timeoutMS)
	}
	if err != nil || code != waitObject0 {
		// The request is still with the driver, or may be, and has to be withdrawn
		// and seen back before the buffer behind it goes anywhere.
		opened.withdraw(wait, pipe)
		if !wait.settle() {
			return 0, true, backend.ErrTimeout
		}
		procOverlapped.Call(opened.handle, uintptr(unsafe.Pointer(&wait.overlapped)),
			uintptr(unsafe.Pointer(&wait.moved)), 0)
		if err != nil {
			return int(wait.moved), false, windowsError("wait for transfer", err)
		}
		return int(wait.moved), false, backend.ErrTimeout
	}
	if ret, _, err := procOverlapped.Call(opened.handle, uintptr(unsafe.Pointer(&wait.overlapped)),
		uintptr(unsafe.Pointer(&wait.moved)), 1); ret == 0 {
		return int(wait.moved), false, windowsError("complete transfer", err)
	}
	return int(wait.moved), false, nil
}

// Control runs a control transfer on the default pipe.
//
// Every pointer is converted to a number in the call's own argument list,
// which is what keeps the memory behind it where it is for the length of the
// call; built into a slice beforehand, the length word could be left on the
// goroutine's stack and moved out from under the driver writing it. The setup
// packet travels by value, as one word on 64-bit and two on 32-bit.
func (opened *winusbDevice) Control(requestType, request byte, value, index uint16,
	buffer []byte, timeoutMS uint32) (int, error) {
	wait, err := newWaiter()
	if err != nil {
		return 0, err
	}
	inFlight := false
	defer func() { wait.release(inFlight, buffer) }()

	packet := setupPacket{RequestType: requestType, Request: request,
		Value: value, Index: index, Length: uint16(len(buffer))}
	words := packet.words()
	var data unsafe.Pointer
	if len(buffer) != 0 {
		data = unsafe.Pointer(&buffer[0])
	}
	var ret uintptr
	if len(words) == 1 {
		ret, _, err = procControl.Call(opened.handle, words[0], uintptr(data), uintptr(len(buffer)),
			uintptr(unsafe.Pointer(&wait.moved)), uintptr(unsafe.Pointer(&wait.overlapped)))
	} else {
		ret, _, err = procControl.Call(opened.handle, words[0], words[1], uintptr(data), uintptr(len(buffer)),
			uintptr(unsafe.Pointer(&wait.moved)), uintptr(unsafe.Pointer(&wait.overlapped)))
	}
	if ret != 0 {
		return int(wait.moved), nil
	}
	if err != syscall.ERROR_IO_PENDING {
		return 0, windowsError("control transfer", err)
	}
	// Pipe zero is the default pipe, which is the one a control transfer runs
	// on and the one it would have to be withdrawn from.
	moved, stillInFlight, err := opened.finish(wait, 0, timeoutMS)
	inFlight = stillInFlight
	return moved, err
}

func (opened *winusbDevice) Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error) {
	wait, err := newWaiter()
	if err != nil {
		return 0, err
	}
	inFlight := false
	defer func() { wait.release(inFlight, buffer) }()

	transfer := procReadPipe
	if endpoint&0x80 == 0 {
		transfer = procWritePipe
	}
	var data unsafe.Pointer
	if len(buffer) != 0 {
		data = unsafe.Pointer(&buffer[0])
	}
	ret, _, err := transfer.Call(opened.handle, uintptr(endpoint), uintptr(data), uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&wait.moved)), uintptr(unsafe.Pointer(&wait.overlapped)))
	if ret != 0 {
		return int(wait.moved), nil
	}
	if err != syscall.ERROR_IO_PENDING {
		return 0, windowsError(fmt.Sprintf("bulk endpoint %#02x", endpoint), err)
	}
	moved, stillInFlight, err := opened.finish(wait, endpoint, timeoutMS)
	inFlight = stillInFlight
	return moved, err
}

func (opened *winusbDevice) SetAlternate(interfaceNumber, alternate int) error {
	if ret, _, err := procSetAlt.Call(opened.handle, uintptr(alternate)); ret == 0 {
		return windowsError("set USB alternate setting", err)
	}
	opened.alternate = alternate
	return nil
}

func (opened *winusbDevice) Close() error {
	if opened.handle != 0 {
		procFree.Call(opened.handle)
		opened.handle = 0
	}
	if opened.file != 0 {
		syscall.CloseHandle(opened.file)
		opened.file = 0
	}
	return nil
}
