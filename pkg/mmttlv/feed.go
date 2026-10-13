// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import "encoding/binary"

// ntpTransmitEnd is how far into an NTP message its transmit timestamp ends:
// the header before it is forty bytes, and the timestamp itself is eight.
const ntpTransmitEnd = 48

// NTP returns the wall clock the transmission is sent against, in NTP
// timestamp format: seconds since 1900 in the upper half and a fraction of a
// second in the lower. It is the transmit timestamp, the last of the four an
// NTP message carries, which is the one the sender filled in as it sent. It
// reports false where the datagram is not the clock or is too short.
func (datagram Datagram) NTP() (uint64, bool) {
	if !datagram.IsNTP() || len(datagram.Payload) < ntpTransmitEnd {
		return 0, false
	}
	return binary.BigEndian.Uint64(datagram.Payload[ntpTransmitEnd-8 : ntpTransmitEnd]), true
}

// The context identifier headers a compressed packet may carry. A packet that
// says it is compressed and then names none of these is not a packet.
const (
	cidPartialIPv4UDP     = 0x20
	cidIPv4Identifier     = 0x21
	cidPartialIPv6UDP     = 0x60
	cidNoCompressedHeader = 0x61
)

// Integrity is how much of a run of bytes reads as TLV, and how the compressed
// packets in it looked.
type Integrity struct {
	// Valid is how much of what was looked at was framed as TLV, nought to one.
	Valid float64
	// Compressed counts the compressed packets that went past, and
	// CompressedOK those that named a context identifier that exists.
	Compressed   int
	CompressedOK int
}

// FindSync says where a TLV stream starts in a run of bytes. A sync byte alone
// is not a start -- the payloads are full of them -- so what makes one is three
// packets in a row whose lengths lead to the next.
func FindSync(buffer []byte) (int, bool) {
	for from := 0; from < len(buffer); from++ {
		if buffer[from] != SyncByte {
			continue
		}
		scan, valid := from, 0
		for valid < 3 && scan+4 <= len(buffer) {
			if buffer[scan] != SyncByte {
				break
			}
			next := scan + 4 + int(binary.BigEndian.Uint16(buffer[scan+2:scan+4]))
			if next > len(buffer) {
				break
			}
			valid++
			scan = next
		}
		if valid >= 3 {
			return from, true
		}
	}
	return 0, false
}

// MeasureIntegrity says how far the framing holds from where it started, which
// is what tells one alignment of the carriers of a split frame from another:
// one put together wrongly stops reading almost at once.
func MeasureIntegrity(buffer []byte, from int) Integrity {
	at := from
	var seen Integrity
	for at+4 <= len(buffer) {
		if buffer[at] != SyncByte || !carries(buffer[at+1]) {
			break
		}
		typ := buffer[at+1]
		length := int(binary.BigEndian.Uint16(buffer[at+2 : at+4]))
		next := at + 4 + length
		if next > len(buffer) {
			break
		}
		if typ == TypeCompressedIP && length >= 3 {
			seen.Compressed++
			switch buffer[at+4+2] {
			case cidPartialIPv4UDP, cidIPv4Identifier, cidPartialIPv6UDP, cidNoCompressedHeader:
			default:
				return integrityOf(seen, at-from, len(buffer)-from)
			}
			seen.CompressedOK++
		}
		at = next
	}
	return integrityOf(seen, at-from, len(buffer)-from)
}

func integrityOf(seen Integrity, read, lookedAt int) Integrity {
	if lookedAt < 1 {
		lookedAt = 1
	}
	seen.Valid = float64(read) / float64(lookedAt)
	return seen
}

// PieceKind says what a Piece is.
type PieceKind int

const (
	// PieceControl is the transmission's own signalling, which is where the
	// network table is.
	PieceControl PieceKind = iota
	// PieceNTP is the clock the transmission is sent against.
	PieceNTP
	// PieceMMTP is an MMTP packet, out of the datagram that carried it.
	PieceMMTP
)

// Piece is what came out of a chunk of a stream.
type Piece struct {
	Kind PieceKind
	// Control is the payload of a PieceControl.
	Control []byte
	// NTP is the timestamp of a PieceNTP.
	NTP uint64
	// MMTP is the packet of a PieceMMTP. Its Payload is a copy, so it outlives
	// the chunk it arrived in.
	MMTP MMTPPacket
	// Offset is how far into the stream a PieceMMTP began, counted from the
	// first byte ever fed. It is what a rate is worked out from.
	Offset int
}

// Feed reads a stream that arrives a chunk at a time. A packet does not stop
// at the end of a chunk, so what is left over is held until the rest of it
// arrives. Whether the framing has been found, and the contexts a compressed
// packet's addresses come from, are held across chunks too.
type Feed struct {
	held     []byte
	synced   bool
	done     int
	stats    Stats
	contexts map[uint16]ipContext
}

func NewFeed() *Feed { return &Feed{contexts: make(map[uint16]ipContext)} }

func (feed *Feed) Stats() Stats { return feed.stats }

// Push takes what has arrived and gives back what it completed.
func (feed *Feed) Push(arrived []byte) []Piece {
	feed.held = append(feed.held, arrived...)
	reader := &Reader{buffer: feed.held, synced: feed.synced, stats: feed.stats, contexts: feed.contexts}
	var pieces []Piece
	for {
		packet, ok := reader.Next()
		if !ok {
			break
		}
		switch packet.Type {
		case TypeControl:
			pieces = append(pieces, Piece{Kind: PieceControl, Control: append([]byte(nil), packet.Payload...)})
		case TypeNull:
		default:
			datagram, ok := reader.Datagram(packet)
			if !ok {
				continue
			}
			// The transmission's clock is sent to a port of its own, and is not
			// the broadcast.
			if datagram.IsNTP() {
				if ntp, ok := datagram.NTP(); ok {
					pieces = append(pieces, Piece{Kind: PieceNTP, NTP: ntp})
				}
				continue
			}
			read, ok := Parse(datagram.Payload)
			if !ok {
				continue
			}
			read.Payload = append([]byte(nil), read.Payload...)
			pieces = append(pieces, Piece{Kind: PieceMMTP, MMTP: read, Offset: feed.done + packet.Offset})
		}
	}
	consumed := reader.offset
	feed.synced = reader.synced
	feed.stats = reader.stats
	feed.held = append(feed.held[:0], feed.held[consumed:]...)
	feed.done += consumed
	return pieces
}
