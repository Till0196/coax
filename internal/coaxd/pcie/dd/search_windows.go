// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Turning a channel into a command. Every standard has its own parameter block
// but they mostly agree: a flags byte, a bandwidth code, a retry count and a
// frequency, with an identifier added where several streams share a carrier.

// Offsets from the start of a command's parameter area, shared by the blocks.
const (
	searchFlags     = 0
	searchBandwidth = 1
	searchRetry     = 3
	searchFrequency = 4
)

// searchFor builds the command that receives a channel. The card wants hertz
// for every standard, including the satellite ones a channel file writes in
// kilohertz; BuildTuneProfile has already made the value uniform.
func searchFor(properties map[string]string) (*mciCommand, error) {
	profile, err := bda.BuildDrivenProfile(properties)
	if err != nil {
		return nil, err
	}
	frequencyHz := uint32(profile.FrequencyKHz) * 1000

	command := &mciCommand{}
	command.putUint32(searchFrequency, frequencyHz)

	switch tuning.Normalize(properties["DELIVERY_SYSTEM"]) {
	case "ISDBS":
		command.command = cmdSearchISDBS
		// The stream identifier is a transport stream id here, which is what
		// clearing bit 0 of the flags says.
		if streamID, ok := streamIDOf(properties); ok {
			command.putUint16(14, uint16(streamID))
		} else {
			command.params[searchFlags] = 1
		}
	case "ISDBT":
		command.command = cmdSearchISDBT
		command.params[searchBandwidth], err = bandwidthCode(properties, 6)
	case "ISDBC":
		command.command = cmdSearchISDBC
		command.params[searchBandwidth], err = bandwidthCode(properties, 6)
		if streamID, ok := streamIDOf(properties); ok {
			command.putUint16(14, uint16(streamID))
		} else {
			command.params[searchFlags] = 1
		}
	case "DVBS", "DVBS2":
		command.command = cmdSearchDVBS
		// Both generations enabled: the firmware works out which it found.
		command.params[searchFlags] = 0x03
		symbolRate, rateErr := tuning.Number(properties, "SYMBOL_RATE", true)
		if rateErr != nil {
			return nil, rateErr
		}
		command.putUint32(8, uint32(symbolRate))
		if streamID, ok := streamIDOf(properties); ok {
			command.params[searchFlags] |= 0x80
			command.params[12] = byte(streamID)
		}
	case "DVBT":
		command.command = cmdSearchDVBT
		command.params[searchBandwidth], err = bandwidthCode(properties, 8)
	case "DVBT2":
		command.command = cmdSearchDVBT2
		command.params[searchBandwidth], err = bandwidthCode(properties, 8)
		if plp, plpErr := tuning.PLP(properties); plpErr == nil && plp >= 0 {
			command.params[searchFlags] |= 0x80
			command.params[12] = byte(plp)
		}
	case "DVBC", "DVBC/ANNEXA", "DVBC/ANNEXC", "ISDBC/ANNEXC":
		command.command = cmdSearchDVBC
		command.params[searchBandwidth], err = bandwidthCode(properties, 8)
	case "DVBC/ANNEXB", "J83B":
		command.command = cmdSearchJ83B
		command.params[searchBandwidth], err = bandwidthCode(properties, 6)
	case "DVBC2":
		command.command = cmdSearchDVBC2
		command.params[searchBandwidth], err = bandwidthCode(properties, 8)
	default:
		return nil, fmt.Errorf("this backend does not support %q",
			properties["DELIVERY_SYSTEM"])
	}
	if err != nil {
		return nil, err
	}
	return command, nil
}

func streamIDOf(properties map[string]string) (uint64, bool) {
	value, err := tuning.Number(properties, "STREAM_ID", false)
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

// bandwidthCode turns a channel width into the code a search takes. The
// firmware takes a code rather than a figure, so a width it has no code for is
// refused rather than silently rounded.
func bandwidthCode(properties map[string]string, fallbackMHz uint32) (byte, error) {
	megahertz := fallbackMHz
	if hertz, err := tuning.BandwidthHz(properties); err == nil && hertz > 0 {
		megahertz = uint32(hertz / 1_000_000)
		if hertz == 1_712_000 {
			return bandwidth1_7MHz, nil
		}
	}
	switch megahertz {
	case 5:
		return bandwidth5MHz, nil
	case 6:
		return bandwidth6MHz, nil
	case 7:
		return bandwidth7MHz, nil
	case 8:
		return bandwidth8MHz, nil
	case 10:
		return bandwidth10MHz, nil
	}
	return 0, fmt.Errorf("the card has no setting for a %d MHz channel", megahertz)
}
