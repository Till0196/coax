// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// The stages a channel goes through, which the progress line names as it goes.
// Words rather than a number, because what a glance wants to know is which of
// the three waits this one is and how long it has been going.
const (
	PhaseTuning   = "tuning"
	PhaseLocking  = "locking"
	PhaseSampling = "sampling"
	PhaseDone     = "done"
)

// phaseWidth is what the stage is padded to, so that the line does not shift
// under the eye as one stage gives way to the next.
const phaseWidth = 8

type Display struct {
	w   io.Writer
	tty bool
	// file is the terminal the lines go to, for asking how wide it is. The
	// answer is asked for again on every line rather than kept, because a
	// window can be resized in the middle of a scan.
	file    *os.File
	current int
	total   int
	label   string
	started time.Time
	// standing is how wide the line now on screen is, with no newline after
	// it. Zero means there is nothing to write over.
	standing int
}

func NewDisplay(w io.Writer) *Display {
	d := &Display{w: w}
	if file, ok := w.(*os.File); ok {
		if stat, err := file.Stat(); err == nil {
			d.tty = stat.Mode()&os.ModeCharDevice != 0
			d.file = file
		}
	}
	return d
}

func (d *Display) Begin(current, total int, label string) {
	d.current, d.total, d.label, d.started = current, total, label, time.Now()
	if d.tty {
		d.Update(PhaseTuning, 0, 0, "")
		return
	}
	fmt.Fprintf(d.w, "[%d/%d] %s ... ", current, total, label)
}

func (d *Display) Update(phase string, current, total int64, detail string) {
	if !d.tty {
		return
	}
	head, tail := d.compose(phase, current, total)
	// Fitted to the terminal, because a line that wraps cannot be rewritten:
	// the carriage return goes to the start of the last row rather than the
	// first, so every update leaves the row above it behind.
	columns := d.columns()
	d.write(fit(head, detail, tail, columns), columns)
}

// compose is the two fixed ends of a progress line: everything up to the
// detail, and the elapsed time that follows it.
func (d *Display) compose(phase string, current, total int64) (head, tail string) {
	percent := 0
	if total > 0 {
		percent = int(current * 100 / total)
		if percent > 100 {
			percent = 100
		}
	}
	filled := percent * 20 / 100
	bar := strings.Repeat("#", filled) + strings.Repeat(" ", 20-filled)
	head = fmt.Sprintf("[%d/%d] [%s] %3d%% %-*s %s",
		d.current, d.total, bar, percent, phaseWidth, phase, d.label)
	tail = fmt.Sprintf(" (%s)", time.Since(d.started).Round(100*time.Millisecond))
	return head, tail
}

// write puts a line where the last one was, covering whatever the last one was
// longer than with spaces.
//
// Erasing to the end of the line would be one escape rather than a run of
// spaces, but the Windows console does not interpret escapes unless a program
// has turned that on: a scan there showed "(13.1s)^[K" on every line.
//
// This is only safe because the line is fitted to the terminal first: padding
// an unfitted line is what wrapped it, and a wrapped line cannot be rewritten.
func (d *Display) write(line string, columns int) {
	if pad := d.standing - width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	if columns > 0 && width(line) > columns-1 {
		line = string([]rune(line)[:columns-1])
	}
	d.standing = width(line)
	fmt.Fprintf(d.w, "\r%s", line)
}

// width is how many columns a line takes, near enough: one per rune. It is
// what the fitting counts in, so it is what the padding has to count in too.
func width(line string) int { return len([]rune(line)) }

// columns is the width to fit a line into, or zero for no limit.
func (d *Display) columns() int {
	if d.file == nil {
		return 0
	}
	return terminalColumns(d.file)
}

// fit joins the three parts of a progress line so that the whole is at most
// columns-1 wide, leaving the last column clear: writing into it makes some
// terminals wrap immediately.
//
// The detail is what gives way, because the ends are what a glance is for. A
// terminal too narrow even for those gets the line cut short instead.
func fit(head, detail, tail string, columns int) string {
	line := head
	if detail != "" {
		line += " " + detail
	}
	line += tail
	if columns <= 0 || width(line) <= columns-1 {
		return line
	}
	room := columns - 1 - width(head) - width(tail)
	if room > 1 {
		return head + " " + string([]rune(detail)[:room-1]) + tail
	}
	return string([]rune(head + tail)[:max(columns-1, 0)])
}

// Finish writes the line a channel leaves behind. Unlike an update it is not
// fitted to the terminal: this one stays on screen and is never rewritten, so a
// narrow window wrapping it costs nothing, while cutting it would lose what the
// channel turned out to carry.
func (d *Display) Finish(message string) {
	if d.tty {
		head, tail := d.compose(PhaseDone, 1, 1)
		d.write(fit(head, message, tail, 0), 0)
		fmt.Fprintln(d.w)
		// The newline ends the line, so the next one starts on clean ground.
		d.standing = 0
		return
	}
	fmt.Fprintln(d.w, message)
}

// Note writes lines under the one a channel finished on, for what the channel
// turned out to carry. It is separate from Finish because the answer is a list
// of unknown length, and either would push the figures off the end of a line
// they are worth comparing down the column of.
//
// The lines arrive with whatever indent they are to have: what is nested under
// what is the caller's to say.
func (d *Display) Note(lines ...string) {
	for _, line := range lines {
		fmt.Fprintln(d.w, line)
	}
}

func (d *Display) Resumed(current, total int, label, status string) {
	if d.tty && d.standing > 0 {
		fmt.Fprintf(d.w, "\r%s\r", strings.Repeat(" ", d.standing))
		d.standing = 0
	}
	fmt.Fprintf(d.w, "[%d/%d] %s ... resumed (%s)\n", current, total, label, status)
}
