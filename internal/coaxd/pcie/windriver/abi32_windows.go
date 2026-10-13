// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows && 386

package windriver

// The pointer-dependent offsets where a user pointer is four bytes; each is
// four or eight bytes below its 64-bit twin in abi64_windows.go. Nothing else
// moves: the scan result, card information and page entries hold only 32-bit
// fields, physical addresses and kernel addresses, and both of those are eight
// bytes either way.
const (
	itemMemoryBar     = 0x24
	itemMemoryOptions = 0x28

	// The pointer needs no padding after the slot's twelve bytes here, so the
	// fields after it sit twelve bytes below their 64-bit places rather than
	// eight.
	configDumpBuffer = 0x0c
	configDumpOffset = 0x10
	configDumpBytes  = 0x14
	configDumpIsRead = 0x18
	configDumpSize   = 0x20

	dmaUserAddress = 0x04
	dmaBytes       = 0x10
	dmaOptions     = 0x14
	dmaPagesCount  = 0x18
	dmaCard        = 0x1c
	dmaPagesOffset = 0x20

	cleanupCommands = 0x04
	cleanupCount    = 0x08
	cleanupOptions  = 0x0c
	cleanupSize     = 0x10
)
