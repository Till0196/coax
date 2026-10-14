// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winusb

import "testing"

func TestSerialInReadsTheInstanceName(t *testing.T) {
	for path, want := range map[string]string{
		`\\?\usb#vid_0511&pid_0045#0#{a5dcbf10-6530-11d2-901f-00c04fb951ed}`:              "0",
		`\\?\usb#vid_28de&pid_2300#lhr-a25cc3b2#{a5dcbf10-6530-11d2-901f-00c04fb951ed}`:   "lhr-a25cc3b2",
		`\\?\usb#vid_05ac&pid_024f#a&26fa1f9b&0&3#{a5dcbf10-6530-11d2-901f-00c04fb951ed}`: "",
		`\\?\usb#vid_05ac&pid_024f`: "",
	} {
		if got := serialIn(path); got != want {
			t.Errorf("serialIn(%q) = %q, want %q", path, got, want)
		}
	}
}
