// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The IT930x image is the scatter packet stream itself: the kernel driver reads
// it930x-firmware.bin and hands each packet straight to the bridge. So
// extracting it is a matter of finding where the stream starts and where it
// stops, and copying it out.
//
// Where it stops is the hard half, because nothing in the stream marks its end
// and unrelated bytes after it may happen to parse as further packets. The
// driver answers that separately: alongside the code it carries a segment table
// whose lengths add up to exactly the length of the code.
const (
	it930xSegmentType = 1
	// The two field widths ITE's drivers use for the table.  The pair is
	// (type, length), each field align bytes wide.
	it930xMinAlign = 4
	it930xMaxAlign = 8
	// A firmware far larger than any seen is still bounded, which keeps the
	// search for candidate sums cheap.
	it930xMaxFirmwareSize = 1 << 20
	// A handful of bytes of unrelated code can parse as a packet or two and
	// coincide with some small sum in a segment table.  Every real image is
	// thousands of bytes, so a floor costs nothing and rules that out.
	it930xMinFirmwareSize = 1024
)

// ExtractIT930xFirmware recovers it930x-firmware.bin from a driver carrying it,
// either an ITE/PLEX Windows driver such as PXW3U4.sys or a Linux module.
func ExtractIT930xFirmware(driver []byte) ([]byte, error) {
	run := findLongestScatterRun(driver)
	if len(run.boundaries) == 0 {
		return nil, errors.New("no IT930x firmware scatter packets found")
	}
	lengths := it930xSegmentSums(driver)
	// Longest first: the whole run is the common case, and a shorter boundary
	// is only taken when the run has overrun into neighbouring data.
	for i := len(run.boundaries) - 1; i >= 0; i-- {
		length := run.boundaries[i]
		if length < it930xMinFirmwareSize {
			break
		}
		if lengths[length] {
			return driver[run.start : run.start+length], nil
		}
	}
	return nil, fmt.Errorf("found %d bytes of scatter packets but no segment table confirming where the firmware ends", run.end-run.start)
}

// it930xSegmentSums collects every length a segment table in the driver
// describes, as a set of candidate firmware lengths.  Every prefix counts,
// because a table may be followed by unrelated words that happen to look like
// further entries.
func it930xSegmentSums(driver []byte) map[int]bool {
	sums := make(map[int]bool)
	for align := it930xMinAlign; align <= it930xMaxAlign; align *= 2 {
		stride := align * 2
		for offset := 0; offset+stride <= len(driver); offset += it930xMinAlign {
			if binary.LittleEndian.Uint32(driver[offset:]) != it930xSegmentType {
				continue
			}
			total := 0
			for entry := offset; entry+stride <= len(driver); entry += stride {
				if binary.LittleEndian.Uint32(driver[entry:]) != it930xSegmentType {
					break
				}
				total += int(binary.LittleEndian.Uint32(driver[entry+align:]))
				if total <= 0 || total > it930xMaxFirmwareSize {
					break
				}
				if total >= it930xMinFirmwareSize {
					sums[total] = true
				}
			}
		}
	}
	return sums
}
