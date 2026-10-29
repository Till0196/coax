// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"encoding/binary"
	"fmt"

	"github.com/Till0196/coax/pkg/win32"
)

// Digital Devices' private interface on a BDA tuner.
//
// The driver publishes filters for enabled standards, but a connected transport
// pin can also select a supported standard for its current graph. This does not
// change the persistent filter mask.

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

// demodulatorSet belongs to node 1 on the connected TS output pin. Requests to
// the filter itself or its RF input do not reach this node.
var demodulatorSet = win32.GUID{
	Data1: 0x0aa8a605, Data2: 0xa240, Data3: 0x11de,
	Data4: [8]byte{0xb1, 0x30, 0, 0, 0, 0, 0x4d, 0x56},
}

type nodeWriter interface {
	SetNode(win32.GUID, uint32, uint32, []byte) error
}

// SelectStandard changes this graph's acquisition standard before tuning.
func SelectStandard(control nodeWriter, mode DemodMode) error {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], uint32(mode))
	return control.SetNode(demodulatorSet, 0, 1, data[:])
}

// SelectStream selects a TMCC slot (0..11) or absolute TSID on ISDB-S, or an input stream on
// DVB-S2. An unspecified stream selects the first ISDB-S stream and resets
// DVB-S2 filtering. Cable capture keeps the complete multiplex, including TSMF.
func SelectStream(control nodeWriter, mode DemodMode, streamID int32) error {
	value := uint32(0xffffffff)
	if streamID < -1 {
		return fmt.Errorf("invalid DD stream selector %d", streamID)
	}
	switch mode {
	case ModeISDBS:
		if streamID > 0xffff {
			return fmt.Errorf("ISDB-S TSID exceeds 16 bits")
		}
		value = 0
		if streamID >= 0 {
			value = uint32(streamID)
			if streamID >= 12 {
				value |= 0x10000
			}
		}
	case ModeDVBS, ModeDVBS2, ModeDVBS2X:
		if streamID > 255 {
			return fmt.Errorf("DVB-S2 input stream ID exceeds 8 bits")
		}
		if streamID >= 0 {
			value = uint32(streamID)
		}
	case ModeDVBT2:
		if streamID >= 0 {
			value = uint32(streamID) & 255
		}
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], value)
		return control.SetNode(win32.GUID{Data1: 0xef30f379, Data2: 0x985b, Data3: 0x4d10, Data4: [8]byte{0xb6, 0x40, 0xa7, 0x9d, 0x5e, 4, 0xe1, 0xe0}}, 12, 1, data[:])
	case ModeDVBC2:
		if streamID > 0xffff {
			return fmt.Errorf("DVB-C2 data slice ID exceeds 16 bits")
		}
		if streamID >= 0 {
			value = uint32(streamID)
		}
	default:
		return nil
	}
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return control.SetNode(demodulatorSet, 1, 1, data[:])
}
