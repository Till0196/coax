// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package t230

import "testing"

func TestProductOfPathReadsTheUSBIDs(t *testing.T) {
	for path, want := range map[string]string{
		`@device:pnp:\\?\usb#vid_0572&pid_c68a#161206#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{972b7bb1-8149-4a72-b891-83e3c7dfe596}`: "MyGica T230C2",
		`@device:pnp:\\?\USB#VID_0572&PID_C689#1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{0}`:                                         "MyGica T230C",
		`@device:pnp:\\?\usb#vid_0572&pid_2856#1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{0}`:                                         "",
		`@device:pnp:\\?\usb#vid_1f4d&pid_c68a#1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{0}`:                                         "",
		`@device:pnp:\\?\pci#ven_6514&dev_6205#1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{0}`:                                         "",
	} {
		board, ok := productOfPath(path)
		if ok != (want != "") || board.name != want {
			t.Errorf("%s: %q %v, want %q", path, board.name, ok, want)
		}
	}
}
