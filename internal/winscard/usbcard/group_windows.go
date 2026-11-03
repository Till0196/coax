// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package usbcard

import "github.com/Till0196/coax/internal/coaxd/bda"

func init() { bdaReaderGroup = bda.CardReaderGroup }
