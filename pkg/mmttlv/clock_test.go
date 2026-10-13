// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import (
	"encoding/binary"
	"math"
	"testing"
)

// The timestamp sits between the packet identifier and the sequence number, and
// it is the only clock a TLV stream offers: reading it from the wrong four
// bytes would give a rate that looks plausible and is nonsense.
func TestParseReadsTheTransmissionClock(t *testing.T) {
	buffer := make([]byte, 12)
	buffer[1] = PayloadTypeSignaling
	binary.BigEndian.PutUint16(buffer[2:4], 0x1234)
	binary.BigEndian.PutUint32(buffer[4:8], 0xaabbccdd)
	binary.BigEndian.PutUint32(buffer[8:12], 0x00000009) // the sequence number
	packet, ok := Parse(buffer)
	if !ok {
		t.Fatal("a smallest legal header must parse")
	}
	if packet.PacketID != 0x1234 || packet.Timestamp != 0xaabbccdd {
		t.Errorf("parsed id %#x timestamp %#x, want 0x1234 and 0xaabbccdd", packet.PacketID, packet.Timestamp)
	}
}

func TestClockMeasuresARate(t *testing.T) {
	// A second of clock and a megabyte between the two readings is 8 Mbit/s.
	var clock Clock
	clock.Add(1, 0, 0)
	clock.Add(1, 1_000_000, TimestampHz)
	rate, ok := clock.Bitrate()
	if !ok {
		t.Fatal("two readings a second apart must give a rate")
	}
	if math.Abs(rate-8_000_000) > 1 {
		t.Errorf("rate = %v, want 8000000", rate)
	}
}

// One reading is a moment, not a rate, and two readings a hair apart divide a
// small number of bytes by a smaller number of ticks -- which is how a stream
// comes to be reported at gigabits.
func TestClockSaysNothingItCannotKnow(t *testing.T) {
	var empty Clock
	if _, ok := empty.Bitrate(); ok {
		t.Error("a clock with no readings must report nothing")
	}
	var one Clock
	one.Add(1, 0, 0)
	if _, ok := one.Bitrate(); ok {
		t.Error("a single reading must report nothing")
	}
	var brief Clock
	brief.Add(1, 0, 0)
	brief.Add(1, 4096, minimumSpan-1)
	if _, ok := brief.Bitrate(); ok {
		t.Error("a span shorter than the minimum must report nothing")
	}
}

// The clock counts seconds in sixteen bits and starts again every eighteen hours
// or so. A subtraction that went negative would be a stream reported as
// impossibly fast, or as nothing at all.
func TestClockCarriesTheWrap(t *testing.T) {
	var clock Clock
	clock.Add(1, 0, math.MaxUint32-TimestampHz/2)
	clock.Add(1, 1_000_000, TimestampHz/2) // one second later, over the end
	rate, ok := clock.Bitrate()
	if !ok {
		t.Fatal("a span that crosses the wrap must still give a rate")
	}
	if math.Abs(rate-8_000_000) > 1000 {
		t.Errorf("rate across the wrap = %v, want about 8000000", rate)
	}
}

// Two assets are two senders, and the one with more readings is the one whose
// timeline the stream is paced by.
func TestClockPrefersTheIdentifierItSawMost(t *testing.T) {
	var clock Clock
	clock.Add(7, 0, 0)
	clock.Add(7, 1_000_000, TimestampHz)
	clock.Add(7, 2_000_000, 2*TimestampHz)
	// A stray asset whose two readings would suggest twice the rate.
	clock.Add(9, 0, 0)
	clock.Add(9, 2_000_000, TimestampHz)
	rate, _ := clock.Bitrate()
	if math.Abs(rate-8_000_000) > 1 {
		t.Errorf("rate = %v, want the 8000000 of the identifier seen most", rate)
	}
}

// Two identifiers read equally often: the answer is the lower one's, every time.
func TestATieBetweenIdentifiersIsSettledTheSameWayEveryTime(t *testing.T) {
	for range 20 {
		var clock Clock
		clock.Add(0x0200, 0, 0)
		clock.Add(0x0200, 1000, TimestampHz)
		clock.Add(0x0100, 0, 0)
		clock.Add(0x0100, 2000, TimestampHz)
		rate, ok := clock.Bitrate()
		if !ok || rate != 2000*8 {
			t.Fatalf("rate = %v, %t, want the lower identifier's 16000", rate, ok)
		}
	}
}
