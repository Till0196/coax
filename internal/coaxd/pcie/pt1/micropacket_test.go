// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pt1

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// builder makes the flow the card would produce, so what comes back out can be
// compared with what went in.
type builder struct {
	words    []uint32
	sequence [receivers]int
}

// packet lays one transport packet of a receiver into the flow.
func (b *builder) packet(receiver int, payload []byte) {
	if len(payload) != packetSize {
		panic("a transport packet is 188 bytes")
	}
	for index := range micropacketsPer {
		word := uint32(receiver+1) << microReceiverShift
		word |= uint32(b.sequence[receiver]) << microSequenceShift
		b.sequence[receiver] = (b.sequence[receiver] + 1) % sequenceModulus
		if index == 0 {
			word |= microFirst
		}
		at := index * micropacketBytes
		word |= uint32(payload[at])<<16 | uint32(payload[at+1])<<8
		if index != micropacketsPer-1 {
			word |= uint32(payload[at+2])
		}
		b.words = append(b.words, word)
	}
}

// page turns what has been built into the bytes of a card page.
func (b *builder) page() []byte {
	page := make([]byte, len(b.words)*4)
	for index, word := range b.words {
		binary.LittleEndian.PutUint32(page[index*4:], word)
	}
	b.words = nil
	return page
}

// samplePacket is a transport packet with every byte different from its
// neighbours, so a byte put in the wrong place shows up.
func samplePacket(seed byte) []byte {
	packet := make([]byte, packetSize)
	packet[0] = 0x47
	for index := 1; index < packetSize; index++ {
		packet[index] = byte(index) ^ seed
	}
	return packet
}

// One packet in, the same packet out. Every byte of it, including the two the
// last micropacket carries rather than three.
func TestAPacketComesBackWhole(t *testing.T) {
	want := samplePacket(0x5a)
	b := &builder{}
	b.packet(2, want)

	d := newDemultiplexer()
	d.feed(b.page())
	got := d.take(2)
	if !bytes.Equal(got, want) {
		t.Errorf("the packet came back as %x, want %x", got, want)
	}
	for receiver := range receivers {
		if receiver == 2 {
			continue
		}
		if taken := d.take(receiver); len(taken) != 0 {
			t.Errorf("receiver %d was given %d bytes of somebody else's stream", receiver, len(taken))
		}
	}
}

// Four streams interleaved a micropacket at a time, which is what the card
// really does, all come back separated and whole.
func TestFourStreamsAreSeparated(t *testing.T) {
	want := [receivers][]byte{}
	parts := [receivers][]uint32{}
	for receiver := range receivers {
		want[receiver] = samplePacket(byte(receiver * 0x11))
		b := &builder{}
		b.packet(receiver, want[receiver])
		parts[receiver] = b.words
	}
	// Round robin, one micropacket at a time.
	b := &builder{}
	for index := range micropacketsPer {
		for receiver := range receivers {
			b.words = append(b.words, parts[receiver][index])
		}
	}

	d := newDemultiplexer()
	d.feed(b.page())
	for receiver := range receivers {
		if got := d.take(receiver); !bytes.Equal(got, want[receiver]) {
			t.Errorf("receiver %d came back as %x, want %x", receiver, got, want[receiver])
		}
		if counts := d.counts(receiver); counts.Packets != 1 || counts.Losses != 0 {
			t.Errorf("receiver %d: %+v", receiver, counts)
		}
	}
}

// A flow joined in the middle of a packet is not guessed at: nothing is handed
// on until the start of a packet comes past.
func TestAFlowJoinedMidPacketWaitsForAStart(t *testing.T) {
	first, second := samplePacket(0x11), samplePacket(0x22)
	b := &builder{}
	b.packet(0, first)
	b.packet(0, second)
	words := b.words
	b.words = words[10:] // join ten micropackets in

	d := newDemultiplexer()
	d.feed(b.page())
	got := d.take(0)
	if !bytes.Equal(got, second) {
		t.Errorf("came back with %d bytes, want only the second packet", len(got))
	}
	if counts := d.counts(0); counts.Packets != 1 {
		t.Errorf("rebuilt %d packets, want 1", counts.Packets)
	}
}

// A break in the card's counter is counted. The counter is three bits and
// steps per micropacket, so a gap of a whole multiple of eight is invisible:
// this counts breaks, not lost micropackets.
func TestABreakInTheCountersIsNoticed(t *testing.T) {
	b := &builder{}
	b.packet(1, samplePacket(0x01))
	// Three micropackets go missing at the packet boundary: the counter is
	// stepped on without them being emitted.
	b.sequence[1] = (b.sequence[1] + 3) % sequenceModulus
	b.packet(1, samplePacket(0x02))

	d := newDemultiplexer()
	d.feed(b.page())
	if counts := d.counts(1); counts.Losses != 1 {
		t.Errorf("counted %d losses, want 1", counts.Losses)
	}
}

// The card marks a micropacket when its own buffer had already overflowed, and
// that is a different thing from a break in the counter: it says data was lost
// inside the card because nothing was reading fast enough.
func TestTheCardsOwnOverflowIsCounted(t *testing.T) {
	b := &builder{}
	b.packet(3, samplePacket(0x33))
	for index := range b.words {
		if index%7 == 0 {
			b.words[index] |= microOverflow
		}
	}
	d := newDemultiplexer()
	d.feed(b.page())
	if counts := d.counts(3); counts.Overflows != 9 {
		t.Errorf("counted %d overflows, want 9", counts.Overflows)
	}
}

// A packet that begins before the last one finished is a packet that was cut
// short, and the one that survives is the new one.
func TestAPacketCutShortIsCountedAndDropped(t *testing.T) {
	whole := samplePacket(0x44)
	b := &builder{}
	b.packet(0, samplePacket(0x99))
	b.words = b.words[:20] // cut it off
	b.packet(0, whole)

	d := newDemultiplexer()
	d.feed(b.page())
	if got := d.take(0); !bytes.Equal(got, whole) {
		t.Errorf("came back with %d bytes, want the whole packet only", len(got))
	}
	if counts := d.counts(0); counts.Resynchronisations != 1 {
		t.Errorf("counted %d resynchronisations, want 1", counts.Resynchronisations)
	}
}

// Words nothing wrote are ignored. The card leaves them zero, and the last
// word of every page is deliberately zeroed by this program as its sentinel.
func TestUnwrittenWordsAreIgnored(t *testing.T) {
	d := newDemultiplexer()
	d.feed(make([]byte, pageBytes))
	for receiver := range receivers {
		if counts := d.counts(receiver); counts != (Counts{}) {
			t.Errorf("receiver %d made something of an empty page: %+v", receiver, counts)
		}
	}
}

// A packet split across two feeds is rebuilt: the demultiplexer carries the
// part-built packet from one page of the card's memory to the next.
func TestAPacketSpanningTwoPagesIsRebuilt(t *testing.T) {
	want := samplePacket(0x77)
	b := &builder{}
	b.packet(0, want)
	words := b.words

	d := newDemultiplexer()
	for _, part := range [][]uint32{words[:30], words[30:]} {
		b.words = part
		d.feed(b.page())
	}
	if got := d.take(0); !bytes.Equal(got, want) {
		t.Errorf("the packet came back as %x, want %x", got, want)
	}
}
