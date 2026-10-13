// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Till0196/coax/pkg/host"
)

// runList reports what the daemon can see. Nothing is opened unless --tuners is
// given: describing a board fully means opening it, which powers the hardware up
// and loads firmware into several models.
func runList(arguments []string, stdout, stderr io.Writer) error {
	command := newHardwareCommand("list", stderr)
	flags := command.flags
	withTuners := flags.Bool("tuners", false, "open each board to confirm its tuners, rather than working them out from what it is")
	asJSON := flags.Bool("json", false, "report as JSON instead of a table")
	detail := flags.Bool("detail", false, "also show what each tuner says about how it is currently tuned; implies --tuners")
	alias(flags, "t", "tuners")
	if err := command.parseOnly(arguments); err != nil {
		return err
	}
	// The settings come off the hardware, so asking for them is asking to open
	// it.
	if *detail {
		*withTuners = true
	}
	ctx := context.Background()
	client, err := command.dial(ctx)
	if err != nil {
		return err
	}
	boards, err := client.Boards(ctx, *withTuners)
	if err != nil {
		return err
	}

	if *asJSON {
		return writeJSON(stdout, boards)
	}
	return writeBoards(stdout, boards, *withTuners, *detail)
}

// writeSettings prints what each tuner said about how it is tuned, a block per
// tuner rather than more columns: the settings differ from one standard to the
// next -- roll-off and pilot on a satellite tuner, guard interval on a
// terrestrial one -- so a shared table would be mostly blanks.
func writeSettings(stdout io.Writer, boards []host.Board) {
	for _, board := range boards {
		for _, tuner := range board.Tuners {
			if len(tuner.Settings) == 0 {
				continue
			}
			fmt.Fprintf(stdout, "\n%s\n", tuner.ID)
			settings := newTable(stdout)
			for _, setting := range tuner.Settings {
				fmt.Fprintf(settings, "  %s\t%s\n", setting.Name, setting.Value)
			}
			_ = settings.Flush()
		}
	}
}

func writeBoards(stdout io.Writer, boards []host.Board, probed, detail bool) error {
	if len(boards) == 0 {
		_, err := fmt.Fprintln(stdout, "no tuners are attached")
		return err
	}
	table := newTable(stdout)
	// The mode column is only there when something has one to show, so a listing
	// of tuners without the setting -- most of them -- carries no empty column.
	modes := false
	for _, board := range boards {
		for _, tuner := range board.Tuners {
			if tuner.Mode != "" {
				modes = true
			}
		}
	}
	if modes {
		fmt.Fprintln(table, "TUNER\tDELIVERY\tMODE\tSTATE\tMODEL")
	} else {
		fmt.Fprintln(table, "TUNER\tDELIVERY\tSTATE\tMODEL")
	}
	for _, board := range boards {
		for _, tuner := range board.Tuners {
			state := tunerState(tuner)
			if modes {
				mode := tuner.Mode
				if mode == "" {
					mode = "-"
				}
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
					tuner.ID, strings.Join(tuner.Deliveries, ", "), mode, state, describe(board, tuner))
				continue
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
				tuner.ID, strings.Join(tuner.Deliveries, ", "), state, describe(board, tuner))
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	if detail {
		writeSettings(stdout, boards)
	}

	if !probed {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Tuners are named from what each board reports about itself.")
		fmt.Fprintln(stdout, "Pass --tuners to open each board and confirm them.")
	}
	for _, board := range boards {
		if board.ProbeError != "" {
			// Asked to confirm the hardware and could not: saying nothing would
			// leave the guessed listing above looking confirmed.
			fmt.Fprintf(stdout, "\n%s could not be opened, so its tuners above are what the model is known to carry:\n  %s\n",
				board.Name, board.ProbeError)
		}
	}
	for _, board := range boards {
		if !board.Stable {
			fmt.Fprintf(stdout, "\n%s is named after the bus address it is at, which changes when it is unplugged.\n", board.Name)
		}
	}
	return nil
}

// tunerState is the STATE column: whether anything holds the tuner, and at what
// rank when that is worth saying. The default rank is left out: almost every
// tuner in use is held at it, and spelling it on every line would bury the one
// line where something was said.
func tunerState(tuner host.Tuner) string {
	if !tuner.Leased {
		return "free"
	}
	if tuner.Priority == nil || *tuner.Priority == host.DefaultPriority {
		return "in use"
	}
	return fmt.Sprintf("in use (priority %d)", *tuner.Priority)
}

// describe names the hardware a tuner is on. The tuner's own name is added only
// when it says something the board's description does not: on a board of
// several tuners it names the chip, and on a single-tuner board it would be the
// same words twice.
func describe(board host.Board, tuner host.Tuner) string {
	description := board.Description
	if description == "" {
		description = board.Backend
	}
	if tuner.Name == "" || alreadyNamed(description, tuner.Name) {
		return description
	}
	return description + " (" + tuner.Name + ")"
}

// alreadyNamed reports whether the board's description accounts for every part
// the tuner is named after. The two are written by different hands and
// punctuate differently -- a board lists "TC90522 + MxL136 + TDA20142" where
// its one tuner is "MxL136 / TDA20142" -- so comparing the whole strings finds
// no overlap and the listing prints the parts twice.
func alreadyNamed(description, tuner string) bool {
	parts := strings.FieldsFunc(tuner, func(r rune) bool { return r == '/' || r == '+' })
	if len(parts) == 0 {
		return true
	}
	for _, part := range parts {
		if part = strings.TrimSpace(part); part == "" || !strings.Contains(description, part) {
			return false
		}
	}
	return true
}
