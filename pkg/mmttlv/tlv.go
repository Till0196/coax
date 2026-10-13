// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// TLV framing: the packets an ISDB-S3 multiplex is made of, and the UDP
// datagrams carried inside their IPv4, IPv6, and compressed-IP payloads.
package mmttlv

import (
	"bytes"
	"encoding/binary"
)

const (
	SyncByte         = 0x7f
	TypeIPv4         = 0x01
	TypeIPv6         = 0x02
	TypeCompressedIP = 0x03
	TypeControl      = 0xfe
	TypeNull         = 0xff
)

type Stats struct {
	Bytes               uint64
	Packets             uint64
	NullPackets         uint64
	ControlPackets      uint64
	IPv4Packets         uint64
	IPv6Packets         uint64
	CompressedIPPackets uint64
	InvalidPackets      uint64
	// BeforeSync counts the bytes stepped over before the framing was found.
	// A sample begins wherever the tuner happened to be, so they are not
	// errors; bytes that break the framing once it has been found are
	// (InvalidPackets).
	BeforeSync uint64
	// CompressedNoContext counts fully compressed packets that arrived before
	// the packet establishing their header-compression context. Their payload
	// is still usable; only the addresses are unknown.
	CompressedNoContext uint64
}

type TLVPacket struct {
	Type    byte
	Payload []byte
	Offset  int
}

type Datagram struct {
	Payload                     []byte
	Source, Destination         []byte
	SourcePort, DestinationPort uint16
	// CID identifies the header-compression context the packet belongs to. It
	// stands in for the addresses while the context that would supply them has
	// not been seen.
	CID    uint16
	HasCID bool
}

func (datagram Datagram) IsNTP() bool { return datagram.DestinationPort == 123 }

type ipContext struct {
	source, destination  []byte
	sourcePort, destPort uint16
}

type Reader struct {
	buffer []byte
	offset int
	// synced says the framing has been found. Until it has, a candidate has
	// to be confirmed by the packet after it (see candidate).
	synced   bool
	stats    Stats
	contexts map[uint16]ipContext
}

func NewReader(buffer []byte) *Reader {
	return &Reader{buffer: buffer, contexts: make(map[uint16]ipContext)}
}

func (reader *Reader) Stats() Stats { return reader.stats }

func carries(typ byte) bool {
	return typ == TypeIPv4 || typ == TypeIPv6 || typ == TypeCompressedIP || typ == TypeControl || typ == TypeNull
}

// Next returns the next packet. A byte that is not one is stepped over to the
// next sync byte.
func (reader *Reader) Next() (TLVPacket, bool) {
	for {
		start := reader.offset
		next, verdict := reader.candidate(start)
		switch verdict {
		case candidateMore:
			// A packet that has not all arrived is left where it is.
			return TLVPacket{}, false
		case candidateNo:
			if reader.synced {
				reader.stats.InvalidPackets++
			}
			found := -1
			if start+1 < len(reader.buffer) {
				found = bytes.IndexByte(reader.buffer[start+1:], SyncByte)
			}
			if found < 0 {
				if !reader.synced {
					reader.stats.BeforeSync += uint64(len(reader.buffer) - start)
				}
				reader.offset = len(reader.buffer)
				return TLVPacket{}, false
			}
			if !reader.synced {
				reader.stats.BeforeSync += uint64(found + 1)
			}
			reader.offset = start + found + 1
			continue
		}
		reader.synced = true
		packet := TLVPacket{Type: reader.buffer[start+1], Payload: reader.buffer[start+4 : next], Offset: start}
		reader.offset = next
		reader.stats.Bytes += uint64(next - start)
		reader.stats.Packets++
		switch packet.Type {
		case TypeNull:
			reader.stats.NullPackets++
		case TypeControl:
			reader.stats.ControlPackets++
		case TypeIPv4:
			reader.stats.IPv4Packets++
		case TypeIPv6:
			reader.stats.IPv6Packets++
		case TypeCompressedIP:
			reader.stats.CompressedIPPackets++
		}
		return packet, true
	}
}

type candidateVerdict int

const (
	candidatePacket candidateVerdict = iota
	candidateNo
	candidateMore
)

// candidate says what is at a position: a packet ending at the returned
// offset, not a packet, or not enough bytes to say.
//
// Until the framing is found a candidate has to be confirmed by what follows
// it. A sync byte alone is no evidence -- payloads are full of them -- and
// taking the first one reads a length out of the middle of a payload and steps
// over whatever it lands past. So an unconfirmed candidate is one whose packet
// is followed by another packet's sync byte and type, or which ends exactly
// where the bytes do. Once the framing is found, the packet alone is enough.
func (reader *Reader) candidate(at int) (int, candidateVerdict) {
	if at+4 > len(reader.buffer) {
		return 0, candidateMore
	}
	head := reader.buffer[at : at+4]
	if head[0] != SyncByte || !carries(head[1]) {
		return 0, candidateNo
	}
	next := at + 4 + int(binary.BigEndian.Uint16(head[2:4]))
	if next > len(reader.buffer) {
		return 0, candidateMore
	}
	if reader.synced || next == len(reader.buffer) {
		return next, candidatePacket
	}
	if next+2 > len(reader.buffer) {
		return 0, candidateMore
	}
	if reader.buffer[next] == SyncByte && carries(reader.buffer[next+1]) {
		return next, candidatePacket
	}
	return 0, candidateNo
}

