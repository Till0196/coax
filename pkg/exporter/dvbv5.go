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

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/plan"
	"github.com/Till0196/coax/pkg/report"
)

// The channel file the DVB tools read, which is the same file a scan is given:
//
//	[27]
//		DELIVERY_SYSTEM = ISDBT
//		FREQUENCY = 557142857
//		BANDWIDTH_HZ = 6000000
//
// This is the only writer whose output can be fed straight back into coax scan.
//
// A section is a tuning request and nothing else, so one scanned result becomes
// exactly one section, and its properties are written back as the scan was given
// them rather than tidied: a channel that locked did so with those spellings.

// DVBv5Options are what a dvbv5 channel file was asked for.
type DVBv5Options struct {
	// TransponderNames writes a satellite channel as its transponder and the
	// stream's place on it -- BS15_1. See satelliteName.
	TransponderNames bool
	// LNBf is the low-noise block on the dish, written into the satellite channels
	// that do not already name one. See writeLNBf.
	LNBf string
	// SatNumber is the position on a DiSEqC switch, written into the satellite
	// channels that do not already name one. Unlike a block it says nothing about
	// the frequency: the same answer for every channel arriving through that
	// switch.
	SatNumber string
}

// LNBfProperty and SatNumberProperty are libdvbv5's own keys -- DTV_DISEQC_LNB
// and DTV_SAT_NUMBER -- so a file carrying them says the same thing to
// dvbv5-zap and to this program.
const (
	LNBfProperty      = "LNB"
	SatNumberProperty = "SAT_NUMBER"
)

// MaxSatNumber is the highest DiSEqC position there is: a committed switch has
// four ports. The standard's number rather than the daemon's.
const MaxSatNumber = 3

// The routes a file is written for, in the order the files are written. The
// empty one is not a route: it is the channels of a scan that was not told
// which lead it was on.
var dvbv5RouteOrder = []string{"TER", "SAT", "CATV", "HIKARI", ""}

// DVBv5Routes is the routes a report found channels on, in the order the files
// are written. The split is by route rather than by delivery system because
// that is the split the reader makes: dvbv5-zap is pointed at one file for one
// tuner. The section names need it too: channel 27 over the air and channel 27
// on a cable are both called 27, and a file cannot hold both. See
// config.ValidRoute.
func DVBv5Routes(value report.Report) []string {
	seen := map[string]bool{}
	for result := range foundIn(value.Results) {
		seen[dvbv5Route(result)] = true
	}
	var routes []string
	for _, route := range dvbv5RouteOrder {
		if seen[route] {
			routes = append(routes, route)
		}
	}
	return routes
}

// dvbv5Route is the lead a result arrived on, as the file names it.
func dvbv5Route(result report.Result) string {
	route := strings.ToUpper(strings.TrimSpace(result.Route))
	if !channelconfig.ValidRoute(route) {
		return ""
	}
	return route
}

// DVBv5FileSuffix is what a route adds to the name a file is written under: the
// distributed files are dvbv5_channels_ter.conf and so on. A scan without a lead
// writes the stem alone.
func DVBv5FileSuffix(route string) string {
	if route == "" {
		return ".conf"
	}
	return "_" + strings.ToLower(route) + ".conf"
}

// dvbv5Section is one section of the file before it is written.
type dvbv5Section struct {
	name       string
	properties map[string]string
	// frequencyHz and streamID are what the sections are put in order by, read off
	// the properties so a section whose frequency cannot be read still sorts
	// somewhere.
	frequencyHz int
	streamID    int
}

