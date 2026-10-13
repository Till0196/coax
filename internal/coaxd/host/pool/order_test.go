// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"slices"
	"testing"
)

// A stream is read by a goroutine of its own, so the hardware under it must not
// be closed until the stream has been: first the stream, then the tuner.
func TestReleaseClosesTheStreamBeforeTheTuner(t *testing.T) {
	pool, _, board := newTestPool(t, Options{Linger: -1})
	board.streams = true
	lease, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: pool.NewOwner()})
	if err != nil {
		t.Fatalf("take a tuner: %v", err)
	}
	stream, err := lease.Stream()
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	lease.Release()
	if got, want := board.recorded(), []string{"stream closed", "tuner closed"}; !slices.Equal(got, want) {
		t.Errorf("closed %v, want %v", got, want)
	}
	// The client noticing later and closing again does nothing more.
	_ = stream.Close()
	if got := board.recorded(); len(got) != 2 {
		t.Errorf("a second close reached the hardware: %v", got)
	}
}

func TestGoneClosesTheStreamBeforeTheBoard(t *testing.T) {
	pool, _, board := newTestPool(t, Options{Linger: -1})
	board.streams = true
	lease, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: pool.NewOwner()})
	if err != nil {
		t.Fatalf("take a tuner: %v", err)
	}
	if _, err := lease.Stream(); err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	lease.board.markGone()
	got := board.recorded()
	if len(got) == 0 || got[0] != "stream closed" {
		t.Errorf("closed %v, want the stream first", got)
	}
	if !slices.Contains(got, "board closed") || !slices.Contains(got, "tuner closed") {
		t.Errorf("closed %v, want the board and the tuner as well", got)
	}
}
