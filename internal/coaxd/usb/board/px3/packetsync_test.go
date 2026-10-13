// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"bytes"
	"testing"
)

// marked is packets whose sync bytes carry bit 7, as the bridge sends them,
// behind a few bytes of a packet already under way.
func marked(packets int, lead int) (sent, want []byte) {
	for index := 0; index < packets; index++ {
		packet := bytes.Repeat([]byte{byte(index)}, packetSize)
		packet[0] = 0xc7
		want = append(want, packet...)
	}
	want = append(bytes.Repeat([]byte{0xc7}, lead), want...)
	sent = bytes.Clone(want)
	for at := lead; at < len(want); at += packetSize {
		want[at] = syncByte
	}
	return sent, want
}

func TestSyncBytesComeBackAcrossReadsOfAnySize(t *testing.T) {
	for _, chunk := range []int{1, 100, 188, 500, 4096} {
		sent, want := marked(20, 37)
		var sync packetSync
		for at := 0; at < len(sent); at += chunk {
			sync.restore(sent[at:min(at+chunk, len(sent))])
		}
		// The lead-in before the first boundary seen is left alone, and so is
		// whatever arrived before three packets could be lined up.
		from := len(sent) - 10*packetSize
		if !bytes.Equal(sent[from:], want[from:]) {
			t.Errorf("reads of %d bytes: sync bytes not restored", chunk)
		}
	}
}

func TestOnlySyncBytesAreTouched(t *testing.T) {
	sent, want := marked(10, 0)
	// A payload byte that happens to read 0xc7 is not a sync byte.
	sent[5], want[5] = 0xc7, 0xc7
	var sync packetSync
	sync.restore(sent)
	if !bytes.Equal(sent, want) {
		t.Error("something other than a sync byte was changed")
	}
}

func TestALostBoundaryIsFoundAgain(t *testing.T) {
	first, _ := marked(10, 0)
	second, want := marked(10, 50)
	var sync packetSync
	sync.restore(first)
	sync.restore(second)
	if !bytes.Equal(second[len(second)-5*packetSize:], want[len(want)-5*packetSize:]) {
		t.Error("the boundary was not found again after it moved")
	}
}
