// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

// Resetting a device that has stopped answering.

package iokit

// slotDeviceReset is ResetDevice, in the original IOUSBDeviceInterface. The
// interface asked for is much later, and these tables only ever grow.
const slotDeviceReset = 25

// resetIOKit puts a device through a bus reset. Only the device interface is
// opened, not an interface of it: claiming interface zero would claim something
// about to be reset, and this way the reset also works on a device whose
// interface another program holds.
//
// Apple marks ResetDevice deprecated in favour of USBDeviceReEnumerate, which
// is not a replacement: re-enumeration needs the device well enough to come
// back, and against a US-3POUT whose bridge had wedged it never once did. See
// docs/setup/os.md.
func resetIOKit(api *ioKit, service uintptr) error {
	open, err := api.openPlugIn()
	if err != nil {
		return err
	}
	deviceInterface, err := api.plugInFor(open, service, uuidDeviceUserClient, uuidDeviceInterface942)
	if err != nil {
		return err
	}
	defer vtable(deviceInterface, slotRelease)

	if result := vtable(deviceInterface, slotDeviceOpen); result != 0 {
		return ioKitError("open the device to reset it", uint32(result))
	}
	defer vtable(deviceInterface, slotDeviceClose)

	if result := vtable(deviceInterface, slotDeviceReset); result != 0 {
		return ioKitError("reset the device", uint32(result))
	}
	return nil
}
