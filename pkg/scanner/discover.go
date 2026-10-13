// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/plan"
)

// DiscoverableSystems are the delivery systems discovery knows how to cover,
// spelled as a channel file spells them. ISDBS3 is here only so it can be asked
// for by name; it is the one system a bare --discover does not cover.
var DiscoverableSystems = []string{"ISDBT", "ISDBS", "ISDBS3", "DVBC/ANNEX_A", "DVBC/ANNEX_B", "DVBS2"}

// DiscoverableSystemNames are the same systems spelled the way a scan writes
// them down and the way this country names them. Every spelling is still taken:
// see resolveSystems.
func DiscoverableSystemNames() []string {
	named := make([]string, 0, len(DiscoverableSystems))
	for _, system := range DiscoverableSystems {
		named = append(named, channelconfig.PreferredDeliverySystem(system))
	}
	return named
}

// resolveSystems settles which systems to cover: the ones named, or every one
// the tuner carries when none were.
func resolveSystems(named []string) (map[string]bool, error) {
	if len(named) == 0 {
		return nil, nil
	}
	chosen := map[string]bool{}
	for _, name := range named {
		system := channelconfig.NormalizeDeliverySystem(name)
		if !slices.Contains(DiscoverableSystems, system) {
			return nil, fmt.Errorf("%q is not a delivery system this can work out on its own; it knows %s, and takes %s",
				name, strings.Join(DiscoverableSystemNames(), ", "), strings.Join(aliasDescriptions(), ", "))
		}
		chosen[system] = true
	}
	return chosen, nil
}

// aliasDescriptions says what the other spellings mean, for a message.
func aliasDescriptions() []string {
	var described []string
	for _, system := range DiscoverableSystems {
		if names := channelconfig.DeliverySystemAliases(system); len(names) > 0 {
			described = append(described, fmt.Sprintf("%s for %s", strings.Join(names, "/"), system))
		}
	}
	return described
}

// Discover works out what a tuner can receive without being given a channel
// file, covering each delivery system the way that system allows. A satellite
// is asked, because one lock yields the network's own account of itself; a
// cable is walked, because its network table is written by whoever runs the
// cable.
func Discover(ctx context.Context, options Options, progress io.Writer, systems []string) ([]channelconfig.Entry, error) {
	if options.Device == nil {
		return nil, fmt.Errorf("receiver device is required")
	}
	options = withSatelliteSupply(options)
	chosen, err := resolveSystems(systems)
	if err != nil {
		return nil, err
	}
	display := newDiscoveryDisplay(progress)
	var entries []channelconfig.Entry
	// A system is covered when it was asked for, or when nothing was asked for
	// and the tuner carries it. Asking for one the tuner does not carry is
	// answered by finding nothing rather than by an error.
	carries := func(system string) bool {
		if chosen != nil {
			return chosen[system]
		}
		if system == "ISDBS3" {
			// Nothing can tell whether a tuner carries ISDB-S3: the Linux DVB API has no
			// delivery system number for it and Windows BDA cannot be asked, so every
			// satellite front end answers yes. Believing that buys a walk over two dozen
			// transponders -- three quarters of this bootstrap's time -- for a network
			// most installations do not have.
			return false
		}
		// A tuner is asked by every name the system goes by, because which one
		// a backend advertises is not something to depend on.
		for _, name := range append([]string{system}, channelconfig.DeliverySystemAliases(system)...) {
			if supported, err := options.Device.Supports(name); err == nil && supported {
				return true
			}
		}
		return false
	}

	if carries("ISDBS") || carries("ISDBS3") {
		found, err := discoverSatellite(ctx, options, display, carries)
		if err != nil {
			display.finish("satellite: " + err.Error())
		}
		entries = append(entries, found...)
	}
	for _, walked := range walkedPlans {
		if !carries(walked.system) {
			continue
		}
		entries = append(entries, discoverWalked(ctx, options, display, walked)...)
	}

	if carries("DVBS2") {
		found := skyEntries()
		display.line("the 124/128 degree east service ... ")
		display.finish(fmt.Sprintf("%d carriers to try", len(found)))
		entries = append(entries, found...)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("nothing to work out without a channel file: this tuner carries none of %s",
			strings.Join(DiscoverableSystemNames(), ", "))
	}
	return entries, nil
}

// walkedPlans are the bands a scan walks rather than asks about, and what a
// carrier of each has to be tuned with.
//
// A cable cannot be asked what it carries in the way a satellite can: its
// network table is written by whoever runs it, and on one feed measured here
// every carrier gave the same answer. What a cable has instead is a band plan
// nobody moves and multiplexing frames that name their own contents.
//
// The table is not asked before the walk either. It was, for a while: the six
// tunes bought nothing and cost two minutes, and by the time a table has
// anything to say the walk has already been round the whole band. So the band
// is walked at the rates below.
var walkedPlans = []walkedPlan{
	{system: "ISDBT", what: "terrestrial", carriers: plan.TerrestrialWalk, properties: map[string]string{
		"DELIVERY_SYSTEM": "ISDBT",
		"BANDWIDTH_HZ":    "6000000",
	}},
	// Written down as ISDBC, which is what this band is: the Japanese cable
	// standard, on the plan below and at the rate below. The tuners lock it as
	// DVB-C Annex A -- one demodulator, one set of parameters -- and that is the
	// name a front end advertises and the name this is matched against, but not
	// what a reader of the scan should be told.
	{system: "DVBC/ANNEX_A", what: "cable", carriers: plan.Cable, properties: map[string]string{
		"DELIVERY_SYSTEM": "ISDBC",
		"SYMBOL_RATE":     "5274000",
		"MODULATION":      "QAM/AUTO",
	}},
	{system: "DVBC/ANNEX_B", what: "the optical distribution", carriers: plan.Optical, properties: map[string]string{
		"DELIVERY_SYSTEM": "DVBC/ANNEX_B",
		"SYMBOL_RATE":     "5600000",
		"MODULATION":      "QAM/256",
	}},
}

