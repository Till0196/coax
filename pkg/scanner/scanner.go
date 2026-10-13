// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package scanner is the loop that tries channels: tune one, wait for the
// demodulator, take a sample and record what came of it. It knows nothing about
// hardware nor about what a channel list looks like afterwards; what it does know
// is when to give up on a channel, which is most of what a scan is.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	scanprogress "github.com/Till0196/coax/pkg/progress"
	"github.com/Till0196/coax/pkg/report"
	"github.com/Till0196/coax/pkg/tuner"
)

type Options struct {
	Device      tuner.Device
	LockTimeout time.Duration
	DataTimeout time.Duration
	// AnalysisTime is the longest a channel is held for analysis. Zero takes the
	// bound from what the channel is; see AnalysisTimeFor.
	AnalysisTime time.Duration
	// HoldAnalysisTime keeps the channel for the whole of AnalysisTime rather than
	// leaving as soon as it has said what it is.
	//
	// The default is to leave early: the tables that say what a channel is arrive in
	// the first seconds. What arrives later is the rest of the network, which comes
	// round slowly. A caller who named a time asked for that much of the stream.
	HoldAnalysisTime bool
	PollInterval     time.Duration
	MinimumBytes     int
	MaximumBytes     int
	RequireData      bool
	Verbose          bool
	Previous         map[string]scanResult
	OnResult         func(scanResult) error
	// Tuner is the tuner this run is pinned to, empty when the daemon is to choose.
	// It tells a resumable result from one another tuner left behind, the progress
	// file being shared by every scan run in a directory.
	Tuner string
	// Dish is what stands between a satellite's downlink frequencies and this tuner.
	// The zero value is the Japanese arrangement. See Dish.
	Dish Dish
	// SatelliteVoltage is the VOLTAGE a satellite channel is tuned with when it
	// names none, as --lnb-power gives it; empty asks for no supply.
	SatelliteVoltage string
}

type scanResult = report.Result
type scanReport = report.Report

func Scan(ctx context.Context, entries []channelconfig.Entry, options Options, progress io.Writer) (scanReport, error) {
	if options.Device == nil {
		return scanReport{}, fmt.Errorf("receiver device is required")
	}
	options = withSatelliteSupply(options)
	report := scanReport{
		Backend: options.Device.Backend(),
		Device:  options.Device.Name(),
		Started: time.Now(),
		Results: make([]scanResult, 0, len(entries)),
	}
	display := scanprogress.NewDisplay(progress)
	defer options.Device.Close()

	for index, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if previous, ok := options.Previous[scanprogress.Key(entry)]; ok && resumable(previous, options.Tuner) {
			report.Results = append(report.Results, previous)
			display.Resumed(index+1, len(entries), channelLabel(entry), previous.Status)
			continue
		}
		display.Begin(index+1, len(entries), channelLabel(entry))
		start := time.Now()
		result, said := scanEntry(ctx, entry, options, display)
		result.ElapsedMS = time.Since(start).Milliseconds()
		if err := appendScanResult(&report, result, options); err != nil {
			return report, err
		}
		display.Finish(said)
		display.Note(channelNotes(result.Analysis)...)
	}
	return report, nil
}

// resumable says whether a previous result stands in for trying the channel
// again.
//
// Only an answer about the channel itself does: it was there, nothing was
// there, or the tuner does not receive that kind of channel. A lock with no
// data behind it and an outright error are answers about the attempt, so the
// channel is retried.
//
// A result another tuner left behind is no answer either: tuners sit on
// different leads and the progress file is shared by every scan run in the same
// directory.
func resumable(previous scanResult, tuner string) bool {
	if tuner != "" && previous.Device != "" && previous.Device != tuner {
		return false
	}
	switch previous.Status {
	case "found", "no_lock", "unsupported":
		return true
	}
	return false
}

// tuneTicker is the part of the progress display a tune has any use for.
type tuneTicker interface {
	Update(phase string, current, total int64, detail string)
}

// tuningTick is how often the progress line is written while a tune is in flight.
const tuningTick = 200 * time.Millisecond

