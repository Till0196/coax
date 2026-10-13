// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pt1

import "encoding/binary"

// What the card's transfer engine delivers: not a transport stream, but one flow of 32-bit
// micropackets interleaving all four receivers a word at a time. Each carries three bytes
// of one receiver's stream and says whose it is:
//
//	bits 31..29  which receiver, counted from one
//	bits 28..26  a counter that steps by one per micropacket of that receiver
//	bit  25      the first micropacket of a transport packet
//	bit  24      the card's own buffer overflowed before this was written
//	bits 23..0   three bytes of the stream, most significant first
//
// Sixty-three micropackets make one 188-byte transport packet: sixty-two of three bytes and
// a last one of two. The counter wraps every eight, so a break is noticed within a few
// hundred bytes but says nothing about how much was lost.
const (
	packetSize       = 188
	micropacketsPer  = 63
	micropacketBytes = 3
	micropacketsPage = 1024
	pageBytes        = micropacketsPage * 4
	sequenceModulus  = 8
)

// The fields of a micropacket.
const (
	microReceiverShift = 29
	microSequenceShift = 26
	microSequenceMask  = 0x7
	microFirst         = 1 << 25
	microOverflow      = 1 << 24
	microPayloadMask   = 1<<24 - 1
)

// Counts is what a receiver's share of the flow has come to.
type Counts struct {
	// Packets is how many whole transport packets were rebuilt.
	Packets uint64
	// Losses is how many times the card's own counter skipped, meaning
	// micropackets went missing between the card and here.
	Losses uint64
	// Overflows is how many micropackets the card marked as written after its
	// own buffer had overflowed: data lost inside the card rather than on the
	// way out of it.
	Overflows uint64
	// Resynchronisations is how many times this had to wait for the start of a
	// packet. One at the beginning is ordinary; more than that follows a loss.
	Resynchronisations uint64
}

// demultiplexer pulls the four streams back out of the interleaved flow. It
// holds one partly built packet per receiver, so it must be fed the pages in
// the order the card wrote them, with nothing skipped.
type demultiplexer struct {
	streams [receivers]stream
}

// stream is one receiver's share.
type stream struct {
	// ready is the packets rebuilt so far, which the reader takes away.
	ready []byte
	// packet is the one being built, and at is how many micropackets of it
	// have arrived.
	packet [packetSize]byte
	at     int
	synced bool
	// sequence is the card's counter as it last was, -1 before any was seen.
	sequence int
	counts   Counts
}

func newDemultiplexer() *demultiplexer {
	d := &demultiplexer{}
	for index := range d.streams {
		d.streams[index].sequence = -1
	}
	return d
}

// feed takes one page of the card's memory: 1024 micropackets.
func (d *demultiplexer) feed(page []byte) {
	for at := 0; at+4 <= len(page); at += 4 {
		d.one(binary.LittleEndian.Uint32(page[at:]))
	}
}

func (d *demultiplexer) one(micropacket uint32) {
	receiver := int(micropacket>>microReceiverShift) - 1
	if receiver < 0 || receiver >= receivers {
		// Nothing wrote this: the card leaves unused words zero, and the
		// sentinel this program puts in the last one is zero too.
		return
	}
	s := &d.streams[receiver]

	if micropacket&microFirst != 0 {
		if s.synced && s.at != 0 {
			// A packet began before the last one finished.
			s.counts.Resynchronisations++
		}
		s.at, s.synced = 0, true
	} else if !s.synced {
		// Nothing to attach this to yet. The flow is joined wherever it
		// happens to be, so this is ordinary until the first start arrives.
		return
	}

	if micropacket&microOverflow != 0 {
		s.counts.Overflows++
	}
	sequence := int(micropacket>>microSequenceShift) & microSequenceMask
	if s.sequence >= 0 && sequence != (s.sequence+1)%sequenceModulus {
		s.counts.Losses++
	}
	s.sequence = sequence

	offset := s.at * micropacketBytes
	s.packet[offset] = byte(micropacket >> 16)
	s.packet[offset+1] = byte(micropacket >> 8)
	// The last micropacket carries two bytes, not three: 63*3 is one more
	// than a packet.
	if s.at != micropacketsPer-1 {
		s.packet[offset+2] = byte(micropacket)
	}

	s.at++
	if s.at >= micropacketsPer {
		s.at = 0
		s.ready = append(s.ready, s.packet[:]...)
		s.counts.Packets++
	}
}

// take hands over what a receiver has ready and forgets it.
func (d *demultiplexer) take(receiver int) []byte {
	s := &d.streams[receiver]
	ready := s.ready
	s.ready = nil
	return ready
}

func (d *demultiplexer) counts(receiver int) Counts { return d.streams[receiver].counts }

// waiting reports how many bytes a receiver has ready.
func (d *demultiplexer) waiting(receiver int) int { return len(d.streams[receiver].ready) }
