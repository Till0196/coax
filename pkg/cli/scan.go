// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/exporter"
	"github.com/Till0196/coax/pkg/host"
	scanprogress "github.com/Till0196/coax/pkg/progress"
	"github.com/Till0196/coax/pkg/scanner"
)

type scanOptions struct {
	tunerID      string
	route        string
	include      string
	exclude      string
	lockTimeout  time.Duration
	priority     int
	dataTimeout  time.Duration
	analysisTime time.Duration
	pollInterval time.Duration
	minimumBytes int
	maximumBytes int
	requireData  bool
	output       string
	listOnly     bool
	discover     discoverFlag
	resume       bool
	restart      bool
	keepProgress bool
	progress     string
}

func runScan(arguments []string, stdout io.Writer, stderr io.Writer) error {
	command := newHardwareCommand("scan", stderr)
	flags := command.flags
	options := scanOptions{}
	flags.StringVar(&options.tunerID, "tuner", "", "tuner to use, as `coax list` names it (any that will serve when omitted)")
	flags.StringVar(&options.route, "route", "", "which lead this tuner's aerial socket is fed from: TER, SAT, CATV or HIKARI; recorded on every channel found, and left unsaid when this is")
	flags.StringVar(&options.include, "include", "", "scan only section names matching this regular expression")
	flags.StringVar(&options.exclude, "exclude", "", "skip section names matching this regular expression")
	flags.DurationVar(&options.lockTimeout, "lock-timeout", 5*time.Second, "frontend lock timeout per channel")
	flags.IntVar(&options.priority, "priority", 0, "how much this work matters, against everything else asking the daemon for a tuner: nothing free and a higher number takes a tuner from a lower one, the same number never does, and whoever asked first keeps it; a scan nobody is waiting on is worth a negative number, which lets a recording take its tuner")
	flags.DurationVar(&options.dataTimeout, "data-timeout", 3*time.Second, "DVR data timeout after lock")
	flags.DurationVar(&options.analysisTime, "analysis-time", 0, "how long each channel is held for NIT, SDT and TSMF analysis, for every channel alike; given, the channel is held for the whole of it, which is what fills in the names of the services on the network's other multiplexes -- their service tables come round slowly. Omitted, a channel is left as soon as it has said what it is, within a bound of 5s over the air and 22s from a satellite or a cable")
	flags.DurationVar(&options.pollInterval, "poll-interval", 50*time.Millisecond, "status and DVR polling interval")
	flags.IntVar(&options.minimumBytes, "min-bytes", 188*16, "minimum DVR bytes required")
	flags.IntVar(&options.maximumBytes, "max-bytes", defaultMaximumBytes, "most bytes kept per channel for analysis; this bounds the capture as surely as --analysis-time does, so it has to be big enough to hold that many seconds of the fastest carrier")
	flags.BoolVar(&options.requireData, "require-data", true, "require DVR data in addition to frontend lock")
	flags.StringVar(&options.output, "output", "", "output file, or - for stdout (a name made from the backend, the tuner and the time, written where the command was run, when omitted)")
	flags.BoolVar(&options.listOnly, "list", false, "parse and list candidates without opening a DVB device")
	flags.Var(&options.discover, "discover", "work the channels out instead of reading a channel file, for every delivery system the tuner carries bar ISDBS3, or for the `systems` named as --discover=ISDBT,ISDBS: "+strings.Join(scanner.DiscoverableSystemNames(), ", ")+" (ISDBS3 is the MMT/TLV satellite services, which are looked for only when named: nothing can tell whether a tuner receives them and the walk is long)")
	flags.BoolVar(&options.resume, "resume", true, "resume completed channels from the progress file")
	flags.BoolVar(&options.restart, "restart", false, "discard saved progress before scanning")
	flags.BoolVar(&options.keepProgress, "keep-progress", false, "keep the progress file after a completed scan")
	flags.StringVar(&options.progress, "progress", "", "progress file path")
	// The same channel a `coax tune` command line describes, so that one channel
	// can be scanned without writing a file for it.
	system := flags.String("system", "", "delivery system of a channel named here, as a channel file spells it: ISDBT, ISDBS, DVBT2, ...")
	channel := flags.String("ch", "", "scan this one channel, by name: a television channel (1 to 12, 13 to 62, or C13 to C63 on a cable), a satellite one written either way it is written (BS01, BS01_1, ND02, 16401), or a 124/128 degree east transponder (JCSAT3A/JD5)")
	frequency := flags.String("freq", "", "scan this one channel, by frequency in Hz, or kHz for satellite")
	streamID := flags.String("stream-id", "", "relative stream to select: an ISDB-S TSID, a DVB-T2 pipe, or a TSID multiplexed into a TSMF carrier")
	lnbPower := flags.String("lnb-power", "", "LNB supply for a satellite channel: off, low or high (11 and 13 mean low, 15 and 18 mean high); with --discover or a channel file it goes on every satellite channel that names no VOLTAGE of its own; none when omitted, as recpt1 and recisdb do")
	gain := flags.String("gain", "", "front-end gain, on hardware that has a setting: auto, min, max, or a level with its unit such as +13dB; the tuner's own default when omitted")
	satNumber := flags.String("sat-number", "", "position on a DiSEqC switch, 0 to 3, when the dish reaches this tuner through one; nothing is sent when this is omitted")
	lnbf := flags.String("lnbf", "", "low-noise block on the dish, when --freq is a downlink frequency rather than an intermediate one: UNIVERSAL, EXTENDED, ENHANCED, STANDARD, DBS, C-BAND, 110BS, JCSAT3A, JCSAT4B")
	property := newProperties(flags)
	alias(flags, "o", "output")
	alias(flags, "t", "tuner")
	alias(flags, "f", "freq")
	if err := command.parse(arguments); err != nil {
		return err
	}
	// Before anything the flags set is read: "--discover ISDBS --lnb-power high"
	// stops the first parse at ISDBS, and the flags after it are only set once
	// the system has been taken and the rest parsed again. Read earlier, those
	// values were the empty ones, and a discovery never fed the dish.
	if err := options.discover.takeLeadingSystems(flags); err != nil {
		return err
	}
	named := channelRequest{
		system: *system, channel: *channel, frequency: *frequency, streamID: *streamID,
		lnbPower: *lnbPower, lnbf: *lnbf, gain: *gain, satNumber: *satNumber,
		extra: property.values,
	}
	if err := options.validate(); err != nil {
		return err
	}

	paths := flags.Args()
	// Three ways to say which channels to scan, and they are alternatives:
	// a channel named on the command line, a channel file, or the satellite's
	// own account of itself.
	switch {
	case named.given() && options.discover.enabled:
		return fmt.Errorf("--discover works the channels out for itself; naming a channel as well says two things at once")
	case named.given() && len(paths) != 0:
		return fmt.Errorf("a channel named on the command line and a channel file are two ways of saying what to scan; give one of them")
	case len(paths) != 0 && options.discover.enabled:
		return fmt.Errorf("--discover works the channels out for itself; do not give it a channel file as well")
	case !named.given() && len(paths) == 0 && !options.discover.enabled:
		return fmt.Errorf("nothing to scan; name one channel with --ch or --freq as `coax tune` takes them, or give a DVBv5 channel file, or --discover to read the channels off the satellite")
	}

	var entries []channelconfig.Entry
	var err error
	switch {
	case named.given():
		// One channel, described where a file's section would have been. The
		// include and exclude patterns are left out of it: they pick sections
		// out of a file, and there is no file to pick from.
		entry, buildErr := namedChannel(named, options.route)
		if buildErr != nil {
			return buildErr
		}
		entries = []channelconfig.Entry{entry}
		if options.listOnly {
			return writeEntries(options.outputOrStdout(), stdout, entries)
		}
	case !options.discover.enabled:
		entries, err = options.loadEntries(paths)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return fmt.Errorf("no channel candidates matched")
		}
		if options.listOnly {
			return writeEntries(options.outputOrStdout(), stdout, entries)
		}
	}

	progressPath := options.progressPath()
	if options.restart {
		if err := os.Remove(progressPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	progressState := scanprogress.New(progressPath)
	if options.resume {
		progressState, err = scanprogress.Load(progressPath)
		if err != nil {
			return err
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client, err := command.dial(ctx)
	if err != nil {
		return err
	}
	tuneOptions := host.TuneOptions{
		Tuner:       options.tunerID,
		LockTimeout: options.lockTimeout,
		Settle:      settleTime,
		// The scan does its own waiting, so that the line it is showing counts
		// up while the demodulator makes up its mind and the signal it reads
		// appears on it.
		NoLockWait: true,
		Priority:   options.priority,
	}
	// Every tuner the daemon lists, unless one was named. A satellite tuner
	// cannot scan a cable band, and a host often carries both.
	tuners, err := scanTuners(ctx, client, options.tunerID)
	if err != nil {
		return err
	}

	// A named channel carries the supply in its own properties; a walk or a file
	// has it put on every satellite channel that does not say.
	var satelliteVoltage string
	if named.lnbPower != "" && !named.given() {
		if satelliteVoltage, err = normalizeLNBPower(named.lnbPower); err != nil {
			return err
		}
	}
	scanConfig := scanner.Options{
		SatelliteVoltage: satelliteVoltage,
		LockTimeout:      options.lockTimeout,
		DataTimeout:      options.dataTimeout,
		AnalysisTime:     options.analysisTime,
		// A time named on the command line is a time to spend, not a bound to
		// stop short of: whoever named it wants what arrives late.
		HoldAnalysisTime: options.analysisTime > 0,
		PollInterval:     options.pollInterval,
		MinimumBytes:     options.minimumBytes,
		MaximumBytes:     options.maximumBytes,
		RequireData:      options.requireData,
		Verbose:          command.host.verbose,
		Previous:         progressState.Results,
		OnResult:         progressState.Record,
	}
	if options.discover.enabled {
		// Discovery needs the tuners, so it happens here rather than where a
		// channel file would have been read.
		found, discoverErr := discoverAcross(ctx, client, tuneOptions, tuners, scanConfig, stderr, options.discover.systems)
		if discoverErr != nil {
			return discoverErr
		}
		entries, err = options.filterEntries(found)
		if err != nil {
			return err
		}
		markRoute(entries, options.route)
		if len(entries) == 0 {
			return fmt.Errorf("no channel candidates matched")
		}
		if options.listOnly {
			return writeEntries(options.outputOrStdout(), stdout, entries)
		}
	}

	runs := planRuns(client, tuneOptions, tuners, entries)
	report, err := scanAcross(ctx, client, tuneOptions, runs, scanConfig, stderr)
	if err != nil {
		return err
	}

	output := options.output
	if output == "" {
		// Nothing was asked for, so the scan leaves its report where it was run, next
		// to the progress file it has been keeping there. The name says which tuner
		// it came off and when; a run over several of them is named for the scan.
		output = reportName(report.Backend, report.Device, time.Now())
		if used := tunersUsed(report.Results); len(used) == 1 {
			output = reportName(report.Backend, used[0], time.Now())
		}
	}
	writer, closeWriter, err := outputWriter(output, stdout)
	if err != nil {
		return err
	}
	defer closeWriter()
	if err := exporter.WriteJSON(writer, report); err != nil {
		return err
	}
	if options.output == "" {
		// Said on stderr, which the report never goes to: a file appearing
		// unannounced is a file nobody looks at.
		fmt.Fprintf(stderr, "scan report written to %s\n", output)
	}
	if !options.keepProgress {
		if err := os.Remove(progressPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (options scanOptions) validate() error {
	if options.lockTimeout <= 0 || options.dataTimeout <= 0 || options.pollInterval <= 0 {
		return fmt.Errorf("timeouts and poll interval must be positive")
	}
	if options.analysisTime < 0 {
		return fmt.Errorf("analysis time cannot be negative; leave it out to take it from what each channel is")
	}
	if options.minimumBytes <= 0 {
		return fmt.Errorf("min-bytes must be positive")
	}
	if options.maximumBytes < options.minimumBytes {
		return fmt.Errorf("max-bytes must be greater than or equal to min-bytes")
	}
	if route := strings.ToUpper(strings.TrimSpace(options.route)); route != "" && !channelconfig.ValidRoute(route) {
		return fmt.Errorf("invalid route %q; it is one of TER, SAT, CATV or HIKARI", options.route)
	}
	return nil
}

// defaultMaximumBytes is how much of a channel is kept to analyse.
//
// It bounds the capture as surely as the analysis time does, and the smaller of
// the two ends it. Measured: at 32 MiB a 38.9 Mbps cable carrier reached the
// limit after six and a half seconds -- less than the ten a network table can
// take -- and then ran out its clock without ever settling. 128 MiB is
// twenty-seven seconds of the fastest carrier here.
const defaultMaximumBytes = 128 * 1024 * 1024

// settleTime is how long the daemon holds a channel after it locks before
// reading the measurements again: a demodulator reports its lock before it has
// a carrier-to-noise figure to go with it. Deliberately not tied to the
// analysis time, which measures something else and is much longer.
const settleTime = time.Second

// progressPath is where a scan keeps what it has already done, so an
// interrupted one carries on rather than starting over. It sits beside the
// report where there is one named; a report whose name is made up at the end
// cannot be one, so that scan keeps its progress where the command was run.
func (options scanOptions) progressPath() string {
	switch {
	case options.progress != "":
		return options.progress
	case options.output != "" && options.output != "-":
		return options.output + ".progress.json"
	default:
		return ".coax.progress.json"
	}
}

// outputOrStdout is where a listing goes: --list never opens a tuner, so there
// is no backend and no tuner to name a file after, and it prints.
func (options scanOptions) outputOrStdout() string {
	if options.output == "" {
		return "-"
	}
	return options.output
}

// reportName is what a scan calls its report when it was not told: the backend
// that drove the tuner, the tuner, and when the scan finished, which is the
// three things that tell one report from another.
func reportName(backend, device string, at time.Time) string {
	return fmt.Sprintf("%s_%s_%s.json",
		fileNamePart(backend), fileNamePart(device), at.Format("20060102_150405"))
}

// fileNamePart makes one part of that name safe to write: a tuner is named
// after a device path or a bus address on most backends, so it arrives full of
// separators that would scatter the report across directories.
func fileNamePart(part string) string {
	part = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '.':
			return r
		default:
			return '-'
		}
	}, part)
	part = strings.Trim(part, "-.")
	if part == "" {
		return "coax"
	}
	return part
}

// markRoute records the lead every channel of this scan arrived on: one value
// for the whole run, because the scan holds one tuner, the tuner has one
// socket, and the socket has one lead. Saying otherwise would send a recording
// to a tuner that cannot reach it.
//
// A channel file's sections are marked as they are parsed; this is the same for
// channels a discovery worked out.
func markRoute(entries []channelconfig.Entry, route string) {
	route = strings.ToUpper(strings.TrimSpace(route))
	if route == "" {
		return
	}
	for index := range entries {
		entries[index].Route = route
	}
}

// namedChannel is the one channel a command line described, as the entry the
// rest of a scan carries. It is built the way `coax tune` builds its channel,
// out of the same fields, so that a channel that tunes scans and one that scans
// tunes.
func namedChannel(request channelRequest, route string) (channelconfig.Entry, error) {
	properties, err := channelProperties(request)
	if err != nil {
		return channelconfig.Entry{}, err
	}
	return channelconfig.Entry{
		Name:       channelName(request, properties),
		Properties: properties,
		// Where a file would have named itself. A report says where each
		// channel came from, and "the command line" is the true answer.
		Source: "command line",
		Route:  strings.ToUpper(strings.TrimSpace(route)),
	}, nil
}

// channelName is what to call a channel nothing named. What the caller wrote
// where they could -- a report read by a person is easier to follow when it
// says BS15_0 than when it says 11996000 -- and the frequency otherwise.
func channelName(request channelRequest, properties map[string]string) string {
	if request.channel != "" {
		return request.channel
	}
	if frequency := properties["FREQUENCY"]; frequency != "" {
		if stream := properties["STREAM_ID"]; stream != "" {
			return frequency + "_" + stream
		}
		return frequency
	}
	return "channel"
}

func (options scanOptions) loadEntries(paths []string) ([]channelconfig.Entry, error) {
	var parsed []channelconfig.Entry
	for _, path := range paths {
		entries, parseErr := channelconfig.Parse(path, options.route)
		if parseErr != nil {
			return nil, parseErr
		}
		parsed = append(parsed, entries...)
	}
	return options.filterEntries(parsed)
}

// filterEntries applies --include and --exclude, wherever the entries came
// from: a channel file, or the satellite's own account of itself.
func (options scanOptions) filterEntries(entries []channelconfig.Entry) ([]channelconfig.Entry, error) {
	include, err := compileOptionalRegexp(options.include)
	if err != nil {
		return nil, fmt.Errorf("include: %w", err)
	}
	exclude, err := compileOptionalRegexp(options.exclude)
	if err != nil {
		return nil, fmt.Errorf("exclude: %w", err)
	}
	if include == nil && exclude == nil {
		return entries, nil
	}
	result := make([]channelconfig.Entry, 0, len(entries))
	for _, entry := range entries {
		if include != nil && !include.MatchString(entry.Name) {
			continue
		}
		if exclude != nil && exclude.MatchString(entry.Name) {
			continue
		}
		result = append(result, entry)
	}
	return result, nil
}

func compileOptionalRegexp(value string) (*regexp.Regexp, error) {
	if value == "" {
		return nil, nil
	}
	return regexp.Compile(value)
}

// writeEntries writes the channel list where the scan's report would have gone,
// so --list and a real scan are told where to put things the same way.
func writeEntries(output string, stdout io.Writer, entries []channelconfig.Entry) error {
	writer, closeWriter, err := outputWriter(output, stdout)
	if err != nil {
		return err
	}
	defer closeWriter()
	return writeJSON(writer, entries)
}
