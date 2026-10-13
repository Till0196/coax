// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !linux && !windows && !darwin

package receiver

import (
	"fmt"
	"strings"
)

// Open reports that this operating system has no receiver backend.
//
// There is no kernel frontend to fall back on here, and no direct USB backend:
// Linux drives usbfs, Windows and macOS drive libusb. The named backends still
// resolve, so the error says which ones exist rather than only that the name
// was wrong.
func Open(options Options) (Device, error) {
	backend := options.Backend
	if backend == "" || backend == "auto" {
		return nil, fmt.Errorf("no receiver backend is available on this operating system; the USB backends (%s) build only for Linux, Windows and macOS", strings.Join(USBBackends(), ", "))
	}
	return nil, fmt.Errorf("backend %q is not available on this operating system; the USB backends build only for Linux, Windows and macOS", backend)
}
