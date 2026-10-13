// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/Till0196/coax/pkg/plan"
	"github.com/Till0196/coax/pkg/report"
)

// Where a channel sits in the numbering a BonDriver hands out, which is what
// both of the Windows formats are written against.
//
// A BonDriver offers channels as a space and an index into it, and neither
// number means anything on its own: the driver reads a channel definition file
// and its lines become that enumeration, in order. A TVTest channel file then
// refers to the same two numbers.
//
// So the two files are not independent. A .ch2 written against a different
// ordering than the ChSet.txt the driver loaded names the wrong channels --
// silently, because the numbers are all valid. They are worked out once, here,
// and both writers render the same answer.

// spaceOrder is the order the spaces appear in a file, and so the order their
// identifiers are given out in. Fixed, so that scanning the same feed twice
// writes the same file.
var spaceOrder = []string{"GR", "BS", "CS", "BS4K", "CS4K", "SKY", "CATV", "OTHER"}

// placedSpace is one channel space, with its channels in the order they are
// numbered. A channel's index in this slice is the index the driver will
// enumerate it at.
type placedSpace struct {
	name     string
	channels []placedChannel
}

// placedChannel is one line of a channel definition file: one carrier, or one
// stream of a multiplexed carrier.
type placedChannel struct {
	space string
	name  string
	// ptxCh is the physical channel number, which is what the driver turns
	// back into a frequency.
	ptxCh int
	// selector is the TSID column of a channel definition file: which stream of
	// the carrier to take, zero where there is no choosing to do. It is not the
	// stream's identifier -- that is in stream, and a carrier holding one stream
	// has a real identifier there.
	selector uint16
	// numbered marks a name still missing the stream's place on its physical
	// channel. A multiplexing frame names its own streams; a satellite transponder
	// does not, and its streams are one scanned channel each.
	numbered bool
	// stream is what this channel carries, for a format with room to say.
	stream carriedStream
}

// place works out where every channel of one delivery system sits. Results the
// mapping cannot place are returned rather than dropped, since the answer is
// usually to give the physical channel a number by hand.
func place(system string, results []report.Result) (spaces []*placedSpace, skipped []string) {
	var all []placedChannel
	for result := range foundIn(results) {
		if found, ok := chsetSystem(result.Properties["DELIVERY_SYSTEM"]); !ok || found != system {
			continue
		}
		placed, reason := placeResult(system, result)
		if reason != "" {
			skipped = append(skipped, fmt.Sprintf("%s: %s", result.Name, reason))
			continue
		}
		all = append(all, placed...)
	}
	numberStreams(all)
	if system == SystemTerrestrial {
		all = append(all, unscannedTerrestrial(all)...)
	}

	gathered := map[string]*placedSpace{}
	for _, channel := range all {
		space := gathered[channel.space]
		if space == nil {
			space = &placedSpace{name: channel.space}
			gathered[channel.space] = space
		}
		space.channels = append(space.channels, channel)
	}
	for _, name := range spaceOrder {
		space := gathered[name]
		if space == nil {
			continue
		}
		// Sorted by physical channel, then by the stream within it, so that
		// the file reads in the order somebody tunes through it.
		slices.SortStableFunc(space.channels, func(left, right placedChannel) int {
			if left.ptxCh != right.ptxCh {
				return cmp.Compare(left.ptxCh, right.ptxCh)
			}
			return strings.Compare(left.name, right.name)
		})
		spaces = append(spaces, space)
	}
	return spaces, skipped
}

// unscannedTerrestrial is the rest of the television plan: every physical
// channel the scan did not find something on.
//
// A channel definition file is a plan rather than a report: the driver turns a
// physical channel number back into a frequency and tunes it, and BonDriver_PX4
// ships one listing all 113 channels with nothing said about any of them, so a
// channel the scan missed can still be tuned. An empty channel is a name and a
// number and nothing else.
//
// Only the terrestrial file is filled. On a cable the same channels carry
// whatever the operator put there, and on a satellite a transponder that
// answered nothing has no streams to name.
func unscannedTerrestrial(placed []placedChannel) []placedChannel {
	found := make(map[int]bool, len(placed))
	for _, channel := range placed {
		found[channel.ptxCh] = true
	}
	var empty []placedChannel
	for _, carrier := range plan.Terrestrial() {
		ptxCh, known := plan.PTXChannel(carrier.Name)
		if !known || found[ptxCh] {
			continue
		}
		empty = append(empty, placedChannel{
			space: "GR",
			// Named as the scanned ones are; only the analysis is missing.
			name:  carrier.Name + terrestrialNameSuffix,
			ptxCh: ptxCh,
		})
	}
	return empty
}

