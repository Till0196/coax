// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package device

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFirmwareIsLookedForWhereItWasPut checks the search rather than any one
// location: a directory named by the operator wins, and the rest are tried in
// order without repeating one.
func TestFirmwareIsLookedForWhereItWasPut(t *testing.T) {
	dirs := FirmwareDirs("/somewhere/of/my/own")
	if len(dirs) == 0 || dirs[0] != "/somewhere/of/my/own" {
		t.Fatalf("the configured directory should come first, got %v", dirs)
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if seen[dir] {
			t.Errorf("%q is searched twice", dir)
		}
		seen[dir] = true
	}
	if len(FirmwareDirs("")) == 0 {
		t.Error("there should be somewhere to look even when nothing is configured")
	}
	for _, dir := range FirmwareDirs("") {
		if !filepath.IsAbs(dir) {
			t.Errorf("%q is looked for relative to the working directory", dir)
		}
	}
}

func TestReadFirmwareFindsAndReports(t *testing.T) {
	dir := t.TempDir()
	want := []byte("an image")
	if err := os.WriteFile(filepath.Join(dir, "dvb-usb-id5520se.fw"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFirmware(dir, "dvb-usb-id5520se.fw")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("read %q, want %q", got, want)
	}

	// The message for a missing blob is the point of this: it has to name the
	// file and everywhere that was looked, or an operator has nothing to act
	// on.
	_, err = ReadFirmware(dir, "dvb-demod-si2183-b60-01.fw")
	var missing *FirmwareMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("missing firmware gave %v, want a FirmwareMissingError", err)
	}
	if !strings.Contains(missing.Error(), "dvb-demod-si2183-b60-01.fw") {
		t.Errorf("the message does not name the file: %s", missing)
	}
	if !strings.Contains(missing.Error(), dir) {
		t.Errorf("the message does not say where it looked: %s", missing)
	}
}
