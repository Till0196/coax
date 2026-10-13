// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/plan"
)

// satellitePasses are the networks a satellite is asked about, and which
// delivery system covering it settles whether that pass is made. The
// transponders are a fixed grid at 110 degrees east and each network's
// information table names every multiplex on it, so one lock anywhere yields
// the whole network.
//
// Four networks that never mention each other: BS and CS carry separate
// networks with separate tables, and the TLV services on each are separate
// again. Left-hand CS transmissions ended 2024-03-31, so that pass finds
// nothing.
var satellitePasses = []satellitePass{
	{"ISDBS", "BS", broadcastTransponders, 1, transportStreamsOf},
	{"ISDBS", "CS", communicationsTransponders, 2, transportStreamsOf},
	{"ISDBS3", "BS TLV", broadcastTransponders, 1, tlvStreamsOf},
	{"ISDBS3", "CS TLV", communicationsTransponders, 1, tlvStreamsOf},
}

type satellitePass struct {
	system string
	what   string
	on     []transponder
	// networks is how many the band carries. It is what ends the walk: a network's
	// table names every transponder of that network, so once they have all spoken
	// there is nothing left to ask. Without it the walk goes on to every transponder
	// no table mentioned, which on BS is three tunes that can only fail.
	networks int
	read     func(context.Context, Options, transponder, string) ([]channelconfig.Entry, uint16, error)
}

