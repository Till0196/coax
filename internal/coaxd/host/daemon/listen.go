// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Till0196/coax/internal/sockdir"
)

// TakeClaim takes the lock that says which process is the daemon. It is the
// first thing a daemon does, before touching hardware: one that enumerates
// first has already opened every board, and several starting together fight
// over boards none will keep -- on a four-tuner card, six daemons started at
// once were still at it half a minute later.
//
// The lock is kernel-held rather than a marker file or a reusable process id,
// so a daemon that crashed leaves nothing stale. The socket cannot serve as the
// lock: one left behind by a killed daemon must be removed before binding, so
// two daemons starting together both remove the path and both bind, and the
// second removal takes away the first's live socket.
//
// The directory is checked before the lock is taken in it: see sockdir.
func TakeClaim(path string, private bool) (*Claim, error) {
	if err := checkSocketPath(path); err != nil {
		return nil, err
	}
	if err := sockdir.Prepare(filepath.Dir(path), private); err != nil {
		return nil, err
	}
	taken, held, err := claim(path)
	if err != nil {
		return nil, fmt.Errorf("claim the tuners for %s: %w", path, err)
	}
	if !held {
		return nil, fmt.Errorf("another daemon is already running for %s", path)
	}
	return &Claim{held: taken}, nil
}

// Claim is the daemon's hold on this machine's tuners. What is held differs by
// system (see claim) and is always something the kernel takes back when this
// process ends.
type Claim struct{ held io.Closer }

// Close releases the claim, last of all and after the tuners are given up, so
// the daemon replacing this one does not start while they are held.
func (claim *Claim) Close() error {
	if claim == nil || claim.held == nil {
		return nil
	}
	held := claim.held
	claim.held = nil
	return held.Close()
}

// Listen opens one of the daemon's sockets. Sound only while holding the Claim,
// since anything already at the path is removed -- without the claim that
// removal takes a running daemon's socket away.
func Listen(path string, private bool) (net.Listener, error) {
	if err := checkSocketPath(path); err != nil {
		return nil, err
	}
	if err := sockdir.Prepare(filepath.Dir(path), private); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove the socket left behind at %s: %w", path, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := openToClients(path, private); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

// probeTimeout bounds the wait for an answer on an existing socket; a daemon on
// the same machine answers at once or not at all.
const probeTimeout = time.Second

func Running(path string) bool {
	probe, err := net.DialTimeout("unix", path, probeTimeout)
	if err != nil {
		return false
	}
	_ = probe.Close()
	return true
}
