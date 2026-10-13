// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Till0196/coax/pkg/host"
)

func sampleBoards() []host.Board {
	return []host.Board{
		{
			Name:        "t230:p1-1",
			Naming:      "port",
			Stable:      true,
			Backend:     "t230",
			Description: "MyGica T230C / T230A / T230C2 (Si2168 + Si2141)",
			Location:    "001:005",
			Tuners: []host.Tuner{
				{ID: "t230:p1-1:0", Index: 0, Name: "Si2141", Deliveries: []string{"DVB-T", "DVB-T2", "DVB-C"}},
			},
		},
		{
			Name:        "px4:ABC12345@1-2.1",
			Naming:      "serial@port",
			Stable:      true,
			Backend:     "px4",
			Description: "PLEX PX-W3U4 (IT930x + TC90522)",
			Location:    "001:006",
			Tuners: []host.Tuner{
				{ID: "px4:ABC12345@1-2.1:0", Index: 0, Name: "RT710", Deliveries: []string{"ISDB-S"}},
				{ID: "px4:ABC12345@1-2.1:2", Index: 2, Name: "R850", Deliveries: []string{"ISDB-T"}, Leased: true},
			},
		},
	}
}

// TestWriteBoardsNamesTheTunersToTuneWith: every line has to carry a name that
// can be pasted into --tuner.
func TestWriteBoardsNamesTheTunersToTuneWith(t *testing.T) {
	var out bytes.Buffer
	if err := writeBoards(&out, sampleBoards(), true, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"t230:p1-1:0", "px4:ABC12345@1-2.1:0", "px4:ABC12345@1-2.1:2",
		"DVB-T2", "ISDB-S", "RT710", "R850",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing does not mention %q:\n%s", want, text)
		}
	}
	// Tuner indices are not contiguous on a board that mixes kinds, so the
	// name has to carry the real index rather than a running count.
	if strings.Contains(text, "px4:ABC12345@1-2.1:1") {
		t.Errorf("the listing renumbered the tuners:\n%s", text)
	}
}

// TestWriteBoardsShowsWhatIsInUse, so that a busy tuner explains itself before
// a tune against it is refused.
func TestWriteBoardsShowsWhatIsInUse(t *testing.T) {
	var out bytes.Buffer
	if err := writeBoards(&out, sampleBoards(), true, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "in use") {
		t.Errorf("the listing does not say which tuner is taken:\n%s", text)
	}
	if !strings.Contains(text, "free") {
		t.Errorf("the listing does not say which tuner is available:\n%s", text)
	}
}

