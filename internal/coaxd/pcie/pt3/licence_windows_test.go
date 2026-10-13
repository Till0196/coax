// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"errors"
	"strings"
	"testing"
)

// A library's bytes, near enough: the two strings amid other content.
func sdkBytes(driver, licence string) []byte {
	return []byte("\x00\x01\x02JanFebMar\x00" + driver + "\x00\x00" + licence +
		"\x00X:\\build\\sdk\\Output\\x64\\Release\\SDK.pdb\x00")
}

func TestTheThreeStringsAreFoundByShape(t *testing.T) {
	const (
		driver  = "windrvr1200_EARTHSOFT_PT3"
		licence = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.WD1200_NL_EARTH_SOFT"
	)
	library, err := libraryIn(sdkBytes(driver, licence))
	if err != nil {
		t.Fatalf("read the library: %v", err)
	}
	if library.Driver != driver {
		t.Errorf("driver %q, want %q", library.Driver, driver)
	}
	if library.Licence != licence {
		t.Errorf("licence %q, want %q", library.Licence, licence)
	}
	// The version in the service name is what names the user-mode library.
	if library.DLL != "wdapi1200.dll" {
		t.Errorf("library %q, want wdapi1200.dll", library.DLL)
	}
}

// Reading these at run time exists so a newer installation is followed.
func TestALaterVersionIsFollowed(t *testing.T) {
	library, err := libraryIn(sdkBytes("windrvr1600_EARTHSOFT_PT3",
		strings.Repeat("ab", 32)+".WD1600_NL_EARTH_SOFT"))
	if err != nil {
		t.Fatalf("read the library: %v", err)
	}
	if library.DLL != "wdapi1600.dll" || library.Driver != "windrvr1600_EARTHSOFT_PT3" {
		t.Errorf("followed to %+v", library)
	}
}

func TestALibraryWithoutThemIsRefused(t *testing.T) {
	for _, one := range []struct {
		name    string
		content []byte
	}{
		{"nothing at all", []byte("no strings here")},
		{"a service name but no licence", sdkBytes("windrvr1200_EARTHSOFT_PT3", "")},
		{"a licence but no service name", []byte(strings.Repeat("cd", 32) + ".WD1200_NL_EARTH_SOFT")},
	} {
		t.Run(one.name, func(t *testing.T) {
			if _, err := libraryIn(one.content); err == nil {
				t.Error("was accepted")
			}
		})
	}
}

// A machine with no SDK has to be told which thing is missing: the driver being
// present makes it look as though it should work.
func TestAMissingSDKSaysSo(t *testing.T) {
	_, err := readLibrary([]string{`Z:\nowhere\SDK_EARTHSOFT_PT3.dll`})
	if err == nil {
		t.Fatal("a library that is not there was read")
	}
	if !errors.Is(err, ErrNoSDK) {
		t.Errorf("reported %v, which does not say the SDK is missing", err)
	}
	if !strings.Contains(err.Error(), sdkLibrary) {
		t.Errorf("did not name the file: %v", err)
	}
}
