// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import "encoding/binary"

// ITE's bridges are fed their firmware through scatter writes.  One packet is
// 0x03, a bank index, 0x00 and a block count, then that many (address high,
// address low, length) descriptors, then the data for those descriptors in the
// reverse of the descriptor order.  Both the IT9175/IT9179 and the IT930x
// drivers hold their firmware already cut into these packets, so finding the
// packets is the first half of extracting either one.
const (
	scatterHeaderSize = 4
	scatterMaxBlocks  = 3
	// scatterMaxPayload is the largest data run the drivers put in one packet.
	scatterMaxPayload = 44
)

// scatterSegment is one address/data run recovered from a scatter packet.
type scatterSegment struct {
	bank uint8
	addr uint16
	data []byte
}

// scatterRun is a run of consecutive scatter packets.
type scatterRun struct {
	start int
	end   int
	// boundaries holds the offset of every packet end relative to start,
	// which is where the run may legitimately be cut short.
	boundaries []int
	segments   []scatterSegment
}

// findLongestScatterRun returns the longest run of scatter packets in the
// driver.  The signature is short enough to occur by chance in unrelated code,
// so every candidate start is parsed and the longest run wins rather than the
// first one that parses at all.
func findLongestScatterRun(driver []byte) scatterRun {
	var longest scatterRun
	for start := 0; start < len(driver); start++ {
		if driver[start] != 0x03 {
			continue
		}
		if run := parseScatterRun(driver, start); run.end-run.start > longest.end-longest.start {
			longest = run
		}
	}
	return longest
}

// parseScatterRun walks consecutive scatter packets from start until one fails
// to parse.
func parseScatterRun(driver []byte, start int) scatterRun {
	run := scatterRun{start: start, end: start}
	for offset := start; offset+scatterHeaderSize <= len(driver); {
		if driver[offset] != 0x03 || driver[offset+2] != 0x00 {
			break
		}
		bank, count := driver[offset+1], int(driver[offset+3])
		if count < 1 || count > scatterMaxBlocks || offset+scatterHeaderSize+count*3 > len(driver) {
			break
		}
		addresses := make([]uint16, count)
		lengths := make([]int, count)
		total := 0
		for i := range lengths {
			field := driver[offset+scatterHeaderSize+i*3:]
			if lengths[i] = int(field[2]); lengths[i] == 0 || lengths[i] > scatterMaxPayload {
				return run
			}
			addresses[i] = binary.BigEndian.Uint16(field)
			total += lengths[i]
		}
		cursor := offset + scatterHeaderSize + count*3
		if cursor+total > len(driver) {
			break
		}
		// The data runs are stored in the reverse of the descriptor order.
		payload := make([]scatterSegment, count)
		for i := count - 1; i >= 0; i-- {
			payload[i] = scatterSegment{bank: bank, addr: addresses[i], data: driver[cursor : cursor+lengths[i]]}
			cursor += lengths[i]
		}
		run.segments = append(run.segments, payload...)
		run.end = cursor
		run.boundaries = append(run.boundaries, cursor-start)
		offset = cursor
	}
	return run
}
