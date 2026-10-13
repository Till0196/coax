// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package progress

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressDisplayKeepsRedirectedOutputLineOriented(t *testing.T) {
	var output bytes.Buffer
	display := NewDisplay(&output)
	display.Begin(2, 5, "SAT/45168 (ISDBS)")
	display.Update(PhaseLocking, 1, 5, "signal")
	display.Finish("found (tlv, 1024 bytes)")
	want := "[2/5] SAT/45168 (ISDBS) ... found (tlv, 1024 bytes)\n"
	if output.String() != want {
		t.Fatalf("progress output = %q, want %q", output.String(), want)
	}
}

// TestFitNeverReachesTheEdge is the whole point of fitting: a line as wide as
// the terminal wraps, and a wrapped line cannot be rewritten in place.
func TestFitNeverReachesTheEdge(t *testing.T) {
	head := "[96/113] [####                ]  21% sampling DVBC/ANNEX_A/45"
	detail := "16.8 MiB"
	tail := " (6.7s)"
	for _, columns := range []int{0, 1, 2, 10, 40, 60, 79, 80, 200} {
		line := fit(head, detail, tail, columns)
		if columns > 0 && len([]rune(line)) > columns-1 {
			t.Errorf("%d columns: %d wide: %q", columns, len([]rune(line)), line)
		}
	}
}

// TestFitKeepsTheEndsAndGivesUpTheDetail. Which channel and how long it has
// taken are what a glance is for; the byte count is what can go.
func TestFitKeepsTheEndsAndGivesUpTheDetail(t *testing.T) {
	head, detail, tail := "[1/2] locking C13", "1.5 MiB", " (2s)"
	full := fit(head, detail, tail, 0)
	if full != "[1/2] locking C13 1.5 MiB (2s)" {
		t.Fatalf("unfitted line = %q", full)
	}
	narrow := fit(head, detail, tail, len([]rune(full)))
	if !strings.HasPrefix(narrow, head) || !strings.HasSuffix(narrow, tail) {
		t.Errorf("the ends did not survive: %q", narrow)
	}
	if narrow == full {
		t.Error("the detail was not shortened at all")
	}
	// Too narrow for even the ends: cut rather than overflow.
	cut := fit(head, detail, tail, 12)
	if cut != head[:11] {
		t.Errorf("cut line = %q, want %q", cut, head[:11])
	}
}

// TestUpdatesCoverWhatTheyShortenWithoutEscapes. The Windows console prints an
// escape it has not been told to interpret, so a scan there showed "(13.1s)^[K"
// on every line; spaces need nothing turned on.
func TestUpdatesCoverWhatTheyShortenWithoutEscapes(t *testing.T) {
	var out bytes.Buffer
	display := &Display{w: &out, tty: true}
	display.write("[1/2] sampling C13 20.2 MiB (13.1s)", 0)
	display.write("[2/2] locking C14 (0.1s)", 0)

	written := out.String()
	if strings.ContainsRune(written, 0x1b) {
		t.Errorf("an escape reached the terminal: %q", written)
	}
	if !strings.HasSuffix(written, "(0.1s)"+strings.Repeat(" ", 11)) {
		t.Errorf("the longer line was not covered: %q", written)
	}
}

// TestPaddingStaysInsideTheTerminal: covering the line before must not be what
// pushes past the edge, which is the wrap this all exists to avoid.
func TestPaddingStaysInsideTheTerminal(t *testing.T) {
	var out bytes.Buffer
	display := &Display{w: &out, tty: true}
	display.write(strings.Repeat("x", 70), 80)
	out.Reset()
	// The window is narrower now, and the line before it was 70 wide.
	display.write("short", 40)
	if got := len([]rune(strings.TrimPrefix(out.String(), "\r"))); got > 39 {
		t.Errorf("wrote %d columns into a 40-column terminal", got)
	}
}
