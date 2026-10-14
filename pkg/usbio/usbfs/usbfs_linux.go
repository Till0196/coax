// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Linux reaches USB devices through usbfs, the character devices under
// /dev/bus/usb: every transfer is an ioctl on the device node, so no C library
// is involved. The struct layouts and ioctl numbers below are the kernel's ABI
// from <linux/usbdevice_fs.h>.

package usbfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Till0196/coax/pkg/usbio/backend"
)

// The ioctl direction bits and the encoding _IOC builds a request number from.
const (
	iocWrite     = 1
	iocRead      = 2
	iocNRBits    = 8
	iocTypeBits  = 8
	iocSizeBits  = 14
	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits
	iocSizeShift = iocTypeShift + iocTypeBits
	iocDirShift  = iocSizeShift + iocSizeBits
)

// ioc builds an ioctl request number the way the kernel's _IOC macro does.
func ioc(direction, letter, number, size uintptr) uintptr {
	return direction<<iocDirShift | letter<<iocTypeShift | number<<iocNRShift | size<<iocSizeShift
}

// The usbfs requests this driver needs. usbfs spells the direction from the
// kernel's point of view, which is why several of them read inverted.
var (
	usbfsControl          = ioc(iocRead|iocWrite, 'U', 0, unsafe.Sizeof(controlTransfer{}))
	usbfsBulk             = ioc(iocRead|iocWrite, 'U', 2, unsafe.Sizeof(bulkTransfer{}))
	usbfsSetInterface     = ioc(iocRead, 'U', 4, unsafe.Sizeof(setInterface{}))
	usbfsSetConfiguration = ioc(iocRead, 'U', 5, unsafe.Sizeof(uint32(0)))
	usbfsSubmitURB        = ioc(iocRead, 'U', 10, unsafe.Sizeof(urb{}))
	usbfsDiscardURB       = ioc(0, 'U', 11, 0)
	usbfsReapURBNonBlock  = ioc(iocWrite, 'U', 13, unsafe.Sizeof(uintptr(0)))
	usbfsClaimInterface   = ioc(iocRead, 'U', 15, unsafe.Sizeof(uint32(0)))
	usbfsReleaseInterface = ioc(iocRead, 'U', 16, unsafe.Sizeof(uint32(0)))
)

// configDescriptorBytes is the fixed part of a configuration descriptor, which
// is all of it that is needed to read the value the device gave it.
const configDescriptorBytes = 9

// controlTransfer is struct usbdevfs_ctrltransfer.
type controlTransfer struct {
	RequestType uint8
	Request     uint8
	Value       uint16
	Index       uint16
	Length      uint16
	Timeout     uint32
	Data        unsafe.Pointer
}

// bulkTransfer is struct usbdevfs_bulktransfer.
//
// The pointer is left to align itself, as the kernel's is: after the timeout on
// a 32-bit kernel, four bytes further on a 64-bit one. So are the pointers in
// the other structs here, which is what lets one declaration serve both.
type bulkTransfer struct {
	Endpoint uint32
	Length   uint32
	Timeout  uint32
	Data     unsafe.Pointer
}

// setInterface is struct usbdevfs_setinterface.
type setInterface struct {
	Interface  uint32
	AltSetting uint32
}

// urb is struct usbdevfs_urb, the header an isochronous submission starts with.
// The per-frame descriptors follow it in the same allocation.
type urb struct {
	Type            uint8
	Endpoint        uint8
	_               [2]uint8
	Status          int32
	Flags           uint32
	Buffer          unsafe.Pointer
	BufferLength    int32
	ActualLength    int32
	StartFrame      int32
	NumberOfPackets int32
	ErrorCount      int32
	Signal          uint32
	UserContext     unsafe.Pointer
}

// urbTypeISO and urbTypeBulk are USBDEVFS_URB_TYPE_ISO and _BULK.
const (
	urbTypeISO  = 0
	urbTypeBulk = 3
)

const usbfsRoot = "/dev/bus/usb"

// sysfsRoot is where the kernel describes the devices without opening them. It
// is a variable so a test can point the listing at a tree of its own.
var sysfsRoot = "/sys/bus/usb/devices"

