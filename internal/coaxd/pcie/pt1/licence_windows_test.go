// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"errors"
	"path/filepath"
	"testing"
)

// The two strings are matched by shape: a service name carrying the WinDriver
// version, and a licence that is a hash, a dot and a token. This library names
// two services, one per card, so both must come back with the licence they
// share.
func TestBothServicesAreFoundByShape(t *testing.T) {
	content := []byte("\x00\x00some other text\x00" +
		"windrvr1200_EARTHSOFT_PT2\x00" +
		"windrvr1200_EARTHSOFT_PT1\x00" +
		"\x00\x00" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.WD1200_NL_EARTH_SOFT\x00" +
		"trailing rubbish")
	found, err := librariesIn(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d services, want 2: %+v", len(found), found)
	}
	for index, want := range []string{"windrvr1200_EARTHSOFT_PT2", "windrvr1200_EARTHSOFT_PT1"} {
		if found[index].Driver != want {
			t.Errorf("service %d is %q, want %q", index, found[index].Driver, want)
		}
		if found[index].DLL != "wdapi1200.dll" {
			t.Errorf("service %d talks through %q, want wdapi1200.dll", index, found[index].DLL)
		}
		if found[index].Licence != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.WD1200_NL_EARTH_SOFT" {
			t.Errorf("service %d carries the licence %q", index, found[index].Licence)
		}
	}
}

// The same service named twice is one service: a library mentions its own name
// in more than one place.
func TestARepeatedServiceIsNamedOnce(t *testing.T) {
	content := []byte("windrvr6_EARTHSOFT_PT1 windrvr6_EARTHSOFT_PT1 " +
		"0000000000000000000000000000000000000000000000000000000000000000.WD600_NL_EARTH_SOFT")
	found, err := librariesIn(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d services, want 1", len(found))
	}
	if found[0].DLL != "wdapi6.dll" {
		t.Errorf("talks through %q, want wdapi6.dll", found[0].DLL)
	}
}

// The version in the service name is where the user-mode library's name comes
// from, so a driver update is followed rather than argued with.
func TestALaterVersionIsFollowed(t *testing.T) {
	content := []byte("windrvr1510_EARTHSOFT_PT2 " +
		"1111111111111111111111111111111111111111111111111111111111111111.WD1510_NL_EARTH_SOFT")
	found, err := librariesIn(content)
	if err != nil {
		t.Fatal(err)
	}
	if found[0].DLL != "wdapi1510.dll" {
		t.Errorf("talks through %q, want wdapi1510.dll", found[0].DLL)
	}
}

// A library with neither string in it is not this vendor's, and saying which
// half is missing is more use than saying it failed.
func TestALibraryWithoutThemIsRefused(t *testing.T) {
	for _, one := range []struct {
		name    string
		content string
		says    string
	}{
		{"nothing at all", "just some bytes", "service name"},
		{"a service name but no licence", "windrvr1200_EARTHSOFT_PT1", "licence"},
		{"a licence but no service name",
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.WD1200_NL_EARTH_SOFT",
			"service name"},
	} {
		t.Run(one.name, func(t *testing.T) {
			_, err := librariesIn([]byte(one.content))
			if err == nil {
				t.Fatal("accepted a library that is not the vendor's")
			}
			if !contains(err.Error(), one.says) {
				t.Errorf("said %q, which does not mention %q", err, one.says)
			}
		})
	}
}

func contains(text, want string) bool {
	for index := 0; index+len(want) <= len(text); index++ {
		if text[index:index+len(want)] == want {
			return true
		}
	}
	return false
}

// A machine without the vendor's library says so as ErrNoSDK, so a listing can
// pass over it rather than fail.
func TestAMissingSDKSaysSo(t *testing.T) {
	_, err := readLibrary([]string{filepath.Join(t.TempDir(), "nothing.dll")})
	if !errors.Is(err, ErrNoSDK) {
		t.Errorf("said %v, want something that is ErrNoSDK", err)
	}
}
