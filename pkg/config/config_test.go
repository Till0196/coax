// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChannelConfigReader(t *testing.T) {
	input := `
# comment
[45168]
    DELIVERY_SYSTEM = ISDBS
    FREQUENCY = 1164560
    STREAM_ID = 45168

[45169]
	DELIVERY_SYSTEM = ISDBS
	FREQUENCY = 1164560
	STREAM_ID = 45169
`
	entries, err := ParseReader(strings.NewReader(input), "test.conf", "SAT")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Name != "45168" || entries[0].Route != "SAT" {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Properties["STREAM_ID"] != "45169" {
		t.Fatalf("unexpected properties: %#v", entries[1].Properties)
	}
}

func TestParseChannelConfigReaderRejectsDuplicateSection(t *testing.T) {
	input := `[1]
DELIVERY_SYSTEM = ISDBT
FREQUENCY = 93142857
[1]
DELIVERY_SYSTEM = ISDBT
FREQUENCY = 93142857
`
	_, err := ParseReader(strings.NewReader(input), "test.conf", "TER")
	if err == nil || !strings.Contains(err.Error(), "duplicate section") {
		t.Fatalf("got %v, want duplicate-section error", err)
	}
}

// Every section of a file takes the lead the scan declared, whatever each one is
// tuned as: a cable carries transmodulated channels and satellite pass-throughs
// on the same lead, and the pass-throughs are ISDB-S on the wire, so reading the
// system as the lead would put them on a dish that may not carry them.
func TestOneScanIsOneLead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.conf")
	input := `[transmodulated]
DELIVERY_SYSTEM = DVBC/ANNEX_A
FREQUENCY = 473000000

[passed through]
DELIVERY_SYSTEM = ISDBS
FREQUENCY = 1164560
STREAM_ID = 45168
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Parse(path, "catv")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Route != "CATV" || entries[1].Route != "CATV" {
		t.Fatalf("the lead is the scan's, not the delivery system's: %#v", entries)
	}
}

// A scan that did not say which lead it was of leaves it unsaid, rather than
// guessing. Mirakurun answers for itself from the channel type.
func TestAnUndeclaredLeadStaysUndeclared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.conf")
	input := `[terrestrial]
DELIVERY_SYSTEM = ISDBT
FREQUENCY = 473142857
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := Parse(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Route != "" {
		t.Fatalf("a lead was invented: %#v", entries)
	}
	if _, err := Parse(path, "AUTO"); err == nil {
		t.Fatal("auto was a way of asking for a guess, and there is no guess to make")
	}
}

// A route is declared, not worked out: nothing but the four names is taken. An
// empty one is not an error here -- it is a scan that did not say which lead it
// was of -- but ValidRoute itself refuses it, and Parse treats empty as unsaid.
func TestValidRoute(t *testing.T) {
	for _, route := range []string{"TER", "SAT", "CATV", "HIKARI"} {
		if !ValidRoute(route) {
			t.Errorf("%q should be a route", route)
		}
	}
	for _, route := range []string{"", "ter", "AUTO", "ISDBT", "OPTICAL"} {
		if ValidRoute(route) {
			t.Errorf("%q should not be a route", route)
		}
	}
}
