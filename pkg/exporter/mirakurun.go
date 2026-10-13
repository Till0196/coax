// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/report"
)

// A Mirakurun channel list is YAML: one entry per channel or per service,
// saying what it is called, what kind it is, and where it is.

// MirakurunOptions are the choices a channel list is written under.
//
// Everything here defaults to off: Mirakurun scans the services on a channel
// itself and finds a multiplexing frame by looking, so stating those things
// pins them to the day the scan ran.
type MirakurunOptions struct {
	// Route is the lead every channel is written as arriving on.
	//
	// Empty writes no route, the default: Mirakurun takes the lead the channel type
	// implies. RouteAsScanned writes what each channel recorded when it was scanned;
	// anything else is written as it stands. Which socket a tuner is plugged into is
	// not in the signal. See config.ValidRoute.
	Route string
	// TransponderNames writes a satellite channel as its transponder and the
	// stream's place on it -- BS15_1 -- rather than as whatever the scan called
	// it.
	TransponderNames bool
	// ServiceIDs writes one entry per service, each carrying its serviceId, rather
	// than one entry per channel. A channel written without one means every service.
	ServiceIDs bool
	// RelativeStreams writes one entry per stream of a multiplexing frame, each
	// carrying its tsmfRelTs, rather than one per carrier. On by default: a
	// frame's slots are separate streams. It only numbers where a frame carries
	// more than one.
	RelativeStreams bool
	// GroupIDs writes the tsmfGroupId of a multiplex sent over several carriers. Off
	// by default: Mirakurun reads the group out of the signal, and a value in the
	// file is a value the receiver stops re-reading.
	GroupIDs bool
}

// RouteAsScanned asks for the lead each channel recorded when it was scanned.
const RouteAsScanned = "SCANNED"

// mirakurunEntry is one line of the list, held until every result has been read
// so that the lines can be put in order before any is written.
type mirakurunEntry struct {
	candidate candidate
	// channel is the name the carrier was scanned under, or the transponder it
	// turned out to be.
	channel string
	route   string
	// satellite is the block on the dish, for the two slots that share one.
	satellite string
	// numbered says whether this carrier's streams carry their place in the frame.
	numbered bool
}

// mirakurunTypeRank orders the kinds the way Mirakurun's own documentation lists
// them. Anything unrecognised sorts last.
func mirakurunTypeRank(channelType string) int {
	for rank, known := range []string{"GR", "BS", "CS", "BS4K", "SKY"} {
		if channelType == known {
			return rank
		}
	}
	return 5
}

