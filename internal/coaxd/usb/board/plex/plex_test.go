// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plex

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// A tuner that does not want the supply lets go of its claim rather than
// switching the line off, because the line is shared: an installation with its
// own power inserter ends with the board's supply never raised, but one tuner
// asking for none must not cut off another that is using it.
func TestWantSharesTheSupplyRatherThanSwitchingIt(t *testing.T) {
	raised := 0
	frame := &Frame{LNB: device.NewLatch(
		func() error { raised++; return nil },
		func() error { raised--; return nil },
	)}
	first, second := frame.NewLNBClaim(), frame.NewLNBClaim()

	if err := first.Want(true); err != nil {
		t.Fatalf("Want: %v", err)
	}
	if err := second.Want(true); err != nil {
		t.Fatalf("Want: %v", err)
	}
	if raised != 1 {
		t.Fatalf("the supply was switched %d times, want once for the first holder", raised)
	}

	// The second tuner retuning to a channel that wants no supply drops its
	// own claim and leaves the first one's alone.
	if err := second.Want(false); err != nil {
		t.Fatalf("Want: %v", err)
	}
	if raised != 1 {
		t.Errorf("one tuner letting go cut the supply off under the other")
	}

	// Only the last holder letting go drops it.
	if err := first.Want(false); err != nil {
		t.Fatalf("Want: %v", err)
	}
	if raised != 0 {
		t.Errorf("the supply stayed up after the last holder let go")
	}
}

// Asking twice for the same thing is what a retune does, and it must not count
// twice: a claim held is one claim however often it is asked for.
func TestWantDoesNotCountARetuneTwice(t *testing.T) {
	held := 0
	frame := &Frame{LNB: device.NewLatch(
		func() error { held++; return nil },
		func() error { held--; return nil },
	)}
	claim := frame.NewLNBClaim()

	for range 3 {
		if err := claim.Want(true); err != nil {
			t.Fatalf("Want: %v", err)
		}
	}
	if frame.LNB.Held() != 1 {
		t.Errorf("three tunes took %d claims, want one", frame.LNB.Held())
	}
	for range 3 {
		if err := claim.Want(false); err != nil {
			t.Fatalf("Want: %v", err)
		}
	}
	if frame.LNB.Held() != 0 || held != 0 {
		t.Errorf("letting go three times left %d claims and the supply at %d", frame.LNB.Held(), held)
	}
}

// A tuner that never wanted the supply never raises it, which is the whole
// point of honouring the setting.
func TestWantFalseNeverRaisesTheSupply(t *testing.T) {
	raised := false
	frame := &Frame{LNB: device.NewLatch(
		func() error { raised = true; return nil },
		func() error { return nil },
	)}
	claim := frame.NewLNBClaim()
	if err := claim.Want(false); err != nil {
		t.Fatalf("Want: %v", err)
	}
	if raised {
		t.Error("a tuner asking for no supply raised it anyway")
	}
}
