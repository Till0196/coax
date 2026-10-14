// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Windows reaches USB devices through WinUSB, the driver Microsoft ships for a
// device with no class driver of its own. The calls below go through the
// standard library's syscall package, and completion comes back through
// OVERLAPPED rather than a callback, so nothing ever calls from C into Go. A
// device has to be bound to WinUSB for this to work, which is what the .inf
// files under driver/windows do.

package winusb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

var (
	setupapi = syscall.NewLazyDLL("setupapi.dll")
	winusb   = syscall.NewLazyDLL("winusb.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetClassDevs      = setupapi.NewProc("SetupDiGetClassDevsW")
	procEnumInterfaces    = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procInterfaceDetail   = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procDestroyDeviceList = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
	procDeviceRegistry    = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")

	procInitialize    = winusb.NewProc("WinUsb_Initialize")
	procFree          = winusb.NewProc("WinUsb_Free")
	procControl       = winusb.NewProc("WinUsb_ControlTransfer")
	procReadPipe      = winusb.NewProc("WinUsb_ReadPipe")
	procWritePipe     = winusb.NewProc("WinUsb_WritePipe")
	procAbortPipe     = winusb.NewProc("WinUsb_AbortPipe")
	procResetPipe     = winusb.NewProc("WinUsb_ResetPipe")
	procSetAlt        = winusb.NewProc("WinUsb_SetCurrentAlternateSetting")
	procQueryPipeEx   = winusb.NewProc("WinUsb_QueryPipeEx")
	procRegisterIsoch = winusb.NewProc("WinUsb_RegisterIsochBuffer")
	procReadIsochAsap = winusb.NewProc("WinUsb_ReadIsochPipeAsap")
	procReadIsoch     = winusb.NewProc("WinUsb_ReadIsochPipe")
	procCurrentFrame  = winusb.NewProc("WinUsb_GetCurrentFrameNumber")
	procUnregisterIso = winusb.NewProc("WinUsb_UnregisterIsochBuffer")
	procOverlapped    = winusb.NewProc("WinUsb_GetOverlappedResult")
	procSetPipePolicy = winusb.NewProc("WinUsb_SetPipePolicy")
	procGetPipePolicy = winusb.NewProc("WinUsb_GetPipePolicy")

	// The plain one, taken on the file handle rather than the interface: it
	// collects a transfer that has already completed without going through
	// WinUSB, and so without waiting behind a control transfer. See the
	// isochronous ring.
	procFileOverlapped = kernel32.NewProc("GetOverlappedResult")

	procCreateEvent = kernel32.NewProc("CreateEventW")
	procResetEvent  = kernel32.NewProc("ResetEvent")
	procCancelIoEx  = kernel32.NewProc("CancelIoEx")
)

// windowsGUID is the layout every Windows interface identifier arrives in.
type windowsGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	// GUID_DEVINTERFACE_USB_DEVICE, which the USB stack registers for every device
	// whatever driver is bound to it. Enumerating this rather than the identifiers
	// the .inf files declare finds a device bound by Zadig, which invents an
	// identifier of its own, just the same.
	usbDeviceGUID = windowsGUID{0xA5DCBF10, 0x6530, 0x11D2,
		[8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED}}
	// GUID_DEVINTERFACE_USB_HOST_CONTROLLER, for numbering the buses.
	usbHostGUID = windowsGUID{0x3ABF6F2D, 0x71C4, 0x462A,
		[8]byte{0x8A, 0x92, 0x1E, 0x68, 0x61, 0xE6, 0xAF, 0x27}}
)

type spDeviceInterfaceData struct {
	Size      uint32
	ClassGUID windowsGUID
	Flags     uint32
	Reserved  uintptr
}

type spDevInfoData struct {
	Size      uint32
	ClassGUID windowsGUID
	DevInst   uint32
	Reserved  uintptr
}

// pipeInformationEx is WINUSB_PIPE_INFORMATION_EX.
type pipeInformationEx struct {
	PipeType                uint32
	PipeID                  uint8
	_                       [1]byte
	MaximumPacketSize       uint16
	Interval                uint8
	_                       [3]byte
	MaximumBytesPerInterval uint32
}

const (
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10
	spdrpService         = 0x04
	spdrpAddress         = 0x1c
	spdrpLocationPaths   = 0x23
	invalidHandle        = ^uintptr(0)

	fileFlagOverlapped = 0x40000000
	genericRead        = 0x80000000
	genericWrite       = 0x40000000
	openExisting       = 3

	waitObject0  = 0x00000000
	waitTimeout  = 0x00000102
	waitInfinite = 0xFFFFFFFF
)