func discoverSatellite(ctx context.Context, options Options, display *discoveryDisplay,
	covering func(string) bool,
) ([]channelconfig.Entry, error) {
	seen := map[string]bool{}
	var entries []channelconfig.Entry
	for _, pass := range satellitePasses {
		if !covering(pass.system) {
			continue
		}
		for _, entry := range discover(ctx, options, display, pass) {
			key := entry.Properties["FREQUENCY"] + "/" + entry.Properties["STREAM_ID"]
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no transponder answered with a network table; give a channel file instead")
	}
	// The tuner is known only once something has been tuned. A driver that names
	// streams by their place on the transponder cannot use the table's identifiers.
	if places := options.Device.RelativeStreams(); places > 0 {
		entries = measureSlots(ctx, options, display, entries, places)
		if len(entries) == 0 {
			return nil, fmt.Errorf("no stream could be given a place on its transponder")
		}
	}
	return entries, nil
}

// discover walks a band's transponders and turns what their network tables say
// into channels.
//
// A network table describes the whole of the network it belongs to, so one
// answer accounts for every transponder of that network. It does not account
// for the others: 110 degree CS is two networks on one band, and stopping at
// the first answer gave three transponders of the twelve up there. So the walk
// goes on to the first transponder no table has spoken for, and stops when none
// is left.
func discover(ctx context.Context, options Options, display *discoveryDisplay,
	pass satellitePass,
) []channelconfig.Entry {
	var found []channelconfig.Entry
	var accounted []int
	var networks []uint16
	heard := map[uint16][]heardTable{}
	visited := map[int]bool{}
	read := func(index int, on transponder) {
		visited[index] = true
		display.line("%s %d/%d %s ... ", pass.what, index+1, len(pass.on), on.name)
		entries, network, err := pass.read(ctx, options, on, pass.system)
		switch {
		case err != nil:
			display.finish(err.Error())
		case len(entries) == 0:
			display.finish("nothing this can reach")
		default:
			display.finish(fmt.Sprintf("found %d streams", len(entries)))
			if !slices.Contains(networks, network) {
				networks = append(networks, network)
			}
			for _, entry := range entries {
				if kHz, convErr := strconv.Atoi(entry.Properties["FREQUENCY"]); convErr == nil {
					accounted = append(accounted, kHz)
				}
			}
			table := heardTable{from: on.name, entries: entries}
			if earlier := heard[network]; len(earlier) > 0 && !earlier[0].agrees(table) {
				display.line("  the table heard on %s disagrees with the one heard on %s: %s",
					on.name, earlier[0].from, earlier[0].difference(table))
				display.finish("")
			}
			heard[network] = append(heard[network], table)
			for _, entry := range entries {
				if !slices.ContainsFunc(found, func(had channelconfig.Entry) bool {
					return samePlace(had, entry)
				}) {
					found = append(found, entry)
				}
			}
		}
	}
	// Every network first, skipping the transponders a table has spoken for.
	for index, on := range pass.on {
		if ctx.Err() != nil || len(networks) >= pass.networks {
			break
		}
		if spokenFor(accounted, on.kHz) {
			continue
		}
		read(index, on)
	}
	// Then a second opinion on each, and all of them where opinions differ.
	// Only the transponders some table named: a position no table mentions has
	// nothing on it by anyone's account.
	for index, on := range pass.on {
		if ctx.Err() != nil || heardEnough(heard, networks) {
			break
		}
		if visited[index] || !spokenFor(accounted, on.kHz) {
			continue
		}
		read(index, on)
	}
	return found
}

// samePlace says two entries name the same multiplex in the same place.
func samePlace(one, other channelconfig.Entry) bool {
	return one.Properties["FREQUENCY"] == other.Properties["FREQUENCY"] &&
		one.Properties["STREAM_ID"] == other.Properties["STREAM_ID"]
}

// heardTable is what one transponder's network table named.
type heardTable struct {
	from    string
	entries []channelconfig.Entry
}

// places is the multiplexes a table named, each by where it is and which it is.
func (table heardTable) places() map[string]bool {
	places := map[string]bool{}
	for _, entry := range table.entries {
		places[entry.Properties["FREQUENCY"]+"/"+entry.Properties["STREAM_ID"]] = true
	}
	return places
}

// agrees says two tables named the same multiplexes in the same places.
func (table heardTable) agrees(other heardTable) bool {
	return maps.Equal(table.places(), other.places())
}

// difference says what one table named that the other did not, both ways, as
// frequency/identifier.
func (table heardTable) difference(other heardTable) string {
	mine, theirs := table.places(), other.places()
	var only, onlyOther []string
	for place := range mine {
		if !theirs[place] {
			only = append(only, place)
		}
	}
	for place := range theirs {
		if !mine[place] {
			onlyOther = append(onlyOther, place)
		}
	}
	slices.Sort(only)
	slices.Sort(onlyOther)
	return fmt.Sprintf("only %s names %v, only %s names %v", table.from, only, other.from, onlyOther)
}

// heardEnough says every network has been heard from two transponders that
// agree. One that has been heard to disagree is never enough: the walk then
// reads every transponder.
func heardEnough(heard map[uint16][]heardTable, networks []uint16) bool {
	for _, network := range networks {
		tables := heard[network]
		if len(tables) < 2 {
			return false
		}
		for _, table := range tables[1:] {
			if !tables[0].agrees(table) {
				return false
			}
		}
	}
	return true
}

// spokenFor says a transponder is one a table already described, so tuning it
// would read the same network again. Near enough rather than exactly: the
// plan's frequencies are nominal and a table's are worked out from the delivery
// descriptor.
func spokenFor(accounted []int, kHz int) bool {
	for _, known := range accounted {
		if known-kHz < spokenForMargin && kHz-known < spokenForMargin {
			return true
		}
	}
	return false
}

// spokenForMargin is how far apart two figures for one transponder may be.
// Japan's satellite transponders are tens of megahertz apart.
const spokenForMargin = 1000

// transportStreamsOf reads the older network, whose streams are asked for by
// relative position because their identifiers are the unknown.
func transportStreamsOf(ctx context.Context, options Options, on transponder, system string) ([]channelconfig.Entry, uint16, error) {
	analysis, _, err := readTransponder(ctx, options, on.kHz, "0", networkTablePeriod, networksSettled)
	if err != nil {
		return nil, 0, err
	}
	if analysis.Transport == nil {
		return nil, 0, fmt.Errorf("no network table in the stream")
	}
	return satelliteEntries(analysis.Transport.Networks, system, options.Dish), analysis.Transport.NetworkID, nil
}

// tlvStreamsOf reads a TLV network. There is no asking by position: the
// demodulator wants an identifier before it will settle on the wideband
// carrier, so one is guessed from the transponder (see transponder.tlvProbe).
// The guess only has to get in.
func tlvStreamsOf(ctx context.Context, options Options, on transponder, system string) ([]channelconfig.Entry, uint16, error) {
	if on.tlvProbe == 0 {
		return nil, 0, fmt.Errorf("no TLV carrier is looked for here")
	}
	analysis, format, err := readTransponder(ctx, options, on.kHz, strconv.Itoa(int(on.tlvProbe)), networkTablePeriod, tlvProbeSettled)
	if err != nil {
		// Transport packets on a TLV probe are the answer, not a failure to read.
		if format == "ts" {
			return nil, 0, fmt.Errorf("not a TLV carrier")
		}
		return nil, 0, err
	}
	if analysis.TLV == nil {
		return nil, 0, fmt.Errorf("no TLV network table in the stream")
	}
	var entries []channelconfig.Entry
	seen := map[uint16]bool{}
	for _, network := range analysis.TLV.Networks {
		for _, stream := range network.Streams {
			if seen[stream.TLVStreamID] {
				continue
			}
			channel, ok := SatelliteChannel(stream.Satellite, system, options.Dish)
			if !ok {
				continue
			}
			seen[stream.TLVStreamID] = true
			entries = append(entries, satelliteEntry(stream.TLVStreamID, channel))
		}
	}
	sortEntries(entries)
	return entries, analysis.TLV.NetworkID, nil
}

// readTransponder tunes one transponder and reads what its stream says about the
// network it belongs to. The format returned is what the sample turned out to be,
// which a caller probing for TLV needs even when the read failed.
func readTransponder(ctx context.Context, options Options, frequencyKHz int, streamID string, atLeast time.Duration, settled func([]byte) bool) (*isdb.Analysis, string, error) {
	return readCarrier(ctx, options, map[string]string{
		"DELIVERY_SYSTEM": "ISDBS",
		"FREQUENCY":       strconv.Itoa(frequencyKHz),
		"STREAM_ID":       streamID,
	}, atLeast, settled)
}

// readCarrier tunes one carrier and reads what its stream says about the
// network it belongs to. The channel is given whole rather than built here,
// because what a carrier needs differs by the band it is on.
func readCarrier(ctx context.Context, options Options, properties map[string]string, atLeast time.Duration, settled func([]byte) bool) (*isdb.Analysis, string, error) {
	// Asking for a stream by position needs the transmission control block, which
	// arrives with the signal. The first attempt on a cold transponder often beats
	// it there; by the second the carrier is up and the block is in.
	var err error
	for attempt := range tuneAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(tuneRetryDelay):
			}
		}
		if err = options.Device.Tune(properties); err == nil {
			break
		}
	}
	if err != nil {
		return nil, "", err
	}
	_, locked, err := waitForLock(ctx, options.Device, options.LockTimeout, options.PollInterval, nil)
	if err != nil {
		return nil, "", err
	}
	if !locked {
		return nil, "", fmt.Errorf("no lock")
	}
	// The network table comes round every ten seconds and on a thin transponder
	// takes most of that to come over, so a shorter capture can miss it while the
	// stream is perfectly good. Only this capture is lengthened, not the scan's.
	reading := options
	reading.AnalysisTime = max(reading.analysisTime(properties["DELIVERY_SYSTEM"]), atLeast)
	sample, _, err := receiveSampleFor(ctx, reading, settled, nil)
	format := isdb.DetectFormat(sample)
	if err != nil {
		return nil, format, err
	}
	analysis := isdb.Analyze(sample, format)
	if analysis == nil {
		return nil, format, fmt.Errorf("nothing readable in the stream")
	}
	return analysis, format, nil
}