// WriteDVBv5 writes the channels found on one route as a dvbv5 channel file.
//
// A note is about a channel that is in the file but did not get everything
// asked for it. A skipped channel is not in the file at all.
func WriteDVBv5(w io.Writer, route string, results []report.Result, options DVBv5Options) (notes, skipped []string, err error) {
	var sections []dvbv5Section
	var dishNotes []string
	taken := map[string]bool{}
	for result := range foundIn(results) {
		if dvbv5Route(result) != route {
			continue
		}
		name := result.Name
		if options.TransponderNames {
			transponder, why := satelliteName(result)
			switch {
			case why != "":
				notes = append(notes, "left as the scan named it: "+why)
			case transponder != "":
				name = transponder
			}
		}
		if taken[name] {
			// Two sections of one name is a file the parser refuses, so the second is
			// left out. It happens where one scan was given two channel files that
			// number their channels the same way.
			skipped = append(skipped, fmt.Sprintf("%s: the file already has a section called %q", result.Name, name))
			continue
		}
		taken[name] = true
		properties := dvbv5Systems(result.Properties)
		properties, why := dishSettings(properties, options)
		if why != "" {
			dishNotes = append(dishNotes, fmt.Sprintf("%s: %s", name, why))
		}
		sections = append(sections, dvbv5Section{
			name:        name,
			properties:  properties,
			frequencyHz: numericProperty(properties, "FREQUENCY"),
			streamID:    numericProperty(properties, "STREAM_ID"),
		})
	}
	notes = append(notes, dishNotes...)
	if len(sections) == 0 {
		return notes, skipped, fmt.Errorf("the scan found no channels on %s to write", DVBv5RouteName(route))
	}
	sections = append(sections, unscannedTerrestrialSections(sections, taken)...)

	// In the order somebody tunes through them, which is the order the distributed
	// files are in: settled by the channels rather than by scan order.
	slices.SortStableFunc(sections, func(left, right dvbv5Section) int {
		if left.frequencyHz != right.frequencyHz {
			return cmp.Compare(left.frequencyHz, right.frequencyHz)
		}
		if left.streamID != right.streamID {
			return cmp.Compare(left.streamID, right.streamID)
		}
		return strings.Compare(left.name, right.name)
	})

	var file strings.Builder
	fmt.Fprintf(&file, "# channel definitions (%s), written by coax\n", DVBv5RouteName(route))
	for _, section := range sections {
		fmt.Fprintf(&file, "[%s]\n", section.name)
		for _, key := range dvbv5PropertyOrder(section.properties) {
			fmt.Fprintf(&file, "\t%s = %s\n", key, section.properties[key])
		}
	}
	_, err = io.WriteString(w, file.String())
	return notes, skipped, err
}

// DVBv5RouteName is what a route is called in a message and in a file heading.
func DVBv5RouteName(route string) string {
	if route == "" {
		return "no declared lead"
	}
	return route
}

// dvbv5PropertyOrder is the order a section's properties are written in:
// DELIVERY_SYSTEM and FREQUENCY first, then the rest alphabetically. The
// properties a channel file may carry are open-ended, so a fixed list here
// would go out of date.
func dvbv5PropertyOrder(properties map[string]string) []string {
	rest := make([]string, 0, len(properties))
	for key := range properties {
		if key != "DELIVERY_SYSTEM" && key != "FREQUENCY" {
			rest = append(rest, key)
		}
	}
	slices.Sort(rest)
	ordered := make([]string, 0, len(properties))
	for _, first := range []string{"DELIVERY_SYSTEM", "FREQUENCY"} {
		if _, ok := properties[first]; ok {
			ordered = append(ordered, first)
		}
	}
	return append(ordered, rest...)
}

// unscannedTerrestrialSections fills in the rest of the television plan: every
// physical channel the scan did not find something on. A channel file is a plan
// rather than a report, so a channel the scan missed can still be tuned. See
// unscannedTerrestrial in placement.go.
//
// Only the terrestrial plan is filled, and only where the scan found
// terrestrial channels on this lead: an unscanned cable channel would need a
// symbol rate and modulation, and an unscanned transponder a stream identifier.
//
// The empty sections copy the spellings of the scanned ones on the same lead. A
// name another section already has is left alone.
func unscannedTerrestrialSections(scanned []dvbv5Section, taken map[string]bool) []dvbv5Section {
	template := map[string]string{}
	covered := map[int]bool{}
	for _, section := range scanned {
		if channelconfig.NormalizeDeliverySystem(section.properties["DELIVERY_SYSTEM"]) != "ISDBT" {
			continue
		}
		covered[section.frequencyHz] = true
		if len(template) == 0 {
			template = section.properties
		}
	}
	if len(template) == 0 {
		return nil
	}
	system, bandwidth := template["DELIVERY_SYSTEM"], template["BANDWIDTH_HZ"]
	if bandwidth == "" {
		bandwidth = strconv.Itoa(terrestrialBandwidthHz)
	}
	var empty []dvbv5Section
	for _, carrier := range plan.Terrestrial() {
		if covered[carrier.Frequency] || taken[carrier.Name] {
			continue
		}
		taken[carrier.Name] = true
		empty = append(empty, dvbv5Section{
			name: carrier.Name,
			properties: map[string]string{
				"DELIVERY_SYSTEM": system,
				"FREQUENCY":       strconv.Itoa(carrier.Frequency),
				"BANDWIDTH_HZ":    bandwidth,
			},
			frequencyHz: carrier.Frequency,
		})
	}
	return empty
}

