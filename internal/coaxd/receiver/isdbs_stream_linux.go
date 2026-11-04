// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"fmt"
	"maps"
	"strconv"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// isdbsStreams is how a driver reads DTV_STREAM_ID on ISDB-S. The API calls it
// the transport stream identifier, and a channel names a stream either that way
// or by its place on the transponder, a value below relativeStreamLimit. The
// drivers do not agree on what to make of the second.
type isdbsStreams int

const (
	// isdbsStreamsAsGiven is a driver that tells the two apart itself.
	isdbsStreamsAsGiven isdbsStreams = iota
	// isdbsIdentifiersOnly writes the value to the demodulator as an identifier
	// whatever it is. No stream has an identifier that small, so a place
	// selects nothing and the demodulator falls back to its first stream:
	// right for place zero and wrong for every other.
	isdbsIdentifiersOnly
	// isdbsMarkedIdentifiers reads a value that fits in sixteen bits as a place
	// and takes only its low three bits, so an identifier has to carry a bit
	// above them. An unmarked identifier whose low bits are not its place
	// selects another stream, or none.
	isdbsMarkedIdentifiers
)

const (
	// relativeStreamLimit is where a place ends and an identifier begins, as
	// the channel parser writes them.
	relativeStreamLimit = 12
	// tmccStreams is how many places the transmission control block has.
	tmccStreams = 8
	// markedIdentifier is the bit that makes ddbridge read the value as an
	// identifier.
	markedIdentifier = 0x10000
)

// isdbsStreamsFor says how the driver behind a frontend reads the value, from
// the kernel driver's name and the frontend's. relative says the drivers that
// would take a place as an identifier have been changed to read it as a place,
// which nothing on the frontend tells.
func isdbsStreamsFor(driver, frontend string, relative bool) isdbsStreams {
	switch {
	case driver == "ddbridge":
		// Every MCI card with an ISDB-S demodulator, the MAX M4, its second
		// version and the M2, is tuned through the same search command.
		return isdbsMarkedIdentifiers
	case frontend == "Toshiba TC90522 ISDB-S module", frontend == "Socionext MN88443x":
		if relative {
			return isdbsStreamsAsGiven
		}
		return isdbsIdentifiersOnly
	}
	return isdbsStreamsAsGiven
}

// isdbsStreamProperties rewrites STREAM_ID for the driver. Anything that is
// not an ISDB-S channel naming a stream is returned as it came.
func isdbsStreamProperties(kind isdbsStreams, frontend string, properties map[string]string) (map[string]string, error) {
	if kind == isdbsStreamsAsGiven || tuning.Normalize(properties["DELIVERY_SYSTEM"]) != "ISDBS" {
		return properties, nil
	}
	if _, named := properties["STREAM_ID"]; !named {
		return properties, nil
	}
	value, err := tuning.Number(properties, "STREAM_ID", false)
	if err != nil {
		return nil, err
	}
	switch kind {
	case isdbsIdentifiersOnly:
		if value != 0 && value < relativeStreamLimit {
			return nil, fmt.Errorf("%s takes STREAM_ID only as a transport stream identifier, so stream %d cannot be chosen by its place on the transponder; give the identifier instead, or start coaxd with --dvb-relative-streams for a driver changed to read it as a place",
				frontend, value)
		}
		return properties, nil
	case isdbsMarkedIdentifiers:
		switch {
		case value < tmccStreams, value > 0xffff:
			return properties, nil
		case value < relativeStreamLimit:
			return nil, fmt.Errorf("STREAM_ID %d is past the %d places of an ISDB-S transponder", value, tmccStreams)
		}
		marked := maps.Clone(properties)
		marked["STREAM_ID"] = strconv.FormatUint(value|markedIdentifier, 10)
		return marked, nil
	}
	return properties, nil
}
