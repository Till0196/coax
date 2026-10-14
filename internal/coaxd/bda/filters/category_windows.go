// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package filters

import "github.com/Till0196/coax/pkg/win32"

var (
	// CategoryNetworkTuner is the KS category every BDA tuner registers in.
	CategoryNetworkTuner = win32.GUID{Data1: 0x71985f48, Data2: 0x1ca1, Data3: 0x11d3, Data4: [8]byte{0x9c, 0xc8, 0x00, 0xc0, 0x4f, 0x79, 0x71, 0xe0}}
	// CategoryReceiverComponent is the KS category the demodulator side of a BDA
	// card registers in; a card with several demodulators publishes one each.
	CategoryReceiverComponent = win32.GUID{Data1: 0xfd0a5af4, Data2: 0xb41d, Data3: 0x11d2, Data4: [8]byte{0x9c, 0x95, 0x00, 0xc0, 0x4f, 0x79, 0x71, 0xe0}}

	clsidSystemDeviceEnum = win32.GUID{Data1: 0x62be5d10, Data2: 0x60eb, Data3: 0x11d0, Data4: [8]byte{0xbd, 0x3b, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86}}
)
