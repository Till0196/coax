// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"fmt"
	"io"
	"strings"
)

// What this build is.
//
// Both may be replaced at build time:
//
//	go build -ldflags "-X github.com/Till0196/coax/pkg/cli.version=0.2.0 -X github.com/Till0196/coax/pkg/cli.commit=$(git rev-parse HEAD)"
var (
	version = "0.1.2"
	commit  = ""
)

// runVersion says what this build is, without asking the daemon: a version that
// only answered when the hardware side was working would be no use for finding
// out why it is not.
func runVersion(_ []string, stdout io.Writer) error {
	fmt.Fprintln(stdout, describeBuild("coax", version, commit))
	return nil
}

// describeBuild is the one line this build says about itself: what it is, which
// release it was meant to be, and which code it actually is. The two part
// company exactly when it matters -- a build from a working tree, a release
// rebuilt with a fix on top, a binary somebody copied and cannot account for.
//
// The daemon says the same thing the same way, in its own copy of this: the two
// programs share no code, only the shape of what they say.
func describeBuild(title, version, commit string) string {
	if version != "" {
		title += " " + version
	}
	if short := shortCommit(commit); short != "" {
		title += " (" + short + ")"
	}
	return title
}

// commitRoom is how much of a commit is shown: the seven characters git itself
// shows, which is what somebody comparing this against a log will be reading.
const commitRoom = 7

// shortCommit trims a commit to something a person can read and repeat. Only a
// long hexadecimal string is trimmed, because only that is a hash with a
// meaningful prefix; anything else a build put there is left as written.
func shortCommit(commit string) string {
	if len(commit) <= commitRoom {
		return commit
	}
	for _, letter := range commit {
		if !strings.ContainsRune("0123456789abcdefABCDEF", letter) {
			return commit
		}
	}
	return commit[:commitRoom]
}