// windowsError turns a Win32 failure into the sentinels the rest of this
// package is written against.
func windowsError(operation string, err error) error {
	var code syscall.Errno
	if errors.As(err, &code) {
		switch code {
		case 121, 1460: // ERROR_SEM_TIMEOUT, ERROR_TIMEOUT
			return backend.ErrTimeout
		case 2, 31, 433, 1167: // FILE_NOT_FOUND, GEN_FAILURE, NO_SUCH_DEVICE, DEVICE_NOT_CONNECTED
			return backend.ErrGone
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// Enumerate lists every USB device Windows has present, with the identity a
// caller selects on.
func Enumerate() ([]backend.Found, error) {
	hosts := hostControllers()
	devices, _, err := procGetClassDevs.Call(uintptr(unsafe.Pointer(&usbDeviceGUID)), 0, 0,
		uintptr(digcfPresent|digcfDeviceInterface))
	if devices == invalidHandle {
		return nil, fmt.Errorf("%w: enumerate USB devices (%v)", backend.ErrNoDevices, err)
	}
	defer procDestroyDeviceList.Call(devices)

	var listed []backend.Found
	for index := uintptr(0); ; index++ {
		data := spDeviceInterfaceData{Size: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}
		if ret, _, _ := procEnumInterfaces.Call(devices, 0, uintptr(unsafe.Pointer(&usbDeviceGUID)),
			index, uintptr(unsafe.Pointer(&data))); ret == 0 {
			break
		}
		path, info, ok := interfaceDetail(devices, &data)
		if !ok {
			continue
		}
		vendor, product, ok := identifiersIn(path)
		if !ok {
			continue
		}
		location := registryString(devices, info, spdrpLocationPaths)
		port := portPath(location, hosts)
		bus, address := 0, registryWord(devices, info, spdrpAddress)
		if port != "" {
			fmt.Sscanf(port, "%d-", &bus)
		}
		devicePath := path
		listed = append(listed, backend.Found{
			ID:       backend.ID{Vendor: vendor, Product: product},
			Location: fmt.Sprintf("%03d:%03d", bus, address),
			Serial:   serialIn(path),
			Port:     port,
			Held:     heldBy(registryString(devices, info, spdrpService)),
			Open:     func() (backend.Device, error) { return openWinUSB(devicePath) },
		})
	}
	return listed, nil
}

// heldBy names the driver a device is bound to when it is not WinUSB. The
// listing above takes in every device whatever its driver, and one bound to a
// vendor's BDA driver would otherwise be offered here as well as by the bda
// backend, and opening it would send WinUSB's requests to that driver. A device
// with no driver at all is not held: nothing else has it, and the open fails
// with the advice to bind it.
func heldBy(service string) string {
	if service == "" || strings.EqualFold(service, "WinUSB") {
		return ""
	}
	return service
}

// interfaceDetail reads one interface's device path and its device node.
func interfaceDetail(devices uintptr, data *spDeviceInterfaceData) (string, *spDevInfoData, bool) {
	detail := make([]byte, 2048)
	// The cbSize of SP_DEVICE_INTERFACE_DETAIL_DATA_W describes the fixed header
	// alone, not the buffer handed in: a four-byte count and the path's first
	// character, padded to 8 where a pointer is 8 bytes and to 6 where it is 4.
	// The wrong value is refused outright.
	header := uint32(8)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		header = 6
	}
	binary.LittleEndian.PutUint32(detail[:4], header)
	info := &spDevInfoData{Size: uint32(unsafe.Sizeof(spDevInfoData{}))}
	var required uint32
	if ret, _, _ := procInterfaceDetail.Call(devices, uintptr(unsafe.Pointer(data)),
		uintptr(unsafe.Pointer(&detail[0])), uintptr(len(detail)),
		uintptr(unsafe.Pointer(&required)), uintptr(unsafe.Pointer(info))); ret == 0 {
		return "", nil, false
	}
	// The path follows the header as UTF-16, read out of the bytes rather than by
	// pointing a fixed-size uint16 array at them.
	return utf16String(detail[4:]), info, true
}

// utf16String reads a NUL-terminated UTF-16 string out of bytes.
func utf16String(data []byte) string {
	units := make([]uint16, 0, len(data)/2)
	for offset := 0; offset+1 < len(data); offset += 2 {
		unit := binary.LittleEndian.Uint16(data[offset:])
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return string(utf16.Decode(units))
}

func registryString(devices uintptr, info *spDevInfoData, property uint32) string {
	buffer := make([]uint16, 1024)
	var kind, size uint32
	if ret, _, _ := procDeviceRegistry.Call(devices, uintptr(unsafe.Pointer(info)), uintptr(property),
		uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)*2), uintptr(unsafe.Pointer(&size))); ret == 0 {
		return ""
	}
	// A multi-string property arrives as consecutive strings; the first is wanted,
	// and UTF16ToString stops at its terminator.
	return syscall.UTF16ToString(buffer)
}

func registryWord(devices uintptr, info *spDevInfoData, property uint32) int {
	var value uint32
	var kind, size uint32
	if ret, _, _ := procDeviceRegistry.Call(devices, uintptr(unsafe.Pointer(info)), uintptr(property),
		uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&value)),
		unsafe.Sizeof(value), uintptr(unsafe.Pointer(&size))); ret == 0 {
		return 0
	}
	return int(value)
}

