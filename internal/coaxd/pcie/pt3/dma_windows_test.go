// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
)

// write fills blocks from..to-1 as the card would, numbering each by its block.
func write(r *ring, from, to int) {
	for block := from; block < to; block++ {
		at := block % ringBlocks * blockSize
		r.data.Bytes[at] = 0x47
		r.data.Bytes[at+1] = byte(block)
	}
}

func handed(r *ring) []int {
	var blocks []int
	r.collect(func(block []byte) { blocks = append(blocks, int(block[1])) })
	return blocks
}

// The blocks the card writes first after starting are dropped, and the ones
// after them handed on in order.
func TestTheFirstBlocksAfterStartingAreDropped(t *testing.T) {
	r := &ring{data: &windriver.DMABuffer{Bytes: make([]byte, ringSize)}}
	for block := range ringBlocks {
		r.data.Bytes[block*blockSize] = sentinel
	}
	r.skip = startBlocks

	// A block is handed on once the one after it has started.
	write(r, 0, startBlocks+3)
	got := handed(r)
	want := []int{startBlocks, startBlocks + 1}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("handed %v, want %v", got, want)
	}

	write(r, startBlocks+3, startBlocks+5)
	got = handed(r)
	want = []int{startBlocks + 2, startBlocks + 3}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("then handed %v, want %v", got, want)
	}
}
