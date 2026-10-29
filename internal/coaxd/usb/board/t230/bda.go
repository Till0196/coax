// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package t230

import (
	"regexp"
	"strconv"
)

// usbIDs finds the USB IDs in a BDA tuner's device path, which Windows writes
// as usb#vid_0572&pid_c68a#....
var usbIDs = regexp.MustCompile(`(?i)usb#vid_([0-9a-f]{4})&pid_([0-9a-f]{4})`)

// productOfPath is the board a BDA tuner's device path names, if it is one of
// this family.
func productOfPath(path string) (product, bool) {
	found := usbIDs.FindStringSubmatch(path)
	if found == nil {
		return product{}, false
	}
	vendor, _ := strconv.ParseUint(found[1], 16, 16)
	id, _ := strconv.ParseUint(found[2], 16, 16)
	if vendor != 0x0572 {
		return product{}, false
	}
	board, ok := products[uint16(id)]
	return board, ok
}
