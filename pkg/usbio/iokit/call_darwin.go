// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Calling C on macOS without cgo. IOKit is reached through libSystem, so a C call cannot be
// avoided -- but cgo can, keeping the build the same as every other target. Three pieces
// do it:
//
//	//go:cgo_import_dynamic  names a libSystem function, which the loader
//	                         resolves to its address at start-up
//	//go:linkname            binds a variable to that symbol, so &variable is
//	                         the function pointer
//	//go:linkname syscall.*  calls a resolved address with the C calling
//	                         convention, on a stack the C side may use
//
// Nothing runs the other way, from C into this program: transfer completions are read off
// their mach port rather than called back (see async_darwin.go), so there is no assembly.

package iokit

import (
	"runtime"
	"syscall"
	"unsafe"
)

//go:cgo_import_dynamic libc_dlopen dlopen "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_dlsym dlsym "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_malloc malloc "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_free free "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_mach_msg mach_msg "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_getpid getpid "/usr/lib/libSystem.B.dylib"

// pthread_sigmask is imported beside them, in signal_darwin.go.

// The imported functions, named as data so their addresses can be taken: the
// loader fills the symbol in with the real function's address, so &variable is
// the pointer to call. The variables are never read -- reading one would read
// the first byte of the function's code.

//go:linkname libc_dlopen libc_dlopen
var libc_dlopen byte

//go:linkname libc_dlsym libc_dlsym
var libc_dlsym byte

//go:linkname libc_malloc libc_malloc
var libc_malloc byte

//go:linkname libc_free libc_free
var libc_free byte

//go:linkname libc_mach_msg libc_mach_msg
var libc_mach_msg byte

//go:linkname libc_getpid libc_getpid
var libc_getpid byte

func symbol(imported *byte) uintptr { return uintptr(unsafe.Pointer(imported)) }

// The standard library's bridge from Go to a C function pointer. It puts the goroutine into
// a system call, so a blocking transfer does not hold up the scheduler.
//
//go:linkname syscallDarwin syscall.syscall
func syscallDarwin(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:linkname syscallDarwin6 syscall.syscall6
func syscallDarwin6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

//go:linkname syscallDarwin9 syscall.syscall9
func syscallDarwin9(fn, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr) (r1, r2 uintptr, err syscall.Errno)

// pin holds Go memory still for a C call that reads or writes through it, and
// keeps it alive until Unpin. Every pointer into Go memory that call or vtable
// passes on goes through here first.
//
// A pointer turned into a uintptr is just a number to the compiler, so the value
// it points at is not kept on the heap on its account: a local whose address is
// only ever passed that way stays on the goroutine's stack, and a stack is moved
// whenever it grows or the collector shrinks it. A call that crosses into C
// through call or vtable runs Go code in between, any of which may grow the
// stack; the C side then reads or writes where the value used to be. Measured on
// a US-3POUT, a mach_msg receive buffer on the stack lost one
// isochronous completion in every few streams -- the message written to the old
// stack, the one before it read a second time -- and the ring stalled on the
// transfer whose completion went missing; the bus frame number read the same way
// came back as the zero the variable started at, and a ring booked against frame
// ten was refused as too old. The standard library's syscall functions are
// exempt only because the compiler knows them by name.
//
// Passing a value to Pin moves it to the heap, which the collector never moves,
// and pins it there, so it is neither moved nor collected until Unpin.
func pin(values ...any) *runtime.Pinner {
	pinner := new(runtime.Pinner)
	for _, value := range values {
		pinner.Pin(value)
	}
	return pinner
}

// call invokes a C function pointer. Every entry point used here takes only
// integers and pointers, so widening each argument to uintptr is exact. A
// pointer among them must be to C memory or to Go memory held with pin.
func call(fn uintptr, args ...uintptr) uintptr {
	var padded [9]uintptr
	copy(padded[:], args)
	switch {
	case len(args) <= 3:
		result, _, _ := syscallDarwin(fn, padded[0], padded[1], padded[2])
		return result
	case len(args) <= 6:
		result, _, _ := syscallDarwin6(fn, padded[0], padded[1], padded[2], padded[3], padded[4], padded[5])
		return result
	default:
		result, _, _ := syscallDarwin9(fn, padded[0], padded[1], padded[2], padded[3],
			padded[4], padded[5], padded[6], padded[7], padded[8])
		return result
	}
}

// The dlopen mode: resolve everything now, and keep the symbols private.
const rtldNow, rtldLocal = 0x2, 0x4

// dlopen opens a shared library by name, returning zero when it is not there.
func dlopen(name string) uintptr {
	bytes := append([]byte(name), 0)
	defer pin(&bytes[0]).Unpin()
	handle, _, _ := syscallDarwin(symbol(&libc_dlopen),
		uintptr(unsafe.Pointer(&bytes[0])), rtldNow|rtldLocal, 0)
	return handle
}

func dlsym(handle uintptr, name string) uintptr {
	bytes := append([]byte(name), 0)
	defer pin(&bytes[0]).Unpin()
	address, _, _ := syscallDarwin(symbol(&libc_dlsym),
		handle, uintptr(unsafe.Pointer(&bytes[0])), 0)
	return address
}

// allocate reserves memory outside the Go heap, for the buffers the kernel
// keeps a pointer to between submitting a transfer and completing it.
func allocate(size int) unsafe.Pointer {
	address, _, _ := syscallDarwin(symbol(&libc_malloc), uintptr(size), 0, 0)
	if address == 0 {
		return nil
	}
	block := unsafe.Slice((*byte)(foreign(address)), size)
	clear(block)
	return foreign(address)
}

// intoForeign copies a caller's bytes into a block outside the Go heap, and
// outOfForeign copies them back. Every transfer stages through such a block:
// the kernel may hold the pointer across the call, and the collector must not
// move what it points at.
func intoForeign(block unsafe.Pointer, data []byte) {
	copy(unsafe.Slice((*byte)(block), len(data)), data)
}

func outOfForeign(data []byte, block unsafe.Pointer, length int) {
	if length > len(data) {
		length = len(data)
	}
	copy(data, unsafe.Slice((*byte)(block), length))
}

// outOfForeignAt copies one stretch out of the middle of a foreign block, which
// is how a frame is taken out of an isochronous transfer's payload.
func outOfForeignAt(data []byte, block unsafe.Pointer, offset, length int) {
	if length > len(data) {
		length = len(data)
	}
	copy(data, unsafe.Slice((*byte)(block), offset+length)[offset:])
}

func release(block unsafe.Pointer) {
	syscallDarwin(symbol(&libc_free), uintptr(block), 0, 0)
}

// foreign turns an address that is not Go's into a pointer; it is used only on
// memory a framework or the C allocator owns, which the collector neither moves
// nor reclaims. Going through the variable's own address avoids go vet's
// unsafeptr check, which flags a direct uintptr conversion -- correctly in
// general, since a uintptr holding a Go address may go stale.
func foreign(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}