// WriteMirakurun writes the scan as a Mirakurun channel list, and reports the
// channels it was asked to rename and could not.
func WriteMirakurun(w io.Writer, value report.Report, options MirakurunOptions) ([]string, error) {
	var unnamed []string
	var entries []mirakurunEntry
	for result := range foundIn(value.Results) {
		lead := options.Route
		if lead == RouteAsScanned {
			lead = result.Route
		}
		name := result.Name
		if options.TransponderNames {
			transponder, why := satelliteName(result)
			if why != "" {
				unnamed = append(unnamed, why)
			}
			if transponder != "" {
				name = transponder
			}
		}
		candidates := candidates(result)
		// A number is worth writing only where it tells two channels apart.
		numbered := options.RelativeStreams && multiplexedStreamCount(result) > 1
		if !options.ServiceIDs {
			candidates = onePerChannel(candidates, numbered)
		}
		satellite := satelliteOf(result)
		for _, candidate := range candidates {
			if !options.ServiceIDs && carrierOnlyNames[candidate.Carriage] {
				candidate.Name = carriageName(name, candidate, numbered)
			}
			entries = append(entries, mirakurunEntry{
				candidate: candidate,
				channel:   name,
				route:     lead,
				satellite: satellite,
				numbered:  numbered,
			})
		}
	}

	// Written in the order Mirakurun itself lists the kinds: a single cable carrier
	// can hold several kinds at once, so scan order would scatter them. The sort is
	// stable, so within one kind the scan order survives.
	slices.SortStableFunc(entries, func(left, right mirakurunEntry) int {
		return cmp.Compare(mirakurunTypeRank(left.candidate.Type), mirakurunTypeRank(right.candidate.Type))
	})

	for _, entry := range entries {
		candidate, name, lead := entry.candidate, entry.channel, entry.route
		numbered := entry.numbered
		if _, err := fmt.Fprintf(w,
			"- name: %s\n  type: %s\n  channel: %s\n",
			yamlString(candidate.Name), candidate.Type, yamlString(name),
		); err != nil {
			return unnamed, err
		}
		if lead != "" {
			if _, err := fmt.Fprintf(w, "  route: %s\n", lead); err != nil {
				return unnamed, err
			}
		}
		// The satellite a 124/128 degree east carrier is on. Written by default rather
		// than behind an option: two satellites reach one block on one lead and only
		// the 22 kHz tone tells them apart.
		//
		// In commandVars rather than a satellite key of its own: both reach the same
		// place, but the key is deprecated since 4.0.0. Either spelling still works,
		// <satelite> included.
		if satellite := entry.satellite; satellite != "" {
			if _, err := fmt.Fprintf(w, "  commandVars:\n    satellite: %s\n", satellite); err != nil {
				return unnamed, err
			}
		}
		if numbered && candidate.HasRelativeStream {
			if _, err := fmt.Fprintf(w, "  tsmfRelTs: %d\n", candidate.RelativeStream); err != nil {
				return unnamed, err
			}
		}
		// The carriers of a split multiplex are written one entry each: Mirakurun tunes
		// the first of a group and bonds the rest onto it, so every carrier has to be
		// listed and say which group it belongs to.
		if options.GroupIDs && candidate.HasGroup {
			if _, err := fmt.Fprintf(w, "  tsmfGroupId: %d\n", candidate.GroupID); err != nil {
				return unnamed, err
			}
		}
		if options.ServiceIDs && candidate.ServiceID != 0 && candidate.Type != "GR" {
			if _, err := fmt.Fprintf(w, "  serviceId: %d\n", candidate.ServiceID); err != nil {
				return unnamed, err
			}
		}
		// Written only where every service on the channel is under conditional access
		// per the SDT. The value is false all the same -- whether a card is at hand is
		// not something the stream says -- so the key is there to be flipped by hand.
		// Anywhere else it is left out, Mirakurun already taking it as false.
		if candidate.UnderConditionalAccess {
			if _, err := fmt.Fprintln(w, "  isDisabled: false"); err != nil {
				return unnamed, err
			}
		}
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintln(w, "[]")
		return unnamed, err
	}
	return unnamed, nil
}

// onePerChannel keeps the first candidate of each channel. The key is the kind --
// not the carrier, since one cable carrier can hold a terrestrial and a satellite
// stream at once -- plus the stream number, but only when that is going to be
// written. The name is the first service's; a multiplex has no name of its own.
func onePerChannel(candidates []candidate, byRelativeStream bool) []candidate {
	kept := make([]candidate, 0, len(candidates))
	at := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		key := candidate.Type
		if byRelativeStream && candidate.HasRelativeStream {
			key = fmt.Sprintf("%s/%d", candidate.Type, candidate.RelativeStream)
		}
		if index, already := at[key]; already {
			// Under conditional access only where everything on it is: one service in
			// the clear is a reason to receive the channel.
			kept[index].UnderConditionalAccess =
				kept[index].UnderConditionalAccess && candidate.UnderConditionalAccess
			continue
		}
		at[key] = len(kept)
		candidate.ServiceID = 0
		kept = append(kept, candidate)
	}
	return kept
}

// multiplexedStreamCount is how many streams the multiplexing frame on a
// carrier holds, counted from the frame rather than from the entries the scan
// turned into.
//
// A slot carrying nothing this list writes makes no entry, but it still
// occupies the frame and the receiver still has to be told which slot to take.
// Counting entries instead wrote a stream without a number, which is the whole
// frame.
func multiplexedStreamCount(result report.Result) int {
	streams, multiplexed := carriedStreams(result)
	if !multiplexed {
		return 0
	}
	seen := make(map[int]bool, len(streams))
	for _, stream := range streams {
		if stream.hasRelative {
			seen[stream.relative] = true
		}
	}
	return len(seen)
}

