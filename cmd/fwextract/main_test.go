// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// packageCarrying writes a zip holding one plain firmware file -- the shape a
// vendor ships when it does not hide the image inside a driver.
func packageCarrying(t *testing.T, directory, name, firmware string) string {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create(firmware)
	if err != nil {
		t.Fatalf("create %s in the archive: %v", firmware, err)
	}
	if _, err := file.Write(bytes.Repeat([]byte{0x5a}, 2048)); err != nil {
		t.Fatalf("write %s: %v", firmware, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close the archive: %v", err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, archive.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// A package given as a full path has its image written beside it, not into the
// working directory.
func TestTheImageLandsBesideThePackage(t *testing.T) {
	held := t.TempDir()
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	input := packageCarrying(t, held, "vendor.zip", "dvb-usb-example-01.fw")
	var stdout, stderr bytes.Buffer
	if err := run([]string{input}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (%s)", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(held, "dvb-usb-example-01.fw")); err != nil {
		t.Errorf("the image is not beside the package it came out of: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "dvb-usb-example-01.fw")); err == nil {
		t.Errorf("the image was written into the working directory")
	}
}

// --output still says where, and still wins.
func TestOutputSaysWhere(t *testing.T) {
	held := t.TempDir()
	input := packageCarrying(t, held, "vendor.zip", "dvb-usb-example-01.fw")
	target := filepath.Join(t.TempDir(), "named.bin")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--output", target, input}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (%s)", err, stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("--output was not honoured: %v", err)
	}
}

// Several packages are all taken, and one that carries nothing does not stop
// the others.
func TestSeveralPackagesAreAllTaken(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	empty := t.TempDir()

	inputs := []string{
		packageCarrying(t, first, "one.zip", "dvb-usb-one-01.fw"),
		filepath.Join(empty, "nothing.zip"),
		packageCarrying(t, second, "two.zip", "dvb-usb-two-01.fw"),
	}
	if err := os.WriteFile(inputs[1], []byte("not an archive"), 0o644); err != nil {
		t.Fatalf("write the empty package: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run(inputs, &stdout, &stderr)
	if err == nil {
		t.Fatal("a package that carried nothing should be reported")
	}
	if !strings.Contains(err.Error(), "1 of 3") {
		t.Errorf("the summary should say how many failed, got %q", err)
	}
	for directory, name := range map[string]string{first: "dvb-usb-one-01.fw", second: "dvb-usb-two-01.fw"} {
		if _, statErr := os.Stat(filepath.Join(directory, name)); statErr != nil {
			t.Errorf("%s was not taken although another package failed: %v", name, statErr)
		}
	}
	// The failing package is named, so several at once can be told apart.
	if !strings.Contains(stderr.String(), "nothing.zip") {
		t.Errorf("the failure does not name the package: %q", stderr.String())
	}
}

// One --output file cannot take several packages.
func TestOneOutputFileWillNotTakeSeveralPackages(t *testing.T) {
	held := t.TempDir()
	inputs := []string{
		packageCarrying(t, held, "one.zip", "dvb-usb-one-01.fw"),
		packageCarrying(t, held, "two.zip", "dvb-usb-two-01.fw"),
	}
	var stdout, stderr bytes.Buffer
	err := run(append([]string{"--output", filepath.Join(held, "named.bin")}, inputs...), &stdout, &stderr)
	if err == nil {
		t.Fatal("two packages into one file should be refused")
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Errorf("the refusal should say what to do instead, got %q", err)
	}
}

// --all with several packages puts each one's images beside it.
func TestAllTakesEveryPackageToItsOwnPlace(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	inputs := []string{
		packageCarrying(t, first, "one.zip", "dvb-usb-one-01.fw"),
		packageCarrying(t, second, "two.zip", "dvb-usb-two-01.fw"),
	}
	var stdout, stderr bytes.Buffer
	if err := run(append([]string{"--all"}, inputs...), &stdout, &stderr); err != nil {
		t.Fatalf("run: %v (%s)", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(first, "dvb-usb-one-01.fw")); err != nil {
		t.Errorf("the first package's image: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "dvb-usb-two-01.fw")); err != nil {
		t.Errorf("the second package's image: %v", err)
	}
}

func TestNoInputIsRefused(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err == nil {
		t.Fatal("no input should be refused")
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("the usage should be shown, got %q", stderr.String())
	}
}
