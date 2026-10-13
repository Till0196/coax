// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package config

import (
	"slices"
	"testing"
)

// TestNormalizeDeliverySystemGathersTheNames: every place that has to decide
// what a channel is asks this, so a name understood in one is understood in all
// -- the property that was missing when the scanner, the exporter and the route
// inference each kept a list of their own.
func TestNormalizeDeliverySystemGathersTheNames(t *testing.T) {
	for _, test := range []struct {
		names     []string
		canonical string
	}{
		{[]string{"ISDBT", "isdb-t", " ISDB-T "}, "ISDBT"},
		{[]string{"ISDBS", "ISDB-S"}, "ISDBS"},
		// The advanced satellite standard is a name of its own rather than
		// another spelling of ISDB-S: a channel file that says ISDBS3 means
		// the TLV multiplex, and the two have to stay tellable apart.
		{[]string{"ISDBS3", "ISDB-S3", "isdb-s3"}, "ISDBS3"},
		{[]string{"DVBS2", "DVB-S2"}, "DVBS2"},
		// The Japanese cable band, by every name it goes by.
		{[]string{
			"DVBC/ANNEX_A", "DVB-C/ANNEX_A", "DVBC/ANNEX_C", "DVB-C/ANNEX_C",
			"ANNEX_C", "J.83C", "j83c", "ISDBC", "ISDB-C", "DVBC", "DVB-C",
		}, "DVBC/ANNEX_A"},
		// And the annex the optical distribution derives from.
		{[]string{"DVBC/ANNEX_B", "DVB-C/ANNEX_B", "ANNEX_B", "J.83B", "J83B"}, "DVBC/ANNEX_B"},
	} {
		for _, name := range test.names {
			if got := NormalizeDeliverySystem(name); got != test.canonical {
				t.Errorf("NormalizeDeliverySystem(%q) = %q, want %q", name, got, test.canonical)
			}
		}
	}
	for _, name := range []string{"", "ATSC", "nonsense", "ANNEX_D"} {
		if got := NormalizeDeliverySystem(name); got != "" {
			t.Errorf("NormalizeDeliverySystem(%q) = %q, want nothing", name, got)
		}
	}
}

// TestDeliverySystemAliasesLeaveOutTheNameItself, so a message listing the
// other names it answers to does not repeat the one already shown.
func TestDeliverySystemAliasesLeaveOutTheNameItself(t *testing.T) {
	aliases := DeliverySystemAliases("DVBC/ANNEX_B")
	if slices.Contains(aliases, "DVBC/ANNEX_B") {
		t.Errorf("the canonical name is among its own aliases: %v", aliases)
	}
	for _, want := range []string{"J.83B", "J83B", "ANNEX_B", "DVB-C/ANNEX_B"} {
		if !slices.Contains(aliases, want) {
			t.Errorf("%s is missing from %v", want, aliases)
		}
	}
	if !slices.IsSorted(aliases) {
		t.Errorf("aliases are not in order: %v", aliases)
	}
}

// TestTheSatelliteSystemsStandInForEachOther: one demodulator receives ISDB-S
// and ISDB-S3 both, and which a tuner advertises is an accident of the interface
// it was enumerated through, since Linux has no delivery system number for the
// advanced one. Anything deciding whether a tuner is worth trying has to know
// that, or an ISDB-S3 channel is refused by the hardware that receives it.
func TestTheSatelliteSystemsStandInForEachOther(t *testing.T) {
	for _, test := range []struct{ asked, want string }{
		{"ISDBS3", "ISDBS"},
		{"ISDB-S3", "ISDBS"},
		{"ISDBS", "ISDBS3"},
	} {
		if got := DeliverySystemSubstitutes(test.asked); !slices.Contains(got, test.want) {
			t.Errorf("DeliverySystemSubstitutes(%q) = %v, want it to include %q", test.asked, got, test.want)
		}
	}
	// Nothing else stands in for anything: a terrestrial front end is not a
	// cable one, whatever the two have in common inside.
	for _, name := range []string{"ISDBT", "DVBC/ANNEX_A", "DVBS2", "nonsense", ""} {
		if got := DeliverySystemSubstitutes(name); len(got) != 0 {
			t.Errorf("DeliverySystemSubstitutes(%q) = %v, want nothing", name, got)
		}
	}
}
