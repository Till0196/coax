// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestHostPipeDeliversStateAndCancellation(t *testing.T) {
	key := fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	state := newHostState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := serveHostPipe(ctx, key, state); err != nil {
		t.Fatal(err)
	}
	name, _ := syscall.UTF16PtrFromString(hostPipe(key))
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(h), hostPipe(key))
	defer file.Close()
	lines := bufio.NewScanner(file)
	for _, want := range []string{"starting", "recovering", "ready", "closing", "closed"} {
		state.set(want)
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("got %q, %v, want %s", lines.Text(), lines.Err(), want)
		}
	}
	event, err := eventHandle()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(event)
	wait, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- waitHandle(wait, event) }()
	stop()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("kernel event wait ignored cancellation")
	}
}

func TestEarlierHostWaitRemainsCancellableUntilProcessExit(t *testing.T) {
	previous := socketKey
	defer func() { socketKey = previous }()
	for _, stage := range []string{"recovering", "closed"} {
		t.Run(stage, func(t *testing.T) {
			socketKey = fmt.Sprintf("wait-test-%d-%d", os.Getpid(), time.Now().UnixNano())
			state := newHostState()
			state.set(stage)
			server, stop := context.WithCancel(context.Background())
			defer stop()
			if err := serveHostPipe(server, socketKey, state); err != nil {
				t.Fatal(err)
			}
			// This process owns the pipe and stays alive even after "closed".
			// Neither a blocked read nor EOF may bypass the request's deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if err := waitForEarlierHost(ctx); err != context.DeadlineExceeded {
				t.Fatalf("earlier host wait: %v", err)
			}
		})
	}
}
