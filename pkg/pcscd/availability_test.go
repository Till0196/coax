// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pcscd

import (
	"errors"
	"testing"
)

type availabilitySlot struct{ failed, present bool }

func (s *availabilitySlot) Readers() ([]string, error) { return []string{"test reader"}, nil }
func (s *availabilitySlot) Open(string) (Slot, error)  { return s, nil }
func (s *availabilitySlot) Present() (bool, error) {
	if s.failed {
		return false, errors.New("reader unavailable")
	}
	return s.present, nil
}
func (s *availabilitySlot) ATR() []byte  { return []byte{0x3b, 0} }
func (s *availabilitySlot) Sharing() int { return 0 }
func (s *availabilitySlot) Close() error { return nil }

func TestReaderFailureIsUnknownAndRecoveryToEmptyIsAnEvent(t *testing.T) {
	slot := &availabilitySlot{present: true}
	table := NewTable(slot, 0, false)
	defer table.Close()
	table.sweep()
	if table.States()[0].state&statePresent == 0 {
		t.Fatal("initial card was not present")
	}
	slot.failed = true
	if !table.sweep() {
		t.Fatal("reader failure was not an event")
	}
	failed := table.States()[0]
	if failed.state != stateUnknown || len(failed.atr) != 0 {
		t.Fatalf("failed reader still looked usable: %+v", failed)
	}
	slot.failed = false
	slot.present = false
	if !table.sweep() {
		t.Fatal("recovery to an empty reader was not an event")
	}
	if table.States()[0].state != stateAbsent {
		t.Fatal("reader did not recover")
	}
}
