// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"runtime"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

// comThread is a goroutine pinned to one OS thread for the life of one board,
// and the only place that board's COM calls are made from.
//
// A thread touching COM must have initialised it, and a goroutine may move
// threads at any suspension point. The pool opens a board on one goroutine and
// closes it from another, and without this the CoUninitialize would land on a
// thread that never initialised, leaving the apartment alive and the next open
// answered with "resource in use".
//
// Stopping closes a channel of its own rather than the calls channel, which
// several goroutines send to. Guarding that with a lock held across the send
// left a Digital Devices MAX M4 with a graph running and no daemon: a status
// request sat in the send behind a slow tuning call, and the board's Close
// waited on the same lock for ever.
type comThread struct {
	calls   chan func()
	done    chan struct{}
	stopped sync.Once
}

func startCOMThread() *comThread {
	thread := &comThread{calls: make(chan func()), done: make(chan struct{})}
	go func() {
		// Never released: when the goroutine ends Go destroys the thread rather
		// than handing it back with COM state on it.
		runtime.LockOSThread()
		// A panic in a call takes the process down, and a card left
		// transferring would outlive it. See device.HaltOnPanic.
		defer device.HaltOnPanic()
		for {
			select {
			case call := <-thread.calls:
				call()
			case <-thread.done:
				return
			}
		}
	}()
	return thread
}

// do runs one call on the thread and waits for it. A call after stop is answered
// with ErrGone: an unplugged board's stream can outlive it, and its Close then
// arrives after the thread has ended. The wait watches done too, so a call
// handed over just as the thread stopped does not wait for a reply that is not
// coming.
func (c *comThread) do(call func() error) error {
	result := make(chan error, 1)
	select {
	case c.calls <- func() { result <- call() }:
	case <-c.done:
		return pool.ErrGone
	}
	select {
	case err := <-result:
		return err
	case <-c.done:
		return pool.ErrGone
	}
}

// stop ends the thread once the running call has finished. It never waits, so
// the caller can get on with giving the rest of the board up; stopping twice is
// harmless.
func (c *comThread) stop() {
	c.stopped.Do(func() { close(c.done) })
}
