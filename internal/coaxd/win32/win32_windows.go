// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package win32 is as much of COM as this program uses.
//
// The generated bindings this used to depend on are sixty-four bit only: two of
// the packages a DirectShow build drags in write their predefined handles as
// sign-extended sixty-four bit constants, which do not fit a pointer on a
// thirty-two bit build. Writing out the hundred or so symbols actually used was
// cheaper than forking eighty megabytes of generated code to change twelve
// constants.
//
// # How a COM call works here
//
// An interface pointer points at a pointer to a table of function addresses; a
// method is a slot in that table, called with the interface pointer as its
// first argument. A binding is therefore a slot number and an argument list,
// and the table need not be declared in full.
//
// The slot numbers are the one thing here that cannot be checked by reading: a
// wrong slot calls a different method with this method's arguments.
package win32

import (
	"fmt"
	"syscall"
	"unsafe"
)

// GUID is an interface or class identifier, laid out as Windows lays it out.
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// String renders a GUID the way Windows writes one, for a message.
func (guid GUID) String() string {
	return fmt.Sprintf("{%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
		guid.Data1, guid.Data2, guid.Data3,
		guid.Data4[0], guid.Data4[1], guid.Data4[2], guid.Data4[3],
		guid.Data4[4], guid.Data4[5], guid.Data4[6], guid.Data4[7])
}

// PWSTR is a pointer to a NUL-terminated wide string, which is what COM hands
// text back as.
type PWSTR *uint16

// BSTR is the other kind of string COM uses: a length-prefixed allocation made
// and freed with the two functions below. It is an address rather than a
// pointer because nothing here reads through one.
type BSTR uintptr

// HRESULT is what a COM method answers with. The sign bit is the whole
// convention: negative is failure, anything else success -- so a method that
// succeeds with something to say (an enumerator that ran out) must be looked
// at, not merely checked.
type HRESULT int32

// Error makes an HRESULT an error, so that a failed call reads as one.
func (result HRESULT) Error() string { return fmt.Sprintf("COM error %#08x", uint32(result)) }

// Failed reports whether a result is a failure.
func (result HRESULT) Failed() bool { return result < 0 }

// ErrIfFailed turns a result into an error, and into nothing at all when it
// succeeded.
func ErrIfFailed(result int32) error {
	if result >= 0 {
		return nil
	}
	return HRESULT(result)
}

// IUnknown is the base of every COM interface: the table of function addresses
// and the three methods every interface begins with. The table is declared far
// larger than any interface here has because Go wants a size and only the slots
// actually called are read.
type IUnknown struct {
	LpVtbl *[1024]uintptr
}

// The three slots every COM interface starts with.
const (
	slotQueryInterface = 0
	slotAddRef         = 1
	slotRelease        = 2
)

// QueryInterface asks an object for another of its interfaces.
func (self *IUnknown) QueryInterface(riid *GUID, ppvObject **IUnknown) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[slotQueryInterface],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(riid)), uintptr(unsafe.Pointer(ppvObject)))
	return ErrIfFailed(int32(result))
}

// AddRef takes a reference.
func (self *IUnknown) AddRef() uint32 {
	result, _, _ := syscall.SyscallN(self.LpVtbl[slotAddRef], uintptr(unsafe.Pointer(self)))
	return uint32(result)
}

// Release gives one back, and the object with the last of them.
func (self *IUnknown) Release() uint32 {
	result, _, _ := syscall.SyscallN(self.LpVtbl[slotRelease], uintptr(unsafe.Pointer(self)))
	return uint32(result)
}

// The identifiers this program asks for by name.
var (
	IID_IUnknown = GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	IID_IPersist = GUID{Data1: 0x0000010c, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	IID_IPropertyBag = GUID{Data1: 0x55272a00, Data2: 0x42cb, Data3: 0x11ce,
		Data4: [8]byte{0x81, 0x35, 0x00, 0xaa, 0x00, 0x4b, 0xb8, 0x51}}
)

// CLSCTX says where an object may be created. Everything here is in this
// process.
type CLSCTX uint32

const CLSCTX_INPROC_SERVER CLSCTX = 1

// COINIT is which threading model a thread joins.
type COINIT uint32

const COINIT_MULTITHREADED COINIT = 0

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	oleaut32 = syscall.NewLazyDLL("oleaut32.dll")
	propsys  = syscall.NewLazyDLL("propsys.dll")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemAlloc   = ole32.NewProc("CoTaskMemAlloc")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")

	procSysAllocString = oleaut32.NewProc("SysAllocString")
	procSysFreeString  = oleaut32.NewProc("SysFreeString")
	procVariantInit    = oleaut32.NewProc("VariantInit")
	procVariantClear   = oleaut32.NewProc("VariantClear")

	procVariantToString = propsys.NewProc("VariantToString")
)

