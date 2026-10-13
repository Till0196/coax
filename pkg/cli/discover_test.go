// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestDiscoverFlagIsASwitchThatTakesAList: written bare, --discover means every
// band the tuner carries; given a value it means those bands and no others,
// which keeps a scan off three hundred carriers when one band was wanted.
func TestDiscoverFlagIsASwitchThatTakesAList(t *testing.T) {
	for _, test := range []struct {
		value   string
		enabled bool
		systems []string
	}{
		{"true", true, nil},
		{"", true, nil},
		{"false", false, nil},
		{"ISDBS", true, []string{"ISDBS"}},
		{"ISDBS,DVBC/ANNEX_B", true, []string{"ISDBS", "DVBC/ANNEX_B"}},
	} {
		var flag discoverFlag
		if err := flag.Set(test.value); err != nil {
			t.Fatalf("Set(%q): %v", test.value, err)
		}
		if flag.enabled != test.enabled || !slices.Equal(flag.systems, test.systems) {
			t.Errorf("Set(%q) gave enabled=%t systems=%v, want %t and %v",
				test.value, flag.enabled, flag.systems, test.enabled, test.systems)
		}
	}
	// A switch that says so is what makes the bare form legal.
	var flag discoverFlag
	if !flag.IsBoolFlag() {
		t.Error("--discover must report itself as a switch or it cannot be written bare")
	}
}

// A delivery system written after --discover rather than attached to it means
// what it plainly means. The flag may be written bare, so the flag package stops
// at it and takes the next word as a channel file -- which used to fail as "do
// not give it a channel file as well", naming the one thing the caller had not
// done.
func TestASystemAfterDiscoverIsTakenAsItsValue(t *testing.T) {
	for _, one := range []struct {
		name  string
		words []string
		want  []string
	}{
		{"one system", []string{"ISDBS"}, []string{"ISDBS"}},
		{"two", []string{"ISDBT", "ISDBS"}, []string{"ISDBT", "ISDBS"}},
		{"spelled another way", []string{"isdb-s"}, []string{"isdb-s"}},
	} {
		t.Run(one.name, func(t *testing.T) {
			got := leadingSystems(one.words)
			if !slices.Equal(got, one.want) {
				t.Fatalf("got %v, want %v", got, one.want)
			}
		})
	}
}

// Anything that is not a system this can work out is still a file name, and a
// file that exists wins even when it is named after one.
func TestAFileIsStillAFile(t *testing.T) {
	for _, one := range []struct {
		words []string
		want  int
	}{
		{[]string{"channels.conf"}, 0},
		{[]string{"DVBT2"}, 0}, // real, but not one this can work out alone
		// Only the leading words are taken: what follows is whatever came
		// next on the command line.
		{[]string{"ISDBS", "channels.conf"}, 1},
		{[]string{"ISDBT", "ISDBS", "--list"}, 2},
		{[]string{"--list", "ISDBS"}, 0},
	} {
		if got := leadingSystems(one.words); len(got) != one.want {
			t.Errorf("leadingSystems(%v) = %v, want %d of them", one.words, got, one.want)
		}
	}

	existing := filepath.Join(t.TempDir(), "ISDBS")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := leadingSystems([]string{existing}); len(got) != 0 {
		t.Error("a file that exists was taken as a delivery system")
	}
}

// TestALeadingListOfSystemsIsTakenAsTheFlagsValue: the flag takes several
// systems written --discover=ISDBT,ISDBS, and the same list after a space has to
// mean the same. Because the flag reports itself as boolean, the flag package
// does not take the next word as its value, so without this the list is read as
// a channel file and refused with a message about channel files.
func TestALeadingListOfSystemsIsTakenAsTheFlagsValue(t *testing.T) {
	for _, test := range []struct {
		words []string
		want  []string
	}{
		{[]string{"ISDBS,ISDBS3"}, []string{"ISDBS,ISDBS3"}},
		{[]string{"ISDBS", "ISDBS3"}, []string{"ISDBS", "ISDBS3"}},
		{[]string{"ISDB-S,ISDB-S3", "--list"}, []string{"ISDB-S,ISDB-S3"}},
		// A word that is not systems is not a list of them, whatever the
		// commas suggest.
		{[]string{"ISDBS,nonsense"}, nil},
		{[]string{"channels.conf"}, nil},
	} {
		got := leadingSystems(test.words)
		if len(got) != len(test.want) {
			t.Errorf("leadingSystems(%q) = %q, want %q", test.words, got, test.want)
			continue
		}
		for i := range got {
			if got[i] != test.want[i] {
				t.Errorf("leadingSystems(%q) = %q, want %q", test.words, got, test.want)
				break
			}
		}
	}
}
