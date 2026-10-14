// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbio

import (
	"github.com/Till0196/coax/pkg/usbio/backend"
	"github.com/Till0196/coax/pkg/usbio/winusb"
)

func enumerate() ([]backend.Found, error) { return winusb.Enumerate() }