// CoInitializeEx joins this thread to COM. It answers a success even when the
// thread was already joined, so the caller must look at the result: it says
// whether this call is the one that has to undo it.
func CoInitializeEx(dwCoInit uint32) (HRESULT, error) {
	// The first argument is reserved and must be nothing.
	result, _, _ := syscall.SyscallN(procCoInitializeEx.Addr(), 0, uintptr(dwCoInit))
	return HRESULT(result), ErrIfFailed(int32(result))
}

// CoUninitialize leaves it.
func CoUninitialize() {
	_, _, _ = syscall.SyscallN(procCoUninitialize.Addr())
}

// CoCreateInstance makes an object of a class and asks it for an interface.
func CoCreateInstance(rclsid *GUID, pUnkOuter *IUnknown, dwClsContext CLSCTX, riid *GUID, ppv **IUnknown) error {
	result, _, _ := syscall.SyscallN(procCoCreateInstance.Addr(),
		uintptr(unsafe.Pointer(rclsid)), uintptr(unsafe.Pointer(pUnkOuter)), uintptr(dwClsContext),
		uintptr(unsafe.Pointer(riid)), uintptr(unsafe.Pointer(ppv)))
	return ErrIfFailed(int32(result))
}

// CoTaskMemAlloc and CoTaskMemFree are COM's own allocator, which is what
// anything handed across a COM boundary has to come from and go back to.
func CoTaskMemAlloc(cb uintptr) unsafe.Pointer {
	address, _, _ := syscall.SyscallN(procCoTaskMemAlloc.Addr(), cb)
	if address == 0 {
		return nil
	}
	return pointerOf(address)
}

// pointerOf turns an address the system handed back into a pointer.
//
// The compiler's check against integer-to-pointer conversion exists because an
// address kept in an integer is invisible to the collector. Neither collection
// nor movement can happen here: the memory belongs to COM's allocator and only
// CoTaskMemFree frees it. The check cannot be told that, so the conversion is
// done the one way it does not see, in this one place.
func pointerOf(address uintptr) unsafe.Pointer {
	var pointer unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&pointer)) = address
	return pointer
}

func CoTaskMemFree(pv unsafe.Pointer) {
	_, _, _ = syscall.SyscallN(procCoTaskMemFree.Addr(), uintptr(pv))
}

// SysAllocString makes a length-prefixed string, and SysFreeString gives it
// back. A BSTR made here must be freed here.
func SysAllocString(value string) BSTR {
	encoded, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		// The only way this fails is a string with a NUL in it, which nothing
		// here builds. An empty string is what COM does with a null pointer.
		return 0
	}
	result, _, _ := syscall.SyscallN(procSysAllocString.Addr(), uintptr(unsafe.Pointer(encoded)))
	return BSTR(result)
}

func SysFreeString(bstrString BSTR) {
	_, _, _ = syscall.SyscallN(procSysFreeString.Addr(), uintptr(bstrString))
}

// VARIANT is COM's any: a tag and a union. Nothing here looks inside one, so
// only its size and alignment matter: sixteen bytes with a four-byte pointer
// and twenty-four with an eight-byte one, which is what these fields come to.
type VARIANT struct {
	Type uint16
	_    [3]uint16
	// The union. Its widest member is two pointers, and everything in it is
	// pointer aligned.
	_ [2]uintptr
}

// VariantInit puts a variant into the empty state it must be in before
// anything is asked to fill it.
func VariantInit(pvarg *VARIANT) {
	_, _, _ = syscall.SyscallN(procVariantInit.Addr(), uintptr(unsafe.Pointer(pvarg)))
}

// VariantClear gives back whatever a variant is holding.
func VariantClear(pvarg *VARIANT) error {
	result, _, _ := syscall.SyscallN(procVariantClear.Addr(), uintptr(unsafe.Pointer(pvarg)))
	return ErrIfFailed(int32(result))
}

// VariantToString renders a variant into a buffer the caller owns.
func VariantToString(varIn *VARIANT, pszBuf PWSTR, cchBuf uint32) error {
	result, _, _ := syscall.SyscallN(procVariantToString.Addr(),
		uintptr(unsafe.Pointer(varIn)), uintptr(unsafe.Pointer(pszBuf)), uintptr(cchBuf))
	return ErrIfFailed(int32(result))
}
