// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86h47

import (
	"bytes"
	"testing"
)

// The framer takes the timestamps off and passes the packets as they are,
// whatever their scrambling control says. Reads are cut wherever the
// endpoint cuts them; a unit split across two must come out whole, and bytes
// before the first sync byte are dropped.
func TestFramerKeepsPackets(t *testing.T) {
	raw := []byte{0x12, 0x34, 0x56} // the tail of a unit from before the start
	var want []byte
	for index, control := range []byte{0x00, 0x40, 0x80, 0xc0} {
		packet := make([]byte, transportSize)
		packet[0], packet[1], packet[2], packet[3] = syncByte, 0x01, 0x00, control|0x10|byte(index)
		for offset := 4; offset < transportSize; offset++ {
			packet[offset] = byte(offset * (index + 3))
		}
		raw = append(raw, 0xc0, 0x00, byte(index), 0x2a) // arrival timestamp
		raw = append(raw, packet...)
		want = append(want, packet...)
	}
	framer := &framer{}
	got := framer.feed(raw[:250], nil)
	got = framer.feed(raw[250:], got)
	if !bytes.Equal(got, want) {
		t.Errorf("framed packets differ:\n got  %x\n want %x", got, want)
	}
}
