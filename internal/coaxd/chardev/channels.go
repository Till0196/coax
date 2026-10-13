// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package chardev drives the tuners px4_drv publishes as character devices.
//
// The interface is not the Linux DVB API but the driver's own "ptx" ioctls, the
// ones recpt1 speaks. PTX_SET_CHANNEL takes a freq_no indexing a table the
// driver holds rather than a frequency, so those tables are inverted here.
package chardev

import (
	"errors"
	"fmt"
)

// ErrNoChannel reports that no channel of the driver's table sits at the
// frequency asked for. The caller turns it into the receiver package's "out of
// range" rather than a failure.
var ErrNoChannel = errors.New("no px4_drv channel number covers that frequency")

// The ISDB-T table px4_drv holds: three runs of channel numbers, each an affine
// function of freq_no in kilohertz, with one correction -- the last channel of
// the low cable run sits 2 MHz above where its run would put it. The numbers
// are the driver's, not the broadcaster's: cable channel C13 is freq_no 3 and
// terrestrial channel 13 is freq_no 63.
const (
	cableLowFirst  = 3
	cableLowLast   = 12
	cableHighFirst = 22
	cableHighLast  = 62
	uhfFirst       = 63
	uhfLast        = 112

	cableZeroKHz = 93_143
	uhfZeroKHz   = 95_143
	channelStep  = 6_000

	// cableLowLastOffsetKHz is the correction the driver applies to C22.
	cableLowLastOffsetKHz = 2_000
)

// The ISDB-S table px4_drv holds: twelve BS transponders and twelve CS ones,
// each run evenly spaced in the first intermediate frequency.
//
// The PT1/PT2 driver has a third run, the twelve odd-numbered CS transponders,
// which px4_drv and the PT3 driver do not reach: the PT3 driver's tuner table
// stops at twenty-four, so asking for one reads off the end of it.
const (
	satelliteBSCount    = 12
	satelliteCSCount    = 12
	satelliteOddCSCount = 12
	bsZeroKHz           = 1_049_480
	bsStepKHz           = 38_360
	csZeroKHz           = 1_613_000
	csStepKHz           = 40_000
	oddCSZeroKHz        = 1_593_000
)

// terrestrialToleranceKHz is how far from a channel's centre a request may sit
// and still name that channel: half a channel, halved again because the offset
// is carried through to the driver rather than rounded away.
const terrestrialToleranceKHz = 3_000

// satelliteToleranceKHz is the same for satellite, far below either run's
// spacing: transponder frequencies are fixed and there is no offset to carry.
const satelliteToleranceKHz = 2_000

// TerrestrialChannel finds the px4_drv channel number covering a frequency, and
// how far the frequency sits from that channel's centre.
//
// The offset is not an error to be rounded away: PTX_SET_CHANNEL carries it in
// the slot field, which the driver adds to the frequency it looked up, so a
// channel file naming 473.142857 MHz is tuned to 473.143 MHz.
//
// # Reaching the channels px4_drv has no number for
//
// The numbering has two holes -- numbers 0..2 and 13..21, the two VHF stretches
// -- which PTX_SET_CHANNEL refuses because the table lacks them, not because
// the hardware cannot go there. So a VHF channel is reached from the nearest
// number the driver does have, with the whole difference in the offset field:
// the driver computes table + slot in kilohertz, does not bound the slot on the
// ISDB-T path, and the field is signed. The worst offset needed is 28 MHz, VHF
// 8 reached from C22.
func TerrestrialChannel(frequencyKHz int64) (channel int, offsetKHz int, err error) {
	// The Earthsoft numbering is the same plan complete, so it decides whether
	// a frequency names a channel at all.
	if _, err := earthsoftTerrestrialChannel(frequencyKHz, pt1LastChannel); err != nil {
		return 0, 0, err
	}
	best, distance := -1, int64(0)
	for candidate := cableLowFirst; candidate <= uhfLast; candidate++ {
		centre, ok := terrestrialCentreKHz(candidate)
		if !ok {
			continue
		}
		away := frequencyKHz - centre
		if away < 0 {
			away = -away
		}
		if best < 0 || away < distance {
			best, distance = candidate, away
		}
	}
	if best < 0 {
		return 0, 0, fmt.Errorf("%w: %d kHz is not within %d kHz of an ISDB-T channel",
			ErrNoChannel, frequencyKHz, terrestrialToleranceKHz)
	}
	centre, _ := terrestrialCentreKHz(best)
	return best, int(frequencyKHz - centre), nil
}

// terrestrialCentreKHz is the frequency the driver tunes for a channel number,
// and false for a number between the runs, which the driver refuses.
func terrestrialCentreKHz(channel int) (int64, bool) {
	switch {
	case channel >= cableLowFirst && channel <= cableLowLast:
		centre := int64(cableZeroKHz + channel*channelStep)
		if channel == cableLowLast {
			centre += cableLowLastOffsetKHz
		}
		return centre, true
	case channel >= cableHighFirst && channel <= cableHighLast:
		return int64(cableZeroKHz + channel*channelStep), true
	case channel >= uhfFirst && channel <= uhfLast:
		return int64(uhfZeroKHz + channel*channelStep), true
	default:
		return 0, false
	}
}

// SatelliteChannel finds the ISDB-S channel number at a first intermediate
// frequency, among the transponders px4_drv and the PT3 driver reach. There is
// no offset: on the satellite path these drivers read the slot field as the
// stream to take.
func SatelliteChannel(frequencyKHz int64) (int, error) {
	return satelliteChannel(frequencyKHz, satelliteBSCount+satelliteCSCount)
}

