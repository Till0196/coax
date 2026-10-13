// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import "fmt"

// checkSocketPath refuses a path too long to be a socket before it is used as
// one. A Unix socket address is a fixed-size field in a kernel structure, and
// going over it fails with EINVAL -- "invalid argument", which says nothing
// about paths -- including when starting a daemon, so the program would launch
// one, wait ten seconds, and blame it for not answering. The daemon checks this
// too; here it arrives before the wait rather than after.
func checkSocketPath(path string) error {
	// The trailing NUL is part of the field.
	if len(path)+1 > maxSocketPath {
		return fmt.Errorf("the socket path %s is %d characters, and this system allows %d: "+
			"choose a shorter directory with --host or COAXD_SOCKET_DIR", path, len(path), maxSocketPath-1)
	}
	return nil
}