// satelliteOf names the satellite a channel arrived from, where naming one
// means anything. Only the 124/128 degree east service has two to choose
// between: the two slots share a block, a lead and an oscillator, and differ
// only in the 22 kHz tone.
//
// Read from the block the channel names where it names one, and from the
// carrier's own name otherwise.
func satelliteOf(result report.Result) string {
	if channelconfig.NormalizeDeliverySystem(result.Properties["DELIVERY_SYSTEM"]) != "DVBS2" {
		return ""
	}
	if block := strings.TrimSpace(result.Properties["LNB"]); block != "" {
		return strings.ToUpper(strings.Trim(block, `"'`))
	}
	satellite, _, found := strings.Cut(result.Name, "/")
	if !found {
		return ""
	}
	satellite = strings.ToUpper(strings.TrimSpace(satellite))
	if !strings.HasPrefix(satellite, "JCSAT") {
		return ""
	}
	return satellite
}

// satelliteName is a channel written the way recpt1 writes one: the
// transponder, and the stream's place on it. It reports the name, and why there
// is not one.
//
// Only ISDB-S and ISDB-S3 have one. The name is decoded from what the channel
// is called where that says, and from the frequency and stream where it does
// not.
//
// The MMT/TLV services are left alone: they are selected by identifier on
// every driver that receives them, and a name saying "the first stream on this
// transponder" would name the older service sharing it.
func satelliteName(result report.Result) (name string, why string) {
	switch channelconfig.NormalizeDeliverySystem(result.Properties["DELIVERY_SYSTEM"]) {
	case "ISDBS", "ISDBS3":
	default:
		return "", ""
	}
	channel, nameErr := channelconfig.ParseSatelliteChannel(result.Name)
	if nameErr != nil {
		frequency, err := strconv.Atoi(strings.TrimSpace(result.Properties["FREQUENCY"]))
		if err != nil {
			return "", fmt.Sprintf("%s: no frequency to place it by", result.Name)
		}
		placed, ok := channelconfig.SatelliteChannelAt(frequency, result.Properties["STREAM_ID"])
		if !ok {
			return "", fmt.Sprintf("%s: no transponder at %d kHz carries the stream %q",
				result.Name, frequency, result.Properties["STREAM_ID"])
		}
		channel = placed
	}
	if channel.AdvancedService() {
		return "", fmt.Sprintf("%s: a TLV stream is asked for by identifier, not by its place", result.Name)
	}
	return channel.Name(), ""
}

// candidate is one line of a Mirakurun channel list before it is written: a
// service, and what has to be said about the channel it is on.
type candidate struct {
	Name      string
	Type      string
	ServiceID uint16
	// RelativeStream is which stream of a multiplexing frame this came out of, and
	// whether it came out of one at all.
	RelativeStream    int
	HasRelativeStream bool
	// GroupID identifies the multiplex a carrier is one of. See splitMultiplex.
	GroupID  byte
	HasGroup bool
	// Carriage is what the network said it was before it was folded onto a type
	// Mirakurun accepts -- CATV and OTHER both arrive as SKY.
	Carriage string
	// UnderConditionalAccess is the SDT's free_CA_mode for the service. The flag in
	// the standard reads backwards, so it is named here for the state it describes.
	UnderConditionalAccess bool
}