// PT1SatelliteChannel is the same for the PT1 and PT2 driver, which reaches
// the odd-numbered CS transponders as well.
func PT1SatelliteChannel(frequencyKHz int64) (int, error) {
	return satelliteChannel(frequencyKHz, satelliteBSCount+satelliteCSCount+satelliteOddCSCount)
}

func satelliteChannel(frequencyKHz int64, count int) (int, error) {
	best, distance := -1, int64(0)
	for candidate := 0; candidate < count; candidate++ {
		centre, ok := satelliteCentreKHz(candidate)
		if !ok {
			continue
		}
		away := frequencyKHz - centre
		if away < 0 {
			away = -away
		}
		if best < 0 || away < distance {
			best, distance = candidate, away
		}
	}
	if best < 0 || distance > satelliteToleranceKHz {
		return 0, fmt.Errorf("%w: %d kHz is not within %d kHz of an ISDB-S transponder",
			ErrNoChannel, frequencyKHz, satelliteToleranceKHz)
	}
	return best, nil
}

func satelliteCentreKHz(channel int) (int64, bool) {
	switch {
	case channel >= 0 && channel < satelliteBSCount:
		return int64(bsZeroKHz + channel*bsStepKHz), true
	case channel < satelliteBSCount+satelliteCSCount:
		return int64(csZeroKHz + (channel-satelliteBSCount)*csStepKHz), true
	case channel < satelliteBSCount+satelliteCSCount+satelliteOddCSCount:
		return int64(oddCSZeroKHz + (channel-satelliteBSCount-satelliteCSCount)*csStepKHz), true
	default:
		return 0, false
	}
}

// KilohertzOf folds a channel file's FREQUENCY down to the kilohertz the driver
// works in, rounding rather than truncating: ISDB-T carriers sit at a 1/7 MHz
// offset, so truncating would detune every terrestrial channel by a kilohertz
// via the offset field above. A value already small enough to be kilohertz is
// left alone.
func KilohertzOf(frequency uint64) int64 {
	if frequency < 10_000_000 {
		return int64(frequency)
	}
	return int64((frequency + 500) / 1000)
}

// The Earthsoft drivers' ISDB-T numbering: the same as px4_drv's above --
// terrestrial channel 13 is number 63 in all three -- but covering the VHF
// channels px4_drv has no number for, as numbers 0..2 and 13..21.
//
// The arithmetic below is the PT1 driver's and reproduces the PT3 driver's
// 112-constant table exactly. They stop in different places: the PT3 table ends
// at 111, while the PT1 driver reaches terrestrial channel 62. Both refuse
// anything past their end.
const (
	earthsoftFirstChannel = 0
	pt3LastChannel        = 111
	pt1LastChannel        = 112
	earthsoftZeroKHz      = 93_143
)

// earthsoftOffsetKHz is the correction both drivers carry. It shifts twice and
// back once across the range, which is what puts the VHF and cable runs where
// broadcasting actually has them.
func earthsoftOffsetKHz(channel int) int64 {
	offset := int64(0)
	if channel >= 12 {
		offset += 2_000
	}
	if channel >= 17 {
		offset -= 2_000
	}
	if channel >= 63 {
		offset += 2_000
	}
	return offset
}

// earthsoftCentreKHz is the frequency these drivers tune for a channel number,
// up to the last one either of them has.
func earthsoftCentreKHz(channel, last int) (int64, bool) {
	if channel < earthsoftFirstChannel || channel > last {
		return 0, false
	}
	return int64(earthsoftZeroKHz+channel*channelStep) + earthsoftOffsetKHz(channel), true
}

// PT3TerrestrialCentreKHz is the frequency the PT3 driver tunes for a channel
// number.
func PT3TerrestrialCentreKHz(channel int) (int64, bool) {
	return earthsoftCentreKHz(channel, pt3LastChannel)
}

// PT1TerrestrialCentreKHz is the same for the PT1 and PT2 driver, which
// reaches one channel further.
func PT1TerrestrialCentreKHz(channel int) (int64, bool) {
	return earthsoftCentreKHz(channel, pt1LastChannel)
}

// PT3TerrestrialChannel finds the ISDB-T channel number covering a frequency.
// Unlike px4_drv's this takes no offset: the PT3 driver reads the slot field for
// satellite only, so a request must name a channel, not a point near one.
func PT3TerrestrialChannel(frequencyKHz int64) (int, error) {
	return earthsoftTerrestrialChannel(frequencyKHz, pt3LastChannel)
}

// PT1TerrestrialChannel is the same for the PT1 and PT2 driver. That driver
// does read the slot field on the terrestrial path, but adds it in whole
// megahertz, too coarse to be worth carrying, so it is left at zero.
func PT1TerrestrialChannel(frequencyKHz int64) (int, error) {
	return earthsoftTerrestrialChannel(frequencyKHz, pt1LastChannel)
}

func earthsoftTerrestrialChannel(frequencyKHz int64, last int) (int, error) {
	best, distance := -1, int64(0)
	for candidate := earthsoftFirstChannel; candidate <= last; candidate++ {
		centre, ok := earthsoftCentreKHz(candidate, last)
		if !ok {
			continue
		}
		away := frequencyKHz - centre
		if away < 0 {
			away = -away
		}
		if best < 0 || away < distance {
			best, distance = candidate, away
		}
	}
	if best < 0 || distance > terrestrialToleranceKHz {
		return 0, fmt.Errorf("%w: %d kHz is not within %d kHz of an ISDB-T channel",
			ErrNoChannel, frequencyKHz, terrestrialToleranceKHz)
	}
	return best, nil
}
