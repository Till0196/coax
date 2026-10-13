// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"errors"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// TestEqualPrioritiesLeaveEachOtherAlone is the rule the daemon had before
// priorities existed, and the one every client that says nothing still gets:
// whoever asked first keeps the tuner.
func TestEqualPrioritiesLeaveEachOtherAlone(t *testing.T) {
	for _, priority := range []int{0, 7, -3} {
		pool, _, _ := newTestPool(t, Options{})
		first, err := pool.Lease(Want{
			TunerID: "px4:ABC123@1-2:2", Owner: pool.NewOwner(), Priority: priority,
		})
		if err != nil {
			t.Fatalf("take the tuner at %d: %v", priority, err)
		}
		_, err = pool.Lease(Want{
			TunerID: "px4:ABC123@1-2:2", Owner: pool.NewOwner(), Priority: priority,
		})
		if !errors.Is(err, device.ErrTunerBusy) {
			t.Errorf("a second request at %d took the tuner: %v", priority, err)
		}
		if first.Preempted() {
			t.Errorf("the holder at %d was reported as preempted", priority)
		}
		first.Release()
	}
}

// TestAHigherPriorityTakesANamedTuner: a recording that outranks a viewer gets
// the tuner it named, and the viewer's lease ends the way an unplugged board
// ends one -- its context closes, so a stream copying to a client stops rather
// than hanging.
func TestAHigherPriorityTakesANamedTuner(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})

	viewer := pool.NewOwner()
	held, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:2", Owner: viewer, Priority: 1})
	if err != nil {
		t.Fatalf("take the tuner: %v", err)
	}

	recorder := pool.NewOwner()
	taken, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:2", Owner: recorder, Priority: 5})
	if err != nil {
		t.Fatalf("outrank the holder: %v", err)
	}
	defer taken.Release()
	if taken.Index() != 2 {
		t.Errorf("took tuner %d, want the one that was named", taken.Index())
	}
	if taken.Priority() != 5 {
		t.Errorf("the new lease is held at %d, want 5", taken.Priority())
	}
	select {
	case <-held.Done():
	default:
		t.Error("the tuner was taken and its old holder was not told")
	}
	if !held.Preempted() {
		t.Error("the old holder cannot tell it was preempted from an ordinary release")
	}
	// The pool no longer counts it against its owner, so the connection
	// closing later gives nothing back twice.
	if got := pool.Held(viewer); got != 0 {
		t.Errorf("the old owner still holds %d things", got)
	}
}

// TestALowerPriorityTakesNothing: outranking is one way round.
func TestALowerPriorityTakesNothing(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})

	held, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:2", Owner: pool.NewOwner(), Priority: 5})
	if err != nil {
		t.Fatalf("take the tuner: %v", err)
	}
	defer held.Release()
	if _, err := pool.Lease(Want{
		TunerID: "px4:ABC123@1-2:2", Owner: pool.NewOwner(), Priority: 1,
	}); !errors.Is(err, device.ErrTunerBusy) {
		t.Errorf("lower-ranked work took the tuner: %v", err)
	}
}

// TestAFreeTunerIsTakenBeforeAnybodyIsStopped is the half that keeps this from
// being destructive: a request that outranks everything still takes the tuner
// nobody wants, and stops nothing.
func TestAFreeTunerIsTakenBeforeAnybodyIsStopped(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})

	held, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: pool.NewOwner(), Priority: 0})
	if err != nil {
		t.Fatalf("take the first tuner: %v", err)
	}
	defer held.Release()

	// The board has two terrestrial tuners; one is free.
	taken, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: pool.NewOwner(), Priority: 9})
	if err != nil {
		t.Fatalf("take the free tuner: %v", err)
	}
	defer taken.Release()
	if taken.Index() == held.Index() {
		t.Error("the free tuner was passed over and a holder stopped instead")
	}
	select {
	case <-held.Done():
		t.Error("a holder was stopped while a tuner stood free")
	default:
	}
}