// whileTuning keeps the progress line moving while the tune is in flight.
//
// A tune is one blocking call and can take seconds: where a vendor BDA graph is
// built against a frequency the aerial carries nothing at, the vendor driver
// waits out its own lock timeout -- five seconds on a TBS 6205se. A line that
// neither moves nor counts up for that long reads as a hang.
func whileTuning(display tuneTicker, tune func() error) error {
	done := make(chan error, 1)
	go func() { done <- tune() }()
	ticking := time.NewTicker(tuningTick)
	defer ticking.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ticking.C:
			display.Update(scanprogress.PhaseTuning, 0, 0, "")
		}
	}
}

// scanEntry is one channel: tune it, wait for a lock, take a sample, and return
// the result plus the line to show for it. Timing, recording and display are
// the caller's, so none of the eight ways a channel can end has to repeat them.
func scanEntry(ctx context.Context, entry channelconfig.Entry, options Options, display *scanprogress.Display) (scanResult, string) {
	result := scanResult{Entry: entry, Status: "error"}
	options.AnalysisTime = options.analysisTime(entry.Properties["DELIVERY_SYSTEM"])

	supported, supportErr := options.Device.Supports(entry.Properties["DELIVERY_SYSTEM"])
	if supportErr != nil {
		result.Error = supportErr.Error()
		return result, "error: " + supportErr.Error()
	}
	if !supported {
		result.Status = "unsupported"
		result.Error = "frontend does not advertise this delivery system"
		return result, "unsupported"
	}

	if tuneErr := whileTuning(display, func() error { return options.Device.Tune(entry.Properties) }); tuneErr != nil {
		result.Error = tuneErr.Error()
		// A frequency the tuner cannot reach is the same answer as a delivery
		// system it does not carry, not a failure of the scan.
		if errors.Is(tuneErr, tuner.ErrOutOfRange) {
			result.Status = "unsupported"
			return result, "unsupported"
		}
		return result, "error: " + tuneErr.Error()
	}

	frontendStatus, locked, lockErr := waitForLock(ctx, options.Device, options.LockTimeout, options.PollInterval,
		func(elapsed time.Duration, status tuner.Status) {
			display.Update(scanprogress.PhaseLocking, elapsed.Milliseconds(), options.LockTimeout.Milliseconds(), statusDetail(status))
		})
	result.ReceiverStatus = frontendStatus.Names
	if lockErr != nil {
		result.Error = lockErr.Error()
		return result, "error: " + lockErr.Error()
	}
	if !locked {
		result.Status = "no_lock"
		return result, "no lock"
	}

	signal := &signalWatch{device: options.Device}
	if options.RequireData {
		sample, took, dataErr := receiveSample(ctx, options, func(elapsed time.Duration, bytes int) {
			detail := sampledRate(bytes, elapsed)
			if measurements := signal.measurements(); measurements != "" {
				if detail != "" {
					detail += " "
				}
				detail += measurements
			}
			display.Update(scanprogress.PhaseSampling, elapsed.Milliseconds(), (options.DataTimeout + options.AnalysisTime).Milliseconds(), detail)
		})
		result.BytesRead = len(sample)
		result.StreamFormat = isdb.DetectFormat(sample)
		result.Analysis = isdb.Analyze(sample, result.StreamFormat)
		result.ThroughputMbps = throughput(len(sample), took)
		if dataErr != nil {
			result.Status = "no_data"
			result.Error = dataErr.Error()
			return result, "locked, but no data"
		}
	}

	result.Status = "found"
	return result, describeFound(result, options.Device, signal.reading)
}

// describeFound is the line a channel that was found is reported under.
func describeFound(result scanResult, device tuner.Device, sampled string) string {
	found := "found"
	if result.StreamFormat != "" {
		found = fmt.Sprintf("found (%s, %d bytes)", result.StreamFormat, result.BytesRead)
	}
	if rate := multiplexRate(result.Analysis); rate != nil {
		found += fmt.Sprintf(" %.2f Mbps", *rate)
	} else if result.ThroughputMbps != nil {
		// Marked, because this was timed by the receiver rather than the sender's
		// clock.
		found += fmt.Sprintf(" ~%.2f Mbps received", *result.ThroughputMbps)
	}
	// What was read while the sample was running is preferred: the stream is
	// closed by now, so a reading taken here could only come from the tune report,
	// which is from before the demodulator had locked.
	measurements := sampled
	if measurements == "" {
		measurements = settledMeasurements(device)
	}
	if measurements != "" {
		found += " " + measurements
	}
	return found
}

// The indents the lines under a channel are written with.
const (
	frameIndent   = "    "
	streamIndent  = "          "
	serviceIndent = "            "
)

