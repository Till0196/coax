// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package idle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestIdleNeedsConsecutiveQuiet: a reconnecting client must not race the daemon
// to a shutdown.
func TestIdleNeedsConsecutiveQuiet(t *testing.T) {
	var attached atomic.Int64
	attached.Store(1)

	idle := Rule{
		Interval: 10 * time.Millisecond,
		Strikes:  3,
		Count:    func() int { return int(attached.Load()) },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() { done <- idle.Wait(ctx) }()

	// While something is attached it must not stop, however long it waits.
	select {
	case <-done:
		t.Fatal("stopped while a client was still attached")
	case <-time.After(80 * time.Millisecond):
	}

	attached.Store(0)
	select {
	case stopped := <-done:
		if !stopped {
			t.Error("ended for a reason other than idleness")
		}
	case <-time.After(time.Second):
		t.Fatal("did not stop once nothing was attached")
	}
}

// TestIdleIsResetByAnyUse: a daemon in intermittent use must not accumulate
// scattered quiet moments into a shutdown.
func TestIdleIsResetByAnyUse(t *testing.T) {
	var attached atomic.Int64
	idle := Rule{
		Interval: 10 * time.Millisecond,
		Strikes:  3,
		Count:    func() int { return int(attached.Load()) },
	}

	// Alternate between busy and quiet so that no three consecutive looks are
	// ever quiet.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(12 * time.Millisecond):
				attached.Store(1 - attached.Load())
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if idle.Wait(ctx) {
		t.Error("a daemon in intermittent use stopped itself")
	}
}

func TestIdleWithNoRuleWaitsForTheContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	rule := Rule{}
	if rule.Wait(ctx) {
		t.Error("reported idleness when there was no rule to judge it by")
	}
}
