// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package windriver

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// pageSize is what the driver counts a locked buffer in.
func pageSize() int { return syscall.Getpagesize() }

// DMAPage is one run of physically contiguous memory inside a locked buffer.
// A buffer that looks like one block here is scattered across physical memory,
// and the card has to be told where each piece is.
type DMAPage struct {
	Physical uint64
	Bytes    uint32
}

// DMABuffer is memory of this process that a card has been given the addresses
// of. It stays put until Unlock -- neither moved nor paged out -- which is what
// makes the physical addresses worth writing into a card.
type DMABuffer struct {
	// Bytes is the memory itself: the caller's, for a locked buffer, and the
	// driver's for one it allocated.
	Bytes []byte
	// Pages is where that memory really is, in the order it is laid out.
	Pages []DMAPage

	driver *driver
	// request is the WD_DMA this buffer was locked by, kept whole: it carries
	// the handle it is unlocked by, and the driver wrote the pages into it.
	request []byte
	// keep is what the caller's memory was cut from, held so nothing collects
	// it while a card is writing into it.
	keep any
}

// LockScatterGather pins a buffer and reports where its pages are. keep must be
// whatever the buffer was cut from, so the collector leaves it alone while the
// card uses it; the buffer itself will do when it was cut from nothing.
func (d *Device) LockScatterGather(buffer []byte, keep any) (*DMABuffer, error) {
	if len(buffer) == 0 {
		return nil, fmt.Errorf("nothing to lock")
	}
	// A run of memory the driver has to describe page by page, plus one: a
	// buffer that does not start on a page boundary reaches into one more page
	// than its length alone would say.
	pages := (len(buffer)+pageSize()-1)/pageSize() + 1
	request, err := newDMARequest(d, dmaScatterOptions, len(buffer), pages)
	if err != nil {
		return nil, err
	}
	putUptr(request, dmaUserAddress, uint64(uintptr(unsafe.Pointer(&buffer[0]))))

	locked := &DMABuffer{Bytes: buffer, driver: d.driver, request: request, keep: keep}
	err = d.driver.control(ioctlDMALock, request)
	// The driver was handed the address of the caller's memory; nothing may
	// collect it while the request is in flight, and nothing may collect it
	// afterwards either while the card is writing into it.
	runtime.KeepAlive(keep)
	runtime.KeepAlive(buffer)
	if err != nil {
		return nil, fmt.Errorf("lock %d bytes for the card at %s: %w", len(buffer), d.slot, err)
	}
	if err := locked.readPages(len(buffer)); err != nil {
		locked.Unlock()
		return nil, err
	}
	return locked, nil
}

// LockContiguousLow has the driver allocate a run of memory below 4GB and maps
// it into this process. This is the only way that works for a card with a
// 32-bit bus: locking memory this process allocated does not, because the
// driver interposes a bounce buffer of its own and the card's transfer lands
// there instead of in the caller's pages. The run being physically contiguous
// also keeps a card's page-address list short.
func (d *Device) LockContiguousLow(size int) (*DMABuffer, error) {
	if size <= 0 {
		return nil, fmt.Errorf("nothing to lock")
	}
	// The driver allocates this one, and a contiguous run is one piece however
	// long it is, so the request needs no room beyond the entries it carries.
	request, err := newDMARequest(d, dmaContiguousOptions, size, 0)
	if err != nil {
		return nil, err
	}
	locked := &DMABuffer{driver: d.driver, request: request}
	if err := d.driver.control(ioctlDMALock, request); err != nil {
		return nil, fmt.Errorf("allocate %d bytes for the card at %s: %w", size, d.slot, err)
	}
	mapped := uptr(request, dmaUserAddress)
	if mapped == 0 {
		// The driver has the allocation all the same, and keeps it until it
		// is unlocked.
		_ = locked.Unlock()
		return nil, fmt.Errorf("allocate %d bytes for the card at %s: the driver reported success and mapped nothing",
			size, d.slot)
	}
	locked.Bytes = atAddress(mapped, uint64(size))
	if err := locked.readPages(size); err != nil {
		locked.Unlock()
		return nil, err
	}
	return locked, nil
}

// newDMARequest is a WD_DMA with room for the pages it may come back with. The
// structure carries the first 256 entries itself and is grown, with a flag to
// say so, for a buffer needing more.
func newDMARequest(device *Device, options uint32, size, pages int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("nothing to lock")
	}
	length := dmaPagesOffset + dmaPagesInline*dmaPageSize
	if pages > dmaPagesInline {
		length += (pages - dmaPagesInline) * dmaPageSize
		options |= dmaLarge
	}
	request := make([]byte, length)
	putU32(request, dmaBytes, uint32(size))
	putU32(request, dmaOptions, options)
	putU32(request, dmaCard, u32(device.registration, registerHandleOffset))
	if options&dmaLarge != 0 {
		// A grown request says how far it was grown. Only a lock of the
		// caller's memory ever is: an allocation is the driver's own and is
		// one piece however long it is, so it counts its pages itself.
		putU32(request, dmaPagesCount, uint32(pages))
	}
	return request, nil
}

func (b *DMABuffer) readPages(size int) error {
	count := int(u32(b.request, dmaPagesCount))
	// Divided rather than multiplied, so a wild count cannot wrap round on a
	// 32-bit build and pass.
	if count <= 0 || count > (len(b.request)-dmaPagesOffset)/dmaPageSize {
		return fmt.Errorf("the driver locked %d bytes and reported %d pages", size, count)
	}
	total := uint64(0)
	for page := range count {
		at := dmaPagesOffset + page*dmaPageSize
		bytes := u32(b.request, at+dmaPageBytes)
		b.Pages = append(b.Pages, DMAPage{
			Physical: u64(b.request, at+dmaPagePhysical),
			Bytes:    bytes,
		})
		total += uint64(bytes)
	}
	// A buffer whose pieces do not add up to it is one this program has
	// misread, and a card told about it would write somewhere unintended.
	if total != uint64(size) {
		return fmt.Errorf("locked %d bytes and got %d pieces covering %d", size, count, total)
	}
	return nil
}

// Describe says what the driver made of the buffer, for a caller that got
// something other than what it asked for.
func (b *DMABuffer) Describe() string {
	if b.request == nil {
		return "unlocked"
	}
	described := fmt.Sprintf("%d bytes, options %#x, %d pages",
		u32(b.request, dmaBytes), u32(b.request, dmaOptions), u32(b.request, dmaPagesCount))
	for index, page := range b.Pages {
		if index == 4 {
			described += ", ..."
			break
		}
		described += fmt.Sprintf(", %#x+%d", page.Physical, page.Bytes)
	}
	return described
}

// Unlock releases the buffer. The memory is the caller's again afterwards, and
// a buffer the driver allocated is gone.
func (b *DMABuffer) Unlock() error {
	if b.request == nil {
		return nil
	}
	if u32(b.request, dmaHandle) == 0 {
		b.request, b.Pages, b.Bytes = nil, nil, nil
		return nil
	}
	// The request is kept until the driver has taken it: it is the only
	// handle on the lock, and a buffer that failed to unlock can be tried
	// again only with it.
	if err := b.driver.control(ioctlDMAUnlock, b.request); err != nil {
		return fmt.Errorf("unlock: %w", err)
	}
	b.request, b.Pages, b.Bytes = nil, nil, nil
	return nil
}
