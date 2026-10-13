// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package windriver

import "testing"

// Only the library version the structure offsets were measured against is
// accepted. Current WinDriver versions lay out at least one of these structures
// differently from the 2015 one this package knows, and reading a field from
// the wrong place opens the wrong card rather than failing.
func TestOnlyTheMeasuredVersionIsAccepted(t *testing.T) {
	for _, one := range []struct {
		dll      string
		accepted bool
	}{
		{"wdapi1200.dll", true},
		{"WDAPI1200.DLL", true},
		{"wdapi1600.dll", false},
		{"wdapi1201.dll", false},
		{"wdapi6.dll", false},
		{"something.dll", false},
		{"", false},
	} {
		err := Library{DLL: one.dll}.measured()
		if one.accepted && err != nil {
			t.Errorf("%q was refused: %v", one.dll, err)
		}
		if !one.accepted && err == nil {
			t.Errorf("%q was accepted, and it is not what was measured", one.dll)
		}
	}
}

func TestAVersionIsWrittenTheWayJungoWritesIt(t *testing.T) {
	for _, one := range []struct{ digits, want string }{
		{"1200", "12.0.0"},
		{"1510", "15.1.0"},
		{"600", "6.0.0"},
		{"12", "an unrecognised version"},
	} {
		if got := describeVersion(one.digits); got != one.want {
			t.Errorf("describeVersion(%q) = %q, want %q", one.digits, got, one.want)
		}
	}
}
