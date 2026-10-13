// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import "github.com/Till0196/coax/internal/coaxd/win32"

// Digital Devices' private interface on a BDA tuner.
//
// These cards do not switch standard: they offer one BDA filter per standard (a
// four-tuner MAX M4 with everything enabled shows thirty-six), and opening one
// picks the standard. Which filters exist is decided at driver start from a
// per-tuner bitmask in its registry key, which the first set below reads and
// writes.
//
// The driver also carries a per-tune standard override on the filter's
// demodulator node, but it is unusable: measured on a MAX M4, every node-scoped
// set answers ERROR_SET_NOT_FOUND through the filter while a filter-scoped set
// answers normally.

// Set is the Digital Devices property set the tuner filter itself answers,
// as opposed to the ones on its nodes.
var Set = win32.GUID{
	Data1: 0x0aa8a502, Data2: 0xa240, Data3: 0x11de,
	Data4: [8]byte{0xb1, 0x30, 0x00, 0x00, 0x00, 0x00, 0x4d, 0x56},
}

// StatisticsSet answers with what the demodulator is measuring, and is the
// reason a C/N ratio can be reported while the driver carries a stream. The only
// other route to that number is the card's command mailbox, which the driver is
// also using and cannot share: reaching it mid-stream stops the stream (measured
// on a MAX M4, one tuner polled every 50ms delivered no bytes at all).
var StatisticsSet = win32.GUID{
	Data1: 0x0aa8a606, Data2: 0xa240, Data3: 0x11de,
	Data4: [8]byte{0xb1, 0x30, 0x00, 0x00, 0x00, 0x00, 0x4d, 0x56},
}

const (
	// ddPropTunerNumber is which tuner of the card this filter is, counted
	// from one, with the card's own index in the high half.
	ddPropTunerNumber = 0
	// ddPropDescriptor is sixty bytes describing the filter: the card it is
	// on, the demodulator node it carries, its own device interface reference
	// string, and the standards the tuner can be made to receive.
	ddPropDescriptor = 3
	// ddPropFilterMask is the standards this tuner currently offers a filter
	// for. Writing it saves the whole card's masks to the driver's registry
	// key as one binary value of eight entries, and the filters follow at the
	// next device start rather than immediately.
	ddPropFilterMask = 4

	ddDescriptorBytes = 60
	// ddSupportedOffset is where the supported-standards mask sits in the
	// descriptor.
	ddSupportedOffset = 44
)

const (
	// ddStatCNR is the carrier-to-noise ratio in tenths of a decibel. On a
	// terrestrial channel the mailbox called 28.0 dB it answers 285, and on a
	// cable channel the mailbox called 38.2 dB it answers 380.
	ddStatCNR = 1
	// ddStatQuality is the driver's own 0..100 figure.
	ddStatQuality = 6
	// ddStatLevel is the input level in tenths of a dBuV. The mailbox reports
	// dBm, and the two agree within half a decibel once the 75-ohm conversion
	// is applied: 766 is 76.6 dBuV, which is -32.1 dBm against a mailbox
	// reading of -32.6.
	ddStatLevel = 7
	// ddStatSymbolRate is symbols per second, and zero for a standard that
	// has none. A cable channel at 5.274 MSym answers 5274036.
	ddStatSymbolRate = 9

	// dBuVToDBm is the difference between the two scales in 75 ohms.
	dBuVToDBm = 108.75
)
