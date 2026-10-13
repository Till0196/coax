// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The version is a version, not a placeholder: it has a default rather than
// being left as "dev", because a program that cannot say what it is when built
// the ordinary way is one nobody can report a bug against.
func TestTheBuildSaysWhatItIs(t *testing.T) {
	if version == "" || version == "dev" {
		t.Errorf("this build calls itself %q", version)
	}
	var out bytes.Buffer
	if err := runVersion(nil, &out); err != nil {
		t.Fatal(err)
	}
	if said := strings.TrimSpace(out.String()); said != describeBuild("coax", version, commit) {
		t.Errorf("version said %q", said)
	}
}

// A commit is shown beside the version, trimmed to the seven characters git
// itself shows. The daemon says the same thing the same way in its own copy of
// this: the two share no code, so the rule is written twice and checked twice.
func TestTheCommitIsShownTheWayGitShowsIt(t *testing.T) {
	for _, one := range []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{"a release from a known commit", "0.1.0", "0123456789abcdef0123456789abcdef01234567", "coax 0.1.0 (0123456)"},
		{"a commit already short", "0.1.0", "abc1234", "coax 0.1.0 (abc1234)"},
		{"no commit at all", "0.1.0", "", "coax 0.1.0"},
		// Anything that is not a hash is somebody's own words, and saying
		// half of them would be worse than saying all.
		{"words rather than a hash", "0.1.0", "built-by-hand", "coax 0.1.0 (built-by-hand)"},
	} {
		t.Run(one.name, func(t *testing.T) {
			if got := describeBuild("coax", one.version, one.commit); got != one.want {
				t.Errorf("got %q, want %q", got, one.want)
			}
		})
	}
}
