// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package sigmask

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
)

// TestHoldBlocksOnlyDuringTheCall checks that the call sees SIGURG blocked on
// its thread and the synchronous signals not, and that the thread has its own
// mask back afterwards.
func TestHoldBlocksOnlyDuringTheCall(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	current := func() uint64 {
		mask, err := Current()
		if err != nil {
			t.Fatalf("read the signal mask: %v", err)
		}
		return mask
	}
	before := current()
	if before&Bit(syscall.SIGURG) != 0 {
		t.Fatalf("SIGURG is already blocked before the call (mask %#x)", before)
	}
	failure := errors.New("the call's own error")
	err := Hold(func() error {
		during := current()
		if during&Bit(syscall.SIGURG) == 0 {
			t.Errorf("SIGURG is not blocked during the call (mask %#x)", during)
		}
		if during&Bit(syscall.SIGSEGV) != 0 {
			t.Errorf("SIGSEGV is blocked during the call (mask %#x)", during)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Errorf("the call's error was not passed on: %v", err)
	}
	if after := current(); after != before {
		t.Errorf("the mask was not put back: %#x before, %#x after", before, after)
	}
}
