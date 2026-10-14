// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build !linux && !windows && !darwin

package usbio

import "github.com/Till0196/coax/pkg/usbio/backend"

// enumerate reports that this operating system has no direct USB backend.
func enumerate() ([]backend.Found, error) { return nil, backend.ErrUnsupported }
