// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package sockdir

import (
	"fmt"
	"os"
)

// Prepare makes the directory. On Windows it sits under the user's own
// LOCALAPPDATA, whose permissions it inherits and which nobody else can write.
func Prepare(dir string, _ bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("make the socket directory: %w", err)
	}
	return nil
}

// Check reports a directory that is not there; there is nothing else to check
// under a per-user directory.
func Check(dir string) error {
	_, err := os.Stat(dir)
	return err
}
