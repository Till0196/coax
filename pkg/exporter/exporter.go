// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package exporter writes a scan out in the shapes other things read.
//
// Three of them, one file each: the scan's own JSON, which is what this program
// reads back; a Mirakurun channel list; and a BonDriver channel definition
// file. Nothing here measures or decides anything about a channel, but each
// format leaves out something the others keep, and what each one can and cannot
// say is what this package is mostly about.
//
// What the two channel formats share is in carriage.go and network.go. What
// they do not share is everything after that, which is why they are not one
// parameterised writer: a Mirakurun list names services, says which lead each
// arrived on, and has a key for the satellite; a channel definition file names
// carriers, has four columns with nowhere to put a lead, and needs a physical
// channel number for every line.
//
// Both channel writers report what they could not do rather than failing: a
// channel that could not be renamed, or one that could not be given a physical
// channel number, is a line on stderr and a file that is otherwise complete.
package exporter

import (
	"encoding/json"
	"io"
	"iter"

	"github.com/Till0196/coax/pkg/report"
)

// WriteJSON writes the scan as it stands, which is the form this program reads
// back: coax export takes one of these and writes one of the others.
func WriteJSON(w io.Writer, value report.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// foundIn is the results a channel writer looks at. A report records everything
// that was tried, because what a scan did not find is worth keeping: a channel
// that would not lock, one no tuner could reach and one that was never tried
// are three different answers. None of it belongs in a channel list, which is a
// list of channels that are there.
func foundIn(results []report.Result) iter.Seq[report.Result] {
	return func(yield func(report.Result) bool) {
		for _, result := range results {
			if result.Status != "found" {
				continue
			}
			if !yield(result) {
				return
			}
		}
	}
}