// Enumerate lists the USB devices the kernel can see. sysfs is preferred: it
// gives every device's identity without opening anything, so a node belonging
// to another driver is never touched. A container handed only /dev/bus/usb has
// the nodes scanned instead.
func Enumerate() ([]backend.Found, error) {
	devices, sysfsErr := enumerateSysfs()
	if len(devices) != 0 {
		return devices, nil
	}
	devices, nodeErr := enumerateNodes()
	if nodeErr == nil {
		return devices, nil
	}
	if sysfsErr != nil {
		return nil, fmt.Errorf("%w: neither %s nor %s could be read (%v); in a container, pass the device nodes in with --device /dev/bus/usb", backend.ErrNoDevices, sysfsRoot, usbfsRoot, nodeErr)
	}
	return nil, fmt.Errorf("list USB devices: %w", nodeErr)
}

func enumerateSysfs() ([]backend.Found, error) {
	root, err := os.Open(sysfsRoot)
	if err != nil {
		return nil, err
	}
	// Only the names are wanted, and asking for them alone spares the directory
	// entry each one would otherwise be wrapped in.
	names, err := root.Readdirnames(-1)
	_ = root.Close()
	if err != nil {
		return nil, err
	}
	var reader sysfsReader
	var devices []backend.Found
	for _, name := range names {
		// Interfaces appear beside devices as "1-2:1.0"; skip them.
		if strings.Contains(name, ":") {
			continue
		}
		directory := filepath.Join(sysfsRoot, name)
		vendor, err := reader.number(directory, "idVendor", 16)
		if err != nil {
			continue
		}
		product, err := reader.number(directory, "idProduct", 16)
		if err != nil {
			continue
		}
		bus, err := reader.number(directory, "busnum", 10)
		if err != nil {
			continue
		}
		address, err := reader.number(directory, "devnum", 10)
		if err != nil {
			continue
		}
		node := filepath.Join(usbfsRoot, fmt.Sprintf("%03d", bus), fmt.Sprintf("%03d", address))
		devices = append(devices, backend.Found{
			ID:       backend.ID{Vendor: uint16(vendor), Product: uint16(product)},
			Location: fmt.Sprintf("%03d:%03d", bus, address),
			// The directory is named after the physical path and the serial sits
			// beside the identifiers, so both come for free.
			Serial: reader.text(directory, "serial"),
			Port:   name,
			Held:   reader.held(directory, name),
			Open:   func() (backend.Device, error) { return openNode(node) },
		})
	}
	return sorted(devices), nil
}

// held names the kernel driver bound to the device's interface 0, the one this
// program claims. A device dvb_usb_dvbsky or px4_drv has bound is listed by the
// dvb or chardev backend already, and claiming it here can only answer EBUSY.
// usbfs is not another driver: it is what a process claiming through usbfs --
// this one, with the board open -- shows as.
func (reader *sysfsReader) held(directory, name string) string {
	configuration, err := reader.read(directory, "bConfigurationValue")
	if err != nil {
		// Unconfigured: no interface for anything to be bound to.
		return ""
	}
	link, err := os.Readlink(filepath.Join(sysfsRoot, name+":"+string(configuration)+".0", "driver"))
	if err != nil {
		return ""
	}
	if driver := filepath.Base(link); driver != "usbfs" {
		return driver
	}
	return ""
}

// enumerateNodes reads the device identity from the head of each usbfs node,
// which begins with the device descriptor. Read permission is enough, so a
// device this program will not claim is left alone.
func enumerateNodes() ([]backend.Found, error) {
	buses, err := os.ReadDir(usbfsRoot)
	if err != nil {
		return nil, err
	}
	var devices []backend.Found
	for _, busEntry := range buses {
		bus, err := strconv.ParseUint(busEntry.Name(), 10, 16)
		if err != nil {
			continue
		}
		busPath := filepath.Join(usbfsRoot, busEntry.Name())
		nodes, err := os.ReadDir(busPath)
		if err != nil {
			continue
		}
		for _, nodeEntry := range nodes {
			address, err := strconv.ParseUint(nodeEntry.Name(), 10, 16)
			if err != nil {
				continue
			}
			node := filepath.Join(busPath, nodeEntry.Name())
			id, err := readNodeID(node)
			if err != nil {
				continue
			}
			devices = append(devices, backend.Found{
				ID:       id,
				Location: fmt.Sprintf("%03d:%03d", bus, address),
				Open:     func() (backend.Device, error) { return openNode(node) },
			})
		}
	}
	return sorted(devices), nil
}