// satelliteEntries turns what the network said into channels to scan. A
// transport is skipped when the network did not say where it is, or said
// somewhere no receiver here can reach: the downlink frequency is tunable only
// once the oscillator has been subtracted, and which oscillator depends on the
// hand of the polarisation.
func satelliteEntries(networks []isdb.NetworkInfo, system string, dish Dish) []channelconfig.Entry {
	seen := map[uint16]bool{}
	var entries []channelconfig.Entry
	for _, network := range networks {
		for _, transport := range network.Transports {
			if seen[transport.TransportStreamID] {
				continue
			}
			channel, ok := SatelliteChannel(transport.Satellite, system, dish)
			if !ok {
				continue
			}
			seen[transport.TransportStreamID] = true
			entries = append(entries, satelliteEntry(transport.TransportStreamID, channel))
		}
	}
	sortEntries(entries)
	return entries
}

// satelliteEntry is one multiplex as a channel to scan: what the descriptor
// said about it, and the identifier that selects it out of the transponder. The
// identifier is the transport's, not the descriptor's.
func satelliteEntry(streamID uint16, channel map[string]string) channelconfig.Entry {
	channel["STREAM_ID"] = strconv.Itoa(int(streamID))
	return channelconfig.Entry{
		Name:       strconv.Itoa(int(streamID)),
		Source:     "the network information table",
		Properties: channel,
	}
}

