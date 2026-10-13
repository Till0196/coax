// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tlv

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestADaysWorth feeds the analyser as much stream as a day of recording is. This is the
// half that had something to grow -- the sequence table -- so it is the half worth running
// overnight's worth of stream through. See the same test in internal/coaxd/mpegts.
//
//	COAX_SOAK=1 go test ./internal/coaxd/tlv/ -run ADaysWorth -v -timeout 60m
func TestADaysWorth(t *testing.T) {
	if os.Getenv("COAX_SOAK") == "" {
		t.Skip("set COAX_SOAK=1 to feed a day of stream through the analyser")
	}
	const perSecond = 2_070_000
	const target = perSecond * 86400

	analyser := New()
	// A multiplex with a realistic number of runs in it, built once.
	//
	// Built once matters. The first version of this test made its packets inside
	// the loop, which meant it measured the allocator rather than the analyser:
	// forty-five minutes in, it had not reached a quarter of a day.
	const runs = 64
	var block []byte
	for identifier := range uint16(runs) {
		block = append(block, compressed(0x1000+identifier, 0)...)
	}
	// Where each packet's MMTP sequence number sits: past the TLV header, the
	// compressed-IP header, and the first eight bytes of the MMTP one.
	const packetSize = headerSize + 3 + 32
	const numberAt = headerSize + 3 + 8
	if len(block) != runs*packetSize {
		t.Fatalf("the block is %d bytes, expected %d", len(block), runs*packetSize)
	}

	before := heapMB()
	fed, started, reported, round := uint64(0), time.Now(), uint64(0), uint32(0)
	for fed < target {
		for index := range runs {
			binary.BigEndian.PutUint32(block[index*packetSize+numberAt:], round)
		}
		round++
		analyser.Feed(block)
		fed += uint64(len(block))
		if fed-reported >= target/16 {
			reported = fed
			fmt.Printf("  %3.0f%% of a day: %6.1f GB fed, heap %.2f MB, buffer cap %d, runs %d, elapsed %v\n",
				100*float64(fed)/target, float64(fed)/(1<<30), heapMB(), cap(analyser.buffer),
				len(analyser.sequences), time.Since(started).Round(time.Second))
		}
	}
	stats := analyser.Stats()
	fmt.Printf("a day of stream: %.1f GB, %d packets, heap %.2f -> %.2f MB, buffer cap %d, runs %d\n",
		float64(stats.Bytes)/(1<<30), stats.Packets, before, heapMB(), cap(analyser.buffer),
		len(analyser.sequences))
	if got := len(analyser.sequences); got != 64 {
		t.Errorf("tracked %d runs over a day of a multiplex with 64", got)
	}
	if stats.SyncLosses != 0 || stats.Discontinuities != 0 || stats.Forgotten != 0 {
		t.Errorf("a day of clean stream reported %+v", stats)
	}
}

func heapMB() float64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}