// readNodeID takes the vendor and product out of the device descriptor a usbfs
// node opens with.
func readNodeID(node string) (backend.ID, error) {
	file, err := os.Open(node)
	if err != nil {
		return backend.ID{}, err
	}
	defer file.Close()
	header := make([]byte, backend.DeviceDescriptorBytes)
	if _, err := io.ReadFull(file, header); err != nil {
		return backend.ID{}, err
	}
	if header[0] != backend.DeviceDescriptorBytes || header[1] != backend.DescriptorTypeDevice {
		return backend.ID{}, fmt.Errorf("%s does not begin with a device descriptor", node)
	}
	return backend.ID{
		Vendor:  uint16(header[8]) | uint16(header[9])<<8,
		Product: uint16(header[10]) | uint16(header[11])<<8,
	}, nil
}

// sorted puts the devices in bus:address order, so a numeric selector picks the
// same one from one run to the next.
func sorted(devices []backend.Found) []backend.Found {
	sort.Slice(devices, func(a, b int) bool { return devices[a].Location < devices[b].Location })
	return devices
}

// sysfsReader reads the small text files sysfs is made of into a buffer it
// keeps.
//
// os.ReadFile sizes its buffer from what the file says it is, and a sysfs
// attribute says four kilobytes however few bytes it holds. Listing the tuners
// reads five of them per device, once a second while a client watches a
// recording -- which made more rubbish for the collector than the stream it was
// watching.
type sysfsReader struct {
	scratch [256]byte
}

// read returns the attribute's contents, trimmed, in the reader's own buffer:
// valid until the next read.
func (reader *sysfsReader) read(directory, name string) ([]byte, error) {
	file, err := os.Open(filepath.Join(directory, name))
	if err != nil {
		return nil, err
	}
	read, err := file.Read(reader.scratch[:])
	_ = file.Close()
	if read == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return bytes.TrimSpace(reader.scratch[:read]), nil
}

func (reader *sysfsReader) number(directory, name string, base int) (uint64, error) {
	data, err := reader.read(directory, name)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(string(data), base, 32)
}

// text reads an attribute holding words, empty when there is none: a device
// without a serial descriptor has no serial file at all.
func (reader *sysfsReader) text(directory, name string) string {
	data, err := reader.read(directory, name)
	if err != nil {
		return ""
	}
	return string(data)
}

// usbfsDevice is one opened usbfs node with interface 0 claimed.
type usbfsDevice struct {
	file *os.File

	// reaped holds the URBs the kernel has handed back that their owner has not
	// come for yet. The reaping queue belongs to the open file rather than the
	// endpoint, so two rings on one device -- two tuners behind one bridge --
	// would otherwise each throw away what the other was waiting for.
	reapMu sync.Mutex
	reaped map[*urb]struct{}
	// orphans are URBs given up on -- by a reader that timed out or a ring that
	// closed -- while the kernel may still hold them. usbfs copies a URB's data
	// and status into this process's memory only when it is reaped, so one
	// reaped later writes into its buffers then, whoever has them by that time.
	// Each is kept here, pinned and reachable, until it is reaped or the node is
	// closed, which kills every URB on it before close returns.
	orphans map[*urb]*runtime.Pinner
	// shut is set once the node is closed, after which no URB is left on it to
	// be written through, and nothing given up on needs keeping.
	shut bool

	// parked is the isochronous ring kept running between streams; see
	// park_linux.go, and closing whether the device is being closed, after which
	// nothing is parked. alternate is the setting interface 0 was last put in,
	// and alternateSet whether it has been put in one at all.
	parkMu       sync.Mutex
	parked       *parkedRing
	closing      bool
	alternate    int
	alternateSet bool
}

func openNode(path string) (backend.Device, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("%w (the device node needs write permission; run as root or install a udev rule)", err)
		}
		return nil, err
	}
	return claim(file)
}

// Adopt takes a usbfs node somebody else opened and drives it from here, for a
// system where a program may not enumerate the bus itself -- on Android an
// application gets its descriptor from the platform's USB service. The
// descriptor is duplicated, so the caller keeps its own. The interface is
// claimed here rather than by whoever opened it: claiming it twice is EBUSY.
func Adopt(file *os.File) (backend.Device, error) {
	if file == nil {
		return nil, errors.New("no file to adopt")
	}
	duplicate, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicate the USB descriptor: %w", err)
	}
	syscall.CloseOnExec(duplicate)
	return claim(os.NewFile(uintptr(duplicate), file.Name()))
}

