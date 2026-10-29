// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package vendors

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"
)

// The two ways a vendor's own items are reached on Windows. One vendor puts them
// on the tuner filter, had for a QueryInterface; the other puts them on the
// filter's input pin, and a KS pin has no handle until connected, so that one
// costs a graph the caller must build. Both end up as Properties.

// iidPinProperties is IKsPropertySet, which is what a connected pin answers.
var iidPinProperties = win32.GUID{
	Data1: 0x31efac30, Data2: 0x515c, Data3: 0x11d0,
	Data4: [8]byte{0xa9, 0xaa, 0x00, 0xaa, 0x00, 0x61, 0xbe, 0x93},
}

// iidControl is IKsControl. IKsPropertySet would do for a plain filter property,
// but only IKsControl takes a request of a caller-chosen size, and these items
// want a 32-byte request even though nothing filter-scoped reads past the first
// 24.
var iidControl = win32.GUID{
	Data1: 0x28f54685, Data2: 0x06fd, Data3: 0x11d2,
	Data4: [8]byte{0xb2, 0x7a, 0x00, 0xa0, 0xc9, 0x22, 0x31, 0x96},
}

// The KSPROPERTY flags that say what a request is for.
const (
	typeGet = 0x00000001
	typeSet = 0x00000002
)

// requestBytes is how large a request these items expect.
const requestBytes = 32

// Control is IKsControl on a filter, which is how a vendor's own items are
// reached without a graph.
type Control struct{ vtbl *[6]uintptr }

func (control *Control) Release() {
	syscall.SyscallN(control.vtbl[2], uintptr(unsafe.Pointer(control)))
}

// putProperty lays a KSPROPERTY out in bytes: the property set as a GUID, the
// item number, then the flags. The fields go one at a time rather than by
// overlaying a struct, because a GUID is not a run of little-endian words (its
// first three members are, the last eight bytes are not).
func putProperty(request []byte, set *win32.GUID, id, flags uint32) {
	binary.LittleEndian.PutUint32(request[0:], set.Data1)
	binary.LittleEndian.PutUint16(request[4:], set.Data2)
	binary.LittleEndian.PutUint16(request[6:], set.Data3)
	copy(request[8:16], set.Data4[:])
	binary.LittleEndian.PutUint32(request[16:], id)
	binary.LittleEndian.PutUint32(request[20:], flags)
}

// request sends one KSPROPERTY request and returns how much came back.
func (control *Control) request(set *win32.GUID, id, flags uint32, data []byte) (uint32, int32) {
	var request [requestBytes]byte
	putProperty(request[:], set, id, flags)

	var returned uint32
	result, _, _ := syscall.SyscallN(control.vtbl[3], uintptr(unsafe.Pointer(control)),
		uintptr(unsafe.Pointer(&request[0])), uintptr(len(request)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&returned)))
	return returned, int32(result)
}

// Exchange sends one request longer than the KSPROPERTY header: an item that
// carries fields of its own after it, as some drivers' items do. request holds
// the whole request, header included; the header is filled in here. A get comes
// back in data, which may be the same length as the request.
func (control *Control) Exchange(set *win32.GUID, id uint32, write bool, request, data []byte) (uint32, error) {
	flags := uint32(typeGet)
	if write {
		flags = typeSet
	}
	putProperty(request, set, id, flags)
	var returned uint32
	result, _, _ := syscall.SyscallN(control.vtbl[3], uintptr(unsafe.Pointer(control)),
		uintptr(unsafe.Pointer(&request[0])), uintptr(len(request)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&returned)))
	if int32(result) < 0 {
		return 0, fmt.Errorf("property %d: HRESULT 0x%08X", id, uint32(result))
	}
	return returned, nil
}

// Get and Set make a filter's control satisfy Properties.
func (control *Control) Get(set win32.GUID, id uint32, data []byte) (uint32, error) {
	returned, result := control.request(&set, id, typeGet, data)
	if result < 0 {
		return 0, fmt.Errorf("read property %d: HRESULT 0x%08X", id, uint32(result))
	}
	return returned, nil
}

func (control *Control) Set(set win32.GUID, id uint32, data []byte) error {
	if _, result := control.request(&set, id, typeSet, data); result < 0 {
		return fmt.Errorf("write property %d: HRESULT 0x%08X", id, uint32(result))
	}
	return nil
}

// Filter is the tuner filter's IKsControl. The caller owns it.
func Filter(filter *directshow.IBaseFilter) (*Control, error) {
	var unknown *win32.IUnknown
	if err := filter.QueryInterface(&iidControl, &unknown); err != nil || unknown == nil {
		return nil, fmt.Errorf("the BDA tuner filter offers no IKsControl")
	}
	return (*Control)(unsafe.Pointer(unknown)), nil
}

type PinProperties struct{ vtbl *[8]uintptr }

func (property *PinProperties) Release() {
	syscall.SyscallN(property.vtbl[2], uintptr(unsafe.Pointer(property)))
}

func (property *PinProperties) get(set *win32.GUID, id uint32, data []byte) (uint32, int32) {
	var returned uint32
	result, _, _ := syscall.SyscallN(property.vtbl[4], uintptr(unsafe.Pointer(property)),
		uintptr(unsafe.Pointer(set)), uintptr(id), 0, 0,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&returned)))
	return returned, int32(result)
}

// getWithInstance carries the request in the property's instance data, where a
// property that takes an argument reads it from.
func (property *PinProperties) getWithInstance(set *win32.GUID, id uint32, instance, data []byte) (uint32, int32) {
	var returned uint32
	result, _, _ := syscall.SyscallN(property.vtbl[4], uintptr(unsafe.Pointer(property)),
		uintptr(unsafe.Pointer(set)), uintptr(id),
		uintptr(unsafe.Pointer(&instance[0])), uintptr(len(instance)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&returned)))
	return returned, int32(result)
}

func (property *PinProperties) set(set *win32.GUID, id uint32, data []byte) int32 {
	result, _, _ := syscall.SyscallN(property.vtbl[3], uintptr(unsafe.Pointer(property)),
		uintptr(unsafe.Pointer(set)), uintptr(id),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	return int32(result)
}

// Get, Set and GetWithInstance make a pin's property set satisfy Properties.
func (property *PinProperties) Get(set win32.GUID, id uint32, data []byte) (uint32, error) {
	returned, result := property.get(&set, id, data)
	if result < 0 {
		return 0, fmt.Errorf("read property %d: HRESULT 0x%08X", id, uint32(result))
	}
	return returned, nil
}

func (property *PinProperties) Set(set win32.GUID, id uint32, data []byte) error {
	if result := property.set(&set, id, data); result < 0 {
		return fmt.Errorf("write property %d: HRESULT 0x%08X", id, uint32(result))
	}
	return nil
}

// GetWithInstance is Get with the request carried in the property's instance
// data, where a property that takes an argument reads it from.
func (property *PinProperties) GetWithInstance(set win32.GUID, id uint32, instance, data []byte) (uint32, error) {
	returned, result := property.getWithInstance(&set, id, instance, data)
	if result < 0 {
		return 0, fmt.Errorf("read property %d: HRESULT 0x%08X", id, uint32(result))
	}
	return returned, nil
}

// Pin is the property set of a connected pin. The caller owns it.
func Pin(pin *directshow.IPin) (*PinProperties, error) {
	var unknown *win32.IUnknown
	if err := pin.QueryInterface(&iidPinProperties, &unknown); err != nil || unknown == nil {
		return nil, fmt.Errorf("the tuner's input pin offers no IKsPropertySet")
	}
	return (*PinProperties)(unsafe.Pointer(unknown)), nil
}
