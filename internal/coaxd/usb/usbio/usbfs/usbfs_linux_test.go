// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbfs

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// wide is whether this is a 64-bit build. The kernel's usbfs structs hold
// pointers, so their sizes, the offsets after the first pointer and the ioctl
// numbers built from the sizes all differ between a 32-bit kernel and a 64-bit
// one; the values below are <linux/usbdevice_fs.h> on each.
const wide = unsafe.Sizeof(uintptr(0)) == 8

// abi picks the 64-bit or the 32-bit value.
func abi(on64, on32 uintptr) uintptr {
	if wide {
		return on64
	}
	return on32
}

// TestStructSizesMatchTheKernelABI pins the layouts usbfs expects: every ioctl
// number is built from one of these sizes, so a drifting field produces a
// request the kernel rejects rather than a compile error.
func TestStructSizesMatchTheKernelABI(t *testing.T) {
	sizes := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"usbdevfs_ctrltransfer", unsafe.Sizeof(controlTransfer{}), abi(24, 16)},
		{"usbdevfs_bulktransfer", unsafe.Sizeof(bulkTransfer{}), abi(24, 16)},
		{"usbdevfs_setinterface", unsafe.Sizeof(setInterface{}), 8},
		{"usbdevfs_urb", unsafe.Sizeof(urb{}), abi(56, 44)},
		{"usbdevfs_iso_packet_desc", unsafe.Sizeof(isoPacketDescriptor{}), 12},
	}
	for _, size := range sizes {
		if size.got != size.want {
			t.Errorf("sizeof(%s) = %d, want %d", size.name, size.got, size.want)
		}
	}
}

// TestFieldOffsetsMatchTheKernelABI pins the offsets the kernel reads the URB
// header at. The size alone would not catch two fields swapping places.
func TestFieldOffsetsMatchTheKernelABI(t *testing.T) {
	var request urb
	offsets := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"type", unsafe.Offsetof(request.Type), 0},
		{"endpoint", unsafe.Offsetof(request.Endpoint), 1},
		{"status", unsafe.Offsetof(request.Status), 4},
		{"flags", unsafe.Offsetof(request.Flags), 8},
		{"buffer", unsafe.Offsetof(request.Buffer), abi(16, 12)},
		{"buffer_length", unsafe.Offsetof(request.BufferLength), abi(24, 16)},
		{"actual_length", unsafe.Offsetof(request.ActualLength), abi(28, 20)},
		{"start_frame", unsafe.Offsetof(request.StartFrame), abi(32, 24)},
		{"number_of_packets", unsafe.Offsetof(request.NumberOfPackets), abi(36, 28)},
		{"error_count", unsafe.Offsetof(request.ErrorCount), abi(40, 32)},
		{"signr", unsafe.Offsetof(request.Signal), abi(44, 36)},
		{"usercontext", unsafe.Offsetof(request.UserContext), abi(48, 40)},
	}
	for _, offset := range offsets {
		if offset.got != offset.want {
			t.Errorf("offsetof(usbdevfs_urb.%s) = %d, want %d", offset.name, offset.got, offset.want)
		}
	}

	var control controlTransfer
	if got := unsafe.Offsetof(control.Timeout); got != 8 {
		t.Errorf("offsetof(usbdevfs_ctrltransfer.timeout) = %d, want 8", got)
	}
	if got, want := unsafe.Offsetof(control.Data), abi(16, 12); got != want {
		t.Errorf("offsetof(usbdevfs_ctrltransfer.data) = %d, want %d", got, want)
	}

	var bulk bulkTransfer
	if got, want := unsafe.Offsetof(bulk.Data), abi(16, 12); got != want {
		t.Errorf("offsetof(usbdevfs_bulktransfer.data) = %d, want %d", got, want)
	}
}

