// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"sync/atomic"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

type LifetimeHost struct{ calls atomic.Int64 }

func (h *LifetimeHost) Close(_ int, _ *struct{}) error       { h.calls.Add(1); return nil }
func (h *LifetimeHost) CloseStream(_ int, _ *struct{}) error { h.calls.Add(1); return nil }
func (h *LifetimeHost) Read(_ HostRead, out *HostData) error {
	h.calls.Add(1)
	out.Data = []byte{1}
	return nil
}

func lifetimeClient(t *testing.T, h *LifetimeHost) *rpc.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("Host", h); err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	go server.ServeConn(left)
	client := rpc.NewClient(right)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestLostHostHandlesCannotReachReplacement(t *testing.T) {
	old := lifetimeClient(t, &LifetimeHost{})
	tuner := &hostedDevice{client: old, id: 1}
	stream := &hostedStream{client: old, id: 2}
	// Closing the RPC connection models EOF when the child exits. A replacement
	// can reuse the same IDs, but old reads and cleanup must never reach it.
	old.Close()
	replacement := &LifetimeHost{}
	next := lifetimeClient(t, replacement)
	host.mutex.Lock()
	previous := host.client
	host.client = next
	host.mutex.Unlock()
	t.Cleanup(func() { host.mutex.Lock(); host.client = previous; host.mutex.Unlock() })
	for _, call := range []func() error{
		tuner.Close, stream.Close,
		func() error { _, err := stream.Read(context.Background(), make([]byte, 16)); return err },
	} {
		if err := call(); !errors.Is(err, pool.ErrGone) {
			t.Fatalf("lost host: %v", err)
		}
	}
	if replacement.calls.Load() != 0 {
		t.Fatal("old handle reached replacement host")
	}
	if err := hostCall(next, "Close", 1, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if replacement.calls.Load() != 1 {
		t.Fatal("replacement cannot serve its own handles")
	}
}
