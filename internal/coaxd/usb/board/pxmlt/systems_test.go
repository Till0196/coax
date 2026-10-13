// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pxmlt

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// TestSupportsMatchesTheDeliveryList keeps the two halves of the board's
// advertisement in step: the list a scan reads and the check a tune makes are
// separate values, so one can be extended without the other.
func TestSupportsMatchesTheDeliveryList(t *testing.T) {
	for _, delivery := range deliveries {
		if !Supports(delivery) {
			t.Errorf("%q is listed but Supports() refuses it", delivery)
		}
	}
	// Not a count, because one listed cable system answers to four spellings:
	// DVB-C, DVB-C/ANNEX_A, DVB-C/ANNEX_C and ISDB-C all name the one mode the
	// chips have.
	listed := map[string]bool{}
	for _, delivery := range deliveries {
		listed[tuning.Normalize(delivery)] = true
	}
	for name := range systems {
		if listed[name] || (tuning.IsJ83AnnexAC(name) && listed["ISDBC"]) {
			continue
		}
		t.Errorf("the table accepts %q, which is not in the delivery list", name)
	}
	for _, delivery := range []string{"ATSC", "ISDB-S3", "DVB-C2"} {
		if Supports(delivery) {
			t.Errorf("Supports(%q) should be false", delivery)
		}
	}
}

// TestEverySpellingOfTheCableBandIsAccepted checks that all four names a channel
// file may use for the band reach the one mode: Japan's cable band is J.83 Annex
// C and the chip has no Annex C core, so it is locked as Annex A. Annex B is a
// different system, the trellis-coded core, and must not share it.
func TestEverySpellingOfTheCableBandIsAccepted(t *testing.T) {
	want := sony.SystemDVBC
	for _, spelling := range []string{
		"ISDB-C", "ISDBC", "isdb_c", "DVB-C", "DVBC", "DVB-C/ANNEX_A", "DVB-C/ANNEX_C",
	} {
		if !Supports(spelling) {
			t.Errorf("Supports(%q) is false", spelling)
			continue
		}
		if got := systems[tuning.Normalize(spelling)]; got != want {
			t.Errorf("%q selects %v, want %v", spelling, got, want)
		}
	}
	if got := systems[tuning.Normalize("DVB-C/ANNEX_B")]; got != sony.SystemJ83B {
		t.Errorf("Annex B selects %v, want %v", got, sony.SystemJ83B)
	}
}

// TestEveryDeliverySelectsTheRightSystem checks that a channel file's spelling
// reaches the sony.System it names, and lands on the right side of the satellite
// split.
func TestEveryDeliverySelectsTheRightSystem(t *testing.T) {
	for delivery, want := range map[string]sony.System{
		"ISDBT":       sony.SystemISDBT,
		"ISDBS":       sony.SystemISDBS,
		"DVBT":        sony.SystemDVBT,
		"DVBT2":       sony.SystemDVBT2,
		"DVBS":        sony.SystemDVBS,
		"DVBS2":       sony.SystemDVBS2,
		"ISDBC":       sony.SystemDVBC,
		"DVBC/ANNEXB": sony.SystemJ83B,
	} {
		got, ok := systems[delivery]
		if !ok {
			t.Errorf("%s is missing from the table", delivery)
			continue
		}
		if got != want {
			t.Errorf("%s selects %v, want %v", delivery, got, want)
		}
		// Tune branches on this, so a system on the wrong side of it would
		// take the other path's sequence and its parameters with it.
		if got.IsSatellite() != (delivery == "ISDBS" || delivery == "DVBS" || delivery == "DVBS2") {
			t.Errorf("%s is on the wrong side of the satellite split", delivery)
		}
	}
}
