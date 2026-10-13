// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
)

// Where the driver's name and licence come from.
//
// The card's kernel driver is a renamed copy of WinDriver and will not answer a
// caller that cannot name it and produce the licence it was built with. Both
// are carried in plain text inside the library Earthsoft ships beside the
// driver, and are read from there at run time rather than written down here.
//
// One library serves both cards and names a driver service for each, so unlike
// the PT3's there is more than one name in the file and nothing says which is
// installed.
const sdkLibrary = "SDK_EARTHSOFT_PT1_PT2.dll"

// What the two strings look like. Matched by shape rather than by offset,
// because an offset is a fact about one build. The driver name carries the
// WinDriver version, which is also how the user-mode library is named, so all
// three follow from the one file.
var (
	driverPattern  = regexp.MustCompile(`windrvr([0-9]+)_[A-Za-z0-9_]+`)
	licencePattern = regexp.MustCompile(`[0-9a-f]{64}\.WD[0-9]+_[A-Z0-9_]+`)
)

// ErrNoSDK reports that the Earthsoft library is not installed. The driver
// alone is not enough: it is the library that says how to address the driver.
var ErrNoSDK = errors.New("the Earthsoft PT1/PT2 SDK is not installed")

var (
	libraryOnce   sync.Once
	libraryFound  []windriver.Library
	libraryFailed error
)

// libraries reports the ways there are to reach a card's driver, reading the
// installed library once.
func libraries() ([]windriver.Library, error) {
	libraryOnce.Do(func() {
		libraryFound, libraryFailed = readLibrary(sdkPaths())
	})
	return libraryFound, libraryFailed
}

// libraryFor picks the driver that serves one model. Earthsoft's library names
// a service per card, and a driver only answers for the card it is bound to, so
// which one to open follows from which card is at the slot. Each driver is
// reached through its own loaded copy of the library, so a machine with both
// cards has both open at once.
func libraryFor(model Model) (windriver.Library, error) {
	found, err := libraries()
	if err != nil {
		return windriver.Library{}, err
	}
	suffix := "_" + strings.ToUpper(model.String())
	for _, one := range found {
		if strings.HasSuffix(one.Driver, suffix) {
			return one, nil
		}
	}
	return windriver.Library{}, fmt.Errorf("%w: it names no driver for the %s (it names %s)",
		ErrNoSDK, model, describeLibraries(found))
}

// sessionFor opens the driver that serves one model.
func sessionFor(model Model) (*windriver.Session, error) {
	one, err := libraryFor(model)
	if err != nil {
		return nil, err
	}
	return one.Open()
}

// anySession opens whichever driver will open, for work that is the same
// whoever answers it: asking the bus what is on it does not depend on which
// card's driver is asked.
func anySession() (*windriver.Session, error) {
	found, err := libraries()
	if err != nil {
		return nil, err
	}
	var last error
	for _, one := range found {
		session, err := one.Open()
		if err == nil {
			return session, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("the library names no driver")
	}
	return nil, last
}

// sdkPaths are where the installer puts its library, best first.
func sdkPaths() []string {
	var paths []string
	if root := os.Getenv("SystemRoot"); root != "" {
		paths = append(paths, filepath.Join(root, "System32", sdkLibrary))
	}
	return append(paths, filepath.Join(`C:\Windows\System32`, sdkLibrary))
}

// readLibrary pulls the driver name and licence out of the first library it
// can read, and derives the user-mode library's name from them.
func readLibrary(paths []string) ([]windriver.Library, error) {
	var last error
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			last = err
			continue
		}
		found, err := librariesIn(content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return found, nil
	}
	if last == nil {
		last = errors.New("no path to look in")
	}
	return nil, fmt.Errorf("%w: %s: %w", ErrNoSDK, sdkLibrary, last)
}

// librariesIn finds every driver service a library names and the licence they
// share.
func librariesIn(content []byte) ([]windriver.Library, error) {
	drivers := driverPattern.FindAllSubmatch(content, -1)
	if drivers == nil {
		return nil, errors.New("no WinDriver service name in it")
	}
	licence := licencePattern.Find(content)
	if licence == nil {
		return nil, errors.New("no WinDriver licence in it")
	}
	var found []windriver.Library
	seen := map[string]bool{}
	for _, driver := range drivers {
		name := string(driver[0])
		if seen[name] {
			continue
		}
		seen[name] = true
		// The service is windrvr<version>_<vendor> and the library that talks
		// to it is wdapi<version>.dll.
		found = append(found, windriver.Library{
			DLL:     "wdapi" + string(driver[1]) + ".dll",
			Driver:  name,
			Licence: string(licence),
		})
	}
	return found, nil
}

func describeLibraries(found []windriver.Library) string {
	if len(found) == 0 {
		return "nothing"
	}
	names := make([]string, 0, len(found))
	for _, one := range found {
		names = append(names, one.Driver)
	}
	return strings.TrimSuffix(found[0].DLL, ".dll") + " as " + strings.Join(names, " or ")
}