type walkedPlan struct {
	system     string
	what       string
	carriers   func() []plan.Carrier
	properties map[string]string
}

// discoverWalked is one band's carriers: the plan, walked as it stands.
func discoverWalked(_ context.Context, _ Options, display *discoveryDisplay, walked walkedPlan) []channelconfig.Entry {
	planned := planEntries(walked.carriers(), walked.properties)
	display.line("%s ... ", walked.what)
	display.finish(fmt.Sprintf("%d carriers to try", len(planned)))
	return planned
}

// mergeCarriers puts what the operator said over the plan.
//
// Nothing calls this today: the walk covers the whole band at the plan's rates,
// and the cable tables seen here name only the carrier they came off. It is
// kept, with cableEntries beside it, for the feed whose table does name its
// carriers.
//
// The plan decides which carriers are walked and the table decides how each is
// tuned. A carrier the plan already has keeps the plan's frequency and takes
// the table's settings: a Japanese cable carries the plan's offset frequency,
// which an operator's table rounds to the nominal figure. A carrier the plan
// does not have is left alone.
func mergeCarriers(planned, named []channelconfig.Entry) []channelconfig.Entry {
	merged := make([]channelconfig.Entry, 0, len(planned))
	taken := make([]bool, len(named))
	for _, carrier := range planned {
		at := frequencyOf(carrier.Properties)
		settings := carrier.Properties
		for index, spoken := range named {
			if taken[index] || !nearby(at, frequencyOf(spoken.Properties)) {
				continue
			}
			taken[index] = true
			settings = withFrequency(spoken.Properties, carrier.Properties["FREQUENCY"])
			break
		}
		carrier.Properties = settings
		merged = append(merged, carrier)
	}
	return merged
}

// frequencyOf is a channel's frequency as a number, or zero where it has none
// this can read. Two carriers that cannot be compared are not the same one.
func frequencyOf(properties map[string]string) int {
	number, err := strconv.Atoi(strings.TrimSpace(properties["FREQUENCY"]))
	if err != nil {
		return 0
	}
	return number
}

// nearby says two figures for one carrier are the same carrier. A band's
// channels are megahertz apart and the two accounts of one differ by the
// offset a plan carries and a table rounds off.
const carrierMargin = 1_000_000

func nearby(one, other int) bool {
	if one == 0 || other == 0 {
		return false
	}
	return one-other < carrierMargin && other-one < carrierMargin
}

// withFrequency is one carrier's settings at the frequency the plan puts it.
func withFrequency(properties map[string]string, frequency string) map[string]string {
	at := make(map[string]string, len(properties))
	for key, value := range properties {
		at[key] = value
	}
	at["FREQUENCY"] = frequency
	return at
}

// planEntries turns a plan into channels to scan. Nothing is tuned here: a plan
// says where the carriers could be and the scan finds out which of them are,
// reading the multiplexing frame header for what is on each.
func planEntries(carriers []plan.Carrier, properties map[string]string) []channelconfig.Entry {
	entries := make([]channelconfig.Entry, 0, len(carriers))
	for _, position := range carriers {
		channel := map[string]string{"FREQUENCY": strconv.Itoa(position.Frequency)}
		maps.Copy(channel, properties)
		entries = append(entries, channelconfig.Entry{
			Name:       position.Name,
			Source:     "the built-in band plan",
			Properties: channel,
		})
	}
	return entries
}

// skyEntries is the 124/128 degree east plan as channels to scan. It is built
// here rather than through planEntries because nothing a carrier needs is the
// same for all of them: the satellite is in the carrier's name, the
// polarisation alternates with the transponder number, and the grid carries two
// generations on the same frequencies. The frequency written is the downlink,
// which the named block converts.
func skyEntries() []channelconfig.Entry {
	channels := plan.SkyChannels()
	entries := make([]channelconfig.Entry, 0, len(channels))
	for _, channel := range channels {
		entries = append(entries, channelconfig.Entry{
			Name:   channel.Name,
			Source: "the published 124/128 degree east frequency table",
			Properties: map[string]string{
				"DELIVERY_SYSTEM": channel.DeliverySystem,
				"FREQUENCY":       strconv.Itoa(channel.DownlinkKHz),
				"LNB":             channel.Satellite,
				"POLARIZATION":    channel.Polarization(),
				"SYMBOL_RATE":     strconv.Itoa(channel.SymbolRate),
				"MODULATION":      channel.Modulation,
			},
		})
	}
	return entries
}
