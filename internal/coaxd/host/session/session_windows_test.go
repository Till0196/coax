// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package session

import (
	"testing"
	"time"
)

func TestSessionEndWaitsForCleanupAndIgnoresCancellation(t *testing.T) {
	stopped := make(chan struct{})
	finish, err := Watch(func() { close(stopped) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	var handle uintptr
	windows.Range(func(key, value any) bool { handle = key.(uintptr); return false })
	if handle == 0 {
		t.Fatal("session window missing")
	}
	send := user32.NewProc("SendMessageTimeoutW")
	// A query grants permission, but shutdown can still be cancelled.
	if ok, _, err := send.Call(handle, wmQueryEndSession, 0, 0, 2, 3000, 0); ok == 0 {
		t.Fatal(err)
	}
	if ok, _, err := send.Call(handle, wmEndSession, 0, 0, 2, 3000, 0); ok == 0 {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("cancelled session stopped the process")
	default:
	}
	replied := make(chan uintptr, 1)
	go func() { ok, _, _ := send.Call(handle, wmEndSession, 1, 0, 2, 3000, 0); replied <- ok }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("session end did not request shutdown")
	}
	select {
	case <-replied:
		t.Fatal("Windows was answered before cleanup finished")
	default:
	}
	finish()
	select {
	case ok := <-replied:
		if ok == 0 {
			t.Fatal("session response timed out")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not release the session response")
	}
}
