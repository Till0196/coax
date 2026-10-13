// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

func TestDetectFormat(t *testing.T) {
	ts := make([]byte, 188*8)
	for offset := 0; offset < len(ts); offset += 188 {
		ts[offset] = 0x47
	}
	if got := DetectFormat(ts); got != "ts" {
		t.Fatalf("TS detected as %q", got)
	}
	tlv := []byte{0x7f, 0x01, 0x00, 0x02, 0xaa, 0xbb, 0x7f, 0xfe, 0x00, 0x01, 0xcc}
	if got := DetectFormat(tlv); got != "tlv" {
		t.Fatalf("TLV detected as %q", got)
	}
	if got := DetectFormat([]byte("not a transport stream")); got != "unknown" {
		t.Fatalf("unknown data detected as %q", got)
	}
}
