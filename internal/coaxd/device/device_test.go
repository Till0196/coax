// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package device

import (
	"strings"
	"sync"
	"testing"
)

func TestOwnerReleasesOnlyAfterTheLastHolder(t *testing.T) {
	t.Parallel()
	released := 0
	owner := NewOwner(func() error { released++; return nil })
	if err := owner.Retain(); err != nil { // a tuner taken from the board
		t.Fatal(err)
	}
	if err := owner.Release(); err != nil { // the board handle itself
		t.Fatal(err)
	}
	if released != 0 {
		t.Fatal("released the USB device while a tuner was still open")
	}
	if err := owner.Release(); err != nil { // the tuner
		t.Fatal(err)
	}
	if released != 1 {
		t.Fatalf("released = %d; want 1", released)
	}
	// Closing twice must not release twice.
	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
	if released != 1 {
		t.Fatalf("released = %d after a double close; want 1", released)
	}
	if err := owner.Retain(); err == nil {
		t.Fatal("retained a released device")
	}
}

func TestOwnerIsConcurrent(t *testing.T) {
	t.Parallel()
	released := 0
	owner := NewOwner(func() error { released++; return nil })
	const holders = 32
	for index := 0; index < holders; index++ {
		if err := owner.Retain(); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	for index := 0; index < holders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = owner.Release()
		}()
	}
	wait.Wait()
	if released != 0 {
		t.Fatal("released while the board handle was still held")
	}
	_ = owner.Release()
	if released != 1 {
		t.Fatalf("released = %d; want 1", released)
	}
}

func TestLatchTurnsOnForTheFirstAndOffForTheLast(t *testing.T) {
	t.Parallel()
	on, off := 0, 0
	latch := NewLatch(func() error { on++; return nil }, func() error { off++; return nil })
	for index := 0; index < 3; index++ {
		if err := latch.Acquire(); err != nil {
			t.Fatal(err)
		}
	}
	if on != 1 || latch.Held() != 3 {
		t.Fatalf("on=%d held=%d; want 1, 3", on, latch.Held())
	}
	for index := 0; index < 2; index++ {
		if err := latch.Release(); err != nil {
			t.Fatal(err)
		}
	}
	if off != 0 {
		t.Fatal("turned off while holders remained")
	}
	if err := latch.Release(); err != nil {
		t.Fatal(err)
	}
	if off != 1 || latch.Held() != 0 {
		t.Fatalf("off=%d held=%d; want 1, 0", off, latch.Held())
	}
	// An extra release is harmless.
	if err := latch.Release(); err != nil || off != 1 {
		t.Fatalf("Release past zero: err=%v off=%d", err, off)
	}
}

func TestTunerSupports(t *testing.T) {
	t.Parallel()
	normalize := func(value string) string {
		return strings.ReplaceAll(strings.ToUpper(value), "-", "")
	}
	tuner := TunerInfo{Deliveries: []string{"ISDB-T"}}
	if !tuner.Supports("ISDBT", normalize) {
		t.Error("Supports rejected a delivery system differing only in punctuation")
	}
	if tuner.Supports("ISDB-S", normalize) {
		t.Error("Supports accepted a delivery system this tuner cannot reach")
	}
}

func TestCheckTuner(t *testing.T) {
	t.Parallel()
	tuners := []TunerInfo{{Index: 0}, {Index: 1}}
	if err := CheckTuner(1, tuners); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{-1, 2} {
		if err := CheckTuner(index, tuners); err == nil {
			t.Errorf("CheckTuner(%d) accepted an out-of-range tuner", index)
		}
	}
}