func (reader *Reader) Datagram(packet TLVPacket) (Datagram, bool) {
	switch packet.Type {
	case TypeIPv4:
		return parseIPv4(packet.Payload)
	case TypeIPv6:
		return parseIPv6(packet.Payload)
	case TypeCompressedIP:
		return reader.parseCompressedIP(packet.Payload)
	default:
		return Datagram{}, false
	}
}

func parseIPv4(payload []byte) (Datagram, bool) {
	if len(payload) < 20 || payload[0]>>4 != 4 {
		return Datagram{}, false
	}
	header := int(payload[0]&0x0f) * 4
	total := int(binary.BigEndian.Uint16(payload[2:4]))
	if header < 20 || total < header+8 || total > len(payload) || payload[9] != 17 || binary.BigEndian.Uint16(payload[6:8])&0x3fff != 0 {
		return Datagram{}, false
	}
	return parseUDP(payload[header:total], payload[12:16], payload[16:20])
}

func parseIPv6(payload []byte) (Datagram, bool) {
	if len(payload) < 40 || payload[0]>>4 != 6 {
		return Datagram{}, false
	}
	end := 40 + int(binary.BigEndian.Uint16(payload[4:6]))
	if end > len(payload) {
		return Datagram{}, false
	}
	next, body := payload[6], payload[40:end]
	for next == 0 || next == 43 || next == 51 || next == 60 {
		if len(body) < 2 {
			return Datagram{}, false
		}
		length := (int(body[1]) + 1) * 8
		if next == 51 {
			length = (int(body[1]) + 2) * 4
		}
		if length > len(body) {
			return Datagram{}, false
		}
		next, body = body[0], body[length:]
	}
	if next != 17 {
		return Datagram{}, false
	}
	return parseUDP(body, payload[8:24], payload[24:40])
}

func (reader *Reader) parseCompressedIP(payload []byte) (Datagram, bool) {
	if len(payload) < 3 {
		return Datagram{}, false
	}
	cid := binary.BigEndian.Uint16(payload[:2]) >> 4
	switch payload[2] {
	case 0x20:
		if len(payload) < 23 {
			return Datagram{}, false
		}
		datagram := Datagram{Source: append([]byte(nil), payload[11:15]...), Destination: append([]byte(nil), payload[15:19]...), SourcePort: binary.BigEndian.Uint16(payload[19:21]), DestinationPort: binary.BigEndian.Uint16(payload[21:23]), Payload: payload[23:], CID: cid, HasCID: true}
		reader.contexts[cid] = ipContext{datagram.Source, datagram.Destination, datagram.SourcePort, datagram.DestinationPort}
		return datagram, true
	case 0x60:
		if len(payload) < 45 {
			return Datagram{}, false
		}
		datagram := Datagram{Source: append([]byte(nil), payload[9:25]...), Destination: append([]byte(nil), payload[25:41]...), SourcePort: binary.BigEndian.Uint16(payload[41:43]), DestinationPort: binary.BigEndian.Uint16(payload[43:45]), Payload: payload[45:], CID: cid, HasCID: true}
		reader.contexts[cid] = ipContext{datagram.Source, datagram.Destination, datagram.SourcePort, datagram.DestinationPort}
		return datagram, true
	case 0x21, 0x61:
		// A fully compressed packet carries no header of its own. Most of a
		// TLV stream is made of these and the packet establishing the context
		// arrives only now and then, so a sample often begins in the middle.
		// Only the addresses are missing; the payload is intact.
		datagram := Datagram{Payload: payload[3:], CID: cid, HasCID: true}
		context, ok := reader.contexts[cid]
		if !ok {
			reader.stats.CompressedNoContext++
			return datagram, true
		}
		datagram.Source, datagram.Destination = context.source, context.destination
		datagram.SourcePort, datagram.DestinationPort = context.sourcePort, context.destPort
		return datagram, true
	default:
		return Datagram{}, false
	}
}

func parseUDP(payload, source, destination []byte) (Datagram, bool) {
	if len(payload) < 8 {
		return Datagram{}, false
	}
	length := int(binary.BigEndian.Uint16(payload[4:6]))
	if length < 8 || length > len(payload) {
		return Datagram{}, false
	}
	return Datagram{Source: append([]byte(nil), source...), Destination: append([]byte(nil), destination...), SourcePort: binary.BigEndian.Uint16(payload[0:2]), DestinationPort: binary.BigEndian.Uint16(payload[2:4]), Payload: payload[8:length]}, true
}
