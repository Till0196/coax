// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRequestsCanLeaveRecoveryIndependently(t *testing.T) {
	repaired := make(chan error)
	defer func() {
		select {
		case <-repaired:
		default:
			close(repaired)
		}
	}()
	pool, registry, _ := newTestPool(t, Options{Settled: NewSettlement(context.Background(), repaired), Settling: []string{"px4"}})
	type pending struct {
		cancel context.CancelFunc
		result chan error
	}
	requests := make([]pending, 2)
	for i := range requests {
		base, cancel := context.WithCancel(context.Background())
		ctx := &observedWait{Context: base, waiting: make(chan struct{})}
		result := make(chan error, 1)
		requests[i] = pending{cancel, result}
		go func() {
			lease, err := pool.LeaseContext(ctx, Want{TunerID: "px4:ABC123@1-2:0", Owner: pool.NewOwner()})
			if lease != nil {
				lease.Release()
			}
			result <- err
		}()
		select {
		case <-ctx.waiting:
		case <-time.After(time.Second):
			t.Fatal("request never began waiting for recovery")
		}
	}
	// Both callers must remain cancellable even when asking for the same slot.
	for _, request := range requests {
		request.cancel()
		select {
		case err := <-request.result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled recovery wait: %v", err)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("cancelled request stayed in the recovery wait")
		}
	}
	if got := registry.openCount(); got != 0 {
		t.Fatalf("opened %d boards before repair finished", got)
	}
	close(repaired)
	// Cancellation did not poison the board or reserve its slot.
	lease, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:0", Owner: pool.NewOwner()})
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestWatchRecoveryWaitUsesRequestDeadline(t *testing.T) {
	repaired := make(chan error)
	defer close(repaired)
	pool, registry, _ := newTestPool(t, Options{Settled: NewSettlement(context.Background(), repaired)})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := pool.WatchContext(ctx, "px4:ABC123@1-2:0"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("watch deadline: %v", err)
	}
	if got := registry.openCount(); got != 0 {
		t.Fatalf("opened %d boards for an abandoned request", got)
	}
}

// observedWait tells the test that the request reached the cancellable wait.
type observedWait struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedWait) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestDaemonCancellationKeepsLateRecoveryClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repaired := make(chan error, 1)
	settled := NewSettlement(ctx, repaired)
	pool, registry, _ := newTestPool(t, Options{Settled: settled})
	cancel()
	if err := settled.Wait(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	repaired <- nil
	if _, err := pool.Lease(Want{Owner: pool.NewOwner()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if registry.openCount() != 0 {
		t.Fatal("opened hardware still held by a cancelled repair")
	}
}

func TestRepairFailureKeepsOnlyItsBackendsClosed(t *testing.T) {
	failure := errors.New("BDA host pipe disconnected")
	repaired := make(chan error, 1)
	repaired <- failure
	settled := NewSettlement(context.Background(), repaired)
	pool, registry, _ := newTestPool(t, Options{Settled: settled, Settling: []string{"tbs"}})
	lease, err := pool.Lease(Want{Owner: pool.NewOwner()})
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if registry.openCount() != 1 {
		t.Fatal("unrelated hardware was blocked")
	}
	affected, opened, _ := newTestPool(t, Options{Settled: settled})
	if _, err := affected.Lease(Want{Owner: affected.NewOwner()}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if opened.openCount() != 0 {
		t.Fatal("opened hardware after losing its host")
	}
}
