// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

func TestCOMThreadRunsCallsAndReturnsTheirErrors(t *testing.T) {
	thread := startCOMThread()
	defer thread.stop()

	if err := thread.do(func() error { return nil }); err != nil {
		t.Fatalf("a call that succeeds must report nil, got %v", err)
	}
	sentinel := errors.New("the hardware said no")
	if err := thread.do(func() error { return sentinel }); err != sentinel {
		t.Fatalf("a call's error must come back as it was, got %v", err)
	}
}

func TestCOMThreadSerialisesCalls(t *testing.T) {
	thread := startCOMThread()
	defer thread.stop()

	// If two calls ever ran at once, both would see running set and fail.
	var running bool
	var order []int
	var group sync.WaitGroup
	var mutex sync.Mutex
	for index := range 8 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_ = thread.do(func() error {
				if running {
					t.Error("two calls ran on the thread at once")
				}
				running = true
				mutex.Lock()
				order = append(order, index)
				mutex.Unlock()
				running = false
				return nil
			})
		}(index)
	}
	group.Wait()
	if len(order) != 8 {
		t.Fatalf("all 8 calls must run, got %d", len(order))
	}
}

func TestCOMThreadAnswersGoneAfterStop(t *testing.T) {
	thread := startCOMThread()
	thread.stop()
	// Stopping twice is harmless: a board can be told it is gone while it is
	// also being shut down.
	thread.stop()

	// A stream whose board was unplugged closes after the thread has ended.
	// That must be an answer, not a panic.
	err := thread.do(func() error {
		t.Error("a call after stop must not run")
		return nil
	})
	if !errors.Is(err, pool.ErrGone) {
		t.Fatalf("a call after stop must report the board gone, got %v", err)
	}
}

func TestCOMThreadSurvivesStopRacingWithCalls(t *testing.T) {
	thread := startCOMThread()
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			// Either answer is right; panicking is not.
			_ = thread.do(func() error { return nil })
		})
	}
	thread.stop()
	group.Wait()
}

// TestCOMThreadStopsWhileACallIsRunning: a board is closed from a different
// goroutine than the one asking it things -- a linger timer against a status
// request already on its way to the thread. If stop could be held up by a
// request that a slow driver is holding up, neither would finish, leaving a
// graph running with nobody to stop it, which on a Digital Devices MAX M4 is
// how Windows is made to bugcheck.
func TestCOMThreadStopsWhileACallIsRunning(t *testing.T) {
	thread := startCOMThread()

	// A call that stands in for a driver that is slow to answer.
	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	go func() { _ = thread.do(func() error { close(entered); <-release; return nil }) }()
	<-entered

	// More callers queue up behind it.
	queued := make(chan error, 4)
	for range cap(queued) {
		go func() { queued <- thread.do(func() error { return nil }) }()
	}

	stopped := make(chan struct{})
	go func() { thread.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop waited for a call that had not finished; a board could not be given up")
	}

	// Whoever was waiting is answered rather than left.
	for range cap(queued) {
		select {
		case err := <-queued:
			if err != nil && !errors.Is(err, pool.ErrGone) {
				t.Errorf("a queued call answered %v, want nil or ErrGone", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a queued call was left waiting after stop")
		}
	}
}
