// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package native

import (
	"syscall"
	"testing"
)

// Every call must land on the one thread the worker pinned, whatever goroutine
// it was sent from. COM cannot be asked here, but the thread identity can, and
// it is the same property.
func TestCOMThreadKeepsEveryCallOnOneThread(t *testing.T) {
	thread := startCOMThread()
	defer thread.stop()

	var first int
	if err := thread.do(func() error { first = syscall.Gettid(); return nil }); err != nil {
		t.Fatal(err)
	}
	for range 32 {
		result := make(chan int, 1)
		go func() {
			_ = thread.do(func() error { result <- syscall.Gettid(); return nil })
		}()
		if tid := <-result; tid != first {
			t.Fatalf("call ran on thread %d, the board's thread is %d", tid, first)
		}
	}
}
