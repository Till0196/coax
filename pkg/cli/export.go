// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/exporter"
	"github.com/Till0196/coax/pkg/report"
)

type exportOptions struct {
	input           string
	output          string
	format          string
	route           string
	channelName     string
	serviceIDs      bool
	relativeStreams bool
	groupIDs        bool
	lnbf            string
	satNumber       string
}

func runExport(arguments []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	command := newCommand("export", stderr)
	flags := command.flags
	options := exportOptions{}
	flags.StringVar(&options.input, "input", "-", "scan JSON file, or - for stdin")
	flags.StringVar(&options.output, "output", "-", "exported file, or - for stdout; a name to write the files beside for chset, ch2 and chset4, which write one per delivery system, and for dvbv5, which writes one per lead")
	flags.StringVar(&options.format, "format", "mirakurun", "export format: mirakurun for a Mirakurun channel list, dvbv5 for a DVBv5 channel file, chset for a BonDriver channel definition file, ch2 for a TVTest channel file, chset4 for an EDCB tuner channel file, or chset5 for EDCB's own channel list")
	flags.StringVar(&options.route, "route", "", "write a route on every channel: TER, SAT, CATV or HIKARI, or scanned for the one each channel recorded; nothing is written when this is empty. For dvbv5 this is which lead's file to write, and every lead the scan recorded is written when it is empty")
	flags.StringVar(&options.channelName, "channel-name", "scanned", "how to write the channel field: scanned, or transponder to name a satellite channel as recpt1 does (BS15_1)")
	flags.BoolVar(&options.serviceIDs, "service-id", false, "write one entry per service with its serviceId, rather than one per channel; Mirakurun scans the services itself when this is off")
	flags.StringVar(&options.lnbf, "lnbf", "", "low-noise block on the dish, written into the DVBv5 satellite channels that do not name one: UNIVERSAL, EXTENDED, ENHANCED, STANDARD, DBS, C-BAND, 110BS, JCSAT3A, JCSAT4B. Naming a block says the frequency is a downlink, so it is not written onto ISDB-S, whose files carry the intermediate frequency already")
	flags.StringVar(&options.satNumber, "sat-number", "", "position on a DiSEqC switch, 0 to 3, written into the DVBv5 satellite channels that do not name one, for a dish that reaches the tuner through one")
	flags.BoolVar(&options.relativeStreams, "tsmf-rel-ts", true, "write one entry per stream of a multiplexing frame with its tsmfRelTs, rather than one per carrier; Mirakurun finds the frame itself when this is off")
	flags.BoolVar(&options.groupIDs, "tsmf-group-id", false, "write the tsmfGroupId of a multiplex carried over several carriers; Mirakurun reads the group out of the signal itself, and a value written here is one it stops re-reading")
	alias(flags, "i", "input")
	alias(flags, "o", "output")
	alias(flags, "f", "format")
	if err := command.parseOnly(arguments); err != nil {
		return err
	}
	// Which options were actually asked for, rather than which are on. The
	// Mirakurun-only options are refused for the other formats, and one of
	// them is on by default -- so what is refused has to be the asking, not
	// the value.
	given := map[string]bool{}
	flags.Visit(func(flag *flag.Flag) { given[flag.Name] = true })
	format := strings.ToLower(strings.TrimSpace(options.format))
	writePerSystemFile, perSystem := windowsFormats[format]
	windows := perSystem || format == "chset5"
	if format != "mirakurun" && format != "dvbv5" && !windows {
		return fmt.Errorf("unsupported export format %q; it is mirakurun, dvbv5, chset, ch2, chset4 or chset5", options.format)
	}

	route := strings.ToUpper(strings.TrimSpace(options.route))
	if route != "" && route != exporter.RouteAsScanned && !channelconfig.ValidRoute(route) {
		return fmt.Errorf("invalid route %q; it is one of TER, SAT, CATV or HIKARI, or scanned", options.route)
	}
	var transponderNames bool
	switch strings.ToLower(strings.TrimSpace(options.channelName)) {
	case "", "scanned":
	case "transponder":
		transponderNames = true
	default:
		return fmt.Errorf("--channel-name is scanned or transponder, not %q", options.channelName)
	}

	// The Mirakurun options describe a Mirakurun channel list and nothing else:
	// a channel definition file has four columns and none of them is a route,
	// a service or a multiplexing number, and a TVTest channel file is a line
	// per service whether or not anybody asks. Refusing beats ignoring.
	if windows {
		for _, given := range [][2]string{
			{"route", options.route},
			{"service-id", boolFlag(options.serviceIDs)},
			{"tsmf-rel-ts", askedFor(given, "tsmf-rel-ts")},
			{"tsmf-group-id", boolFlag(options.groupIDs)},
			{"channel-name", transponderFlag(transponderNames)},
		} {
			if given[1] != "" {
				return fmt.Errorf("--%s describes a Mirakurun channel list, and %s has nowhere to put it",
					given[0], formatDescriptions[format])
			}
		}
	}

	// The dish settings go into the one format that is itself a tuning request.
	// A Mirakurun channel list leaves the tuning to whatever it runs, and the
	// Windows formats have no column for a block or a switch.
	if format != "dvbv5" {
		for _, given := range [][2]string{{"lnbf", options.lnbf}, {"sat-number", options.satNumber}} {
			if given[1] != "" {
				return fmt.Errorf("--%s is written into a DVBv5 channel file, which is what --format dvbv5 writes", given[0])
			}
		}
	}
	if options.satNumber != "" {
		position, err := strconv.Atoi(strings.TrimSpace(options.satNumber))
		if err != nil || position < 0 || position > exporter.MaxSatNumber {
			return fmt.Errorf("--sat-number is a DiSEqC position of 0 to %d, not %q; a committed switch has four ports",
				exporter.MaxSatNumber, options.satNumber)
		}
	}

	reader, closeReader, err := inputReader(options.input, stdin)
	if err != nil {
		return err
	}
	defer closeReader()
	report, err := readJSONReport(reader)
	if err != nil {
		return err
	}

	// A dvbv5 channel file holds a delivery system, a frequency and whatever
	// else it takes to tune, and nothing else: a route is which file it is
	// rather than a line in one, and dvbv5-zap tunes the carrier and hands over
	// everything on it, so it cannot be asked for a service or a frame's stream.
	if format == "dvbv5" {
		for _, given := range [][2]string{
			{"service-id", boolFlag(options.serviceIDs)},
			{"tsmf-rel-ts", askedFor(given, "tsmf-rel-ts")},
			{"tsmf-group-id", boolFlag(options.groupIDs)},
		} {
			if given[1] != "" {
				return fmt.Errorf("--%s describes a Mirakurun channel list, and a DVBv5 channel file has nowhere to put it", given[0])
			}
		}
		if route == exporter.RouteAsScanned {
			return fmt.Errorf("--route scanned is what a DVBv5 export does anyway; name a lead to write only its file, or leave it out")
		}
		return writePerRoute(report, route, exporter.DVBv5Options{
			TransponderNames: transponderNames,
			LNBf:             strings.TrimSpace(options.lnbf),
			SatNumber:        strings.TrimSpace(options.satNumber),
		}, options.output, stdout, stderr)
	}

	if format == "chset5" {
		// One file for the whole scan rather than one per delivery system:
		// EDCB keeps one of these for the installation, not for a tuner.
		writer, closeWriter, err := outputWriter(options.output, stdout)
		if err != nil {
			return err
		}
		defer closeWriter()
		return exporter.WriteChSet5(writer, report)
	}
	if perSystem {
		return writePerSystem(writePerSystemFile, format, report, options.output, stdout, stderr)
	}

	writer, closeWriter, err := outputWriter(options.output, stdout)
	if err != nil {
		return err
	}
	defer closeWriter()
	unnamed, err := exporter.WriteMirakurun(writer, report, exporter.MirakurunOptions{
		Route:            route,
		TransponderNames: transponderNames,
		ServiceIDs:       options.serviceIDs,
		RelativeStreams:  options.relativeStreams,
		GroupIDs:         options.groupIDs,
	})
	reportUnnamed(stderr, unnamed)
	return err
}

