// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Till0196/coax/pkg/plan"
)

type SatelliteChannel struct {
	// Transponder is the name it was given, tidied: BS01, ND02.
	Transponder string
	// FrequencyKHz is where that transponder is, at the receiver.
	FrequencyKHz int
	// Stream is the place on the transponder, and whether one was named: a
	// position rather than an identifier -- BS01_1 is the transponder's second
	// stream -- which is the numbering the demodulators and the drivers talking to
	// them directly use.
	Stream    int
	HasStream bool
	// StreamID is the transport stream identifier, and whether the name carried
	// one. A name written as a place does not: which stream sits there is in the
	// transmission control block. See ParseSatelliteChannel.
	StreamID    uint16
	HasStreamID bool
}

// ParseSatelliteChannel reads a satellite channel written either way it is
// written: BS01, BS01_1, ND02 and CS4, or a bare transport stream identifier
// such as 16401. Both spellings are in the wild -- recpt1 and its successors
// name a transponder and the stream's place, while the DVB channel files
// distributed for Japan name their sections by the identifier.
//
// What came back says which was written, because the two are not the same
// thing: a place cannot be turned into an identifier, while an identifier
// carries its transponder and place and so says both.
func ParseSatelliteChannel(name string) (SatelliteChannel, error) {
	tidied := strings.ToUpper(strings.TrimSpace(name))
	if channel, ok := satelliteFromStreamID(tidied); ok {
		return channel, nil
	}
	transponder, stream, hasStream := tidied, 0, false
	if head, tail, split := strings.Cut(tidied, "_"); split {
		place, err := strconv.Atoi(tail)
		if err != nil || place < 0 {
			return SatelliteChannel{}, fmt.Errorf("%q does not name a stream after the underscore; write the place on the transponder, as in BS01_1", name)
		}
		transponder, stream, hasStream = head, place, true
	}
	for _, band := range plan.SatelliteBands {
		for _, prefix := range band.Prefixes {
			digits, found := strings.CutPrefix(transponder, prefix)
			if !found || digits == "" {
				continue
			}
			number, err := strconv.Atoi(digits)
			if err != nil {
				continue
			}
			// Every second number, starting where the band starts: a name
			// whose parity is wrong belongs to the other hand of the same
			// satellite and is looked for in a band further down.
			index, inBand := band.Index(number)
			if !inBand {
				continue
			}
			return SatelliteChannel{
				Transponder:  plan.SatelliteTransponderName(prefix, number),
				FrequencyKHz: band.FirstKHz + index*band.SpacingKHz,
				Stream:       stream,
				HasStream:    hasStream,
			}, nil
		}
	}
	return SatelliteChannel{}, fmt.Errorf("%q is not a satellite channel; write BS and an odd number to 23, or ND and an even number to 24, with the stream after an underscore where one is needed, or write the transport stream identifier on its own", name)
}

// Name is the channel as recpt1 writes one: the transponder, and the stream's
// place after an underscore where one was known.
func (channel SatelliteChannel) Name() string {
	if !channel.HasStream {
		return channel.Transponder
	}
	return fmt.Sprintf("%s_%d", channel.Transponder, channel.Stream)
}

// AdvancedService reports whether this is one of the TLV services rather
// than one of the older ones. It matters to anything that would write the
// channel as a place: those streams are selected by identifier on every driver
// that can receive them, so "the first stream on this transponder" would name
// the older service sharing it.
//
// Only an identifier can say -- the network is in its top four bits -- so a
// channel written as a place is taken at face value.
func (channel SatelliteChannel) AdvancedService() bool {
	if !channel.HasStreamID {
		return false
	}
	switch channel.StreamID >> 12 {
	case 11, 12:
		return true
	default:
		return false
	}
}

// SatelliteChannelAt names the channel at a frequency and a stream, for a
// channel whose own name says nothing. The stream is read as an identifier when
// it is one and as a place otherwise, told apart by size: an identifier of any
// of these networks is at least 0x4000 and a place is one of the twelve or so a
// transponder holds. Same division px4_drv makes.
func SatelliteChannelAt(frequencyKHz int, stream string) (SatelliteChannel, bool) {
	if channel, ok := satelliteFromStreamID(strings.TrimSpace(stream)); ok {
		if channel.FrequencyKHz != frequencyKHz {
			// The identifier says where it is, and it is not here: the
			// channel is wrong rather than this reading of it.
			return SatelliteChannel{}, false
		}
		return channel, true
	}
	transponder, ok := satelliteTransponderAt(frequencyKHz)
	if !ok {
		return SatelliteChannel{}, false
	}
	place, err := strconv.Atoi(strings.TrimSpace(stream))
	if err != nil || place < 0 || place >= slotsPerTransponder {
		return SatelliteChannel{}, false
	}
	return SatelliteChannel{
		Transponder:  transponder,
		FrequencyKHz: frequencyKHz,
		Stream:       place,
		HasStream:    true,
	}, true
}

// slotsPerTransponder is how many places a transponder holds, which is what
// separates a place from an identifier when a channel writes one.
const slotsPerTransponder = 12

func satelliteTransponderAt(frequencyKHz int) (string, bool) {
	transponder, ok := plan.SatelliteTransponderAt(frequencyKHz)
	if !ok {
		return "", false
	}
	return transponder.Name, true
}

// satelliteFromStreamID reads a transport stream identifier as the channel it identifies.
// ARIB lays it out as the network it belongs to, the transponder it is on, and its place
// on that transponder:
//
//	 15    12 11         9 8        4 3        0
//	+--------+------------+----------+----------+
//	| network|            |transponder|  place  |
//	+--------+------------+----------+----------+
//
// -- so 0x4011 is the broadcasting network, transponder 1, second stream: BS01_1. The
// middle bits belong to whoever operates the stream and vary between streams on one
// transponder, which is why BS03 carries 0x4030 beside 0x4631.
//
// The network says which family of transponders to look in: 4 and 11 are the broadcasting
// satellites and their advanced service, 6, 7 and 12 the communications satellites and
// theirs. Odd and even numbers belong to opposite hands of the same satellite, and a
// number of the wrong parity for one band is found in the band below it.
//
// Checked against every section of the ISDB-S channel file distributed for Japan.
func satelliteFromStreamID(name string) (SatelliteChannel, bool) {
	number, err := strconv.Atoi(name)
	if err != nil || number < 0 || number > 0xffff {
		return SatelliteChannel{}, false
	}
	identifier := uint16(number)
	var prefix string
	switch identifier >> 12 {
	case 4, 11:
		prefix = "BS"
	case 6, 7, 12:
		prefix = "ND"
	default:
		return SatelliteChannel{}, false
	}
	transponder := int(identifier>>4) & 0x1f
	place := int(identifier & 0x0f)
	for _, band := range plan.SatelliteBands {
		if !band.Carries(prefix) {
			continue
		}
		index, inBand := band.Index(transponder)
		if !inBand {
			continue
		}
		return SatelliteChannel{
			Transponder:  plan.SatelliteTransponderName(prefix, transponder),
			FrequencyKHz: band.FirstKHz + index*band.SpacingKHz,
			Stream:       place,
			HasStream:    true,
			StreamID:     identifier,
			HasStreamID:  true,
		}, true
	}
	return SatelliteChannel{}, false
}