// TestIoctlNumbers checks the request numbers against the constants from
// <linux/usbdevice_fs.h>, as a 64-bit and as a 32-bit kernel defines them. A
// wrong one means every transfer fails with EINVAL, which cannot be caught
// without a device attached.
func TestIoctlNumbers(t *testing.T) {
	requests := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"USBDEVFS_CONTROL", usbfsControl, abi(0xC0185500, 0xC0105500)},
		{"USBDEVFS_BULK", usbfsBulk, abi(0xC0185502, 0xC0105502)},
		{"USBDEVFS_SETINTERFACE", usbfsSetInterface, 0x80085504},
		{"USBDEVFS_SETCONFIGURATION", usbfsSetConfiguration, 0x80045505},
		{"USBDEVFS_SUBMITURB", usbfsSubmitURB, abi(0x8038550A, 0x802C550A)},
		{"USBDEVFS_DISCARDURB", usbfsDiscardURB, 0x0000550B},
		{"USBDEVFS_REAPURBNDELAY", usbfsReapURBNonBlock, abi(0x4008550D, 0x4004550D)},
		{"USBDEVFS_CLAIMINTERFACE", usbfsClaimInterface, 0x8004550F},
		{"USBDEVFS_RELEASEINTERFACE", usbfsReleaseInterface, 0x80045510},
	}
	for _, request := range requests {
		if request.got != request.want {
			t.Errorf("%s = %#08x, want %#08x", request.name, request.got, request.want)
		}
	}
}

// TestEnumerateDoesNotFailOnThisMachine is a smoke test: it skips when listing
// fails, and otherwise checks every device listed has a location and an opener.
func TestEnumerateDoesNotFailOnThisMachine(t *testing.T) {
	devices, err := Enumerate()
	if err != nil {
		t.Skipf("this machine has no readable sysfs USB tree: %v", err)
	}
	for _, candidate := range devices {
		if candidate.Location == "" {
			t.Error("an enumerated device has no bus:address location")
		}
		if candidate.Open == nil {
			t.Errorf("device %s cannot be opened", candidate.Location)
		}
	}
}

// TestEnumerateSysfsReadsIdentity checks the listing against a tree of this
// test's own making: the serial and port path a stable board name is built from
// are picked up without opening anything, and a device without a serial is
// listed all the same.
func TestEnumerateSysfsReadsIdentity(t *testing.T) {
	root := t.TempDir()
	write := func(directory, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", directory, name, err)
		}
	}
	device := func(name, vendor, product, bus, address string, serial *string) {
		t.Helper()
		directory := filepath.Join(root, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("make %s: %v", directory, err)
		}
		write(directory, "idVendor", vendor)
		write(directory, "idProduct", product)
		write(directory, "busnum", bus)
		write(directory, "devnum", address)
		if serial != nil {
			write(directory, "serial", *serial)
		}
	}

	serial := "ABC12345"
	device("1-2", "0511", "083f", "1", "7", &serial)
	device("2-1.4", "0572", "c689", "2", "3", nil)
	// An interface directory sits beside the devices and must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "1-2:1.0"), 0o755); err != nil {
		t.Fatalf("make the interface directory: %v", err)
	}

	saved := sysfsRoot
	sysfsRoot = root
	defer func() { sysfsRoot = saved }()

	devices, err := enumerateSysfs()
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("listed %d devices, want 2", len(devices))
	}

	first := devices[0]
	if first.Location != "001:007" {
		t.Errorf("located the first device at %q, want 001:007", first.Location)
	}
	if first.ID != (backend.ID{Vendor: 0x0511, Product: 0x083f}) {
		t.Errorf("read the first device as %04x:%04x", first.ID.Vendor, first.ID.Product)
	}
	if first.Serial != "ABC12345" {
		t.Errorf("read the serial as %q, want ABC12345", first.Serial)
	}
	if first.Port != "1-2" {
		t.Errorf("read the port path as %q, want 1-2", first.Port)
	}

	second := devices[1]
	if second.Port != "2-1.4" {
		t.Errorf("read the second port path as %q, want 2-1.4", second.Port)
	}
	if second.Serial != "" {
		t.Errorf("invented a serial %q for a device that has none", second.Serial)
	}
}

