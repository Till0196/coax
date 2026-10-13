// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
)

// How the transfer is arranged.
//
// A page table is one 4 KiB page holding the page frame numbers of 511 data
// pages, with the next table's page frame number at its head; the tables form a
// closed ring the card walks for ever. It does not walk freely: a counter says
// how many tables it may still fill and it stops at zero, so a reader that
// falls behind stalls the card rather than being overwritten. There is no
// interrupt.
//
// How far the card has got inside a table is not in a register either. Every
// data page is a whole number of micropackets, so its last word is never zero
// once written: this zeroes that word and watches for it to change.
const (
	// pagesPerTable is what a table page has room for after its own link.
	pagesPerTable = 511
	// tableCount is how many tables the ring holds, and so how much stream is
	// buffered. Each table is 2 MB and four receivers at their fastest come to
	// about 12.5 MB/s, so this is roughly 600 ms -- deliberately small, because
	// the memory must be physically contiguous and below 4 GB, which is scarce on
	// a running Windows.
	tableCount = 4

	// tableLinkOffset is where a table page keeps the next table's page frame
	// number, and tableEntriesOffset where its own list begins.
	tableLinkOffset    = 0
	tableEntriesOffset = 4
)

// pageBits is the shift between a physical address and its page frame number.
const pageBits = 12

// The sizes the memory is asked for in, largest first. The allocation is
// physically contiguous and the card is happy with consecutive page frame
// numbers, so a larger run means fewer allocations -- but a large contiguous
// run below 4 GB is what a machine up for a while may not have, so this walks
// down until something is given.
var chunkSizes = []int{512 << 10, 128 << 10, 32 << 10, 4 << 10}

// pool is the card-reachable memory, and the pages cut out of it.
type pool struct {
	chunks []*windriver.DMABuffer
	pages  []poolPage
	// ladder is how far down chunkSizes the last successful allocation was, so
	// a machine that will not give a large run is not asked again per chunk.
	ladder int
}

// poolPage is one 4 KiB page: the bytes as this process sees them, and the
// page frame number the card knows it by.
type poolPage struct {
	bytes []byte
	frame uint32
}

func newPool(device *windriver.Device, count int) (*pool, error) {
	p := &pool{}
	for len(p.pages) < count {
		wanted := (count - len(p.pages)) * pageSize
		chunk, err := p.take(device, wanted)
		if err != nil {
			p.close()
			return nil, err
		}
		if err := p.cut(chunk); err != nil {
			p.close()
			return nil, err
		}
	}
	p.pages = p.pages[:count]
	return p, nil
}

// take asks for as much as it can get in one run, walking the sizes down.
func (p *pool) take(device *windriver.Device, wanted int) (*windriver.DMABuffer, error) {
	var last error
	for step := p.ladder; step < len(chunkSizes); step++ {
		size := chunkSizes[step]
		if size > wanted {
			continue
		}
		chunk, err := device.LockContiguousLow(size)
		if err == nil {
			p.chunks = append(p.chunks, chunk)
			p.ladder = step
			return chunk, nil
		}
		last = err
	}
	// Nothing in the ladder was small enough (only when less than a page is
	// left), or every size failed.
	if wanted < pageSize {
		return nil, fmt.Errorf("asked for %d bytes, which is less than a page", wanted)
	}
	return nil, fmt.Errorf("the card's memory could not be taken: %w", last)
}

// cut turns a run into pages. The run must begin on a page boundary and be a
// whole number of pages, which the library's allocation is; anything else
// would tell the card about memory that is not there.
func (p *pool) cut(chunk *windriver.DMABuffer) error {
	if len(chunk.Pages) != 1 {
		return fmt.Errorf("a run of %d bytes came back in %d pieces", len(chunk.Bytes), len(chunk.Pages))
	}
	physical := chunk.Pages[0].Physical
	if physical%pageSize != 0 {
		return fmt.Errorf("a run begins at %#x, which is not a page boundary", physical)
	}
	if physical+uint64(len(chunk.Bytes)) > 1<<32 {
		return fmt.Errorf("a run at %#x reaches past four gigabytes, which this card cannot address", physical)
	}
	for at := 0; at+pageSize <= len(chunk.Bytes); at += pageSize {
		p.pages = append(p.pages, poolPage{
			bytes: chunk.Bytes[at : at+pageSize],
			frame: uint32((physical + uint64(at)) >> pageBits),
		})
	}
	return nil
}

func (p *pool) close() {
	for _, chunk := range p.chunks {
		_ = chunk.Unlock()
	}
	p.chunks, p.pages = nil, nil
}

