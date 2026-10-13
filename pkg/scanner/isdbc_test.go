// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"slices"
	"testing"
)

// The Japanese cable band is written down as what it is. The tuners lock it as
// DVB-C Annex A and that is what a front end advertises, but a reader of a
// scan is told the standard, not the demodulator's name for it.
func TestTheCableBandIsWrittenDownAsISDBC(t *testing.T) {
	for _, walked := range walkedPlans {
		if walked.what != "cable" {
			continue
		}
		if got := walked.properties["DELIVERY_SYSTEM"]; got != "ISDBC" {
			t.Errorf("the cable band is written down as %q", got)
		}
		// Matched against what a tuner says it carries, which is the DVB name.
		if walked.system != "DVBC/ANNEX_A" {
			t.Errorf("the cable band is matched as %q", walked.system)
		}
		return
	}
	t.Fatal("there is no cable band to walk")
}

// Every spelling is still taken, whichever one a viewer types.
func TestTheCableBandIsAskedForByEitherName(t *testing.T) {
	for _, name := range []string{"ISDBC", "ISDB-C", "DVBC/ANNEX_A", "J.83C"} {
		chosen, err := resolveSystems([]string{name})
		if err != nil {
			t.Fatalf("--discover=%s: %v", name, err)
		}
		if !chosen["DVBC/ANNEX_A"] {
			t.Errorf("--discover=%s chose %v", name, chosen)
		}
	}
	// And the reader is told the one name.
	named := DiscoverableSystemNames()
	if !slices.Contains(named, "ISDBC") || slices.Contains(named, "DVBC/ANNEX_A") {
		t.Errorf("the systems are offered as %v", named)
	}
}