// channelNotes is what a channel turned out to carry, written under its own
// line: each slot of the multiplexing frame, and under it the network and
// services of the stream in that slot. The slots are headings -- a carrier can
// hold four -- which is why they are not on the channel's own line.
func channelNotes(analysis *isdb.Analysis) []string {
	if analysis == nil {
		return nil
	}
	multiplex := analysis.TSMF
	if multiplex == nil || len(multiplex.RelativeStreams) == 0 {
		// Nothing above it to belong to, so a channel that named no services says
		// nothing rather than leaving a bare identifier behind.
		if len(streamServices(analysis.Transport, analysis.TLV)) == 0 {
			return nil
		}
		return streamNotes(analysis.Transport, analysis.TLV)
	}
	var lines []string
	if carrier := carrierLine(multiplex); carrier != "" {
		lines = append(lines, frameIndent+carrier)
	}
	for _, slot := range multiplex.RelativeStreams {
		lines = append(lines, frameIndent+slotLine(slot))
		if len(streamServices(slot.Transport, slot.TLV)) > 0 {
			lines = append(lines, streamNotes(slot.Transport, slot.TLV)...)
			continue
		}
		// Nothing of its own to say. What the network table says about it is the next
		// best thing, and its own heading is what is left when even that is missing.
		if listed := listedNotes(analysis, slot); len(listed) > 0 {
			lines = append(lines, listed...)
			continue
		}
		lines = append(lines, streamNotes(slot.Transport, slot.TLV)...)
	}
	return lines
}

// listedNotes is what the network table says a slot carries, for a slot whose
// own service table did not arrive.
//
// A multiplex puts its slots on unequal shares of the carrier, and a thin one's
// service table can be slower than a scan is willing to wait. The network table
// describes every stream of the network and arrives on the slots that are not
// thin.
//
// Marked as the network table's word rather than the stream's, that being
// weaker. The names beside them come from whichever service table named the
// service, so a slot that was never tuned can still be named.
func listedNotes(analysis *isdb.Analysis, slot isdb.RelativeStreamInfo) []string {
	streamID, _ := slotIdentifiers(slot)
	if streamID == 0 {
		return nil
	}
	described, network, found := networkListing(analysis, streamID)
	if !found || len(described.Services) == 0 {
		return nil
	}
	heading := []string{network, identifier("TSID", described.TransportStreamID),
		identifier("ONID", described.OriginalNetworkID)}
	lines := []string{streamIndent + strings.Join(slices.DeleteFunc(heading, func(part string) bool {
		return part == ""
	}), "  ") + "  (from the network table)"}
	services := make([]isdb.ServiceInfo, 0, len(described.Services))
	for _, listed := range described.Services {
		services = append(services, isdb.ServiceInfo{
			ServiceID:   listed.ServiceID,
			Type:        listed.Type,
			Name:        listed.Name,
			DecodeLossy: listed.DecodeLossy,
		})
	}
	for _, line := range describeServices(services) {
		lines = append(lines, serviceIndent+line)
	}
	return lines
}

// networkListing finds a stream's entry in whichever network table this channel
// carried. Any slot may be the one carrying the table, so all are looked through.
func networkListing(analysis *isdb.Analysis, streamID uint16) (isdb.NetworkTransport, string, bool) {
	transports := []*isdb.TransportInfo{analysis.Transport}
	if analysis.TSMF != nil {
		for _, slot := range analysis.TSMF.RelativeStreams {
			transports = append(transports, slot.Transport)
		}
	}
	for _, transport := range transports {
		if transport == nil {
			continue
		}
		for _, network := range transport.Networks {
			for _, described := range network.Transports {
				if described.TransportStreamID == streamID {
					return described, network.Name, true
				}
			}
		}
	}
	return isdb.NetworkTransport{}, "", false
}

// streamNotes is one stream written out: the network and identifiers that
// select it again, then a line for each service it carries. Named rather than
// counted, and identified, because a channel file selects a stream by number.
//
// A stream that named no services still gets its line, so a slot does not break
// the shape the ones around it were read in.
func streamNotes(transport *isdb.TransportInfo, tlv *isdb.TLVInfo) []string {
	var lines []string
	if heading := streamHeading(transport, tlv); heading != "" {
		lines = append(lines, streamIndent+heading)
	}
	for _, line := range describeServices(streamServices(transport, tlv)) {
		lines = append(lines, serviceIndent+line)
	}
	return lines
}

