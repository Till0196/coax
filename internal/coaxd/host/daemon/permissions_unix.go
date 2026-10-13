// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package daemon

import (
	"fmt"
	"os"
)

// openToClients gives a socket the permissions its clients need. In a private
// directory the directory is the boundary and none are needed; in a shared one
// they are, because a daemon started by root serves programs run by others.
func openToClients(path string, private bool) error {
	mode := os.FileMode(0o666)
	if private {
		mode = 0o600
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set the permissions on %s: %w", path, err)
	}
	return nil
}

// closeToOthers leaves a socket to this user alone.
func closeToOthers(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set the permissions on %s: %w", path, err)
	}
	return nil
}
