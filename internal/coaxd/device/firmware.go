// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package device

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Where a board's firmware is looked for, and how it is read. The blobs are the
// Linux ones whatever the system -- a chip takes the same image, and the names
// they are distributed under (dvb-usb-id5520se.fw,
// dvb-demod-si2183-b60-01.fw) are the names every driver uses -- so only the
// places to look differ.

// FirmwareDirs is where a blob is looked for, in order.
//
// A directory named on the command line comes first. The rest are the places it
// is reasonable to have put it on each system: where the operating system keeps
// firmware, beside the program itself, which is how a folder-of-files install
// carries its blobs with it, and where this user's own data belongs. A place
// only this user can write comes after the ones an administrator set up.
//
// Nothing is looked for relative to the working directory: a daemon started
// from a directory others can write would load whatever was put there.
func FirmwareDirs(configured string) []string {
	var dirs []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		for _, seen := range dirs {
			if seen == dir {
				return
			}
		}
		dirs = append(dirs, dir)
	}
	beside := func() {
		if executable, err := os.Executable(); err == nil {
			dir := filepath.Dir(executable)
			add(filepath.Join(dir, "firmware"))
			add(dir)
		}
	}

	add(configured)
	switch runtime.GOOS {
	case "linux":
		add("/lib/firmware")
		add("/usr/lib/firmware")
		add("/usr/local/lib/firmware")
		beside()
	case "darwin":
		add("/usr/local/lib/firmware")
		add("/opt/homebrew/lib/firmware")
		beside()
		if home, err := os.UserHomeDir(); err == nil {
			add(filepath.Join(home, "Library", "Application Support", "coaxd", "firmware"))
		}
	case "windows":
		if data := os.Getenv("ProgramData"); data != "" {
			add(filepath.Join(data, "coaxd", "firmware"))
		}
		beside()
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			add(filepath.Join(local, "coaxd", "firmware"))
		}
	default:
		beside()
	}
	return dirs
}

// ReadFirmware reads one blob by the name its vendor distributes it under. The
// point is the message when there is none: "no such file" tells an operator
// nothing, so the error names the file and every place that was looked.
func ReadFirmware(configured, name string) ([]byte, error) {
	dirs := FirmwareDirs(configured)
	for _, dir := range dirs {
		image, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return image, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, name), err)
		}
	}
	return nil, &FirmwareMissingError{Name: name, Dirs: dirs}
}

// FindFirmware is where ReadFirmware would read a blob from, for a loader that
// takes a path rather than the bytes. It looks in the same places in the same
// order, and fails the same way when there is none.
func FindFirmware(configured, name string) (string, error) {
	dirs := FirmwareDirs(configured)
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("look for %s: %w", path, err)
		}
	}
	return "", &FirmwareMissingError{Name: name, Dirs: dirs}
}

// FirmwareMissingError says which blob is missing and where it was looked for.
type FirmwareMissingError struct {
	Name string
	Dirs []string
}

func (e *FirmwareMissingError) Error() string {
	return fmt.Sprintf("this board needs the firmware file %s, which is not in any of: %s"+
		" -- it is the same file the Linux driver for this chip uses, and fwextract"+
		" takes it out of the vendor's own package",
		e.Name, strings.Join(e.Dirs, ", "))
}
