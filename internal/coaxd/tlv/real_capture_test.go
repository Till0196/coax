// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tlv

import (
	"os"
	"testing"
)

// TestARealCapture runs the analyser over a recording taken off a real
// ISDB-S3 tuner, which is the only way to find out whether the format was
// read as the broadcaster writes it rather than as the standard describes it.
//
// The file is not in the repository -- it is tens of megabytes of broadcast --
// so the test names it through an environment variable and skips without one.
//
//	COAX_TLV_CAPTURE=/path/to/capture.tlv go test ./internal/coaxd/tlv/ -run RealCapture -v
func TestARealCapture(t *testing.T) {
	path := os.Getenv("COAX_TLV_CAPTURE")
	if path == "" {
		t.Skip("set COAX_TLV_CAPTURE to a recording off a real ISDB-S3 tuner")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the capture: %v", err)
	}

	analyser := New()
	// Fed in the sizes a driver hands over rather than all at once, so that
	// the carry across reads is exercised the way it will be in use.
	const read = 6016
	for offset := 0; offset < len(data); offset += read {
		analyser.Feed(data[offset:min(offset+read, len(data))])
	}

	stats := analyser.Stats()
	t.Logf("%+v", stats)
	if !analyser.Synced() {
		t.Fatal("a real TLV recording did not line up")
	}
	if stats.Packets == 0 {
		t.Fatal("found no packets in a real TLV recording")
	}
	// The capture starts wherever the ring buffer was, so a few bytes at the
	// front are expected to be skipped -- but only a few.
	if stats.Skipped > maxPacket {
		t.Errorf("skipped %d bytes of %d", stats.Skipped, stats.Bytes)
	}
	if stats.SyncLosses != 0 {
		t.Errorf("lost sync %d times in a recording that should be whole", stats.SyncLosses)
	}
	if stats.Undecodable != 0 {
		t.Errorf("%d of %d packets could not be walked down to their MMTP header",
			stats.Undecodable, stats.Packets)
	}
}
