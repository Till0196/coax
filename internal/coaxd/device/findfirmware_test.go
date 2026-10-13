// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package device

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A loader that takes a path gets the same file ReadFirmware would read, and
// the same error, naming every place looked, when there is none.
func TestFindFirmwareLooksWhereReadFirmwareDoes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "isdbt_rio.inp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := FindFirmware(dir, "isdbt_rio.inp")
	if err != nil || path != filepath.Join(dir, "isdbt_rio.inp") {
		t.Fatalf("FindFirmware = %q, %v", path, err)
	}
	_, err = FindFirmware(dir, "nothing.inp")
	var missing *FirmwareMissingError
	if !errors.As(err, &missing) || missing.Name != "nothing.inp" || missing.Dirs[0] != dir {
		t.Fatalf("FindFirmware of a missing blob = %v", err)
	}
}
