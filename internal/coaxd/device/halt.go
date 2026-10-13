// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package device

import "sync"

// A card that transfers into this process's memory on its own keeps doing so
// after the process has gone, into pages the system has handed to someone else.
// Closing it tidily is what stops it, and a panic closes nothing. So every such
// card registers here how to stop it in a hurry, and the places that reach the
// hardware stop all of them before letting a panic take the process down.

var (
	haltMutex sync.Mutex
	halts     = map[int]func(){}
	haltNext  int
)

// OnHalt registers how to stop one card in a hurry: with no lock taken and
// nothing waited for, since whoever panicked may hold anything. The returned
// function takes the registration back, for when the card is closed.
func OnHalt(stop func()) (forget func()) {
	haltMutex.Lock()
	defer haltMutex.Unlock()
	key := haltNext
	haltNext++
	halts[key] = stop
	return func() {
		haltMutex.Lock()
		defer haltMutex.Unlock()
		delete(halts, key)
	}
}

// Halt stops every registered card. One that panics in turn does not keep the
// rest running.
func Halt() {
	haltMutex.Lock()
	stops := make([]func(), 0, len(halts))
	for _, stop := range halts {
		stops = append(stops, stop)
	}
	haltMutex.Unlock()
	for _, stop := range stops {
		func() {
			defer func() { _ = recover() }()
			stop()
		}()
	}
}

// HaltOnPanic is deferred where a goroutine reaches the hardware. A panic going
// past it stops every card first and then carries on: nothing is turned into an
// error, because whatever panicked may have left a lock held or a structure half
// written, and a process going on from there is worse than one that ends.
func HaltOnPanic() {
	if reason := recover(); reason != nil {
		Halt()
		panic(reason)
	}
}
