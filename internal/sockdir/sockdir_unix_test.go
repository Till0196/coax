// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !windows

package sockdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareMakesAPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "coaxd")
	if err := Prepare(dir, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("made it %v, want 0700", perm)
	}
}

// TestPrepareClosesItsOwn: a directory of this user's left open is closed
// rather than refused.
func TestPrepareClosesItsOwn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "coaxd")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(dir, true); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("left it %v, want 0700", perm)
	}
}

func TestCheckRefusesWhatOthersCanWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "coaxd")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir); err == nil {
		t.Error("took a directory anybody can write to")
	}
	if err := Prepare(dir, false); err == nil {
		t.Error("prepared a directory anybody can write to")
	}
}

func TestCheckFollowsItsOwnLink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "coaxd")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Check(link); err != nil {
		t.Errorf("refused this user's own link: %v", err)
	}
}

func TestCheckSaysWhenMissing(t *testing.T) {
	if err := Check(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("answered %v, want not-exist", err)
	}
}
