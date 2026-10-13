// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

package iokit

import (
	"errors"
	"strings"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

func TestLocationPortPath(t *testing.T) {
	for location, want := range map[uint32]string{
		0x00000000: "",
		0x14000000: "20",
		0x14200000: "20-2",
		0x14240000: "20-2.4",
		0x14243000: "20-2.4.3",
		0x01100000: "1-1",
	} {
		if got := locationPortPath(location); got != want {
			t.Errorf("locationPortPath(%#08x) = %q, want %q", location, got, want)
		}
	}
}

// TestIOKitErrorCodes checks that the IOKit return codes a transfer can fail
// with are mapped onto the backend's own errors.
func TestIOKitErrorCodes(t *testing.T) {
	for _, want := range []struct {
		code   uint32
		is     error
		reason string
	}{
		{kIOReturnNoDevice, backend.ErrGone, "no device"},
		{kIOReturnNotAttached, backend.ErrGone, "not attached"},
		{kIOReturnUnsupported, backend.ErrUnsupported, "unsupported"},
		{kIOReturnTimeout, backend.ErrTimeout, "timed out"},
		{kIOUSBTransactionTimeout, backend.ErrTimeout, "the USB transaction timed out"},
	} {
		if err := ioKitError("test", want.code); !errors.Is(err, want.is) {
			t.Errorf("%#08x (%s) gave %v, want %v", want.code, want.reason, err, want.is)
		}
	}
	// A code with no meaning here keeps its number, so an unrecognised failure
	// can still be looked up.
	if err := ioKitError("test", 0xe0004063); errors.Is(err, backend.ErrTimeout) ||
		!strings.Contains(err.Error(), "0xe0004063") {
		t.Errorf("an unmapped code gave %v", err)
	}
}