// dishSettings adds what was asked for about the dish to one channel, and says
// what it did not add. A channel that already says one of these keeps what it
// says. Nothing is checked against a table of block names: a file written for
// dvbv5-zap may name a block this program has never heard of.
func dishSettings(properties map[string]string, options DVBv5Options) (map[string]string, string) {
	if options.LNBf == "" && options.SatNumber == "" {
		return properties, ""
	}
	system := channelconfig.NormalizeDeliverySystem(properties["DELIVERY_SYSTEM"])
	if !dvbv5Satellite[system] {
		return properties, ""
	}
	settings := map[string]string{}
	var why string
	if options.SatNumber != "" && properties[SatNumberProperty] == "" {
		settings[SatNumberProperty] = options.SatNumber
	}
	switch {
	case options.LNBf == "" || properties[LNBfProperty] != "":
	case !writeLNBf(system):
		why = fmt.Sprintf("written without a block, because %s carries the intermediate frequency already and naming a block is what asks for a downlink to be converted",
			system)
	default:
		settings[LNBfProperty] = options.LNBf
	}
	if len(settings) == 0 {
		return properties, why
	}
	// Copied rather than written into: the properties belong to the report.
	with := make(map[string]string, len(properties)+len(settings))
	for key, value := range properties {
		with[key] = value
	}
	for key, value := range settings {
		with[key] = value
	}
	return with, why
}

// dvbv5Systems writes a channel's delivery system the way libdvbv5 spells it,
// which is not always the way this program records it.
//
// ISDB-S3 is the one that differs. The Linux DVB API numbers ISDB-S and nothing
// beyond it, so a file naming ISDB-S3 is one dvbv5-zap and dvbv5-scan refuse to
// parse. It goes out as ISDB-S, which is what those tools tune it as: one
// demodulator receives both, and the stream identifier beside it separates
// them.
//
// The Japanese cable band is the other one: a scan writes ISDBC, and dvbv5
// knows it only as the DVB-C annex its tuners lock it with.
func dvbv5Systems(properties map[string]string) map[string]string {
	written, ok := dvbv5SystemNames[channelconfig.NormalizeDeliverySystem(properties["DELIVERY_SYSTEM"])]
	if !ok || written == properties["DELIVERY_SYSTEM"] {
		return properties
	}
	// Copied rather than written into: the properties belong to the report.
	copied := make(map[string]string, len(properties))
	for key, value := range properties {
		copied[key] = value
	}
	copied["DELIVERY_SYSTEM"] = written
	return copied
}

// dvbv5SystemNames are the systems dvbv5 has no name of its own for, and what to
// write instead. See dvbv5Systems for why.
var dvbv5SystemNames = map[string]string{
	"ISDBS3":       "ISDBS",
	"DVBC/ANNEX_A": "DVBC/ANNEX_A",
}

// dvbv5Satellite are the delivery systems a dish setting is about.
var dvbv5Satellite = map[string]bool{
	"ISDBS": true, "ISDBS3": true, "DVBS": true, "DVBS2": true,
}

// writeLNBf reports whether a channel of this delivery system is one a block
// can be named on. Naming a block says the frequency is a downlink with an
// oscillator to come off it, and the Japanese satellite channel files carry the
// intermediate frequency already -- writing a block into one of those subtracts
// the oscillator twice. So only the systems whose files are written as
// downlinks: the 124/128 degree east service. A scan whose own file named a
// block keeps it either way.
func writeLNBf(system string) bool {
	return system == "DVBS" || system == "DVBS2"
}

// terrestrialBandwidthHz is how wide an ISDB-T channel is, for the empty
// sections.
const terrestrialBandwidthHz = 6_000_000

// numericProperty reads a property as the number it is, or zero where it is not
// one. Only used to put sections in order.
func numericProperty(properties map[string]string, key string) int {
	value, err := strconv.Atoi(strings.TrimSpace(properties[key]))
	if err != nil {
		return 0
	}
	return value
}
