// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package windriver

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// The kernel driver's own interface, which is what this package talks to.
//
// WinDriver ships a user-mode library, wdapi1200.dll, whose WDC layer keeps the
// driver handle in a global of its own, and a second WDC_DriverOpen answers
// "operation already done". A vendor renames the kernel driver but not the
// library, so an Earthsoft PT3 and a PT2 in one machine are two kernel services
// reached through one wdapi1200.dll: one loaded library drives one of the cards
// and not the other.
//
// The way past that is not to load the library twice but not to load it at all.
// Its low-level calls are not exported -- WD_Open and the rest are inline in
// the vendor's header, each a CreateFile or a DeviceIoControl on a handle
// passed in -- so this package opens each kernel service itself and sends the
// same requests.
//
// The codes and layouts here were read out of wdapi1200.dll's own WDC
// functions. They describe WinDriver 12.0.0 and no other version: see measured.

// The requests the driver answers. Every one of them is METHOD_NEITHER, so the
// driver reads and writes the caller's buffer where it stands rather than
// through a copy, and every one answers with a four-byte status.
const (
	ioctlVersion        = 0x95383443
	ioctlLicence        = 0x95383793
	ioctlPciScanCards   = 0x9538368f
	ioctlPciGetCardInfo = 0x953837a3
	ioctlPciConfigDump  = 0x9538346b
	ioctlCardRegister   = 0x9538379b
	ioctlCardUnregister = 0x9538379f
	ioctlDMALock        = 0x953836fb
	ioctlDMAUnlock      = 0x9538340b
	ioctlCleanupSetup   = 0x95383657
)

// WD_VERSION: the number the driver was built as, then its own description of
// itself. The number is what has to match; the text is for a log.
const (
	versionSize   = 0x84
	versionNumber = 0x00
	versionText   = 0x04
	// versionMeasured is WinDriver 12.0.0, the version these layouts were read
	// from. The library's own check is "not older than me"; this one is exact,
	// because a newer driver may lay its structures out differently with nothing
	// to say so.
	versionMeasured = 1200
)

// WD_LICENSE: the vendor's licence string, which the driver answers every other
// request without.
const (
	licenceSize = 0x408
	licenceText = 0x00
	// licenceRoom is how much of it the string may take: the two words after
	// it are the driver's answer.
	licenceRoom = 0x400
)

// driver is one open kernel service, shared by every session that asked for the
// same one. Each service is its own handle: that is the point of talking to the
// driver directly.
type driver struct {
	name    string
	handle  syscall.Handle
	holders int
}

var (
	driversMutex sync.Mutex
	drivers      = map[string]*driver{}
)

// holdDriver opens a kernel service, or takes a share of one already open.
func holdDriver(l Library) (*driver, error) {
	driversMutex.Lock()
	defer driversMutex.Unlock()
	if held, already := drivers[l.Driver]; already {
		held.holders++
		return held, nil
	}
	handle, err := openService(l.Driver)
	if err != nil {
		return nil, err
	}
	held := &driver{name: l.Driver, handle: handle, holders: 1}
	if err := held.checkVersion(); err != nil {
		syscall.CloseHandle(handle)
		return nil, err
	}
	if err := held.registerLicence(l.Licence); err != nil {
		syscall.CloseHandle(handle)
		return nil, err
	}
	drivers[l.Driver] = held
	return held, nil
}

// release gives up one share of a driver, closing it with the last one.
func (d *driver) release() error {
	driversMutex.Lock()
	defer driversMutex.Unlock()
	d.holders--
	if d.holders > 0 {
		return nil
	}
	delete(drivers, d.name)
	if err := syscall.CloseHandle(d.handle); err != nil {
		return fmt.Errorf("close %s: %w", d.name, err)
	}
	d.handle = syscall.InvalidHandle
	return nil
}

// openService opens the kernel service by name, the way WD_Open does: the
// service is a device named after itself, opened for reading -- the driver
// takes its requests as controls -- and shared, since one card's driver serves
// every process that has one.
func openService(name string) (syscall.Handle, error) {
	path, err := syscall.UTF16PtrFromString(`\\.\` + name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	handle, err := syscall.CreateFile(path,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil, syscall.OPEN_EXISTING, fileFlagOverlapped, 0)
	if err != nil {
		// A service that is not there is what an uninstalled driver looks
		// like, and is worth saying differently from a service that refused.
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
			return syscall.InvalidHandle, fmt.Errorf("%w: %s", ErrNotInstalled, name)
		}
		return syscall.InvalidHandle, fmt.Errorf("open %s: %w", name, err)
	}
	return handle, nil
}

// fileFlagOverlapped is FILE_FLAG_OVERLAPPED, which is how WinDriver's own
// WD_Open asks for the handle. Every request here is then sent without an
// overlapped structure, as the library sends its own: the driver answers these
// requests where it stands. A request that did queue would be read back before
// it finished, so control refuses one that says it is still going.
const fileFlagOverlapped = 0x40000000

// control sends one request. The buffer is the request and the answer both: the
// driver writes back into it where it stands.
func (d *driver) control(code uint32, buffer []byte) error {
	if d.handle == syscall.InvalidHandle {
		return errors.New("the driver is closed")
	}
	// Preset to WD_WINDRIVER_STATUS_ERROR, so a request that answered nothing
	// does not read as success.
	status := uint32(0x20000000)
	var returned uint32
	err := syscall.DeviceIoControl(d.handle, code,
		&buffer[0], uint32(len(buffer)),
		(*byte)(unsafe.Pointer(&status)), 4, &returned, nil)
	if err != nil {
		if errors.Is(err, syscall.ERROR_IO_PENDING) {
			return fmt.Errorf("%s answered request %#x later rather than now", d.name, code)
		}
		return fmt.Errorf("%s request %#x: %w", d.name, code, err)
	}
	if status != statusSuccess {
		return errors.New(describe(uintptr(status)))
	}
	return nil
}

// checkVersion refuses a driver whose structures this package does not know.
// The layouts here hold for one version of WinDriver only, and a field read
// from the wrong place does not fail -- it opens the wrong card, or hands a
// card the wrong address to write to.
func (d *driver) checkVersion() error {
	version := make([]byte, versionSize)
	if err := d.control(ioctlVersion, version); err != nil {
		return fmt.Errorf("ask %s what version it is: %w", d.name, err)
	}
	if number := u32(version, versionNumber); number != versionMeasured {
		return fmt.Errorf("%s is WinDriver build %d, and this program's structure layouts were "+
			"measured against build %d; they have to be measured again before it can be used "+
			"(see internal/coaxd/pcie/windriver/ioctl_windows.go)", d.name, number, versionMeasured)
	}
	return nil
}

// version is what the driver says about itself, for a listing or a log.
func (d *driver) version() string {
	buffer := make([]byte, versionSize)
	if err := d.control(ioctlVersion, buffer); err != nil {
		return ""
	}
	return cstring(buffer[versionText:])
}

// registerLicence hands over the string the vendor's driver was built with.
// Without it the driver answers every later request with WD_NO_LICENSE.
func (d *driver) registerLicence(licence string) error {
	buffer := make([]byte, licenceSize)
	if len(licence) >= licenceRoom {
		return fmt.Errorf("the licence for %s is %d bytes, which does not fit", d.name, len(licence))
	}
	copy(buffer[licenceText:], licence)
	if err := d.control(ioctlLicence, buffer); err != nil {
		return fmt.Errorf("register the licence with %s: %w", d.name, err)
	}
	return nil
}