// reportUnnamed says which channels are in the file under the name the scan gave
// them rather than the one asked for. They work, but a file that quietly holds
// two spellings would be found out later rather than now.
func reportUnnamed(stderr io.Writer, unnamed []string) {
	for _, why := range unnamed {
		fmt.Fprintf(stderr, "left as the scan named it: %s\n", why)
	}
}

// reportNotes says what a channel in the file did not get; each note says which
// thing was left out, since more than one can be.
func reportNotes(stderr io.Writer, notes []string) {
	for _, note := range notes {
		fmt.Fprintf(stderr, "note: %s\n", note)
	}
}

// writePerRoute writes one DVBv5 channel file per lead the scan recorded, or the
// one file that was asked for. One per lead because that is the split the reader
// makes: dvbv5-zap is pointed at one file for one tuner, and a tuner has one
// socket fed from one lead. So --output names a stem rather than a file, and
// standard output is only possible when there is one lead to write.
func writePerRoute(value report.Report, route string, options exporter.DVBv5Options,
	output string, stdout, stderr io.Writer,
) error {
	routes := exporter.DVBv5Routes(value)
	if route != "" {
		if !slices.Contains(routes, route) {
			return fmt.Errorf("the scan found no channels on %s", route)
		}
		routes = []string{route}
	}
	if len(routes) == 0 {
		return fmt.Errorf("the scan found no channels this can write")
	}
	if output == "-" {
		if len(routes) > 1 {
			return fmt.Errorf("this scan covers %s, which are %d files; give --output a name to write them beside, or --route one of them",
				strings.Join(routeNames(routes), " and "), len(routes))
		}
		notes, skipped, err := exporter.WriteDVBv5(stdout, routes[0], value.Results, options)
		reportNotes(stderr, notes)
		reportSkipped(stderr, skipped)
		return err
	}
	for _, one := range routes {
		path := strings.TrimSuffix(output, ".conf") + exporter.DVBv5FileSuffix(one)
		writer, closeWriter, err := outputWriter(path, stdout)
		if err != nil {
			return err
		}
		notes, skipped, err := exporter.WriteDVBv5(writer, one, value.Results, options)
		closeWriter()
		reportNotes(stderr, notes)
		reportSkipped(stderr, skipped)
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "wrote %s\n", path)
	}
	return nil
}