// streamHeading names the stream the services under it came out of.
func streamHeading(transport *isdb.TransportInfo, tlv *isdb.TLVInfo) string {
	var parts []string
	switch {
	// Whichever side named services is the one being described. A stream that named
	// none is still headed by what it is.
	case transport != nil && (len(transport.Services) > 0 || tlv == nil):
		parts = append(parts, networkName(transport))
		parts = append(parts, identifier("TSID", transport.TransportStreamID))
		parts = append(parts, identifier("ONID", transport.OriginalNetworkID))
	case tlv != nil:
		parts = append(parts, tlvNetworkName(tlv))
		parts = append(parts, identifier("TLVSID", tlv.TLVStreamID))
		parts = append(parts, identifier("ONID", tlv.OriginalNetworkID))
	}
	return strings.Join(slices.DeleteFunc(parts, func(part string) bool { return part == "" }), "  ")
}

// identifier is one named number of a heading, left out when the stream did not
// say it: a zero here is silence rather than a value.
func identifier(name string, value uint16) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%s=%d", name, value)
}

// networkName is what the stream's own network calls itself. The table describes
// other networks as well, so the one this stream belongs to is the one to take.
func networkName(transport *isdb.TransportInfo) string {
	for _, network := range transport.Networks {
		if network.Actual && network.Name != "" {
			return network.Name
		}
	}
	return ""
}

// tlvNetworkName is the same for a TLV stream, whose table names its network by
// identifier rather than marking one of them as its own.
func tlvNetworkName(tlv *isdb.TLVInfo) string {
	for _, network := range tlv.Networks {
		if network.NetworkID == tlv.NetworkID && network.Name != "" {
			return network.Name
		}
	}
	return ""
}

// describeServices is one line per service: what selects it, what it calls
// itself, who runs it, and what it is.
func describeServices(services []isdb.ServiceInfo) []string {
	lines := make([]string, 0, len(services))
	for _, service := range services {
		name := service.Name
		if name == "" {
			// The sample was too short for the table that names them, or the service
			// came from a table that carries no names.
			name = "(unnamed)"
		}
		line := fmt.Sprintf("%d  %s", service.ServiceID, name)
		if service.Provider != "" {
			line += "  " + service.Provider
		}
		if kind := serviceKind(service); kind != "" {
			line += "  (" + kind + ")"
		}
		lines = append(lines, line)
	}
	return lines
}

// serviceKind is what a service is and whether it is under conditional access,
// which is what tells a channel worth recording from the data and promotional
// services beside it.
func serviceKind(service isdb.ServiceInfo) string {
	var parts []string
	// One-segment is television, but its service type is the plain data one, so only
	// the layer the network puts it on tells it from a data service.
	if service.PartialReception {
		parts = append(parts, "1seg")
	} else if kind := serviceTypeName(service.Type); kind != "" {
		parts = append(parts, kind)
	}
	if service.FreeCAMode {
		// free_CA_mode: a card is needed to make anything of the streams.
		parts = append(parts, "scrambled")
	}
	return strings.Join(parts, ", ")
}

// serviceTypeNames are the service types of ARIB STD-B10 part 2, in the words a
// person reading a scan wants rather than the standard's own.
var serviceTypeNames = map[byte]string{
	0x01: "TV",
	0x02: "audio",
	0xa1: "temporary video",
	0xa2: "temporary audio",
	0xa3: "temporary data",
	0xa4: "engineering",
	0xa5: "promotional video",
	0xa6: "promotional audio",
	0xa7: "promotional data",
	0xa8: "data for later use",
	0xa9: "stored data",
	0xaa: "bookmark list",
	0xab: "server simulcast",
	0xac: "independent file",
	0xad: "UHD TV",
	0xc0: "data",
}

// serviceTypeName is what a service type is called. An unknown type is shown as
// its number rather than left out; a zero is the table having said nothing.
func serviceTypeName(serviceType byte) string {
	if serviceType == 0 {
		return ""
	}
	if name, known := serviceTypeNames[serviceType]; known {
		return name
	}
	return fmt.Sprintf("type %#02x", serviceType)
}

// analysisServices is the services of whichever half of the analysis has them: a
// transport stream carries them in its SDT, and a TLV stream in its MH-SDT.
func analysisServices(analysis *isdb.Analysis) []isdb.ServiceInfo {
	if analysis == nil {
		return nil
	}
	return streamServices(analysis.Transport, analysis.TLV)
}

