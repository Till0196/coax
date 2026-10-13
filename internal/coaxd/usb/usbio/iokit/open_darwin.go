// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Opening a device through IOKit, and the transfers that follow.
//
// A device is reached through a plug-in: a pointer to a table of function
// pointers whose first argument is the table itself. The tables are versioned
// and additive, so the slot numbers below hold from the version they were
// introduced in onwards.

package iokit

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// The plug-in types and interface versions, as their bytes. Passed as they are
// rather than through CFUUIDGetConstantUUIDWithBytes, which takes the sixteen
// bytes as sixteen arguments and does not fit through the calling bridge here.
var (
	uuidDeviceUserClient    = [16]byte{0x9d, 0xc7, 0xb7, 0x80, 0x9e, 0xc0, 0x11, 0xd4, 0xa5, 0x4f, 0x00, 0x0a, 0x27, 0x05, 0x28, 0x61}
	uuidInterfaceUserClient = [16]byte{0x2d, 0x97, 0x86, 0xc6, 0x9e, 0xf3, 0x11, 0xd4, 0xad, 0x51, 0x00, 0x0a, 0x27, 0x05, 0x28, 0x61}
	uuidPlugInInterface     = [16]byte{0xc2, 0x44, 0xe8, 0x58, 0x10, 0x9c, 0x11, 0xd4, 0x91, 0xd4, 0x00, 0x50, 0xe4, 0xc6, 0x42, 0x6f}
	uuidDeviceInterface942  = [16]byte{0x56, 0xad, 0x08, 0x9d, 0x87, 0x8d, 0x4b, 0xea, 0xa1, 0xf5, 0x2c, 0x8d, 0xc4, 0x3e, 0x8a, 0x98}
	uuidIfaceInterface942   = [16]byte{0x87, 0x52, 0x66, 0x3b, 0xc0, 0x7b, 0x4b, 0xae, 0x95, 0x84, 0x22, 0x03, 0x2f, 0xab, 0x9c, 0x5a}
)

// The IOKit results this file names, from IOKit/IOReturn.h and IOKit/usb/USB.h:
//
//	kIOReturnNoDevice           iokit_common_err(0x2c0)
//	kIOReturnExclusiveAccess    iokit_common_err(0x2c5)
//	kIOReturnUnsupported        iokit_common_err(0x2c7)
//	kIOReturnTimeout            iokit_common_err(0x2d6), a transfer that expired
//	kIOReturnNotAttached        iokit_common_err(0x2d9)
//	kIOReturnAborted            iokit_common_err(0x2eb), a call cut short
//	kIOUSBTransactionTimeout    iokit_usb_err(0x51), a timeout from the USB stack
//
// Check these against the headers, not off a nearby constant: the common ones are not in
// the order their names suggest.
const (
	kIOReturnNoDevice        = 0xe00002c0
	kIOReturnExclusiveAccess = 0xe00002c5
	kIOReturnUnsupported     = 0xe00002c7
	kIOReturnTimeout         = 0xe00002d6
	kIOReturnNotAttached     = 0xe00002d9
	// kIOReturnAborted is what an IOKit call reports when a signal cut it short.
	// Nothing here produces one any more (see callUninterrupted); it is named so
	// an unforeseen one is recognisable.
	kIOReturnAborted         = 0xe00002eb
	kIOUSBTransactionTimeout = 0xe0004051
)

// The slots this uses, counted from the published interface definitions. Every
// table starts with the four every plug-in has -- a reserved word, then
// QueryInterface, AddRef, Release -- and grows from there.
const (
	slotQueryInterface = 1
	slotRelease        = 3

	slotDeviceOpen              = 8
	slotDeviceClose             = 9
	slotDeviceSetConfiguration  = 23
	slotDeviceCreateIfaceIter   = 28
	slotDeviceRequestTO         = 30
	slotDeviceGetConfiguration  = 22
	slotDeviceGetNumberOfConfig = 19

	slotInterfaceOpen        = 8
	slotInterfaceClose       = 9
	slotInterfaceNumEndpoint = 19
	slotInterfaceSetAlt      = 22
	slotInterfacePipeProps   = 26
	slotInterfaceClearStall  = 30
	slotInterfaceBusFrame    = 23
	slotInterfaceReadIsoch   = 35
	slotInterfaceFrameTime   = 54
	slotInterfaceAbortPipe   = 28
	slotInterfaceReadTO      = 39
	slotInterfaceWriteTO     = 40
)

