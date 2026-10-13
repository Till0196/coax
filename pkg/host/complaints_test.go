// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"strings"
	"testing"
)

func TestComplaintsQuoteWhatTheDaemonSaid(t *testing.T) {
	var heard complaints
	if got := heard.quoted(); got != "" {
		t.Fatalf("a daemon that said nothing must add nothing, got %q", got)
	}

	heard.Write([]byte("cannot bind /run/coaxd.sock: permission denied\n"))
	want := "\nthe daemon said:\n  cannot bind /run/coaxd.sock: permission denied"
	if got := heard.quoted(); got != want {
		t.Errorf("quoted() = %q, want %q", got, want)
	}

	// Windows line endings come off the pipe as the daemon wrote them and must
	// not be carried into the middle of the error.
	heard = complaints{}
	heard.Write([]byte("first\r\nsecond\r\n"))
	if got := heard.quoted(); strings.Contains(got, "\r") {
		t.Errorf("quoted() kept a carriage return: %q", got)
	}
}

// TestComplaintsSayEachReasonOnce guards the noise: the daemon is started
// again every second until the deadline, so the same failure arrives ten times.
func TestComplaintsSayEachReasonOnce(t *testing.T) {
	var heard complaints
	for range 10 {
		heard.Write([]byte("bind: permission denied\ngiving up\n"))
	}
	if got, want := strings.Count(heard.quoted(), "giving up"), 1; got != want {
		t.Errorf("a repeated line was shown %d times, want %d:\n%s", got, want, heard.quoted())
	}
}

// TestComplaintsKeepTheFirstReason is the property that matters when a daemon
// is started several times: the first line says why, and the retries repeat it.
func TestComplaintsKeepTheFirstReason(t *testing.T) {
	var heard complaints
	written, err := heard.Write([]byte("the reason\n" + strings.Repeat("noise\n", 4096)))
	if err != nil {
		t.Fatalf("Write must not fail, got %v", err)
	}
	if written != len("the reason\n")+len(strings.Repeat("noise\n", 4096)) {
		t.Errorf("Write must report the whole write taken, or the copy stops at the brim; got %d", written)
	}
	if len(heard.text) > complaintRoom {
		t.Errorf("kept %d bytes, more than the %d it is allowed", len(heard.text), complaintRoom)
	}
	if !strings.HasPrefix(heard.quoted(), "\nthe daemon said:\n  the reason") {
		t.Errorf("the first line was lost: %.60q", heard.quoted())
	}
}