// claim claims interface 0, which makes the kernel hand the device's transfers
// to this process.
func claim(file *os.File) (backend.Device, error) {
	opened := &usbfsDevice{file: file}
	number := uint32(0)
	err := ioctlOf(opened, usbfsClaimInterface, &number)
	if errors.Is(err, syscall.ENOENT) {
		// There is no interface 0 because the device has no configuration chosen:
		// enumeration leaves a device addressed, and only a SET_CONFIGURATION gives
		// it interfaces to claim. The kernel normally chooses one itself, so a device
		// found this way was left unconfigured by whoever had it last.
		if configured := configure(opened); configured == nil {
			err = ioctlOf(opened, usbfsClaimInterface, &number)
		}
	}
	if err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EBUSY) {
			return nil, fmt.Errorf("claim USB interface 0: %w (a kernel driver already has the device)", err)
		}
		if errors.Is(err, syscall.ENOENT) {
			return nil, fmt.Errorf("claim USB interface 0: %w (the device has no configuration chosen"+
				" and would not take one)", err)
		}
		return nil, fmt.Errorf("claim USB interface 0: %w", err)
	}
	return opened, nil
}

// configure gives an unconfigured device its first configuration.
func configure(opened *usbfsDevice) error {
	value, err := firstConfiguration(opened.file)
	if err != nil {
		return err
	}
	number := uint32(value)
	return ioctlOf(opened, usbfsSetConfiguration, &number)
}

// firstConfiguration reads the value of the device's first configuration from
// the descriptors the usbfs node begins with. It is not always 1: the value is
// the device's to choose, and only the descriptor says what it picked.
func firstConfiguration(file *os.File) (byte, error) {
	descriptors := make([]byte, backend.DeviceDescriptorBytes+configDescriptorBytes)
	read, err := file.ReadAt(descriptors, 0)
	if read < len(descriptors) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, fmt.Errorf("read the device descriptors: %w", err)
	}
	first := descriptors[backend.DeviceDescriptorBytes:]
	if first[0] < configDescriptorBytes || first[1] != backend.DescriptorTypeConfig {
		return 0, errors.New("the device has no configuration descriptor")
	}
	// bConfigurationValue, the fifth field of a configuration descriptor.
	return first[5], nil
}

// ioctlOf and ioctlResultOf run one usbfs request whose argument is a struct or
// a word, so the conversion to unsafe.Pointer happens once rather than at each
// request site.
func ioctlOf[Argument any](opened *usbfsDevice, request uintptr, argument *Argument) error {
	return opened.ioctl(request, unsafe.Pointer(argument))
}

func ioctlResultOf[Argument any](opened *usbfsDevice, request uintptr, argument *Argument) (int, error) {
	return opened.ioctlResult(request, unsafe.Pointer(argument))
}

func (opened *usbfsDevice) ioctl(request uintptr, argument unsafe.Pointer) error {
	_, err := opened.ioctlResult(request, argument)
	return err
}

// ioctlResult runs one usbfs request and reports the value it returned, which
// for the transfer requests is the number of bytes moved.
//
// EINTR is retried rather than reported: the Go runtime preempts goroutines
// with SIGURG, so any ioctl can come back interrupted, and a raw syscall does
// not retry the way the standard library's I/O does. The open file is kept
// reachable across the call, because Fd() does nothing to keep the *os.File
// alive.
func (opened *usbfsDevice) ioctlResult(request uintptr, argument unsafe.Pointer) (int, error) {
	defer runtime.KeepAlive(opened.file)
	for {
		result, _, errno := syscall.Syscall(syscall.SYS_IOCTL, opened.file.Fd(), request, uintptr(argument))
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, errno
		}
		return int(result), nil
	}
}

