// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package injected

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// usbDeviceMajor is the character-device major number of the usbfs nodes under
// /dev/bus/usb.
const usbDeviceMajor = 189

// isUSBNode refuses a descriptor that is not a usbfs device node: whatever is
// attached is opened as a tuner and driven with usbfs ioctls.
func isUSBNode(file *os.File) error {
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &stat); err != nil {
		return fmt.Errorf("look at the descriptor handed over: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFCHR {
		return errors.New("the descriptor handed over is not a device node")
	}
	device := uint64(stat.Rdev)
	if major := (device>>8)&0xfff | (device>>32)&^uint64(0xfff); major != usbDeviceMajor {
		return fmt.Errorf("the descriptor handed over is device %d, not a USB device (%d)", major, usbDeviceMajor)
	}
	return nil
}
