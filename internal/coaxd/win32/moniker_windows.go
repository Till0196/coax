// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// The interfaces Windows names its devices through. A moniker is a name for an
// object that has not been made yet: an enumerator hands out monikers, binding
// one makes the object, and the property bag beside it holds the device's own
// description.

// IBindCtx and IErrorLog are only ever passed as nothing here, so they are
// named rather than declared.
type (
	IBindCtx  struct{ IUnknown }
	IErrorLog struct{ IUnknown }
)

// IMoniker is a name for an object.
type IMoniker struct{ IUnknown }

// BindToObject makes the object the moniker names and asks it for an interface.
func (self *IMoniker) BindToObject(pbc *IBindCtx, pmkToLeft *IMoniker, riidResult *GUID, ppvResult **IUnknown) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[8],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pbc)), uintptr(unsafe.Pointer(pmkToLeft)),
		uintptr(unsafe.Pointer(riidResult)), uintptr(unsafe.Pointer(ppvResult)))
	return ErrIfFailed(int32(result))
}

// BindToStorage reaches what the moniker is stored in rather than what it
// names, which for a device is where its description is kept.
func (self *IMoniker) BindToStorage(pbc *IBindCtx, pmkToLeft *IMoniker, riid *GUID, ppvObj **IUnknown) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[9],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pbc)), uintptr(unsafe.Pointer(pmkToLeft)),
		uintptr(unsafe.Pointer(riid)), uintptr(unsafe.Pointer(ppvObj)))
	return ErrIfFailed(int32(result))
}

// GetDisplayName is the moniker written out, which is the string this program
// uses to name a tuner from outside.
func (self *IMoniker) GetDisplayName(pbc *IBindCtx, pmkToLeft *IMoniker, ppszDisplayName *PWSTR) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[20],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pbc)), uintptr(unsafe.Pointer(pmkToLeft)),
		uintptr(unsafe.Pointer(ppszDisplayName)))
	return ErrIfFailed(int32(result))
}

// IEnumMoniker walks a list of monikers.
type IEnumMoniker struct{ IUnknown }

// Next takes the next few. It answers a result as well as an error because
// running out is a success that has to be noticed; pceltFetched says how many
// of the slice were filled in.
func (self *IEnumMoniker) Next(rgelt []*IMoniker, pceltFetched *uint32) (HRESULT, error) {
	var first **IMoniker
	if len(rgelt) > 0 {
		first = &rgelt[0]
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(len(rgelt)),
		uintptr(unsafe.Pointer(first)), uintptr(unsafe.Pointer(pceltFetched)))
	return HRESULT(result), ErrIfFailed(int32(result))
}

// IPropertyBag is where a device keeps what it has to say about itself.
type IPropertyBag struct{ IUnknown }

// Read takes one named property.
func (self *IPropertyBag) Read(pszPropName string, pVar *VARIANT, pErrorLog *IErrorLog) error {
	name, err := syscall.UTF16PtrFromString(pszPropName)
	if err != nil {
		return err
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(name)),
		uintptr(unsafe.Pointer(pVar)), uintptr(unsafe.Pointer(pErrorLog)))
	return ErrIfFailed(int32(result))
}

// Write puts one back.
func (self *IPropertyBag) Write(pszPropName string, pVar *VARIANT) error {
	name, err := syscall.UTF16PtrFromString(pszPropName)
	if err != nil {
		return err
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[4],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(pVar)))
	return ErrIfFailed(int32(result))
}