// hostControllers lists where every USB host controller sits. Windows has no bus
// numbering to borrow, so one is made by sorting the controllers on physical
// location and numbering from one; that does not depend on Windows' enumeration
// order, so a name built on it survives a reboot.
func hostControllers() []string {
	devices, _, _ := procGetClassDevs.Call(uintptr(unsafe.Pointer(&usbHostGUID)), 0, 0,
		uintptr(digcfPresent|digcfDeviceInterface))
	if devices == invalidHandle {
		return nil
	}
	defer procDestroyDeviceList.Call(devices)
	var paths []string
	for index := uintptr(0); ; index++ {
		data := spDeviceInterfaceData{Size: uint32(unsafe.Sizeof(spDeviceInterfaceData{}))}
		if ret, _, _ := procEnumInterfaces.Call(devices, 0, uintptr(unsafe.Pointer(&usbHostGUID)),
			index, uintptr(unsafe.Pointer(&data))); ret == 0 {
			break
		}
		_, info, ok := interfaceDetail(devices, &data)
		if !ok {
			continue
		}
		if location := registryString(devices, info, spdrpLocationPaths); location != "" {
			paths = append(paths, location)
		}
	}
	sort.Strings(paths)
	return paths
}

// portPath turns a device's location into the "bus-port.port" shape this package
// names devices with, which is the shape Linux uses too.
func portPath(location string, hosts []string) string {
	at := strings.Index(location, "#USBROOT(")
	if at < 0 {
		return ""
	}
	bus := 0
	for index, host := range hosts {
		if strings.HasPrefix(location, host+"#") {
			bus = index + 1
			break
		}
	}
	if bus == 0 {
		return ""
	}
	var ports []string
	for _, part := range strings.Split(location[at:], "#") {
		if strings.HasPrefix(part, "USB(") && strings.HasSuffix(part, ")") {
			ports = append(ports, part[4:len(part)-1])
		}
	}
	if len(ports) == 0 {
		return ""
	}
	return fmt.Sprintf("%d-%s", bus, strings.Join(ports, "."))
}

// serialIn pulls the serial number out of a device interface path. Windows
// names a device instance after the serial its descriptor carries -- the "0" in
// \\?\usb#vid_0511&pid_0045#0#{a5dcbf10-...} -- so it is known without opening
// the device. A device with no serial gets an instance name Windows made up
// from where it is plugged in, and those always have an ampersand in them.
func serialIn(path string) string {
	parts := strings.Split(path, "#")
	if len(parts) < 4 || parts[2] == "" || strings.Contains(parts[2], "&") {
		return ""
	}
	return parts[2]
}

// identifiersIn pulls the vendor and product out of a device interface path,
// which carries them: \\?\usb#vid_0511&pid_0045#0#{a5dcbf10-...}
func identifiersIn(path string) (uint16, uint16, bool) {
	lower := strings.ToLower(path)
	var vendor, product uint16
	for _, part := range []struct {
		prefix string
		into   *uint16
	}{{"vid_", &vendor}, {"pid_", &product}} {
		at := strings.Index(lower, part.prefix)
		if at < 0 || at+len(part.prefix)+4 > len(lower) {
			return 0, 0, false
		}
		var value uint16
		if _, err := fmt.Sscanf(lower[at+len(part.prefix):at+len(part.prefix)+4], "%04x", &value); err != nil {
			return 0, 0, false
		}
		*part.into = value
	}
	return vendor, product, true
}