// TestTheLeastImportantHolderIsTheOneStopped: with every matching tuner in
// use, what stops is the least important work rather than whichever board
// enumerated first.
func TestTheLeastImportantHolderIsTheOneStopped(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})

	// Tuner 2 comes first in the listing and is held by the more important of
	// the two, so taking the first match found would stop the wrong one.
	important, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:2", Owner: pool.NewOwner(), Priority: 4})
	if err != nil {
		t.Fatalf("take tuner 2: %v", err)
	}
	defer important.Release()
	expendable, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:3", Owner: pool.NewOwner(), Priority: 1})
	if err != nil {
		t.Fatalf("take tuner 3: %v", err)
	}

	taken, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: pool.NewOwner(), Priority: 5})
	if err != nil {
		t.Fatalf("outrank a holder: %v", err)
	}
	defer taken.Release()
	if taken.Index() != 3 {
		t.Errorf("took tuner %d, want 3 -- the one the least important work held", taken.Index())
	}
	select {
	case <-important.Done():
		t.Error("the more important holder was the one stopped")
	default:
	}
	select {
	case <-expendable.Done():
	default:
		t.Error("the least important holder was not stopped")
	}
}

// TestNobodyToOutrankIsStillBusy: a request that beats nothing gets the answer
// it always got, rather than an error about priorities.
func TestNobodyToOutrankIsStillBusy(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})

	for _, index := range []int{2, 3} {
		lease, err := pool.Lease(Want{
			TunerID: tunerName(index), Owner: pool.NewOwner(), Priority: 5,
		})
		if err != nil {
			t.Fatalf("take tuner %d: %v", index, err)
		}
		defer lease.Release()
	}
	if _, err := pool.Lease(Want{
		DeliverySystem: "ISDB-T", Owner: pool.NewOwner(), Priority: 5,
	}); !errors.Is(err, ErrBusy) {
		t.Errorf("a request among equals answered %v, want busy", err)
	}
}

// TestADeliverySystemNothingReachesIsStillUnsupported: priority does not turn
// a tuner into one that can receive what it cannot. The answer has to stay
// apart from busy, since one is worth trying again and the other never is.
func TestADeliverySystemNothingReachesIsStillUnsupported(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})
	if _, err := pool.Lease(Want{
		DeliverySystem: "DVB-T2", Owner: pool.NewOwner(), Priority: 99,
	}); !errors.Is(err, ErrNoTuner) {
		t.Errorf("asking at a high priority for a system nothing reaches answered %v", err)
	}
}

// TestReusingATunerCarriesTheLatestPriority: a client keeps its tuner across
// channels by naming it, and the priority it names it at is the one that
// defends it from then on.
func TestReusingATunerCarriesTheLatestPriority(t *testing.T) {
	pool, _, _ := newTestPool(t, Options{})
	owner := pool.NewOwner()

	first, err := pool.Lease(Want{DeliverySystem: "ISDB-T", Owner: owner, Priority: 1})
	if err != nil {
		t.Fatalf("take a tuner: %v", err)
	}
	defer first.Release()

	again, err := pool.Lease(Want{TunerID: first.ID(), Owner: owner, Priority: 6})
	if err != nil {
		t.Fatalf("keep the tuner: %v", err)
	}
	if again != first {
		t.Fatal("asking for the tuner already held handed out a different one")
	}
	if first.Priority() != 6 {
		t.Errorf("the lease is held at %d, want the 6 its latest request said", first.Priority())
	}
	// And it now stands off what would have taken it a moment ago.
	if _, err := pool.Lease(Want{
		TunerID: first.ID(), Owner: pool.NewOwner(), Priority: 5,
	}); !errors.Is(err, device.ErrTunerBusy) {
		t.Errorf("the raised priority did not defend the tuner: %v", err)
	}
}

func tunerName(index int) string {
	return "px4:ABC123@1-2:" + string(rune('0'+index))
}
