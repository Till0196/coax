// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/host"
	scanprogress "github.com/Till0196/coax/pkg/progress"
	"github.com/Till0196/coax/pkg/report"
	"github.com/Till0196/coax/pkg/scanner"
)

// scanTuners is which tuners a scan runs over: the one named, or every one the
// daemon lists.
//
// A scan with none named used to take whichever tuner the daemon handed out
// first and stay on it for the whole run. On a host carrying a satellite tuner
// and a cable card that meant the cable was never scanned -- and the cable
// channels were still tried, a hundred milliseconds and an error apiece.
func scanTuners(ctx context.Context, client *host.Client, named string) ([]string, error) {
	if named != "" {
		return []string{named}, nil
	}
	boards, err := client.Boards(ctx, false)
	if err != nil {
		return nil, err
	}
	var tuners []string
	for _, board := range boards {
		for _, one := range board.Tuners {
			tuners = append(tuners, one.ID)
		}
	}
	if len(tuners) == 0 {
		// Nothing listed is not nothing attached: a backend that cannot
		// enumerate still tunes. Leave the choice to the daemon, as before.
		return []string{""}, nil
	}
	return tuners, nil
}

// tunerLabel names a tuner for the progress output. A scan over several of
// them says nothing about which one it is on otherwise, and a satellite tuner
// and a cable card fail in quite different ways.
func tunerLabel(tuner string) string {
	if tuner == "" {
		return "any tuner"
	}
	return tuner
}

// forTuner is the tuning options pinned to one tuner.
func forTuner(options host.TuneOptions, tuner string) host.TuneOptions {
	options.Tuner = tuner
	return options
}

// remainingSystems is what this tuner is worth asking about: the systems it
// carries, less the ones an earlier tuner has already covered. Without the
// subtraction a host with two satellite tuners would walk the same transponders
// twice.
//
// A tuner that cannot be asked at all -- one named that the daemon does not
// have -- is reported rather than taken for one that carries nothing: that is
// a mistyped --tuner, and saying no channel matched sends the reader to look at
// the wrong thing.
func remainingSystems(device *host.Device, asked []string, covered map[string]bool) ([]string, error) {
	candidates := asked
	if len(candidates) == 0 {
		for _, system := range scanner.DiscoverableSystems {
			if system == "ISDBS3" {
				// Nothing can tell whether a tuner carries it, so every satellite front end
				// says yes and the walk is long. Named or not at all.
				continue
			}
			candidates = append(candidates, system)
		}
	}
	var wanted []string
	var failed error
	answered := false
	for _, system := range candidates {
		if covered[system] {
			continue
		}
		supported, err := device.Supports(system)
		if err != nil {
			failed = err
			continue
		}
		answered = true
		if supported {
			wanted = append(wanted, system)
		}
	}
	if !answered && failed != nil {
		return nil, failed
	}
	return wanted, nil
}

// discoverAcross works the channels out on every tuner in turn, each covering
// the systems no earlier one could.
func discoverAcross(ctx context.Context, client *host.Client, tuneOptions host.TuneOptions,
	tuners []string, base scanner.Options, progress io.Writer, asked []string,
) ([]channelconfig.Entry, error) {
	covered := map[string]bool{}
	seen := map[string]bool{}
	var entries []channelconfig.Entry
	var refused error
	for _, tuner := range tuners {
		device := client.Open(forTuner(tuneOptions, tuner))
		wanted, err := remainingSystems(device, asked, covered)
		if err != nil {
			_ = device.Close()
			refused = err
			continue
		}
		if len(wanted) == 0 {
			_ = device.Close()
			continue
		}
		options := base
		options.Device = device
		fmt.Fprintf(progress, "%s: working out %s\n", tunerLabel(tuner), strings.Join(wanted, ", "))
		found, err := scanner.Discover(ctx, options, progress, wanted)
		_ = device.Close()
		if err != nil {
			// One tuner finding nothing is not the scan failing: the next one
			// may carry what this one does not. Kept in case none of them do.
			refused = err
			continue
		}
		for _, entry := range found {
			// Covered by what was found rather than by what was asked for. A tuner
			// pointed at a dead lead answers for a system and comes back with nothing,
			// and marking it covered then would take the system away from the tuner that
			// can receive it.
			covered[channelconfig.NormalizeDeliverySystem(entry.Properties["DELIVERY_SYSTEM"])] = true
		}
		for _, entry := range found {
			key := scanprogress.Key(entry)
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 && refused != nil {
		return nil, refused
	}
	return entries, nil
}

// scanRun is one tuner's share of a scan.
type scanRun struct {
	tuner   string
	entries []channelconfig.Entry
}

// planRuns hands every channel to a tuner that receives it, keeping the order
// the channels came in. A channel no tuner receives is left with the first of
// them, so that it is reported as unsupported rather than dropped without a
// word.
func planRuns(client *host.Client, tuneOptions host.TuneOptions,
	tuners []string, entries []channelconfig.Entry,
) []scanRun {
	if len(tuners) == 1 {
		return []scanRun{{tuner: tuners[0], entries: entries}}
	}
	devices := make(map[string]*host.Device, len(tuners))
	for _, tuner := range tuners {
		devices[tuner] = client.Open(forTuner(tuneOptions, tuner))
	}
	defer func() {
		for _, device := range devices {
			_ = device.Close()
		}
	}()

	chosen := map[string]string{}
	byTuner := map[string][]channelconfig.Entry{}
	for _, entry := range entries {
		system := entry.Properties["DELIVERY_SYSTEM"]
		tuner, known := chosen[system]
		if !known {
			tuner = tuners[0]
			for _, candidate := range tuners {
				if supported, err := devices[candidate].Supports(system); err == nil && supported {
					tuner = candidate
					break
				}
			}
			chosen[system] = tuner
		}
		byTuner[tuner] = append(byTuner[tuner], entry)
	}

	runs := make([]scanRun, 0, len(byTuner))
	for _, tuner := range tuners {
		if share := byTuner[tuner]; len(share) > 0 {
			runs = append(runs, scanRun{tuner: tuner, entries: share})
		}
	}
	return runs
}

// scanAcross runs each tuner's share and gathers the findings into one report.
func scanAcross(ctx context.Context, client *host.Client, tuneOptions host.TuneOptions,
	runs []scanRun, base scanner.Options, progress io.Writer,
) (report.Report, error) {
	var gathered report.Report
	for index, run := range runs {
		device := client.Open(forTuner(tuneOptions, run.tuner))
		options := base
		options.Device = device
		options.Tuner = run.tuner
		if len(runs) > 1 {
			fmt.Fprintf(progress, "%s: %d channels\n", tunerLabel(run.tuner), len(run.entries))
		}
		// Scan closes the device it was given.
		one, err := scanner.Scan(ctx, run.entries, options, progress)
		if index == 0 {
			gathered = one
		} else {
			gathered.Results = append(gathered.Results, one.Results...)
		}
		if err != nil {
			return gathered, err
		}
	}
	if len(runs) > 1 {
		// One report, several tuners: which tuner each channel came off is on
		// the result rather than here.
		gathered.Backend, gathered.Device = "several", fmt.Sprintf("%d tuners", len(runs))
	}
	return gathered, nil
}

// tunersUsed is the tuners a report's results came off, in the order they were
// first seen, for naming the file the report is written to.
func tunersUsed(results []report.Result) []string {
	var used []string
	for _, result := range results {
		if result.Device != "" && !slices.Contains(used, result.Device) {
			used = append(used, result.Device)
		}
	}
	return used
}
