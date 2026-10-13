// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// The card's own driver, reached directly rather than through BDA. A Digital
// Devices card is a bridge FPGA with a 64 KiB register window, and the vendor's
// base driver hands that window to user mode as it stands: four control codes,
// no interpretation, no state of its own. Unlike the TBS cards, the BDA graph
// here is needed only to carry the transport stream out.

// ddInterfaceClass is the device interface class the base driver publishes.
var ddInterfaceClass = guid{
	Data1: 0x543b1b1f, Data2: 0x1f93, Data3: 0x4b47,
	Data4: [8]byte{0x80, 0x21, 0x8e, 0x70, 0xb8, 0xed, 0x90, 0x03},
}

// The base driver's control codes. Device type 0x4444 is "DD"; all four are
// METHOD_BUFFERED and ask only for read access. The offset each takes is a byte
// offset into the register window and is rejected at or above 0x10000, its
// whole size; nothing else is validated.
const (
	ioctlReadBlock  = 0x44447c00 // in: offset; out: that many bytes
	ioctlWriteBlock = 0x44447c04 // in: offset then the bytes; out: nothing
	ioctlReadReg    = 0x44447c08 // in: offset; out: one 32-bit word
	ioctlWriteReg   = 0x44447c0c // in: offset and value; out: nothing
)

// registerWindow is how far the base driver will let an offset reach.
const registerWindow = 0x10000

// bridge is one card's register window, open.
type bridge struct {
	path   string
	handle syscall.Handle
}

func openBridge(path string) (*bridge, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("open the Digital Devices bridge %q: %w", path, err)
	}
	return &bridge{path: path, handle: handle}, nil
}

func (device *bridge) close() error {
	if device.handle == 0 || device.handle == syscall.InvalidHandle {
		return nil
	}
	err := syscall.CloseHandle(device.handle)
	device.handle = syscall.InvalidHandle
	return err
}

func (device *bridge) control(code uint32, in, out []byte) error {
	var inPtr, outPtr *byte
	if len(in) != 0 {
		inPtr = &in[0]
	}
	if len(out) != 0 {
		outPtr = &out[0]
	}
	var returned uint32
	return syscall.DeviceIoControl(device.handle, code,
		inPtr, uint32(len(in)), outPtr, uint32(len(out)), &returned, nil)
}

func (device *bridge) readRegister(offset uint32) (uint32, error) {
	if offset >= registerWindow {
		return 0, fmt.Errorf("register %#x is outside the card's window", offset)
	}
	in := encodeUint32(offset)
	out := make([]byte, 4)
	if err := device.control(ioctlReadReg, in, out); err != nil {
		return 0, fmt.Errorf("read register %#x: %w", offset, err)
	}
	return decodeUint32(out), nil
}

func (device *bridge) writeRegister(offset, value uint32) error {
	if offset >= registerWindow {
		return fmt.Errorf("register %#x is outside the card's window", offset)
	}
	in := append(encodeUint32(offset), encodeUint32(value)...)
	if err := device.control(ioctlWriteReg, in, nil); err != nil {
		return fmt.Errorf("write register %#x: %w", offset, err)
	}
	return nil
}

// readBytes reads a run of registers in one call: a mailbox command would
// otherwise take thirty-two round trips through the driver.
func (device *bridge) readBytes(offset uint32, length int) ([]byte, error) {
	if uint32(length) > registerWindow || offset+uint32(length) > registerWindow {
		return nil, fmt.Errorf("registers %#x..%#x are outside the card's window",
			offset, offset+uint32(length))
	}
	out := make([]byte, length)
	if err := device.control(ioctlReadBlock, encodeUint32(offset), out); err != nil {
		return nil, fmt.Errorf("read registers at %#x: %w", offset, err)
	}
	return out, nil
}

func (device *bridge) writeBytes(offset uint32, data []byte) error {
	if offset+uint32(len(data)) > registerWindow {
		return fmt.Errorf("registers %#x..%#x are outside the card's window",
			offset, offset+uint32(len(data)))
	}
	in := append(encodeUint32(offset), data...)
	if err := device.control(ioctlWriteBlock, in, nil); err != nil {
		return fmt.Errorf("write registers at %#x: %w", offset, err)
	}
	return nil
}

func encodeUint32(value uint32) []byte {
	return []byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)}
}

func decodeUint32(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
}

// The card's identifying registers, at the head of the window.
const (
	regHardwareVersion = 0x0000
	regRegisterMapVer  = 0x0004
	regDeviceID        = 0x0008
	regBoardID         = 0x000c
)

// regFirmwareBanner holds the firmware's own name and version as printable text
// ("MaxM4 1.2" on the card this was written against). It sits in the tail of the
// reply buffer, past anything a reply fills in, so it survives ordinary use. The
// firmware decides which commands exist: this one has no GET_CAPABILITIES and no
// SET_INPUT_CONFIG, a later one may.
const (
	regFirmwareBanner = 0x06f0
	firmwareBannerLen = 16
)