// cableEntries is the same for a cable network, which describes its own
// multiplexes in the same table under a descriptor of its own.
//
// A cable is asked alongside the band plan rather than instead of it: a
// Japanese operator's table may describe one stream however many carriers it is
// asked on, so a table that named three carriers has not said the other hundred
// are empty. What it does say is how the ones it names are modulated. See
// discoverWalked.
func cableEntries(networks []isdb.NetworkInfo, system string) []channelconfig.Entry {
	seen := map[uint16]bool{}
	var entries []channelconfig.Entry
	for _, network := range networks {
		for _, transport := range network.Transports {
			if seen[transport.TransportStreamID] {
				continue
			}
			channel, ok := CableChannel(transport.Cable, system)
			if !ok {
				continue
			}
			seen[transport.TransportStreamID] = true
			entries = append(entries, channelconfig.Entry{
				// By frequency: a cable has no plan to name a carrier from, and the
				// identifier is not one a person recognises.
				Name:       channel["FREQUENCY"],
				Source:     "the network information table",
				Properties: channel,
			})
		}
	}
	sortEntries(entries)
	return entries
}

// sortEntries puts channels in transponder order, then by identifier, so a scan
// runs up the band and the same satellite gives the same report twice running.
func sortEntries(entries []channelconfig.Entry) {
	slices.SortStableFunc(entries, func(left, right channelconfig.Entry) int {
		leftHz, _ := strconv.Atoi(left.Properties["FREQUENCY"])
		rightHz, _ := strconv.Atoi(right.Properties["FREQUENCY"])
		if leftHz != rightHz {
			return cmp.Compare(leftHz, rightHz)
		}
		return strings.Compare(left.Name, right.Name)
	})
}

const (
	// How hard a transponder is tried before moving to the next one.
	tuneAttempts   = 3
	tuneRetryDelay = 700 * time.Millisecond
	// networkTablePeriod is how long a network table can take to arrive whole: ten
	// seconds to come round plus, on a thin transponder, most of another ten to come
	// over. See scanner.AnalysisTimeFor.
	networkTablePeriod = satelliteAnalysisTime
)

// The transponders tried, by satellite. Left-hand transponders are not among
// them: a network reached from a right-hand one describes its left-hand
// transponders itself.
var (
	broadcastTransponders      = bootstrapTransponders("BS", 0xb000)
	communicationsTransponders = bootstrapTransponders("ND", 0xc000)
)

