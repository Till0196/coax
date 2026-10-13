// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import (
	"encoding/binary"
	"testing"
)

// ipv4UDP wraps a UDP body in a minimal IPv4 header, from 10.0.0.1 to
// 239.0.0.1.
func ipv4UDP(sourcePort, destinationPort uint16, body []byte) []byte {
	udp := binary.BigEndian.AppendUint16(nil, sourcePort)
	udp = binary.BigEndian.AppendUint16(udp, destinationPort)
	udp = binary.BigEndian.AppendUint16(udp, uint16(8+len(body)))
	udp = append(udp, 0, 0)
	udp = append(udp, body...)
	ip := []byte{0x45, 0x00}
	ip = binary.BigEndian.AppendUint16(ip, uint16(20+len(udp)))
	ip = append(ip, 0, 0, 0, 0, 64, 17, 0, 0, 10, 0, 0, 1, 239, 0, 0, 1)
	return append(ip, udp...)
}

func TestTheWallClockIsTakenOutOfTheMessageThatCarriesIt(t *testing.T) {
	message := make([]byte, 48)
	binary.BigEndian.PutUint64(message[40:], 0xe9abcdef12345678)
	reader := NewReader(packet(TypeIPv4, ipv4UDP(123, 123, message)...))
	tlvPacket, ok := reader.Next()
	if !ok {
		t.Fatal("the packet is there")
	}
	datagram, ok := reader.Datagram(tlvPacket)
	if !ok || !datagram.IsNTP() {
		t.Fatalf("datagram %+v %v", datagram, ok)
	}
	if ntp, ok := datagram.NTP(); !ok || ntp != 0xe9abcdef12345678 {
		t.Fatalf("ntp %x %v", ntp, ok)
	}
}

func TestADatagramTooShortForATimestampHasNone(t *testing.T) {
	reader := NewReader(packet(TypeIPv4, ipv4UDP(0x1234, 123, []byte{0xaa, 0xbb})...))
	tlvPacket, _ := reader.Next()
	datagram, ok := reader.Datagram(tlvPacket)
	if !ok || !datagram.IsNTP() || datagram.SourcePort != 0x1234 {
		t.Fatalf("datagram %+v %v", datagram, ok)
	}
	if _, ok := datagram.NTP(); ok {
		t.Fatal("a timestamp out of two bytes")
	}
}

// integrityStream is a run of well-formed packets: a null, a compressed one,
// and a null again, which is what these streams are mostly made of.
func integrityStream() []byte {
	var buffer []byte
	for range 6 {
		buffer = append(buffer, SyncByte, TypeNull, 0x00, 0x18)
		buffer = append(buffer, make([]byte, 24)...)
		buffer = append(buffer, SyncByte, TypeCompressedIP, 0x00, 0x10, 0x00, 0x00, 0x61)
		buffer = append(buffer, make([]byte, 13)...)
	}
	return buffer
}

func TestAStreamThatIsFramedIsFoundAndReadsToTheEnd(t *testing.T) {
	buffer := integrityStream()
	at, ok := FindSync(buffer)
	if !ok || at != 0 {
		t.Fatalf("found at %d %v", at, ok)
	}
	seen := MeasureIntegrity(buffer, at)
	if seen.Valid < 0.99 || seen.Compressed != 6 || seen.CompressedOK != 6 {
		t.Fatalf("integrity %+v", seen)
	}
}

func TestASyncByteInAPayloadIsNotAStart(t *testing.T) {
	buffer := append([]byte{0x7f, 0x7f, 0x7f, 0x7f, 0x00, 0x11, 0x22}, integrityStream()...)
	if at, ok := FindSync(buffer); !ok || at != 7 {
		t.Fatalf("found at %d %v", at, ok)
	}
}

func TestAStreamThatIsNotFramedReadsNoDistance(t *testing.T) {
	buffer := make([]byte, 4096)
	for at := range buffer {
		buffer[at] = byte(at * 37 % 251)
	}
	if at, ok := FindSync(buffer); ok {
		if seen := MeasureIntegrity(buffer, at); seen.Valid >= 0.5 {
			t.Fatalf("noise read as a stream: %+v", seen)
		}
	}
}

// A packet split across two chunks is waited for, and the framing found in
// the first chunk is kept for the second.
func TestAFeedHoldsWhatIsLeftOfAChunk(t *testing.T) {
	feed := NewFeed()
	first := append(packet(TypeNull, 0, 0), SyncByte)
	if pieces := feed.Push(first); len(pieces) != 0 || feed.Stats().Packets != 0 {
		t.Fatalf("a packet taken before it could be confirmed: %+v %+v", pieces, feed.Stats())
	}
	rest := append([]byte{TypeControl, 0x00, 0x02, 0x40, 0x01}, packet(TypeNull, 0, 0)...)
	pieces := feed.Push(rest)
	if len(pieces) != 1 || pieces[0].Kind != PieceControl || string(pieces[0].Control) != "\x40\x01" {
		t.Fatalf("pieces %+v", pieces)
	}
	if stats := feed.Stats(); stats.Packets != 3 || stats.InvalidPackets != 0 || stats.BeforeSync != 0 {
		t.Fatalf("stats %+v", stats)
	}
}

func TestAFeedCountsOffsetsFromTheStartOfTheStream(t *testing.T) {
	mmtp := []byte{0x00, PayloadTypeSignaling, 0x00, 0x05}
	mmtp = binary.BigEndian.AppendUint32(mmtp, 0x11223344)
	mmtp = binary.BigEndian.AppendUint32(mmtp, 7)
	mmtp = append(mmtp, 0xaa)
	carried := packet(TypeIPv4, ipv4UDP(0x1000, 0x2000, mmtp)...)
	stream := append(packet(TypeNull, 0, 0), carried...)
	stream = append(stream, packet(TypeNull, 0, 0)...)

	feed := NewFeed()
	var pieces []Piece
	for _, chunk := range [][]byte{stream[:5], stream[5:20], stream[20:]} {
		pieces = append(pieces, feed.Push(chunk)...)
	}
	if len(pieces) != 1 || pieces[0].Kind != PieceMMTP {
		t.Fatalf("pieces %+v", pieces)
	}
	got := pieces[0]
	if got.Offset != 6 || got.MMTP.PacketID != 5 || got.MMTP.SequenceNumber != 7 || string(got.MMTP.Payload) != "\xaa" {
		t.Fatalf("piece %+v", got)
	}
}
