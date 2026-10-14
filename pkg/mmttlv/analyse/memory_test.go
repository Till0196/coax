// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package analyse

import (
	"encoding/binary"
	"math/rand"
	"testing"
)

// The analyser lives for as long as a recording does, which is hours. Nothing
// in it may grow with the length of the stream -- only with what the stream
// actually contains, and that is bounded.

func TestACleanStreamHoldsNothingThatGrows(t *testing.T) {
	analyser := New()
	data := stream(0x1000, 200)
	for range 2000 { // sixteen megabytes, fed as a driver would
		analyser.Feed(data)
	}
	if got := len(analyser.buffer); got != 0 {
		t.Errorf("held %d bytes after a stream of whole packets", got)
	}
	if got := len(analyser.sequences); got != 1 {
		t.Errorf("tracked %d runs in a stream with one", got)
	}
	if got := cap(analyser.buffer); got > 2*(len(data)+maxPacket) {
		t.Errorf("the buffer grew to %d for feeds of %d", got, len(data))
	}
}

// A stream that never lines up must not accumulate what it could not use.
func TestNoiseHoldsNothingThatGrows(t *testing.T) {
	analyser := New()
	noise := make([]byte, 6016)
	source := rand.New(rand.NewSource(4))
	for range 2000 { // twelve megabytes of it
		source.Read(noise)
		analyser.Feed(noise)
	}
	if got := cap(analyser.buffer); got > 2*(huntLimit+maxPacket) {
		t.Errorf("held %d bytes while hunting", got)
	}
	if got := len(analyser.sequences); got != 0 {
		t.Errorf("tracked %d runs in noise", got)
	}
}

// TestTheSequenceTableIsBounded is the one thing here that could have grown
// without limit: a stream being read wrongly invents a new run for every
// packet, and a recording runs for hours.
func TestTheSequenceTableIsBounded(t *testing.T) {
	analyser := New()
	for index := range 200_000 {
		packet := compressed(uint16(index%65535), uint32(index))
		// A new compression context as well, so that neither half of the key
		// repeats.
		binary.BigEndian.PutUint16(packet[4:6], uint16((index*7)%4096)<<4)
		analyser.Feed(packet)
	}
	if got := len(analyser.sequences); got > maxRuns {
		t.Errorf("tracked %d runs, which is past the %d bound", got, maxRuns)
	}
	if analyser.Stats().Forgotten == 0 {
		t.Error("the table was emptied without saying so")
	}
	// And a real run is still followed after all that.
	before := analyser.Stats().Discontinuities
	analyser.Feed(stream(0x1000, 50))
	if analyser.Stats().Discontinuities != before {
		t.Error("a clean run after the churn was reported as broken")
	}
}

// A multiplex with a lot of services is still nowhere near the bound, so the
// guard above never fires on anything real.
func TestARealisticNumberOfRunsIsNotForgotten(t *testing.T) {
	analyser := New()
	for round := range uint32(50) {
		for identifier := range uint16(64) {
			analyser.Feed(compressed(0x1000+identifier, round))
		}
	}
	if got := analyser.Stats().Forgotten; got != 0 {
		t.Errorf("sixty-four runs emptied the table %d times", got)
	}
	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("sixty-four interleaved runs gave %d discontinuities", got)
	}
}
