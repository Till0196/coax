// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package native

import (
	"errors"
	"io"
)

// HostCommand is the argument the daemon starts its BDA host with.
const HostCommand = "bda-host"

// ServeHost is the BDA host, which only Windows has.
func ServeHost(io.Reader, io.WriteCloser, []string) error {
	return errors.New("the BDA host runs on Windows only")
}

// SetSocket does nothing here: only Windows has a BDA host to name.
func SetSocket(string) {}

// ReleaseHost has no child process to release here.
func ReleaseHost() {}
