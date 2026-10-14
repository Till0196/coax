// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Reaching USB devices through IOKit, which is the framework libusb itself uses
// on this system. Going direct avoids requiring libusb to be installed, and
// nothing else changes.
//
// The API has been still for a long time: the SDK's IOUSBLib.h was last touched
// in 2018, its newest interface version adds no function to the one before it,
// and one function in the whole header is deprecated. Apple's newer IOUSBHost
// is an Objective-C wrapper over the same kernel service.
//
// The mechanics are the same trick as the Windows side: a plug-in interface is
// a pointer to a table of function pointers whose first argument is itself.

package iokit

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// The frameworks, opened by path because that is where the system keeps them.
const (
	ioKitFramework        = "/System/Library/Frameworks/IOKit.framework/IOKit"
	coreFoundationLibrary = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
)

// ioKit is the set of functions this backend resolves.
type ioKit struct {
	serviceMatching        uintptr
	getMatchingServices    uintptr
	iteratorNext           uintptr
	objectRelease          uintptr
	registryCreateProperty uintptr

	stringCreate  uintptr
	stringGetCStr uintptr
	numberGetVal  uintptr
	getTypeID     uintptr
	numberTypeID  uintptr
	stringTypeID  uintptr
	release       uintptr

	// keys are the property names, made once because every device is asked for the
	// same ones and a CFString has to be created to ask. They are never released,
	// which is why the whole of this is loaded once for the process.
	keysMutex sync.Mutex
	keys      map[string]uintptr
}

// loadIOKit finds the calls this package makes, once for the process: a listing
// runs on every request, and loading afresh each time would make the property
// names again each time and leave the last lot behind.
func loadIOKit() (*ioKit, error) {
	loadOnce.Do(func() { loaded, loadErr = loadIOKitOnce() })
	return loaded, loadErr
}

var (
	loadOnce sync.Once
	loaded   *ioKit
	loadErr  error
)

func loadIOKitOnce() (*ioKit, error) {
	iokit := dlopen(ioKitFramework)
	if iokit == 0 {
		return nil, fmt.Errorf("IOKit could not be opened at %s", ioKitFramework)
	}
	corefoundation := dlopen(coreFoundationLibrary)
	if corefoundation == 0 {
		return nil, fmt.Errorf("CoreFoundation could not be opened at %s", coreFoundationLibrary)
	}

	api := &ioKit{keys: map[string]uintptr{}}
	for _, entry := range []struct {
		into   *uintptr
		handle uintptr
		symbol string
	}{
		{&api.serviceMatching, iokit, "IOServiceMatching"},
		{&api.getMatchingServices, iokit, "IOServiceGetMatchingServices"},
		{&api.iteratorNext, iokit, "IOIteratorNext"},
		{&api.objectRelease, iokit, "IOObjectRelease"},
		{&api.registryCreateProperty, iokit, "IORegistryEntryCreateCFProperty"},
		{&api.stringCreate, corefoundation, "CFStringCreateWithCString"},
		{&api.stringGetCStr, corefoundation, "CFStringGetCString"},
		{&api.numberGetVal, corefoundation, "CFNumberGetValue"},
		{&api.getTypeID, corefoundation, "CFGetTypeID"},
		{&api.numberTypeID, corefoundation, "CFNumberGetTypeID"},
		{&api.stringTypeID, corefoundation, "CFStringGetTypeID"},
		{&api.release, corefoundation, "CFRelease"},
	} {
		address := dlsym(entry.handle, entry.symbol)
		if address == 0 {
			return nil, fmt.Errorf("%s is missing from the system frameworks", entry.symbol)
		}
		*entry.into = address
	}
	return api, nil
}

// The CoreFoundation constants this needs: UTF-8, and the number types the USB
// properties are stored as.
const (
	cfStringEncodingUTF8 = 0x08000100
	cfNumberSInt32Type   = 3
	cfNumberSInt64Type   = 4
)

// deviceClasses are the names the USB device service goes by, newest first:
// Catalina renamed IOUSBDevice to IOUSBHostDevice when the host stack was
// rewritten, and the old name still matches on every system since.
var deviceClasses = []string{"IOUSBHostDevice", "IOUSBDevice"}

// key makes -- and keeps -- the CFString for a property name.
func (api *ioKit) key(name string) uintptr {
	api.keysMutex.Lock()
	defer api.keysMutex.Unlock()
	if made, ok := api.keys[name]; ok {
		return made
	}
	bytes := append([]byte(name), 0)
	pinned := pin(&bytes[0])
	made := call(api.stringCreate, 0, uintptr(unsafe.Pointer(&bytes[0])), cfStringEncodingUTF8)
	pinned.Unpin()
	api.keys[name] = made
	return made
}

