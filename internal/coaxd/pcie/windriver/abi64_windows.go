// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows && !386

package windriver

// The pointer-dependent offsets where a user pointer is eight bytes. See
// abi32_windows.go for the other set.
const (
	// In WD_ITEMS, after the physical address, size, translated address and
	// mapping; the mapping is the pointer.
	itemMemoryBar     = 0x28
	itemMemoryOptions = 0x2c

	// In WD_PCI_CONFIG_DUMP, after the slot and the pointer to the caller's
	// bytes. The slot is twelve bytes and the pointer is aligned to eight, so
	// four bytes of padding sit between them.
	configDumpBuffer = 0x10
	configDumpOffset = 0x18
	configDumpBytes  = 0x1c
	configDumpIsRead = 0x20
	configDumpSize   = 0x28

	// In WD_DMA, after the handle, user address and kernel address.
	dmaUserAddress = 0x08
	dmaBytes       = 0x18
	dmaOptions     = 0x1c
	dmaPagesCount  = 0x20
	dmaCard        = 0x24
	dmaPagesOffset = 0x28

	// In WD_CARD_CLEANUP, after the card and the padding for the pointer.
	cleanupCommands = 0x08
	cleanupCount    = 0x10
	cleanupOptions  = 0x14
	cleanupSize     = 0x18
)
