// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// The MMTP packet header, which is what a TLV payload turns out to be once the
// IP and UDP layers are peeled off.
package mmttlv

import "encoding/binary"

const PayloadTypeSignaling = 0x02

type MMTPPacket struct {
	PacketID    uint16
	PayloadType byte
	// Timestamp is when the sender handed this packet to the delivery layer,
	// in NTP short format: seconds in the upper 16 bits and a fraction of a
	// second in the lower 16. It is the transmission clock of the stream, and
	// the only one TLV has -- there is no programme clock reference here.
	Timestamp uint32
	// SequenceNumber counts the packets of this packet identifier, so a gap in
	// it is packets lost on the way.
	SequenceNumber uint32
	// RAP says the payload is a point a decoder can start from.
	RAP       bool
	Payload   []byte
	Scrambled bool
}

func Parse(buffer []byte) (MMTPPacket, bool) {
	if len(buffer) < 12 {
		return MMTPPacket{}, false
	}
	packet := MMTPPacket{
		PacketID:    binary.BigEndian.Uint16(buffer[2:4]),
		PayloadType: buffer[1] & 0x3f,
		Timestamp:   binary.BigEndian.Uint32(buffer[4:8]),
		// packet_sequence_number, then RAP_flag in the lowest bit of the first byte.
		SequenceNumber: binary.BigEndian.Uint32(buffer[8:12]),
		RAP:            buffer[0]&0x01 != 0,
	}
	offset := 12
	if buffer[0]&0x20 != 0 {
		offset += 4
	}
	if offset > len(buffer) {
		return MMTPPacket{}, false
	}
	if buffer[0]&0x02 != 0 {
		if offset+4 > len(buffer) {
			return MMTPPacket{}, false
		}
		typ := binary.BigEndian.Uint16(buffer[offset : offset+2])
		length := int(binary.BigEndian.Uint16(buffer[offset+2 : offset+4]))
		offset += 4
		if offset+length > len(buffer) {
			return MMTPPacket{}, false
		}
		if typ == 0 && hasScrambleExtension(buffer[offset:offset+length]) {
			packet.Scrambled = true
		}
		offset += length
	}
	packet.Payload = buffer[offset:]
	return packet, true
}

func hasScrambleExtension(buffer []byte) bool {
	for len(buffer) >= 4 {
		typ := binary.BigEndian.Uint16(buffer[:2]) & 0x7fff
		last := buffer[0]&0x80 != 0
		length := int(binary.BigEndian.Uint16(buffer[2:4]))
		if 4+length > len(buffer) {
			return false
		}
		if typ == 1 {
			return length > 0 && buffer[4]&0x1f != 0
		}
		buffer = buffer[4+length:]
		if last {
			return false
		}
	}
	return false
}