// TestWriteBoardsSaysWhenItDidNotLook: tuners worked out from the model rather
// than the hardware can be wrong, and the reader has to be told how to find
// out.
func TestWriteBoardsSaysWhenItDidNotLook(t *testing.T) {
	var out bytes.Buffer
	if err := writeBoards(&out, sampleBoards(), false, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	if !strings.Contains(out.String(), "--tuners") {
		t.Errorf("an unprobed listing does not say how to confirm it:\n%s", out.String())
	}

	out.Reset()
	if err := writeBoards(&out, sampleBoards(), true, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	if strings.Contains(out.String(), "--tuners") {
		t.Errorf("a probed listing still offers to probe:\n%s", out.String())
	}
}

// TestWriteBoardsWarnsAboutAnUnstableName: a board named after the bus address
// it happens to be at is named something else after a replug, so a name written
// into a Mirakurun configuration would stop working.
func TestWriteBoardsWarnsAboutAnUnstableName(t *testing.T) {
	boards := sampleBoards()
	boards[0].Stable = false
	boards[0].Naming = "bus"
	var out bytes.Buffer
	if err := writeBoards(&out, boards, true, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	if !strings.Contains(out.String(), boards[0].Name) || !strings.Contains(out.String(), "unplugged") {
		t.Errorf("the listing does not warn that a name will change:\n%s", out.String())
	}
}

func TestWriteBoardsExplainsAnEmptyResult(t *testing.T) {
	var out bytes.Buffer
	if err := writeBoards(&out, nil, false, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	if !strings.Contains(out.String(), "no tuners") {
		t.Errorf("an empty listing should say so plainly:\n%s", out.String())
	}
}

// TestDescribeFallsBackToTheBackend, because a board that was never opened has
// no description to print and a blank column tells the reader nothing.
func TestDescribeFallsBackToTheBackend(t *testing.T) {
	board := host.Board{Backend: "px4"}
	if got := describe(board, host.Tuner{}); got != "px4" {
		t.Errorf("described an unopened board as %q, want the backend name", got)
	}
	if got := describe(board, host.Tuner{Name: "R850"}); got != "px4 (R850)" {
		t.Errorf("described a named tuner as %q", got)
	}
}

// TestDescribeDoesNotRepeatPartsTheBoardAlreadyNames: the board and the tuner
// punctuate their parts differently, and comparing the whole strings once left
// a US-3POUT listed as "US-3POUT (TC90522 + MxL136 + TDA20142) (MxL136 /
// TDA20142)".
func TestDescribeDoesNotRepeatPartsTheBoardAlreadyNames(t *testing.T) {
	for _, one := range []struct {
		description string
		tuner       string
		want        string
	}{
		{"US-3POUT (TC90522 + MxL136 + TDA20142)", "MxL136 / TDA20142", "US-3POUT (TC90522 + MxL136 + TDA20142)"},
		{"DigiBest DTV02A-1T1S-U (IT930x + TC90522 + R850/RT710)", "R850 / RT710", "DigiBest DTV02A-1T1S-U (IT930x + TC90522 + R850/RT710)"},
		{"PLEX PX-W3U4 (IT930x + TC90522 + R850/RT710)", "RT710", "PLEX PX-W3U4 (IT930x + TC90522 + R850/RT710)"},
		{"KEIAN KTV-FSUSB2 (EM2874 + MB86A20S/A21S)", "MxL135RF / TDA18211HD", "KEIAN KTV-FSUSB2 (EM2874 + MB86A20S/A21S) (MxL135RF / TDA18211HD)"},
	} {
		board := host.Board{Description: one.description}
		if got := describe(board, host.Tuner{Name: one.tuner}); got != one.want {
			t.Errorf("describe(%q, %q) = %q, want %q", one.description, one.tuner, got, one.want)
		}
	}
}

// TestWriteBoardsReportsABoardItCouldNotOpen: a listing asked to confirm the
// hardware and unable to must say so, since the tuners it prints are then what
// the model is known to carry rather than what is there.
func TestWriteBoardsReportsABoardItCouldNotOpen(t *testing.T) {
	boards := sampleBoards()
	boards[0].ProbeError = "read required firmware /lib/firmware/dvb-demod-si2168-d60-01.fw: no such file or directory"
	var out bytes.Buffer
	if err := writeBoards(&out, boards, true, false); err != nil {
		t.Fatalf("writeBoards: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "dvb-demod-si2168-d60-01.fw") {
		t.Errorf("the listing hides why a board could not be opened:\n%s", text)
	}
	if !strings.Contains(text, boards[0].Name) {
		t.Errorf("the listing does not say which board it was:\n%s", text)
	}
}

// TestTheStateColumnSaysWhatRankHoldsATuner: the default rank is left out, so
// the one line where somebody has said something stands out from the many
// where nobody has.
func TestTheStateColumnSaysWhatRankHoldsATuner(t *testing.T) {
	def := host.DefaultPriority
	raised := 7
	for _, test := range []struct {
		name  string
		tuner host.Tuner
		want  string
	}{
		{"a free tuner", host.Tuner{}, "free"},
		{"one held at the default", host.Tuner{Leased: true, Priority: &def}, "in use"},
		// A daemon too old to report one says nothing rather than zero.
		{"one held by a daemon that does not say", host.Tuner{Leased: true}, "in use"},
		{"one held above it", host.Tuner{Leased: true, Priority: &raised}, "in use (priority 7)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := tunerState(test.tuner); got != test.want {
				t.Errorf("state = %q, want %q", got, test.want)
			}
		})
	}
}
