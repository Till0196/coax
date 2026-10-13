// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package chardev

import (
	"testing"
	"unsafe"
)

// The request numbers are what the driver switches on, and a wrong one is not
// a wrong answer but no answer: the driver's default branch reports the ioctl
// does not exist. They are pinned here against the values the C macros in
// px4_drv's include/ptx_ioctl.h expand to. PTX_GET_CNR is declared over a
// pointer, so its number differs between a 64-bit and a 32-bit build.
func TestRequestNumbersMatchTheDriverHeader(t *testing.T) {
	cnr := uintptr(0x80088d04)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		cnr = 0x80048d04
	}
	for _, one := range []struct {
		name    string
		request uintptr
		want    uintptr
	}{
		{"PTX_SET_CHANNEL", setChannelRequest, 0x40088d01},
		{"PTX_START_STREAMING", startRequest, 0x00008d02},
		{"PTX_STOP_STREAMING", stopRequest, 0x00008d03},
		{"PTX_GET_CNR", cnrRequest, cnr},
		{"PTX_ENABLE_LNB_POWER", enableLNBRequest, 0x40048d05},
		{"PTX_DISABLE_LNB_POWER", disableLNBRequest, 0x00008d06},
		{"PTX_SET_SYSTEM_MODE", systemModeRequest, 0x40048d0b},
	} {
		if one.request != one.want {
			t.Errorf("%s = %#x, want %#x", one.name, one.request, one.want)
		}
	}
}