func placeResult(system string, result report.Result) ([]placedChannel, string) {
	streams, multiplexed := carriedStreams(result)
	if len(streams) == 0 {
		return nil, "nothing identifiable was found in it"
	}
	if system == SystemSatellite || system == SystemDVBS2 {
		return placeSatellite(system, result, streams)
	}
	ptxCh, ok := cablePTXChannel(system, result)
	if !ok {
		return nil, fmt.Sprintf("%q is not a channel this can give a physical channel to", result.Name)
	}
	placed := make([]placedChannel, 0, len(streams))
	for _, stream := range streams {
		space := chsetSpaceName(system, stream.networkID)
		if space == "" {
			continue
		}
		if !multiplexed {
			// A carrier holding one stream is one channel, so the TSID column
			// is left at zero -- what that column means with no choice to
			// make.
			placed = append(placed, placedChannel{
				space: space, name: soleStreamName(system, result),
				ptxCh: ptxCh, stream: stream,
			})
			continue
		}
		// A stream of a multiplex is named for the way in: the channel it
		// arrived on and its number within that channel's frame, which is
		// also what has to be asked for to receive it.
		placed = append(placed, placedChannel{
			space: space, name: fmt.Sprintf("%s/TS%d", carrierName(system, result), stream.relative),
			ptxCh: ptxCh, selector: stream.streamID, stream: stream,
		})
	}
	if len(placed) == 0 {
		return nil, "it carries nothing that belongs to a channel space"
	}
	return placed, ""
}

func placeSatellite(system string, result report.Result, streams []carriedStream) ([]placedChannel, string) {
	ptxCh, name, ok := satellitePTXChannel(system, result.Properties["FREQUENCY"])
	if !ok {
		return nil, fmt.Sprintf("%s kHz is not a transponder this knows a physical channel for",
			result.Properties["FREQUENCY"])
	}
	placed := make([]placedChannel, 0, len(streams))
	for _, stream := range streams {
		space := chsetSpaceName(system, stream.networkID)
		if space == "" {
			continue
		}
		placed = append(placed, placedChannel{
			space: space,
			// The transponder, with the stream's place on it added once every
			// stream of that transponder has been seen -- how a satellite
			// channel has always been written.
			name:     name,
			ptxCh:    ptxCh,
			selector: stream.streamID,
			numbered: true,
			stream:   stream,
		})
	}
	if len(placed) == 0 {
		return nil, "it carries nothing that belongs to a channel space"
	}
	return placed, ""
}

// numberStreams gives each stream its place on its physical channel, in order
// of transport stream identifier rather than scan order, so that a scan which
// lost one still names the rest the same way and two scans of one feed write
// the same file.
func numberStreams(placed []placedChannel) {
	byChannel := map[int][]int{}
	for index, channel := range placed {
		if channel.numbered {
			byChannel[channel.ptxCh] = append(byChannel[channel.ptxCh], index)
		}
	}
	for _, indices := range byChannel {
		if len(indices) == 1 {
			// A transponder carrying one stream is named by itself: saying
			// TS0 of one would only invite the question of where TS1 went.
			placed[indices[0]].numbered = false
			continue
		}
		slices.SortStableFunc(indices, func(left, right int) int {
			return cmp.Compare(placed[left].stream.streamID, placed[right].stream.streamID)
		})
		for place, index := range indices {
			placed[index].name = fmt.Sprintf("%s/TS%d", placed[index].name, place)
			placed[index].numbered = false
		}
	}
}

// chsetSpaceName is the space a stream belongs in, from its original network
// identifier, which says the same thing however the stream arrived -- BS
// carried over a cable transmodulation declares the network BS does. What it
// does not cover is a cable operator's own carriage, which has no network of
// its own.
func chsetSpaceName(system string, networkID uint16) string {
	space := channelTypeForNetwork(networkID)
	switch space {
	case "":
		return ""
	case "GR":
		return "GR"
	case "OTHER":
		// A network nobody recognises, arriving over a cable, is that cable
		// operator's own carriage under an identifier from outside the block they are
		// supposed to name themselves from. This deliberately does not catch SKY.
		if system == SystemCable {
			return "CATV"
		}
	}
	return space
}