// streamServices is the same question asked of one stream.
func streamServices(transport *isdb.TransportInfo, tlv *isdb.TLVInfo) []isdb.ServiceInfo {
	if transport != nil && len(transport.Services) > 0 {
		return transport.Services
	}
	if tlv != nil {
		return tlv.Carried()
	}
	return nil
}

// analysisTime is the bound for one channel: what the caller asked for, or what
// the channel is, when the caller left it open.
func (options Options) analysisTime(deliverySystem string) time.Duration {
	if options.AnalysisTime > 0 {
		return options.AnalysisTime
	}
	return AnalysisTimeFor(deliverySystem)
}

// AnalysisTimeFor is how long a channel of one kind is worth holding.
//
// The two figures differ by an order of magnitude because the tables do. Over
// the air the network table comes round every second; from a satellite every
// ten, and a thin transponder measured at 13 Mbps spends 9.25s transmitting the
// table it sends every 10. A capture beginning part way through has to wait for
// the next, so twenty seconds is the shortest bound that reliably holds one
// whole.
//
// A cable takes the satellite's figure because it carries satellite multiplexes
// remodulated onto it, cadence and all.
//
// This is a maximum; a channel that has said everything is left before it.
func AnalysisTimeFor(deliverySystem string) time.Duration {
	switch channelconfig.CanonicalDeliverySystem(deliverySystem) {
	case "ISDBT", "DVBT", "DVBT2":
		return terrestrialAnalysisTime
	default:
		return satelliteAnalysisTime
	}
}

const (
	terrestrialAnalysisTime = 5 * time.Second
	satelliteAnalysisTime   = 22 * time.Second
)

// carrierLine says which carrier of a split multiplex this is, and nothing for
// the usual multiplex that sits on one: a third of a multiplex holds a third of
// what is on it, and a channel that did not say so reads as broken.
func carrierLine(multiplex *isdb.TSMFInfo) string {
	if multiplex == nil || multiplex.NumberOfCarriers <= 1 {
		return ""
	}
	return fmt.Sprintf("TSMF carrier %d/%d", multiplex.CarrierSequence, multiplex.NumberOfCarriers)
}

// slotLine names one slot of a TSMF multiplex as the multiplex names it: the
// relative stream number from the frame header, and the identifiers it declares.
//
// A header that left a slot's identifiers at zero is not a slot without them: the
// stream in the slot says what it is in its own tables, and that stands in.
//
// The format is the slot's own, not the carrier's: the stream format beside a
// scan's byte count says "ts" whatever the slots hold, so a slot carrying TLV
// would otherwise be indistinguishable from an ordinary one.
func slotLine(relative isdb.RelativeStreamInfo) string {
	format := relative.Format
	if format == "" {
		// The header named a slot this program has no reading for.
		format = "?"
	}
	streamID, networkID := slotIdentifiers(relative)
	return fmt.Sprintf("TSMF [#%d %s TSID=%d ONID=%d]", relative.RelativeStream, format, streamID, networkID)
}

// slotIdentifiers is which stream a slot holds and which network it belongs to,
// as the frame header says, or as the stream itself says where the header said
// nothing.
func slotIdentifiers(relative isdb.RelativeStreamInfo) (streamID, networkID uint16) {
	streamID, networkID = relative.StreamID, relative.OriginalNetworkID
	if transport := relative.Transport; transport != nil {
		if streamID == 0 {
			streamID = transport.TransportStreamID
		}
		if networkID == 0 {
			networkID = transport.OriginalNetworkID
		}
	}
	if tlv := relative.TLV; tlv != nil {
		if streamID == 0 {
			streamID = tlv.TLVStreamID
		}
		if networkID == 0 {
			networkID = tlv.OriginalNetworkID
		}
	}
	return streamID, networkID
}

// multiplexRate is how fast the carrier runs. A multiplex carrying several
// streams in slots has no clock of its own, so its rate is worked out from
// those slots.
func multiplexRate(analysis *isdb.Analysis) *float64 {
	if analysis == nil {
		return nil
	}
	if analysis.BitrateMbps != nil {
		return analysis.BitrateMbps
	}
	if analysis.TSMF == nil {
		return nil
	}
	return analysis.TSMF.BitrateMbps
}

