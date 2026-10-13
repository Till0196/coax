// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package build_test

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/build"
)

// TestDescribeNamesTheBuild covers the line the icon shows about itself.
//
// A daemon somebody did not start is first of all a question of which one it
// is, and a version alone does not always answer it: the same version is built
// from a working tree, rebuilt with a fix on top, and copied between machines.
func TestDescribeNamesTheBuild(t *testing.T) {
	for _, want := range []struct{ version, commit, line string }{
		{"1.2.3", "0123456789abcdef0123456789abcdef01234567", "coaxd 1.2.3 (0123456)"},
		{"1.2.3", "abc1234", "coaxd 1.2.3 (abc1234)"},
		{"1.2.3", "", "coaxd 1.2.3"},
		{"", "abc1234", "coaxd (abc1234)"},
		{"", "", "coaxd"},
		// Not a hash, so not somebody's prefix to cut: left as it was written.
		{"1.2.3", "built-by-hand-today", "coaxd 1.2.3 (built-by-hand-today)"},
	} {
		if got := build.Describe("coaxd", want.version, want.commit); got != want.line {
			t.Errorf("build.Describe(%q, %q) = %q, want %q", want.version, want.commit, got, want.line)
		}
	}
}
