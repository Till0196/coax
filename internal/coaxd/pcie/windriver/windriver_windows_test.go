// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package windriver

import (
	"errors"
	"testing"
)

// A missing driver must be reported as ErrNotInstalled: that is what every
// machine without such a card looks like. Tests that open a real card live in
// the package that knows which card it is.
func TestAMissingDriverIsReportedAsMissing(t *testing.T) {
	library := Library{
		DLL:     "wdapi1200.dll",
		Driver:  "windrvr1200_NO_SUCH_SERVICE",
		Licence: "",
	}
	session, err := library.Open()
	if err == nil {
		session.Close()
		t.Fatal("a driver that does not exist opened")
	}
	if !errors.Is(err, ErrNotInstalled) {
		t.Errorf("reported %v, which does not say the driver is missing", err)
	}
}

// A library whose version these layouts were not measured against is refused
// before anything is opened, because a field read from the wrong place does not
// fail -- it opens the wrong card.
func TestAnUnmeasuredVersionIsRefused(t *testing.T) {
	library := Library{DLL: "wdapi1510.dll", Driver: "windrvr1510_SOMETHING", Licence: ""}
	session, err := library.Open()
	if err == nil {
		session.Close()
		t.Fatal("a version this program has not been measured against opened")
	}
	if errors.Is(err, ErrNotInstalled) {
		t.Errorf("reported %v, which says the driver is missing rather than unmeasured", err)
	}
}
