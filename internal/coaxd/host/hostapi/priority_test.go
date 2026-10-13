// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package hostapi

import "testing"

func TestParsePriority(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		want   int
		refuse bool
	}{
		// The one that matters most: every client written before this header
		// existed sends none, and has to keep meaning exactly what it meant.
		{name: "no header is the default", header: "", want: DefaultPriority},
		{name: "whitespace is no header", header: "  ", want: DefaultPriority},
		{name: "a number is itself", header: "5", want: 5},
		{name: "written zero is the default too", header: "0", want: 0},
		{name: "space around it is ignored", header: " 12 ", want: 12},
		{name: "below the default is allowed", header: "-3", want: -3},
		{name: "and says so with a sign", header: "+3", want: 3},
		// Refused rather than read as zero: a client that meant to say
		// something and was misunderstood would find its recording quietly
		// stopped by work it thought it outranked.
		{name: "a word is refused", header: "high", refuse: true},
		{name: "a fraction is refused", header: "1.5", refuse: true},
		{name: "two numbers are refused", header: "1, 2", refuse: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParsePriority(test.header)
			if test.refuse {
				if err == nil {
					t.Fatalf("%q was read as %d, want it refused", test.header, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q: %v", test.header, err)
			}
			if got != test.want {
				t.Errorf("%q read as %d, want %d", test.header, got, test.want)
			}
		})
	}
}
