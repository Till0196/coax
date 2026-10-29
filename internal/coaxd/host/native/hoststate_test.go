// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

func TestHostStateSendsTransitionsAndClosesWithItsContext(t *testing.T) {
	state := newHostState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := net.Pipe()
	defer reader.Close()
	done := make(chan struct{})
	go func() { defer close(done); state.send(ctx, writer) }()
	lines := bufio.NewScanner(reader)
	for _, want := range []string{"starting", "recovering", "ready", "closing", "closed"} {
		state.set(want)
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("state=%q error=%v, want %s", lines.Text(), lines.Err(), want)
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal state kept the sender alive")
	}
}

func TestCancelledHostStateUnblocksAReaderThatHasGoneAway(t *testing.T) {
	state := newHostState()
	reader, writer := net.Pipe()
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); state.send(ctx, writer) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled state sender remained in write")
	}
}
