// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package build says what a build is, in one line. It exists so that the icon
// in the notification area, the version flag and anything else that has to name
// this build all say the same thing.
package build

import "strings"

// Describe is the one line a build says about itself: what it is, which release
// it was meant to be, and which code it actually is.
//
// The commit is worth the room beside the version: the two part company exactly
// when it matters -- a build from a working tree, a release rebuilt with a fix
// on top, a machine somebody copied a binary to and cannot now account for.
func Describe(title, version, commit string) string {
	if version != "" {
		title += " " + version
	}
	if short := ShortCommit(commit); short != "" {
		title += " (" + short + ")"
	}
	return title
}

// commitRoom is how much of a commit is shown: the seven characters git itself
// shows, which is what somebody comparing this against a log will be reading.
const commitRoom = 7

// ShortCommit trims a commit to something a person can read and repeat.
//
// Only a long hexadecimal string is trimmed, because only that is a hash with
// a meaningful prefix. Anything else a build put there is somebody's own words
// and is left as they wrote it.
func ShortCommit(commit string) string {
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