func (opened *usbfsDevice) Control(requestType, request byte, value, index uint16, buffer []byte, timeoutMS uint32) (int, error) {
	transfer := controlTransfer{
		RequestType: requestType,
		Request:     request,
		Value:       value,
		Index:       index,
		Length:      uint16(len(buffer)),
		Timeout:     timeoutMS,
		Data:        bufferPointer(buffer),
	}
	transferred, err := ioctlResultOf(opened, usbfsControl, &transfer)
	runtime.KeepAlive(buffer)
	if err != nil {
		if errors.Is(err, syscall.ETIMEDOUT) {
			return 0, backend.ErrTimeout
		}
		if disconnected(err) {
			return 0, backend.ErrGone
		}
		return 0, fmt.Errorf("control transfer: %w", err)
	}
	return transferred, nil
}

// disconnected reports whether an errno means this handle can no longer reach the
// hardware; the kernel picks one by how far the transfer had got. ENODEV, ENXIO,
// ESHUTDOWN and ESRCH all mean: give the handle up and reopen.
func disconnected(err error) bool {
	return errors.Is(err, syscall.ENODEV) ||
		errors.Is(err, syscall.ENXIO) ||
		errors.Is(err, syscall.ESHUTDOWN) ||
		errors.Is(err, syscall.ESRCH)
}

func (opened *usbfsDevice) Bulk(endpoint byte, buffer []byte, timeoutMS uint32) (int, error) {
	transfer := bulkTransfer{
		Endpoint: uint32(endpoint),
		Length:   uint32(len(buffer)),
		Timeout:  timeoutMS,
		Data:     bufferPointer(buffer),
	}
	transferred, err := ioctlResultOf(opened, usbfsBulk, &transfer)
	runtime.KeepAlive(buffer)
	if err != nil {
		if errors.Is(err, syscall.ETIMEDOUT) {
			return transferred, backend.ErrTimeout
		}
		if disconnected(err) {
			return transferred, backend.ErrGone
		}
		return transferred, fmt.Errorf("bulk endpoint %#02x: %w", endpoint, err)
	}
	return transferred, nil
}

func (opened *usbfsDevice) SetAlternate(interfaceNumber, alternate int) error {
	// Putting the interface in the setting it is already in would withdraw every
	// URB on it, the parked ring's among them, for nothing. In any other setting
	// the ring has no endpoint to run on, so it goes first.
	if opened.hasParked() {
		if interfaceNumber == 0 && opened.alternateSet && alternate == opened.alternate {
			return nil
		}
		opened.dropParked()
	}
	request := setInterface{Interface: uint32(interfaceNumber), AltSetting: uint32(alternate)}
	if err := ioctlOf(opened, usbfsSetInterface, &request); err != nil {
		return fmt.Errorf("set USB alternate setting %d on interface %d: %w", alternate, interfaceNumber, err)
	}
	if interfaceNumber == 0 {
		opened.alternate, opened.alternateSet = alternate, true
	}
	return nil
}

// Isochronous submits one isochronous URB, waits for the kernel to hand it back,
// then packs the frames that carried data to the front of the buffer. usbfs has
// no blocking reap with a timeout, so the wait is a poll.
func (opened *usbfsDevice) Isochronous(endpoint byte, buffer []byte, packetSize, packets int, timeoutMS uint32) (int, error) {
	// The URB header and its frame descriptors have to be one allocation, the way
	// the kernel's variable-length struct is laid out.
	block := make([]byte, int(unsafe.Sizeof(urb{}))+packets*int(unsafe.Sizeof(isoPacketDescriptor{})))
	request := (*urb)(unsafe.Pointer(&block[0]))
	frames := unsafe.Slice((*isoPacketDescriptor)(unsafe.Pointer(&block[unsafe.Sizeof(urb{})])), packets)

	// The URB fills a buffer of its own, copied into the caller's only once it
	// is back: one given up on below is still written into whenever it is
	// reaped, and by then the caller may be using its buffer for something else.
	data := make([]byte, packetSize*packets)
	request.Type = urbTypeISO
	request.Endpoint = endpoint
	request.Buffer = unsafe.Pointer(&data[0])
	request.BufferLength = int32(len(data))
	request.NumberOfPackets = int32(packets)
	for index := range frames {
		frames[index].Length = uint32(packetSize)
	}

	// The kernel keeps both pointers until the URB is reaped, so they must not
	// move in the meantime. The pinner is this call's own: one shared across the
	// device would be unpinned under another tuner's transfer by whichever call
	// finished first.
	pinner := new(runtime.Pinner)
	pinner.Pin(&block[0])
	pinner.Pin(&data[0])

	opened.forget(request)
	if err := ioctlOf(opened, usbfsSubmitURB, request); err != nil {
		pinner.Unpin()
		return 0, fmt.Errorf("submit isochronous transfer on endpoint %#02x: %w", endpoint, err)
	}
	if err := opened.reap(request, timeoutMS); err != nil {
		// A URB that never came back has to be withdrawn and reaped, or the kernel
		// keeps referring to the buffer after this call has returned. One that
		// does not come back even then is handed to the device to keep.
		_ = ioctlOf(opened, usbfsDiscardURB, request)
		if opened.reap(request, 100) != nil {
			opened.adopt(request, pinner)
			return 0, err
		}
		pinner.Unpin()
		return 0, err
	}
	pinner.Unpin()

	transferred := packFrames(buffer, data, frames, packetSize)
	if request.Status != 0 {
		if disconnected(syscall.Errno(-request.Status)) {
			return transferred, backend.ErrGone
		}
		return transferred, fmt.Errorf("isochronous endpoint %#02x: transfer status %d", endpoint, request.Status)
	}
	return transferred, nil
}

