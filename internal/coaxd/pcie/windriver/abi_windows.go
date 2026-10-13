// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package windriver

// Structure layouts WinDriver v12.0.0 expects, for both the x86_64 and the x86
// build. A wrong offset does not fail cleanly: it reads the wrong field and
// opens the wrong card.
//
// Only pointer width moves anything, and only in the places named in
// abi32_windows.go and abi64_windows.go. Physical and kernel addresses stay
// eight bytes wide on both builds.
//
// The driver is x86_64 and x86 only; on Windows for ARM there is nothing to
// open.
const (
	// statusSuccess is WD_STATUS_SUCCESS. Every other value is an error whose
	// text is in status_windows.go.
	statusSuccess = 0
)

// WD_PCI_SCAN_CARDS: what is being looked for, then how many were found, what
// each one is, and where each one sits.
const (
	scanVendorOffset = 0x000
	scanDeviceOffset = 0x004
	scanCountOffset  = 0x008
	scanIDOffset     = 0x00c
	scanCards        = 100
	idSize           = 8
	scanSlotOffset   = scanIDOffset + scanCards*idSize
	// slotSize is a WD_PCI_SLOT: bus, slot and function, and no padding after
	// them -- the structure that follows the array holds nothing wider.
	slotSize = 12
	scanSize = 0x7e0
)

// WD_CARD: how many resources a card has, then the resources. It is the head of
// both structures below, at a different place in each.
const (
	cardItemsFromCard = 0
	cardItemFromCard  = 8
	// cardItemsMax is how many resources a card may declare, which is what
	// WD_CARD holds and what the requests below have room for.
	cardItemsMax = 20
	cardSize     = 0x468
)

// WD_PCI_CARD_INFO: the slot at 0, then the card.
const (
	cardOffset      = 0x10
	cardItemsOffset = cardOffset + cardItemsFromCard
	cardItemOffset  = cardOffset + cardItemFromCard
	cardInfoSize    = cardOffset + cardSize
	// cardInfoRoom oversizes the buffer past WinDriver's own limit of twenty
	// resources. The request still says cardInfoSize, so a card declaring more
	// is a card the driver truncates rather than one that writes past the end.
	cardItemsRoom = 32
	cardInfoRoom  = cardItemOffset + cardItemsRoom*itemSize
)

// WD_CARD_REGISTER: the card to open, then what the driver gives back -- the
// handle it is closed by, and, written into the card's own resources, where
// each of its memory windows now stands in this process.
const (
	registerCardOffset   = 0x000
	registerItemOffset   = registerCardOffset + cardItemFromCard
	registerOptions      = 0x468
	registerHandleOffset = 0x46c
	registerSize         = 0x4f8
)

// Resource kinds a card declares, and the fields of a memory one. Every entry
// starts with its kind; a memory entry carries its physical address and length
// after that, where the driver mapped it further on, and its base address
// register further on again.
const (
	itemInterrupt = 1
	itemMemory    = 2
	itemBus       = 5

	itemSize = 56

	itemMemoryPhysical = 0x08
	itemMemoryBytes    = 0x10
	// itemMemoryTranslated is the address the driver reaches the window by,
	// which is not this process's. itemMemoryUser is the one that is: the
	// window mapped into this process, which is how a register is read.
	itemMemoryTranslated = 0x18
	itemMemoryUser       = 0x20

	// itemDoNotMapKernel is WD_ITEM_MEM_DO_NOT_MAP_KERNEL: the window is mapped
	// into this process and not into the kernel, and itemMemoryTranslated stays
	// zero. See Session.register.
	itemDoNotMapKernel = 1
)

// itemMemoryBar and itemMemoryOptions are in the per-build files: a user
// pointer sits between them and the fields above.

// WD_PCI_CONFIG_DUMP reads and writes configuration space. The bytes travel in
// the caller's own memory rather than in the request, so the request holds a
// pointer to them and everything after that pointer is in the per-build files.
const (
	configDumpSlot = 0x00
)

// PCI configuration space offsets, from the specification: every PCI device
// answers at the same places.
const (
	// configCommand bit 2 is bus mastering: whether the card may start transfers
	// of its own rather than only answer them.
	configCommand   = 0x04
	configBusMaster = 1 << 2
	// configClassRevision is the class code with the revision in its low byte.
	configClassRevision = 0x08
	// configBar0 is the first of the six base address registers. A 64-bit one
	// takes two of them.
	configBar0 = 0x10
	configBars = 6

	// What the low bits of a base address register mean.
	barIsIO     = 1 << 0
	barTypeMask = 0x6
	barType64   = 0x4
	barAddrMask = ^uint32(0xf)
)

// WD_DMA: handle, user address, kernel address, size, options, page count,
// card, then the page entries. On the 64-bit build the handle is followed by
// padding for the pointer after it; on the 32-bit build there is no padding and
// the user address is half as wide, so everything from the size onwards sits
// eight bytes lower.
const (
	dmaHandle       = 0x00
	dmaPagePhysical = 0x00
	dmaPageBytes    = 0x08
	dmaPageSize     = 16
	// dmaPagesInline is how many page entries the structure holds before it
	// has to be grown; a longer buffer is asked for by dmaLarge and paid for
	// with dmaPageSize more bytes each.
	dmaPagesInline = 256
)

// dmaScatterOptions is what LockScatterGather passes. Without the top bit the
// driver interposes a bounce buffer of its own below 4GB, a card then reads
// that instead of what we wrote, and the caller's pages stay empty.
const (
	dmaScatterOptions = 0x60 | 0x80
	// dmaContiguousOptions has the driver allocate the memory itself,
	// physically contiguous and below 4GB, and map it into this process. The
	// extra bit over the scatter options turns a lock of the caller's memory
	// into an allocation of the driver's own.
	dmaContiguousOptions = 0x60 | 0x01
	// dmaKernelBuffer is that bit on its own, which tells the two apart.
	dmaKernelBuffer = 0x01
	// dmaLarge says the buffer has been grown past its inline page entries.
	dmaLarge = 0x04
)

// WD_TRANSFER: one register access the driver makes itself. The same 32 bytes
// on both builds: the address is a kernel one, and the value is a union eight
// bytes wide, filled from the bottom.
const (
	transferPort    = 0x00
	transferCommand = 0x08
	transferBytes   = 0x0c
	transferData    = 0x18
	transferSize    = 0x20

	// transferWriteMemory32 is WM_DWORD: a 32-bit store to a memory window,
	// made through the window's kernel mapping.
	transferWriteMemory32 = 35
)

// WD_CARD_CLEANUP: the card, then the accesses the driver makes for it when the
// card is let go. The card is at the start on both builds; the pointer to the
// accesses and what follows it are in the per-build files.
const (
	cleanupCard = 0x00

	// cleanupForce is WD_FORCE_CLEANUP. Without it the accesses are made only
	// when the driver handle closes without the card having been closed first,
	// which is what a process that died looks like; with it they are made on
	// an ordinary close as well.
	cleanupForce = 1
)