// kIOUSBFindInterfaceDontCare, in every field of the interface search.
const findInterfaceDontCare = 0xffff

// vtable calls a method on a plug-in interface: the value handed out points at a
// pointer to the table, so the method is two dereferences away and the object
// itself is the first argument.
func vtable(object uintptr, slot int, args ...uintptr) uintptr {
	table := *(*uintptr)(foreign(object))
	method := *(*uintptr)(foreign(table + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	return call(method, append([]uintptr{object}, args...)...)
}

// halves splits a UUID into the two words a sixteen-byte argument is passed in.
func halves(uuid [16]byte) (uintptr, uintptr) {
	var low, high uint64
	for index := 0; index < 8; index++ {
		low |= uint64(uuid[index]) << (8 * index)
		high |= uint64(uuid[8+index]) << (8 * index)
	}
	return uintptr(low), uintptr(high)
}

// ioKitOpen holds what opening a device needs beyond the enumeration entries.
type ioKitOpen struct {
	createPlugIn  uintptr
	destroyPlugIn uintptr
	uuidCreate    uintptr
}

// openPlugIn resolves the calls that turn a registry entry into an interface,
// which enumeration does not need.
func (api *ioKit) openPlugIn() (*ioKitOpen, error) {
	iokit := dlopen(ioKitFramework)
	corefoundation := dlopen(coreFoundationLibrary)
	if iokit == 0 || corefoundation == 0 {
		return nil, errors.New("the system frameworks could not be opened")
	}
	open := &ioKitOpen{
		createPlugIn:  dlsym(iokit, "IOCreatePlugInInterfaceForService"),
		destroyPlugIn: dlsym(iokit, "IODestroyPlugInInterface"),
		uuidCreate:    dlsym(corefoundation, "CFUUIDCreateFromUUIDBytes"),
	}
	if open.createPlugIn == 0 || open.destroyPlugIn == 0 || open.uuidCreate == 0 {
		return nil, errors.New("IOKit is missing the plug-in calls this needs")
	}
	return open, nil
}

// plugInFor asks the kernel for a service's plug-in and queries the wanted
// interface out of it.
func (api *ioKit) plugInFor(open *ioKitOpen, service uintptr, clientType, wanted [16]byte) (uintptr, error) {
	typeLow, typeHigh := halves(clientType)
	plugInLow, plugInHigh := halves(uuidPlugInInterface)
	clientUUID := call(open.uuidCreate, 0, typeLow, typeHigh)
	plugInUUID := call(open.uuidCreate, 0, plugInLow, plugInHigh)
	defer call(api.release, clientUUID)
	defer call(api.release, plugInUUID)

	var plugin uintptr
	var score int32
	pinned := pin(&plugin, &score)
	result := call(open.createPlugIn, service, clientUUID, plugInUUID,
		uintptr(unsafe.Pointer(&plugin)), uintptr(unsafe.Pointer(&score)))
	pinned.Unpin()
	if result != 0 || plugin == 0 {
		return 0, fmt.Errorf("no plug-in for this device: IOCreatePlugInInterfaceForService returned %#08x", uint32(result))
	}
	// Destroyed rather than released: a plug-in is started when created and
	// releasing the last reference does not stop it, leaving a started plug-in
	// behind on every open. On a US-3POUT that made control transfers start timing
	// out on about the seventh open.
	defer call(open.destroyPlugIn, plugin)

	wantedLow, wantedHigh := halves(wanted)
	var wantedInterface uintptr
	pinned = pin(&wantedInterface)
	result = vtable(plugin, slotQueryInterface, wantedLow, wantedHigh, uintptr(unsafe.Pointer(&wantedInterface)))
	pinned.Unpin()
	if result != 0 || wantedInterface == 0 {
		return 0, fmt.Errorf("this plug-in does not offer the interface asked for (%#08x)", uint32(result))
	}
	return wantedInterface, nil
}

// iokitDevice is one opened device: the device interface, the interface
// interface for interface zero, and which pipe each endpoint is.
type iokitDevice struct {
	api    *ioKit
	device uintptr
	iface  uintptr
	// pipes maps an endpoint address to the pipe index the interface knows it by.
	// IOKit numbers pipes within an interface from one, zero being the control
	// endpoint, and says nothing about addresses until asked.
	pipes map[byte]uint8
}

func openIOKit(api *ioKit, service uintptr) (backend.Device, error) {
	open, err := api.openPlugIn()
	if err != nil {
		return nil, err
	}
	deviceInterface, err := api.plugInFor(open, service, uuidDeviceUserClient, uuidDeviceInterface942)
	if err != nil {
		return nil, err
	}
	opened := &iokitDevice{api: api, device: deviceInterface, pipes: map[byte]uint8{}}

	if result := vtable(deviceInterface, slotDeviceOpen); result != 0 {
		vtable(deviceInterface, slotRelease)
		return nil, ioKitError("open the device", uint32(result))
	}

	// A device that has not been configured has no interfaces to find. One that
	// has is left alone: re-setting the configuration would reset every interface
	// on it, including one another program is using.
	var configuration uint8
	pinned := pin(&configuration)
	result := vtable(deviceInterface, slotDeviceGetConfiguration, uintptr(unsafe.Pointer(&configuration)))
	pinned.Unpin()
	if result == 0 && configuration == 0 {
		if result := vtable(deviceInterface, slotDeviceSetConfiguration, 1); result != 0 {
			opened.Close()
			return nil, ioKitError("configure the device", uint32(result))
		}
	}

	if err := opened.claimFirstInterface(open); err != nil {
		opened.Close()
		return nil, err
	}
	return opened, nil
}

// openControlOnlyIOKit opens a device without claiming an interface: the
// default control pipe belongs to the device, not to any interface. That
// matters for a device too stuck to be configured, and for anything that would
// rather not take an interface away. What comes back has no endpoints.
func openControlOnlyIOKit(api *ioKit, service uintptr) (backend.Device, error) {
	open, err := api.openPlugIn()
	if err != nil {
		return nil, err
	}
	deviceInterface, err := api.plugInFor(open, service, uuidDeviceUserClient, uuidDeviceInterface942)
	if err != nil {
		return nil, err
	}
	opened := &iokitDevice{api: api, device: deviceInterface, pipes: map[byte]uint8{}}
	if result := vtable(deviceInterface, slotDeviceOpen); result != 0 {
		vtable(deviceInterface, slotRelease)
		return nil, ioKitError("open the device", uint32(result))
	}
	return opened, nil
}

// claimFirstInterface opens interface zero and learns its pipes.
func (opened *iokitDevice) claimFirstInterface(open *ioKitOpen) error {
	// The search is for anything: these boards have one interface, the wanted one.
	request := [4]uint16{findInterfaceDontCare, findInterfaceDontCare, findInterfaceDontCare, findInterfaceDontCare}
	var iterator uintptr
	pinned := pin(&request, &iterator)
	result := vtable(opened.device, slotDeviceCreateIfaceIter,
		uintptr(unsafe.Pointer(&request[0])), uintptr(unsafe.Pointer(&iterator)))
	pinned.Unpin()
	if result != 0 {
		return ioKitError("list the device's interfaces", uint32(result))
	}
	defer call(opened.api.objectRelease, iterator)

	service := call(opened.api.iteratorNext, iterator)
	if service == 0 {
		return errors.New("this device has no interface to claim")
	}
	defer call(opened.api.objectRelease, service)

	interfaceInterface, err := opened.api.plugInFor(open, service, uuidInterfaceUserClient, uuidIfaceInterface942)
	if err != nil {
		return err
	}
	if result := vtable(interfaceInterface, slotInterfaceOpen); result != 0 {
		vtable(interfaceInterface, slotRelease)
		return ioKitError("claim interface 0", uint32(result))
	}
	opened.iface = interfaceInterface
	return opened.readPipes()
}

// readPipes asks the interface what each of its pipes is, so a caller naming an
// endpoint address can be answered.
func (opened *iokitDevice) readPipes() error {
	var count uint8
	pinned := pin(&count)
	result := vtable(opened.iface, slotInterfaceNumEndpoint, uintptr(unsafe.Pointer(&count)))
	pinned.Unpin()
	if result != 0 {
		return ioKitError("count the interface's endpoints", uint32(result))
	}
	for pipe := uint8(1); pipe <= count; pipe++ {
		var direction, number, transferType, interval uint8
		var maxPacketSize uint16
		pinned := pin(&direction, &number, &transferType, &maxPacketSize, &interval)
		result := vtable(opened.iface, slotInterfacePipeProps, uintptr(pipe),
			uintptr(unsafe.Pointer(&direction)), uintptr(unsafe.Pointer(&number)),
			uintptr(unsafe.Pointer(&transferType)), uintptr(unsafe.Pointer(&maxPacketSize)),
			uintptr(unsafe.Pointer(&interval)))
		pinned.Unpin()
		if result != 0 {
			continue
		}
		// kUSBIn is 1, and an endpoint address carries the direction in its top
		// bit, which is how every caller here names one.
		address := number
		if direction == 1 {
			address |= 0x80
		}
		opened.pipes[address] = pipe
	}
	return nil
}

func (opened *iokitDevice) pipeFor(endpoint byte) (uint8, error) {
	if pipe, ok := opened.pipes[endpoint]; ok {
		return pipe, nil
	}
	if opened.iface == 0 {
		return 0, errors.New("this device was opened for control transfers only, and has no endpoints")
	}
	return 0, fmt.Errorf("this interface has no endpoint %#02x", endpoint)
}

// deviceRequest is IOUSBDevRequestTO, laid out as the framework declares it.
type deviceRequest struct {
	requestType       uint8
	request           uint8
	value             uint16
	index             uint16
	length            uint16
	data              unsafe.Pointer
	lengthDone        uint32
	noDataTimeout     uint32
	completionTimeout uint32
	_                 uint32
}

// Control runs a transfer on the default control pipe.
func (opened *iokitDevice) Control(requestType, request byte, value, index uint16, buffer []byte, timeoutMS uint32) (int, error) {
	// The kernel may hold the buffer across the call, so it must be one the
	// garbage collector does not move.
	var staging unsafe.Pointer
	if len(buffer) > 0 {
		staging = allocate(len(buffer))
		if staging == nil {
			return 0, errors.New("out of memory for a control transfer")
		}
		defer release(staging)
		if requestType&0x80 == 0 {
			intoForeign(staging, buffer)
		}
	}
	message := deviceRequest{
		requestType:       requestType,
		request:           request,
		value:             value,
		index:             index,
		length:            uint16(len(buffer)),
		data:              staging,
		noDataTimeout:     timeoutMS,
		completionTimeout: timeoutMS,
	}
	pinned := pin(&message)
	result := vtableUninterrupted(opened.device, slotDeviceRequestTO, uintptr(unsafe.Pointer(&message)))
	pinned.Unpin()
	if result != 0 {
		return 0, ioKitError("control transfer", uint32(result))
	}
	done := int(message.lengthDone)
	if done > len(buffer) {
		done = len(buffer)
	}
	if staging != nil && requestType&0x80 != 0 {
		outOfForeign(buffer, staging, done)
	}
	return done, nil
}

// Bulk runs a transfer on a bulk endpoint, whose direction the address decides.
func (opened *iokitDevice) Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error) {
	pipe, err := opened.pipeFor(endpoint)
	if err != nil {
		return 0, err
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	staging := allocate(len(buffer))
	if staging == nil {
		return 0, errors.New("out of memory for a bulk transfer")
	}
	defer release(staging)

	size := uint32(len(buffer))
	defer pin(&size).Unpin()
	// The two calls disagree about the size argument: ReadPipeTO takes a pointer
	// to it, reporting through the same word how much arrived, while WritePipeTO
	// takes the count itself. Passing a pointer to both makes a write ask for
	// however many bytes the address happens to be, which the kernel answers with
	// kIOReturnCannotWire.
	var result uintptr
	if endpoint&0x80 != 0 {
		result = vtableUninterrupted(opened.iface, slotInterfaceReadTO, uintptr(pipe), uintptr(staging),
			uintptr(unsafe.Pointer(&size)), uintptr(timeoutMS), uintptr(timeoutMS))
	} else {
		intoForeign(staging, buffer)
		result = vtableUninterrupted(opened.iface, slotInterfaceWriteTO, uintptr(pipe), uintptr(staging),
			uintptr(size), uintptr(timeoutMS), uintptr(timeoutMS))
	}
	if result != 0 {
		return 0, ioKitError("bulk transfer", uint32(result))
	}
	done := int(size)
	if done > len(buffer) {
		done = len(buffer)
	}
	if endpoint&0x80 != 0 {
		outOfForeign(buffer, staging, done)
	}
	return done, nil
}

// Isochronous reads one transfer's worth: a ring of depth one opened and closed
// around the read, IOKit having no synchronous isochronous call.
func (opened *iokitDevice) Isochronous(endpoint byte, buffer []byte, packetSize, packets int, timeoutMS uint32) (int, error) {
	stream, err := opened.OpenIsoStream(endpoint, packetSize, packets, 1, timeoutMS)
	if err != nil {
		return 0, err
	}
	defer stream.Close()
	return stream.Read(buffer, timeoutMS)
}

func (opened *iokitDevice) OpenIsoStream(endpoint byte, packetSize, packets, depth int, timeoutMS uint32) (backend.IsoStream, error) {
	return opened.startIsoRing(endpoint, packetSize, packets, depth)
}

func (opened *iokitDevice) SetAlternate(interfaceNumber, alternate int) error {
	if opened.iface == 0 {
		return errors.New("this device was opened for control transfers only, and has no interface to switch")
	}
	if result := vtableUninterrupted(opened.iface, slotInterfaceSetAlt, uintptr(alternate)); result != 0 {
		return ioKitError("select the alternate setting", uint32(result))
	}
	return opened.readPipes()
}

func (opened *iokitDevice) Close() error {
	if opened.iface != 0 {
		vtable(opened.iface, slotInterfaceClose)
		vtable(opened.iface, slotRelease)
		opened.iface = 0
	}
	if opened.device != 0 {
		vtable(opened.device, slotDeviceClose)
		vtable(opened.device, slotRelease)
		opened.device = 0
	}
	return nil
}

// ioKitError names a kernel return code, translating the ones callers act on
// into the errors this package already has.
func ioKitError(operation string, code uint32) error {
	switch code {
	case kIOReturnExclusiveAccess:
		return fmt.Errorf("%s: something else already has this device", operation)
	case kIOReturnNoDevice, kIOReturnNotAttached:
		return fmt.Errorf("%s: %w", operation, backend.ErrGone)
	case kIOReturnUnsupported:
		return fmt.Errorf("%s: %w", operation, backend.ErrUnsupported)
	case kIOReturnAborted:
		return fmt.Errorf("%s: the call was cut short before the device answered", operation)
	case kIOReturnTimeout, kIOUSBTransactionTimeout:
		// A stream read that came back empty says this, and so does a device that
		// has stopped answering; both are "nothing arrived in time" to a caller
		// reading an endpoint.
		return fmt.Errorf("%s: %w", operation, backend.ErrTimeout)
	}
	return fmt.Errorf("%s: IOKit returned %#08x", operation, code)
}