func (opened *usbfsDevice) reap(request *urb, timeoutMS uint32) error {
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		back, err := opened.returned(request)
		if err != nil {
			return err
		}
		if back {
			return nil
		}
		if time.Now().After(deadline) {
			return backend.ErrTimeout
		}
		// The frames of one URB take a millisecond apiece to arrive, so there
		// is nothing to gain from spinning tighter than that.
		time.Sleep(time.Millisecond)
	}
}

// returned reports whether the kernel has handed a URB back, reaping whatever
// else is ready on the way and keeping it for whoever submitted it.
func (opened *usbfsDevice) returned(request *urb) (bool, error) {
	opened.reapMu.Lock()
	defer opened.reapMu.Unlock()
	if _, ok := opened.reaped[request]; ok {
		delete(opened.reaped, request)
		return true, nil
	}
	for {
		var completed *urb
		err := ioctlOf(opened, usbfsReapURBNonBlock, &completed)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) {
				return false, nil
			}
			return false, fmt.Errorf("reap transfer: %w", err)
		}
		if completed == request {
			return true, nil
		}
		if pinner, ok := opened.orphans[completed]; ok {
			// Back at last, and copied in by the reap just now, into memory that was
			// kept for it. Nobody is waiting for it.
			delete(opened.orphans, completed)
			pinner.Unpin()
			continue
		}
		if opened.reaped == nil {
			opened.reaped = make(map[*urb]struct{})
		}
		opened.reaped[completed] = struct{}{}
	}
}

// adopt keeps a URB that was given up on, with the pinner holding its memory,
// until the kernel hands it back or the node is closed. See orphans.
func (opened *usbfsDevice) adopt(request *urb, pinner *runtime.Pinner) {
	opened.reapMu.Lock()
	defer opened.reapMu.Unlock()
	if opened.shut {
		pinner.Unpin()
		return
	}
	if opened.orphans == nil {
		opened.orphans = make(map[*urb]*runtime.Pinner)
	}
	opened.orphans[request] = pinner
}

// forget drops a URB that is about to be submitted from what has been reaped,
// so one reaped late and never come for cannot answer for the next transfer
// that happens to sit at the same address.
func (opened *usbfsDevice) forget(request *urb) {
	opened.reapMu.Lock()
	defer opened.reapMu.Unlock()
	delete(opened.reaped, request)
}

func (opened *usbfsDevice) Close() error {
	opened.parkMu.Lock()
	opened.closing = true
	opened.dropParkedLocked()
	opened.parkMu.Unlock()
	number := uint32(0)
	releaseErr := ioctlOf(opened, usbfsReleaseInterface, &number)
	closeErr := opened.file.Close()
	// Closing the node kills every URB on it before close returns, so nothing
	// given up on can be written into any more.
	opened.reapMu.Lock()
	opened.shut = true
	for request, pinner := range opened.orphans {
		pinner.Unpin()
		delete(opened.orphans, request)
	}
	opened.reapMu.Unlock()
	if releaseErr != nil {
		return fmt.Errorf("release USB interface 0: %w", releaseErr)
	}
	return closeErr
}
