// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !linux && !windows

package injected

import (
	"errors"
	"fmt"
	"os"
)

// isUSBNode refuses a descriptor that is not a device node. Only Linux has
// usbfs, so nothing finer can be said here.
func isUSBNode(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("look at the descriptor handed over: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return errors.New("the descriptor handed over is not a device node")
	}
	return nil
}