// number reads an integer property, and says whether the device had one.
func (api *ioKit) number(service uintptr, name string) (uint64, bool) {
	property := call(api.registryCreateProperty, service, api.key(name), 0, 0)
	if property == 0 {
		return 0, false
	}
	defer call(api.release, property)
	if call(api.getTypeID, property) != call(api.numberTypeID) {
		return 0, false
	}
	var value int64
	defer pin(&value).Unpin()
	if call(api.numberGetVal, property, cfNumberSInt64Type, uintptr(unsafe.Pointer(&value))) == 0 {
		return 0, false
	}
	return uint64(value), true
}

func (api *ioKit) text(service uintptr, name string) string {
	property := call(api.registryCreateProperty, service, api.key(name), 0, 0)
	if property == 0 {
		return ""
	}
	defer call(api.release, property)
	if call(api.getTypeID, property) != call(api.stringTypeID) {
		return ""
	}
	buffer := make([]byte, 256)
	defer pin(&buffer[0]).Unpin()
	if call(api.stringGetCStr, property, uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)), cfStringEncodingUTF8) == 0 {
		return ""
	}
	for index, character := range buffer {
		if character == 0 {
			return string(buffer[:index])
		}
	}
	return string(buffer)
}

// Enumerate lists the USB devices the registry knows about. Nothing is opened:
// the kernel published all of it when the device was enumerated, so this never
// disturbs a device somebody else is using.
func Enumerate() ([]backend.Found, error) {
	api, err := loadIOKit()
	if err != nil {
		return nil, err
	}

	var devices []backend.Found
	for _, class := range deviceClasses {
		bytes := append([]byte(class), 0)
		pinned := pin(&bytes[0])
		matching := call(api.serviceMatching, uintptr(unsafe.Pointer(&bytes[0])))
		pinned.Unpin()
		if matching == 0 {
			continue
		}
		var iterator uintptr
		// The main port is zero, which is what kIOMainPortDefault has always
		// been, and kIOMasterPortDefault before the macOS 12 rename.
		pinned = pin(&iterator)
		result := call(api.getMatchingServices, 0, matching, uintptr(unsafe.Pointer(&iterator)))
		pinned.Unpin()
		if result != 0 {
			return nil, fmt.Errorf("list USB devices: IOServiceGetMatchingServices returned %#08x", uint32(result))
		}
		for {
			service := call(api.iteratorNext, iterator)
			if service == 0 {
				break
			}
			found, ok := api.describe(service)
			if !ok {
				call(api.objectRelease, service)
				continue
			}
			devices = append(devices, found)
		}
		call(api.objectRelease, iterator)
		if len(devices) > 0 {
			break
		}
	}
	return devices, nil
}

// describe reads what a listing needs out of one device's registry entry.
func (api *ioKit) describe(service uintptr) (backend.Found, bool) {
	vendor, hasVendor := api.number(service, "idVendor")
	product, hasProduct := api.number(service, "idProduct")
	if !hasVendor || !hasProduct {
		return backend.Found{}, false
	}
	location, _ := api.number(service, "locationID")
	address, _ := api.number(service, "USB Address")

	// The registry entry is kept because opening the device needs it; the caller
	// hands every candidate back through Release.
	return backend.Found{
		ID:              backend.ID{Vendor: uint16(vendor), Product: uint16(product)},
		Location:        fmt.Sprintf("%03d:%03d", uint32(location)>>24, address),
		Serial:          api.text(service, "USB Serial Number"),
		Port:            locationPortPath(uint32(location)),
		Open:            func() (backend.Device, error) { return openIOKit(api, service) },
		OpenControlOnly: func() (backend.Device, error) { return openControlOnlyIOKit(api, service) },
		Reset:           func() error { return resetIOKit(api, service) },
		Release:         func() { call(api.objectRelease, service) },
	}, true
}

// locationPortPath renders a location ID as a port path, "20-1.4": the kernel
// packs the controller into the top byte and one port into each nibble below
// it, most significant first, with unused nibbles left as zero.
func locationPortPath(location uint32) string {
	if location == 0 {
		return ""
	}
	path := fmt.Sprintf("%d", location>>24)
	for shift := 20; shift >= 0; shift -= 4 {
		port := (location >> uint(shift)) & 0xf
		if port == 0 {
			break
		}
		if shift == 20 {
			path += fmt.Sprintf("-%d", port)
		} else {
			path += fmt.Sprintf(".%d", port)
		}
	}
	return path
}