// TestEnumerateSysfsReadsHeld pins how a device bound to a kernel driver is told
// apart: by the driver link of interface 0 under the active configuration. usbfs
// is this program's own claim and leaves the device free, as does a device with
// no driver bound or no configuration.
func TestEnumerateSysfsReadsHeld(t *testing.T) {
	root := t.TempDir()
	device := func(name, address, configuration, driver string) {
		t.Helper()
		directory := filepath.Join(root, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("make %s: %v", directory, err)
		}
		values := map[string]string{"idVendor": "0572", "idProduct": "c689", "busnum": "1", "devnum": address}
		if configuration != "" {
			values["bConfigurationValue"] = configuration
		}
		for key, value := range values {
			if err := os.WriteFile(filepath.Join(directory, key), []byte(value+"\n"), 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", directory, key, err)
			}
		}
		if configuration == "" {
			return
		}
		face := filepath.Join(root, name+":"+configuration+".0")
		if err := os.MkdirAll(face, 0o755); err != nil {
			t.Fatalf("make %s: %v", face, err)
		}
		if driver != "" {
			if err := os.Symlink("../../../bus/usb/drivers/"+driver, filepath.Join(face, "driver")); err != nil {
				t.Fatalf("link the driver of %s: %v", face, err)
			}
		}
	}
	device("1-1", "2", "1", "dvb_usb_dvbsky")
	device("1-2", "3", "1", "usbfs")
	device("1-3", "4", "1", "")
	device("1-4", "5", "", "")
	device("1-5", "6", "2", "px4_drv")

	saved := sysfsRoot
	sysfsRoot = root
	defer func() { sysfsRoot = saved }()

	devices, err := enumerateSysfs()
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	want := map[string]string{"1-1": "dvb_usb_dvbsky", "1-2": "", "1-3": "", "1-4": "", "1-5": "px4_drv"}
	if len(devices) != len(want) {
		t.Fatalf("listed %d devices, want %d", len(devices), len(want))
	}
	for _, found := range devices {
		if found.Held != want[found.Port] {
			t.Errorf("read %s as held by %q, want %q", found.Port, found.Held, want[found.Port])
		}
	}
}

// TestDisconnectedRecognisesEveryErrno pins the errnos the kernel uses when a
// handle can no longer reach its device. ESRCH is among them because a MyGica
// T230C2 taken off the bus answered with it and nothing else: usbfs will not
// submit to an endpoint whose interface has gone. Without it the failure
// surfaced as a tuning fault that no retry could clear.
func TestDisconnectedRecognisesEveryErrno(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENODEV, syscall.ENXIO, syscall.ESHUTDOWN, syscall.ESRCH} {
		if !disconnected(errno) {
			t.Errorf("%v is not recognised as a disconnection", errno)
		}
	}
	for _, errno := range []syscall.Errno{syscall.ETIMEDOUT, syscall.EBUSY, syscall.EACCES} {
		if disconnected(errno) {
			t.Errorf("%v is wrongly read as a disconnection", errno)
		}
	}
}

// TestFirstConfigurationReadsTheValueTheDeviceChose checks that the
// configuration offered to an unconfigured device is the one its descriptors
// name, not the 1 that is merely usual.
func TestFirstConfigurationReadsTheValueTheDeviceChose(t *testing.T) {
	device := make([]byte, backend.DeviceDescriptorBytes)
	device[0] = backend.DeviceDescriptorBytes
	device[1] = backend.DescriptorTypeDevice
	config := []byte{9, backend.DescriptorTypeConfig, 32, 0, 1, 7, 0, 0x80, 250}
	node := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(node, append(device, config...), 0o600); err != nil {
		t.Fatalf("write the node: %v", err)
	}
	file, err := os.Open(node)
	if err != nil {
		t.Fatalf("open the node: %v", err)
	}
	defer file.Close()
	value, err := firstConfiguration(file)
	if err != nil {
		t.Fatalf("read the first configuration: %v", err)
	}
	if value != 7 {
		t.Errorf("first configuration = %d, want 7", value)
	}
}

// TestFirstConfigurationRefusesADeviceWithoutOne keeps a node that holds only a
// device descriptor from being handed a configuration value made of whatever
// followed it.
func TestFirstConfigurationRefusesADeviceWithoutOne(t *testing.T) {
	device := make([]byte, backend.DeviceDescriptorBytes)
	device[0] = backend.DeviceDescriptorBytes
	device[1] = backend.DescriptorTypeDevice
	node := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(node, append(device, make([]byte, configDescriptorBytes)...), 0o600); err != nil {
		t.Fatalf("write the node: %v", err)
	}
	file, err := os.Open(node)
	if err != nil {
		t.Fatalf("open the node: %v", err)
	}
	defer file.Close()
	if _, err := firstConfiguration(file); err == nil {
		t.Error("a node with no configuration descriptor was given one anyway")
	}
}