// channelLabel names a channel while it is being scanned, by what it is tuned as
// rather than which lead it is on: the lead is declared once with --route, while
// the delivery system differs between a cable's transmodulated channels and the
// transponders it passes through.
func channelLabel(entry channelconfig.Entry) string {
	system := entry.Properties["DELIVERY_SYSTEM"]
	if system == "" {
		return entry.Name
	}
	return fmt.Sprintf("%s/%s", system, entry.Name)
}

// noteDevice records which tuner the scan is running on. It is called as each
// result lands rather than once at the start, because the daemon only picks a
// tuner at the first tune. The last write is the truth: one scan holds one
// tuner for its run.
func noteDevice(report *scanReport, options Options) {
	if options.Device == nil {
		return
	}
	report.Backend = options.Device.Backend()
	report.Device = options.Device.Name()
}

func appendScanResult(report *scanReport, result scanResult, options Options) error {
	noteDevice(report, options)
	if options.Device != nil {
		result.Device = options.Device.Name()
	}
	report.Results = append(report.Results, result)
	if options.OnResult != nil {
		return options.OnResult(result)
	}
	return nil
}

// statusDetail renders a frontend status for the progress line: where the lock
// has got to, then whichever signal measurements the backend could make.
func statusDetail(status tuner.Status) string {
	parts := make([]string, 0, 2)
	if names := lockDetail(status.Names); names != "" {
		parts = append(parts, names)
	}
	if measurements := statusMeasurements(status); measurements != "" {
		parts = append(parts, measurements)
	}
	return strings.Join(parts, " ")
}

// lockDetail is the lock state in as few words as it takes.
//
// A frontend reports every rung it has climbed, and the rungs are worth reading
// only while the top is out of reach. Once it has locked they say nothing LOCK
// does not, and crowd out the measurements a locked channel is watched for.
func lockDetail(names []string) string {
	for _, name := range names {
		if strings.EqualFold(name, "LOCK") {
			return name
		}
	}
	return strings.Join(names, ",")
}

// statusMeasurements renders just the signal readings, for the places that
// already say what the lock state is.
func statusMeasurements(status tuner.Status) string {
	parts := make([]string, 0, 3)
	if status.CNR.Known {
		parts = append(parts, fmt.Sprintf("CNR %.1fdB", status.CNR.Value))
	}
	if status.Strength.Known {
		parts = append(parts, fmt.Sprintf("%.0fdBm", status.Strength.Value))
	}
	if status.Quality.Known {
		parts = append(parts, fmt.Sprintf("quality %.0f%%", status.Quality.Value))
	}
	return strings.Join(parts, " ")
}

// signalInterval is how often the signal is read while a channel is being
// sampled: slow enough to cost the scan nothing, fast enough to watch a figure
// climb.
const signalInterval = time.Second

// signalWatch reads the signal measurements while a channel is being sampled.
//
// A demodulator reports its lock before it has a carrier-to-noise figure, so
// the sampling line would otherwise show nothing about reception for the whole
// wait -- which is what somebody watches to tell a strong channel from a
// marginal one.
//
// Throttled because reading the status is a request of its own and this runs on
// every pass of a fast loop. A reading that fails is left as it was: the last
// figure is better than none.
type signalWatch struct {
	device  tuner.Device
	read    time.Time
	reading string
}

func (watch *signalWatch) measurements() string {
	if !watch.read.IsZero() && time.Since(watch.read) < signalInterval {
		return watch.reading
	}
	watch.read = time.Now()
	if status, err := watch.device.Status(); err == nil {
		watch.reading = statusMeasurements(status)
	}
	return watch.reading
}

// sampledRate is how fast the stream is arriving: a byte count only says the
// sample is longer than it was, while the rate says whether the carrier is
// running at the rate its multiplex should. Empty until there is enough of a
// capture to divide.
func sampledRate(bytes int, elapsed time.Duration) string {
	rate := throughput(bytes, elapsed)
	if rate == nil {
		return ""
	}
	return fmt.Sprintf("%.2f Mbps", *rate)
}

// settledMeasurements reads the signal measurements once more, after the
// channel has been held for a while. A demodulator reports its lock before it
// has a carrier-to-noise figure and the scan moves on as soon as it locks, so
// reading again at the end is what makes the number appear at all.
func settledMeasurements(device tuner.Device) string {
	status, err := device.Status()
	if err != nil {
		return ""
	}
	return statusMeasurements(status)
}