// transponder is one of the fixed positions, and how to get in on it.
type transponder struct {
	name string
	kHz  int
	// tlvProbe is an identifier to try when looking for a TLV network. The advanced
	// services number their streams from the transponder they are on (BS 7 carries
	// 0xB070), a guess good enough to get in.
	tlvProbe uint16
}

// bootstrapTransponders is one satellite's right-hand transponders, out of the
// published plan, with a way in to a TLV network on each.
func bootstrapTransponders(prefix string, tlvPrefix uint16) []transponder {
	var list []transponder
	for _, one := range plan.RightHandSatelliteTransponders() {
		if !strings.HasPrefix(one.Name, prefix) {
			continue
		}
		list = append(list, transponder{
			name:     one.Name,
			kHz:      one.FrequencyKHz,
			tlvProbe: tlvPrefix | uint16(one.Number<<4),
		})
	}
	return list
}

// A satellite demodulator is told which stream to demultiplex by identifier,
// which it finds in the transmission control block. Most drivers pass an
// identifier straight through; the Earthsoft character-device drivers read that
// block themselves and take an index into it instead -- the numbering recpt1
// writes as BS01_1 -- so the identifiers a network information table gives must
// be translated first.
//
// Which tuners those are is not a list here: keying it by backend name was
// wrong in both directions. The tuner is asked instead. See
// host.Device.RelativeStreams.

// slotSampleTime is long enough for a programme association table, where a stream
// says its own identifier; it comes round about ten times a second.
const slotSampleTime = 2 * time.Second

// measureSlots works out which place on its transponder each stream occupies,
// by tuning the places and reading what comes out. The character-device
// interface has no ioctl returning the transmission control block, so it costs
// a tune per stream and is done only for the drivers that need it.
//
// An empty place is recognised by what the driver does with it: the index is
// not checked against the block, so it selects nothing and the previous stream
// keeps coming. An identifier already seen therefore means the places have run
// out.
func measureSlots(ctx context.Context, options Options, display *discoveryDisplay,
	entries []channelconfig.Entry, places int,
) []channelconfig.Entry {
	slots := map[string]map[uint16]int{}
	for _, entry := range entries {
		frequency := entry.Properties["FREQUENCY"]
		if _, done := slots[frequency]; done {
			continue
		}
		if ctx.Err() != nil {
			return entries
		}
		slots[frequency] = measureTransponder(ctx, options, display, frequency, places)
	}

	translated := make([]channelconfig.Entry, 0, len(entries))
	for _, entry := range entries {
		streamID, err := strconv.Atoi(entry.Properties["STREAM_ID"])
		if err != nil {
			continue
		}
		slot, known := slots[entry.Properties["FREQUENCY"]][uint16(streamID)]
		if !known {
			display.finish(fmt.Sprintf("%s: no place on its transponder answered with it", entry.Name))
			continue
		}
		entry.Properties = maps.Clone(entry.Properties)
		entry.Properties["STREAM_ID"] = strconv.Itoa(slot)
		entry.Source = "the network information table, and the transponder for its place on it"
		translated = append(translated, entry)
	}
	return translated
}

// measureTransponder reads the identifier at each place of one transponder.
func measureTransponder(ctx context.Context, options Options, display *discoveryDisplay,
	frequency string, places int,
) map[uint16]int {
	kilohertz, err := strconv.Atoi(frequency)
	if err != nil {
		return nil
	}
	found := map[uint16]int{}
	display.line("places on %s kHz ... ", frequency)
	for slot := range places {
		if ctx.Err() != nil {
			break
		}
		analysis, _, err := readTransponder(ctx, options, kilohertz, strconv.Itoa(slot), slotSampleTime, analysisSettled)
		if err != nil || analysis == nil || analysis.Transport == nil {
			break
		}
		streamID := analysis.Transport.TransportStreamID
		if streamID == 0 {
			break
		}
		if _, repeated := found[streamID]; repeated {
			// The place is empty and the driver left the last stream selected.
			break
		}
		found[streamID] = slot
	}
	display.finish(fmt.Sprintf("%d streams", len(found)))
	return found
}