// candidates is the services a scanned carrier turned into, as a Mirakurun
// channel list would name them. A service described in more than one place is
// taken once, and a terrestrial multiplex collapses to one channel however many
// services it carries -- one channel is what Mirakurun tunes.
func candidates(result report.Result) []candidate {
	streams, _ := carriedStreams(result)
	groupID, split := splitMultiplex(result)
	var found []candidate
	seen := make(map[string]bool)
	for _, stream := range streams {
		channelType := channelTypeForNetwork(stream.networkID)
		if channelType == "" {
			continue
		}
		named := len(found)
		for _, service := range stream.services {
			if !televisionServiceType(service.Type) {
				continue
			}
			key := fmt.Sprintf("%d:%s:%d", stream.networkID, channelType, service.ServiceID)
			if channelType == "GR" {
				key = "GR"
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			name := service.Name
			if name == "" {
				name = fallbackName(result, service.ServiceID)
			}
			found = append(found, candidate{
				Name: name, Type: mirakurunChannelType(channelType), Carriage: channelType,
				ServiceID:      service.ServiceID,
				RelativeStream: stream.relative, HasRelativeStream: stream.hasRelative,
				GroupID: groupID, HasGroup: split,
				UnderConditionalAccess: service.FreeCAMode,
			})
		}
		if len(found) == named && split && stream.streamID != 0 {
			// A carrier of a split multiplex holds a fraction of each stream on it and
			// nothing whole, the service table included. Dropping the carrier for want
			// of a service name loses the whole multiplex, since none of its carriers is
			// any better off -- so the stream it holds names it instead.
			found = append(found, candidate{
				Name: fmt.Sprintf("%d", stream.streamID),
				Type: mirakurunChannelType(channelType), Carriage: channelType,
				RelativeStream: stream.relative, HasRelativeStream: stream.hasRelative,
				GroupID: groupID, HasGroup: split,
			})
		}
	}
	return found
}

// splitMultiplex is the identifier of the multiplex a carrier is one of, and
// whether it is one of several at all. Each carrier of a wide multiplex carries
// the group identifier they share, which is what a receiver bonds them by and,
// since every carrier of one group holds the same streams, the only thing
// telling groups apart.
func splitMultiplex(result report.Result) (byte, bool) {
	if result.Analysis == nil || result.Analysis.TSMF == nil {
		return 0, false
	}
	frame := result.Analysis.TSMF
	if frame.NumberOfCarriers <= 1 {
		return 0, false
	}
	return frame.GroupID, true
}

// carriageName names a whole-channel entry after the carrier it is on rather
// than after the first service found on it. Which channels those are is
// carrierOnlyNames, the same rule the channel definition files are written
// under: the platforms that sell carriage by the slot.
//
// A broadcast multiplex is named for its broadcaster and a viewer knows the
// name. A communications satellite transponder, a 124/128 degrees east feed and
// a cable operator's own carriage are each a shelf of unrelated channels, in an
// order that changes as contracts come and go, so naming the entry after
// whichever service the scan read first misdescribes it and differs between two
// scans of one feed. The carrier is what the entry selects, so the carrier is
// what it is called.
//
// A carrier holding several streams of a frame is several entries, so the
// stream's place in the frame joins the name to tell them apart.
func carriageName(channel string, entry candidate, numbered bool) string {
	if numbered && entry.HasRelativeStream {
		return fmt.Sprintf("%s_%d", channel, entry.RelativeStream)
	}
	return channel
}

// mirakurunChannelType folds this program's types onto the ones a Mirakurun
// channel definition takes. Mirakurun has no CS4K, CATV or OTHER: TLV from the
// communications satellites goes to BS4K, and cable carriage and an
// unrecognised network to SKY.
func mirakurunChannelType(channelType string) string {
	switch channelType {
	case "CS4K":
		return "BS4K"
	case "CATV", "OTHER":
		return "SKY"
	default:
		return channelType
	}
}

// fallbackName names a service the tables did not name, from what the channel was
// tuned as rather than the lead it arrived on.
func fallbackName(result report.Result, serviceID uint16) string {
	system := result.Properties["DELIVERY_SYSTEM"]
	if system == "" {
		return fmt.Sprintf("%s:%d", result.Name, serviceID)
	}
	return fmt.Sprintf("%s %s:%d", system, result.Name, serviceID)
}

func televisionServiceType(serviceType byte) bool {
	switch serviceType {
	case 0x01, 0xa1, 0xa5, 0xad:
		return true
	default:
		return false
	}
}

// yamlString writes a double-quoted YAML scalar, escaping what YAML needs escaped
// and nothing else. Not strconv.Quote, which escapes everything Go calls
// unprintable, the ideographic space included -- a service name written with one
// came out as "テスト\u3000放送".
func yamlString(value string) string {
	var quoted strings.Builder
	quoted.Grow(len(value) + 2)
	quoted.WriteByte('"')
	for _, letter := range value {
		switch {
		case letter == '"' || letter == '\\':
			quoted.WriteByte('\\')
			quoted.WriteRune(letter)
		case letter == '\n':
			quoted.WriteString(`\n`)
		case letter == '\r':
			quoted.WriteString(`\r`)
		case letter == '\t':
			quoted.WriteString(`\t`)
		case letter < 0x20, letter == 0x7f, letter == 0x85, letter == 0xa0,
			letter == 0x2028, letter == 0x2029, letter == utf8.RuneError:
			// The control characters, the two Unicode line breaks, and the spaces YAML
			// would read as something other than themselves.
			fmt.Fprintf(&quoted, "\\u%04x", letter)
		default:
			quoted.WriteRune(letter)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
