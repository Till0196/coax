// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"context"
	"net"
	"syscall"
	"testing"
)

func TestThePortOpensATCPSocket(t *testing.T) {
	config := publicListenConfig()
	listener, err := config.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	raw, err := listener.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var protocol int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		protocol, sockErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_PROTOCOL)
	}); err != nil {
		t.Fatal(err)
	}
	if sockErr != nil {
		t.Fatal(sockErr)
	}
	if protocol != syscall.IPPROTO_TCP {
		t.Fatalf("the port's socket is protocol %d, want TCP (%d)", protocol, syscall.IPPROTO_TCP)
	}
}
