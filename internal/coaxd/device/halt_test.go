// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package device

import "testing"

func TestHaltOnPanicStopsEveryCardAndPanicsOn(t *testing.T) {
	stopped := 0
	forgetFirst := OnHalt(func() { stopped++ })
	forgetSecond := OnHalt(func() { panic("a card that panics in turn") })
	forgetThird := OnHalt(func() { stopped++ })
	defer forgetFirst()
	defer forgetSecond()
	defer forgetThird()

	reason := func() (reason any) {
		defer func() { reason = recover() }()
		func() {
			defer HaltOnPanic()
			panic("wanted")
		}()
		return nil
	}()
	if reason != "wanted" {
		t.Errorf("the panic came out as %v, want the one that was raised", reason)
	}
	if stopped != 2 {
		t.Errorf("%d cards were stopped, want both that did not panic", stopped)
	}
}

func TestHaltOnPanicDoesNothingWithoutAPanic(t *testing.T) {
	stopped := false
	forget := OnHalt(func() { stopped = true })
	func() {
		defer HaltOnPanic()
	}()
	forget()
	Halt()
	if stopped {
		t.Error("a card was stopped with nothing panicking, or after it was forgotten")
	}
}
