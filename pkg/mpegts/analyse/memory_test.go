// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package analyse

import (
	"math/rand"
	"testing"
)

// The analyser lives for as long as a recording does, which is hours. Nothing
// in it may grow with the length of the stream.
//
// Here that is easy to hold to, because there is nothing to grow: the
// continuity counters are one array with an entry per PID, and there are 8192
// PIDs whether the stream uses them or not. The tests are still worth having,
// so that adding a map here later fails rather than leaks.

func TestACleanStreamHoldsNothingThatGrows(t *testing.T) {
	analyser := New()
	data := stream(0x100, 1000)
	for range 500 { // ninety megabytes
		analyser.Feed(data)
	}
	if got := len(analyser.buffer); got != 0 {
		t.Errorf("held %d bytes after a stream of whole packets", got)
	}
	// Twice the feed leaves room for how append rounds up a slice, and is
	// still nowhere near the ninety megabytes that went past.
	if got := cap(analyser.buffer); got > 2*(len(data)+PacketSize) {
		t.Errorf("the buffer grew to %d for feeds of %d", got, len(data))
	}
}

func TestNoiseHoldsNothingThatGrows(t *testing.T) {
	analyser := New()
	noise := make([]byte, 6016)
	source := rand.New(rand.NewSource(4))
	for range 2000 { // twelve megabytes of it
		source.Read(noise)
		analyser.Feed(noise)
	}
	if got := cap(analyser.buffer); got > 2*(len(noise)+huntLimit+PacketSize) {
		t.Errorf("held %d bytes while hunting", got)
	}
}

// Every PID at once, which is the most this can ever be asked to remember.
func TestEveryPIDAtOnceIsStillOneArray(t *testing.T) {
	analyser := New()
	for pid := range NullPID {
		analyser.Feed(stream(pid, 4))
	}
	if got := len(analyser.counters); got != NullPID+1 {
		t.Errorf("the counter table is %d entries", got)
	}
	if got := len(analyser.buffer); got != 0 {
		t.Errorf("held %d bytes after whole packets", got)
	}
}
