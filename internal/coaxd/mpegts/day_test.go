// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// TestADaysWorth feeds the analyser as much stream as a day of recording is, and reports
// what it is holding at the end.
//
// A day of a Japanese satellite multiplex is about 175 gigabytes. Nothing here is expected
// to grow with it -- the point is that "expected" is not the same as "measured", and a
// recording that runs overnight is exactly where a slow leak would show up.
//
//	COAX_SOAK=1 go test ./internal/coaxd/mpegts/ -run ADaysWorth -v -timeout 60m
func TestADaysWorth(t *testing.T) {
	if os.Getenv("COAX_SOAK") == "" {
		t.Skip("set COAX_SOAK=1 to feed a day of stream through the analyser")
	}
	const perSecond = 2_070_000 // BS01 as measured on the PT3
	const target = perSecond * 86400

	analyser := New()
	// Fed in the sizes the daemon reads in, so the carry across reads happens
	// as often as it really would.
	data := stream(0x100, 32) // 6016 bytes, one chardev read
	before := heapMB()
	fed, started, reported := uint64(0), time.Now(), uint64(0)
	for fed < target {
		analyser.Feed(data)
		fed += uint64(len(data))
		if fed-reported >= target/8 {
			reported = fed
			t.Logf("%3.0f%% of a day: %6.1f GB fed, heap %.2f MB, buffer cap %d, elapsed %v",
				100*float64(fed)/target, float64(fed)/(1<<30), heapMB(), cap(analyser.buffer),
				time.Since(started).Round(time.Second))
		}
	}
	stats := analyser.Stats()
	t.Logf("a day of stream: %.1f GB, %d packets, heap %.2f -> %.2f MB, buffer cap %d",
		float64(stats.Bytes)/(1<<30), stats.Packets, before, heapMB(), cap(analyser.buffer))
	if got := cap(analyser.buffer); got > 2*(len(data)+PacketSize) {
		t.Errorf("the buffer grew to %d over a day", got)
	}
	if stats.SyncLosses != 0 || stats.Discontinuities != 0 {
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
