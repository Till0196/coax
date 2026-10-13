// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

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

// The card's kernel driver is a renamed copy of WinDriver and will not answer a
// caller that cannot name it and produce the licence it was built with. Both
// are plain text inside the library Earthsoft ships beside the driver, and are
// read from there at run time: writing them down would put a vendor's licence
// token in this repository and pin the program to one version of a library it
// does not own.
const sdkLibrary = "SDK_EARTHSOFT_PT3.dll"

// What the two strings look like. Found by shape rather than by offset, because
// an offset is a fact about one build and a shape is a fact about the format.
// The driver name carries the WinDriver version, which is also how the user-mode
// library is named, so all three follow from the one file.
var (
	driverPattern  = regexp.MustCompile(`windrvr([0-9]+)_[A-Za-z0-9_]+`)
	licencePattern = regexp.MustCompile(`[0-9a-f]{64}\.WD[0-9]+_[A-Z0-9_]+`)
)

// ErrNoSDK reports that the Earthsoft library is not installed. The driver alone
// is not enough: the library is what says how to address the driver.
var ErrNoSDK = errors.New("the Earthsoft PT3 SDK is not installed")

var (
	libraryOnce   sync.Once
	libraryFound  windriver.Library
	libraryFailed error
)

// library reports how to reach the card's driver, reading it once.
func library() (windriver.Library, error) {
	libraryOnce.Do(func() {
		libraryFound, libraryFailed = readLibrary(sdkPaths())
	})
	return libraryFound, libraryFailed
}

// sdkPaths are where the installer puts its library, best first: the system
// directory. Because it is read as a file rather than loaded, the path has to
// be spelled out, and spelled out so a machine whose Windows is not on C still
// works.
func sdkPaths() []string {
	var paths []string
	if root := os.Getenv("SystemRoot"); root != "" {
		paths = append(paths, filepath.Join(root, "System32", sdkLibrary))
	}
	return append(paths, filepath.Join(`C:\Windows\System32`, sdkLibrary))
}

// readLibrary pulls the driver name and licence out of the first library it can
// read, and works out the user-mode library's name from them.
func readLibrary(paths []string) (windriver.Library, error) {
	var last error
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			last = err
			continue
		}
		library, err := libraryIn(content)
		if err != nil {
			return windriver.Library{}, fmt.Errorf("%s: %w", path, err)
		}
		return library, nil
	}
	if last == nil {
		last = errors.New("no path to look in")
	}
	return windriver.Library{}, fmt.Errorf("%w: %s: %w", ErrNoSDK, sdkLibrary, last)
}

// libraryIn finds the three strings in a library's bytes.
func libraryIn(content []byte) (windriver.Library, error) {
	driver := driverPattern.FindSubmatch(content)
	if driver == nil {
		return windriver.Library{}, errors.New("no WinDriver service name in it")
	}
	licence := licencePattern.Find(content)
	if licence == nil {
		return windriver.Library{}, errors.New("no WinDriver licence in it")
	}
	// The service is windrvr<version>_<vendor> and the library that talks to it
	// is wdapi<version>.dll, so the version in the name settles both.
	version := string(driver[1])
	return windriver.Library{
		DLL:     "wdapi" + version + ".dll",
		Driver:  string(driver[0]),
		Licence: string(licence),
	}, nil
}

// describeLibrary is what a log says about the installation.
func describeLibrary(library windriver.Library) string {
	return strings.TrimSuffix(library.DLL, ".dll") + " as " + library.Driver
}