// firmware reports what the card's firmware calls itself, or "" if it left no
// banner. For display only; the version scheme is the vendor's.
func (device *bridge) firmware() string {
	banner, err := device.readBytes(regFirmwareBanner, firmwareBannerLen)
	if err != nil {
		return ""
	}
	text := make([]byte, 0, len(banner))
	for _, character := range banner {
		if character == 0 {
			break
		}
		if character < 0x20 || character > 0x7e {
			return ""
		}
		text = append(text, character)
	}
	return strings.TrimSpace(string(text))
}

// identity is what a card says it is.
type identity struct {
	device uint32 // PCI vendor and device, as DEVICE_ID reports them
	board  uint32 // subsystem, which is what tells the models apart
}

func (device *bridge) identity() (identity, error) {
	deviceID, err := device.readRegister(regDeviceID)
	if err != nil {
		return identity{}, err
	}
	boardID, err := device.readRegister(regBoardID)
	if err != nil {
		return identity{}, err
	}
	return identity{device: deviceID, board: boardID}, nil
}

// pciInstanceOf pulls the PCI device instance out of a bridge's interface path,
// which is what a tuner's instance is matched against. The path is
// "\\?\pci#ven...#<instance>#{class}", with an instance's backslashes written as
// hashes; this puts them back.
func pciInstanceOf(path string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(path), `\\?\`)
	if index := strings.LastIndex(trimmed, "#{"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return strings.ReplaceAll(trimmed, "#", `\`)
}

// guid is the shape SetupAPI takes an interface class in.
type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	setupapi                             = syscall.NewLazyDLL("setupapi.dll")
	procSetupDiGetClassDevsW             = setupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces      = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiDestroyDeviceInfoList     = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
)

// deviceInterfaceData is SP_DEVICE_INTERFACE_DATA.
type deviceInterfaceData struct {
	cbSize    uint32
	classGUID guid
	flags     uint32
	reserved  uintptr
}

const (
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10
)

// bridgePaths lists the interface path of every Digital Devices card present. It
// answers with paths rather than handles because opening a card is exclusive
// enough to be worth deferring.
func bridgePaths() ([]string, error) {
	class := ddInterfaceClass
	set, _, err := procSetupDiGetClassDevsW.Call(
		uintptr(unsafe.Pointer(&class)), 0, 0, digcfPresent|digcfDeviceInterface)
	if set == uintptr(syscall.InvalidHandle) {
		return nil, fmt.Errorf("enumerate Digital Devices cards: %w", err)
	}
	defer procSetupDiDestroyDeviceInfoList.Call(set)

	var paths []string
	for index := uintptr(0); ; index++ {
		data := deviceInterfaceData{cbSize: uint32(unsafe.Sizeof(deviceInterfaceData{}))}
		found, _, _ := procSetupDiEnumDeviceInterfaces.Call(
			set, 0, uintptr(unsafe.Pointer(&class)), index, uintptr(unsafe.Pointer(&data)))
		if found == 0 {
			break
		}
		path, err := interfaceDetail(set, &data)
		if err != nil {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// interfaceDetail asks for one interface's path, in the two calls SetupAPI
// wants: one to learn the size, one to fill the buffer.
func interfaceDetail(set uintptr, data *deviceInterfaceData) (string, error) {
	var needed uint32
	procSetupDiGetDeviceInterfaceDetailW.Call(
		set, uintptr(unsafe.Pointer(data)), 0, 0, uintptr(unsafe.Pointer(&needed)), 0)
	if needed < 8 {
		return "", fmt.Errorf("SetupAPI reported no room for a device path")
	}
	buffer := make([]byte, needed)
	// cbSize is the size of the fixed part of the structure, not of the
	// buffer: a four-byte count plus the first character of the path, padded
	// to 8 where a pointer is 8 bytes and 6 where it is 4. The wrong value is
	// refused outright.
	copy(buffer, encodeUint32(detailHeaderSize()))
	ok, _, err := procSetupDiGetDeviceInterfaceDetailW.Call(
		set, uintptr(unsafe.Pointer(data)), uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(needed), uintptr(unsafe.Pointer(&needed)), 0)
	if ok == 0 {
		return "", err
	}
	return utf16BytesToString(buffer[4:]), nil
}

// detailHeaderSize is the fixed part of SP_DEVICE_INTERFACE_DETAIL_DATA_W, as
// the running Windows counts it.
func detailHeaderSize() uint32 {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return 8
	}
	return 6
}

func utf16BytesToString(data []byte) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		unit := uint16(data[i]) | uint16(data[i+1])<<8
		if unit == 0 {
			break
		}
		units = append(units, unit)
	}
	return syscall.UTF16ToString(units)
}
