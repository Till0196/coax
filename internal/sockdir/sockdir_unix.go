// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package sockdir

import (
	"fmt"
	"os"
	"syscall"
)

// Prepare makes the directory, or takes the one already there, and refuses it
// unless only this user or root can change what is in it. A private directory
// is closed to everybody else too, and one of this user's that is not is
// closed.
func Prepare(dir string, private bool) error {
	mode := os.FileMode(0o755)
	if private {
		mode = 0o700
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return fmt.Errorf("make the socket directory: %w", err)
	}
	if err := Check(dir); err != nil {
		return err
	}
	if !private {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		if owner(info) != os.Getuid() {
			return fmt.Errorf("the socket directory %s is open to other users and is not this user's to close", dir)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("close the socket directory %s to other users: %w", dir, err)
		}
	}
	return nil
}

// Check refuses a directory somebody other than this user or root could have
// put things in: one they own, one they may write to, or a symbolic link they
// made. A directory that is not there yet is reported as such.
func Check(dir string) error {
	link, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if link.Mode()&os.ModeSymlink != 0 {
		if err := trusted(dir, link); err != nil {
			return err
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("the socket directory %s is not a directory", dir)
	}
	if err := trusted(dir, info); err != nil {
		return err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("the socket directory %s can be written by other users (%v); "+
			"remove it or choose another with COAXD_SOCKET_DIR", dir, info.Mode().Perm())
	}
	return nil
}

func trusted(dir string, info os.FileInfo) error {
	if uid := owner(info); uid != os.Getuid() && uid != 0 {
		return fmt.Errorf("the socket directory %s belongs to another user (uid %d); "+
			"remove it or choose another with COAXD_SOCKET_DIR", dir, uid)
	}
	return nil
}

func owner(info os.FileInfo) int {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid)
	}
	return -1
}
