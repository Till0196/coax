// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestTheLockFileGoesWithTheDaemon: nothing is left beside the socket once the
// claim is given up.
func TestTheLockFileGoesWithTheDaemon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http.sock")
	held, ok, err := claim(path)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := os.Stat(path + lockSuffix); err != nil {
		t.Fatalf("no lock file while held: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(path + lockSuffix); !os.IsNotExist(err) {
		t.Errorf("the lock file was left behind: %v", err)
	}
}

// TestALockOnARemovedFileIsNotTaken is the race removing the file opens: one
// that had the old file open when it was removed gets its lock, and must see
// that the name no longer leads there.
func TestALockOnARemovedFileIsNotTaken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http.sock")
	name := path + lockSuffix
	old, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(old.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if stillNamed(old, name) {
		t.Error("a removed file counted as the lock")
	}
	held, ok, err := claim(path)
	if err != nil || !ok {
		t.Fatalf("a lock on a removed file kept the next daemon out: %v %v", ok, err)
	}
	_ = held.Close()
}
