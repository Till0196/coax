// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"io"
	"syscall"

	"github.com/Till0196/coax/internal/coaxd/host/userlock"
)

// claim takes a mutex named after the socket (see userlock.Create). A daemon
// already holding it keeps this one from starting.
func claim(path string) (io.Closer, bool, error) {
	handle, existed, err := userlock.Create("coaxd-" + userlock.Key(path))
	if err != nil {
		return nil, false, err
	}
	if existed {
		_ = syscall.CloseHandle(handle)
		return nil, false, nil
	}
	return mutexHandle(handle), true, nil
}

// mutexHandle is the held mutex. Closing it is letting go.
type mutexHandle syscall.Handle

func (handle mutexHandle) Close() error { return syscall.CloseHandle(syscall.Handle(handle)) }