func routeNames(routes []string) []string {
	named := make([]string, 0, len(routes))
	for _, route := range routes {
		named = append(named, exporter.DVBv5RouteName(route))
	}
	return named
}

// boolFlag and transponderFlag say whether a flag was given, in the shape the
// checks above read.
func boolFlag(given bool) string {
	if given {
		return "yes"
	}
	return ""
}

func transponderFlag(given bool) string {
	if given {
		return "transponder"
	}
	return ""
}

// windowsFormats are the formats written for the Windows tools, one file per
// delivery system. See writePerSystem.
var windowsFormats = map[string]func(io.Writer, string, []report.Result) ([]string, error){
	"chset":  exporter.WriteChSet,
	"ch2":    exporter.WriteCh2,
	"chset4": exporter.WriteChSet4,
}

// writePerSystem writes one file per delivery system, because none of these
// formats has anywhere to say which system it is: a channel definition file is
// loaded as one for the whole file, and a TVTest channel file names the spaces
// of one BonDriver. The file name has to carry it, so --output names a stem
// rather than a file and standard output is only possible for a single system.
func writePerSystem(write func(io.Writer, string, []report.Result) ([]string, error),
	format string, value report.Report, output string, stdout, stderr io.Writer,
) error {
	systems := exporter.ChSetSystems(value)
	if len(systems) == 0 {
		return fmt.Errorf("the scan found no channels this can write")
	}
	if output == "-" {
		if len(systems) > 1 {
			return fmt.Errorf("this scan covers %s, which are %d files; give --output a name to write them beside",
				strings.Join(systemNames(systems), " and "), len(systems))
		}
		skipped, err := write(stdout, systems[0], value.Results)
		reportSkipped(stderr, skipped)
		return err
	}
	for _, system := range systems {
		path := fmt.Sprintf("%s-%s%s", output, system, formatExtensions[format])
		writer, closeWriter, err := outputWriter(path, stdout)
		if err != nil {
			return err
		}
		skipped, err := write(writer, system, value.Results)
		closeWriter()
		reportSkipped(stderr, skipped)
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "wrote %s\n", path)
	}
	return nil
}

// formatDescriptions name a format in a message.
var formatDescriptions = map[string]string{
	"chset":  "a channel definition file",
	"ch2":    "a TVTest channel file",
	"chset4": "an EDCB tuner channel file",
	"chset5": "an EDCB channel list",
}

// formatExtensions are what each format's files are called, from the stem on:
// a channel definition file is named by whatever reads it, and TVTest wants its
// file called after the BonDriver it goes with, which a scan cannot know.
var formatExtensions = map[string]string{
	"chset": ".ChSet.txt",
	"ch2":   ".ch2",
	// EDCB looks for a file named after the driver and the tuner and falls
	// back to one with an empty pair of brackets. A scan knows neither name,
	// so the brackets are left empty for the caller to fill in.
	"chset4": "().ChSet4.txt",
}

func systemNames(systems []string) []string {
	named := make([]string, 0, len(systems))
	for _, system := range systems {
		named = append(named, exporter.SystemDisplayName(system))
	}
	return named
}

// reportSkipped says what did not make it into a file, which is worth a line
// because the file looks complete either way.
func reportSkipped(stderr io.Writer, skipped []string) {
	for _, reason := range skipped {
		fmt.Fprintf(stderr, "not written: %s\n", reason)
	}
}

func readJSONReport(reader io.Reader) (report.Report, error) {
	decoder := json.NewDecoder(reader)
	var value report.Report
	if err := decoder.Decode(&value); err != nil {
		return report.Report{}, fmt.Errorf("decode scan JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return report.Report{}, fmt.Errorf("scan JSON contains more than one value")
		}
		return report.Report{}, fmt.Errorf("decode trailing scan JSON: %w", err)
	}
	return value, nil
}

// askedFor renders a flag as the refusals read it: named when the caller gave
// it, empty when it is only sitting at its default.
func askedFor(given map[string]bool, name string) string {
	if given[name] {
		return name
	}
	return ""
}
