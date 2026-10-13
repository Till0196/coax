// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

// outputWriter is where a subcommand's output goes: a file, or standard output
// for "-". The closer is always safe to call and does nothing for standard
// output, which is not this program's to close.
func outputWriter(path string, stdout io.Writer) (io.Writer, func(), error) {
	if path == "-" {
		return stdout, func() {}, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return file, func() { _ = file.Close() }, nil
}

// inputReader is the other way round: a file, or standard input.
func inputReader(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open input %s: %w", path, err)
	}
	return file, func() { _ = file.Close() }, nil
}

// writeJSON writes one value as this program's JSON, the same everywhere:
// indented so a person can read it, and with HTML escaping off so a service name
// carrying an ampersand comes out as itself rather than as an entity.
func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// newTable is the aligned listing the subcommands print.
func newTable(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}
