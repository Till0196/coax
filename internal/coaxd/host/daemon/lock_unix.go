// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package daemon

import (
	"io"
	"os"
	"syscall"
)

// lockSuffix names the lock file beside the socket it guards.
const lockSuffix = ".lock"

// claim takes an exclusive lock on a file beside the socket. The kernel drops
// the lock however this process ends.
//
// The file goes away with the daemon. Removing a lock file is safe only in
// this order: whoever holds the lock removes the name before letting go, and
// whoever takes the lock checks afterwards that the name still leads to the
// file it locked. A daemon that was waiting on the file just removed finds the
// name gone or leading elsewhere and starts again on the file there now, rather
// than holding a lock nobody else can see. A daemon that crashed leaves the
// file, which means nothing: it is in the socket directory, which the system
// clears ($XDG_RUNTIME_DIR, $TMPDIR, /run).
//
// It requires the filesystem holding the socket directory to honour flock across
// processes, which a local one does and a network one may not. See
// docs/setup/os.md.
func claim(path string) (io.Closer, bool, error) {
	name := path + lockSuffix
	for {
		file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return nil, false, err
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = file.Close()
			if err == syscall.EWOULDBLOCK {
				return nil, false, nil
			}
			return nil, false, err
		}
		if stillNamed(file, name) {
			return lockFile{file: file, name: name}, true, nil
		}
		_ = file.Close()
	}
}

// stillNamed reports whether the name still leads to the file that was opened
// under it, which it does not once the holder before has removed it.
func stillNamed(file *os.File, name string) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	named, err := os.Lstat(name)
	if err != nil {
		return false
	}
	return os.SameFile(opened, named)
}

// lockFile is the held lock. Closing removes the name while the lock is still
// held, then lets go.
type lockFile struct {
	file *os.File
	name string
}

func (held lockFile) Close() error {
	_ = os.Remove(held.name)
	return held.file.Close()
}
