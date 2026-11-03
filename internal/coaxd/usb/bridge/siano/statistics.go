// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package siano

import (
	"encoding/binary"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// ISDBTSNR reads the whole-dB SNR from sms_isdbt_stats_ex, without the message
// header or request-result word. RIO images populate this field while leaving
// snr_full_res empty; reception_quality is a separate percentage.
func ISDBTSNR(statistics []byte) device.Measurement {
	const snrOffset = 20
	if len(statistics) < snrOffset+4 {
		return device.Measurement{}
	}
	kind := binary.LittleEndian.Uint32(statistics)
	size := binary.LittleEndian.Uint32(statistics[4:])
	if kind != ModeISDBT && kind != ModeISDBTBDA || size < snrOffset+4 || uint64(size) > uint64(len(statistics)) {
		return device.Measurement{}
	}
	if binary.LittleEndian.Uint32(statistics[12:]) == 0 {
		return device.Measurement{}
	}
	snr := int32(binary.LittleEndian.Uint32(statistics[snrOffset:]))
	if snr <= 0 {
		return device.Measurement{}
	}
	return device.Measured(float64(snr))
}

// ISDBTInBandPower reads the in-band power in dBm from sms_isdbt_stats_ex,
// without the message header or request-result word: the input level, as the
// Linux driver reports the signal strength. It is read once the RF has locked.
func ISDBTInBandPower(statistics []byte) device.Measurement {
	const powerOffset = 28
	if len(statistics) < powerOffset+4 {
		return device.Measurement{}
	}
	kind := binary.LittleEndian.Uint32(statistics)
	size := binary.LittleEndian.Uint32(statistics[4:])
	if kind != ModeISDBT && kind != ModeISDBTBDA || size < powerOffset+4 || uint64(size) > uint64(len(statistics)) {
		return device.Measurement{}
	}
	if binary.LittleEndian.Uint32(statistics[8:]) == 0 {
		return device.Measurement{}
	}
	return device.Measured(float64(int32(binary.LittleEndian.Uint32(statistics[powerOffset:]))))
}