// pageSize is 4 KiB: the operating system's page, and also exactly what 1024
// micropackets come to.
const pageSize = pageBytes

// ring is the whole transfer: the tables, their pages, and how far reading has
// got.
type ring struct {
	card *card
	pool *pool
	// tables are the table pages, in the order the card walks them.
	tables []poolPage
	// data is every data page, table by table: data[t*pagesPerTable+b].
	data []poolPage

	// table and page are where the next page to be read sits.
	table, page int
	// granted is how many tables the card has been given.
	granted uint64
}

func newRing(held *card) (*ring, error) {
	r := &ring{card: held}
	taken, err := newPool(held.device, tableCount*(1+pagesPerTable))
	if err != nil {
		return nil, err
	}
	r.pool = taken

	// The first pages of the pool become the tables and the rest the data.
	r.tables = taken.pages[:tableCount]
	r.data = taken.pages[tableCount:]

	// Every page is marked not yet written before the card is told of any.
	for index := range r.data {
		r.mark(index)
	}
	// Then the tables, each pointing at its own pages and at the next table,
	// the last back to the first.
	for index := range r.tables {
		page := r.tables[index].bytes
		next := r.tables[(index+1)%tableCount].frame
		binary.LittleEndian.PutUint32(page[tableLinkOffset:], next)
		for entry := range pagesPerTable {
			at := tableEntriesOffset + entry*4
			binary.LittleEndian.PutUint32(page[at:], r.data[index*pagesPerTable+entry].frame)
		}
	}
	return r, nil
}

// mark puts a data page back to not yet written.
func (r *ring) mark(index int) {
	page := r.data[index].bytes
	binary.LittleEndian.PutUint32(page[pageSize-4:], 0)
}

// written reports whether the card has filled the page the reading is at.
func (r *ring) written() bool {
	page := r.data[r.table*pagesPerTable+r.page].bytes
	return binary.LittleEndian.Uint32(page[pageSize-4:]) != 0
}

func (r *ring) current() []byte {
	return r.data[r.table*pagesPerTable+r.page].bytes
}

// advance moves past the page just read, granting the card another table's
// worth whenever one has been finished with.
func (r *ring) advance() error {
	r.mark(r.table*pagesPerTable + r.page)
	r.page++
	if r.page < pagesPerTable {
		return nil
	}
	r.page = 0
	r.table = (r.table + 1) % tableCount
	r.granted++
	return r.card.command(cmdAddTable)
}

// arm sets the card going: the counter is set to the number of tables, then
// the card is pointed at the first, which is what starts it.
func (r *ring) arm() error {
	if err := r.card.command(cmdInitTableCount); err != nil {
		return err
	}
	for range tableCount {
		if err := r.card.command(cmdAddTable); err != nil {
			return err
		}
	}
	if err := r.card.write(regTable, r.tables[0].frame); err != nil {
		return err
	}
	r.table, r.page = 0, 0
	return r.card.command(cmdRegisterTables)
}

// stop takes the tables away, which is what stops the transfer.
func (r *ring) stop() error { return r.card.command(cmdForgetTables) }

func (r *ring) close() error {
	if err := r.stop(); err != nil {
		// The card was not told to stop, so it may go on filling the tables
		// it was given. Its memory is kept rather than handed back; the card
		// is closed with bus mastering taken away.
		strandedMutex.Lock()
		stranded = append(stranded, r)
		strandedMutex.Unlock()
		r.card.device.Untrusted()
		return fmt.Errorf("stop the transfer: %w; its memory is kept rather than given back", err)
	}
	// The card may still have a transfer in flight when the tables go, writing
	// into memory that has been handed back. Nothing can be polled for it, so
	// this waits the longest a page can take.
	time.Sleep(settleAfterStop)
	if r.pool != nil {
		r.pool.close()
		r.pool = nil
	}
	return nil
}

// stranded keeps, for the life of the process, the memory of a ring whose card
// could not be told to stop.
var (
	strandedMutex sync.Mutex
	stranded      []*ring
)

// halt stops the card in a hurry: the tables taken away, and bus mastering with
// them so that a card which does not listen writes nowhere. The register is
// written without the lock that guards it, because whoever panicked may hold
// it. See device.OnHalt.
func (c *card) halt() {
	_ = c.write(regCommand, cmdForgetTables)
	_ = c.device.DisableBusMastering()
}

// settleAfterStop is a margin before the memory is taken away; the card stops
// at a page boundary rather than mid-page.
const settleAfterStop = 20 * time.Millisecond
