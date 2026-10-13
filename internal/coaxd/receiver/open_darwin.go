// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build darwin

package receiver

import (
	"fmt"
	"strings"
)

// Open picks a receiver backend. macOS has no kernel frontend to fall back on,
// so every backend here is a direct USB one.
func Open(options Options) (Device, error) {
	backend := options.Backend
	if backend == "" || backend == "auto" {
		backend = "t230"
	}
	if device, handled, err := openUSB(backend, options); handled {
		return device, err
	}
	return nil, fmt.Errorf("backend %q is not available on macOS; try one of %s", backend, strings.Join(USBBackends(), ", "))
}
