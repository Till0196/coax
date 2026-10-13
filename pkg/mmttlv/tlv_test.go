// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import "testing"

func TestReaderResynchronizesAndCountsNullPacket(t *testing.T) {
	reader := NewReader([]byte{0, 1, SyncByte, TypeNull, 0, 0})
	packet, ok := reader.Next()
	if !ok || packet.Type != TypeNull || packet.Offset != 2 {
		t.Fatalf("packet = %#v, ok = %t", packet, ok)
	}
	stats := reader.Stats()
	if stats.Packets != 1 || stats.NullPackets != 1 || stats.InvalidPackets != 0 || stats.BeforeSync != 2 {
		t.Fatalf("stats = %#v", stats)
	}
}

// packet builds one TLV packet.
func packet(typ byte, payload ...byte) []byte {
	header := []byte{SyncByte, typ, byte(len(payload) >> 8), byte(len(payload))}
	return append(header, payload...)
}

// compressedIPv6 builds a fully compressed IPv6 packet: a context identifier
// and the payload, with no header of its own.
func compressedIPv6(cid uint16, payload ...byte) []byte {
	body := []byte{byte(cid >> 4), byte(cid<<4) & 0xf0, 0x61}
	return packet(TypeCompressedIP, append(body, payload...)...)
}

// Almost every packet of a TLV stream is fully compressed, and the packet that
// establishes the context arrives only now and then, so a sample starting in
// the middle has no context for a while. The payload is intact regardless --
// only the addresses are missing -- and discarding it would throw away the
// signalling that names the services.
func TestFullyCompressedPacketSurvivesAMissingContext(t *testing.T) {
	reader := NewReader(compressedIPv6(0x321, 0x11, 0x22, 0x33))
	tlvPacket, ok := reader.Next()
	if !ok {
		t.Fatal("the packet was not read")
	}
	datagram, ok := reader.Datagram(tlvPacket)
	if !ok {
		t.Fatal("a fully compressed packet with no context should still yield its payload")
	}
	if got := string(datagram.Payload); got != "\x11\x22\x33" {
		t.Fatalf("payload = %x, want 112233", datagram.Payload)
	}
	if len(datagram.Source) != 0 || len(datagram.Destination) != 0 {
		t.Errorf("addresses were invented: %x -> %x", datagram.Source, datagram.Destination)
	}
	if !datagram.HasCID || datagram.CID != 0x321 {
		t.Errorf("CID = %#x (present %v), want 0x321", datagram.CID, datagram.HasCID)
	}
	if reader.Stats().CompressedNoContext != 1 {
		t.Errorf("the missing context was not counted: %+v", reader.Stats())
	}
}

// The other half: once the addresses have been transmitted they are applied to
// the packets that follow.
func TestFullyCompressedPacketUsesTheContextOnceSeen(t *testing.T) {
	// A partial IPv6 packet carries the addresses and ports, and establishes
	// the context for the identifier it names.
	body := make([]byte, 45)
	body[0], body[1], body[2] = 0x32, 0x10, 0x60
	source := body[9:25]
	destination := body[25:41]
	for index := range source {
		source[index] = byte(index + 1)
		destination[index] = byte(index + 0x40)
	}
	body[41], body[42] = 0x12, 0x34
	body[43], body[44] = 0x56, 0x78

	stream := append(packet(TypeCompressedIP, body...), compressedIPv6(0x321, 0xaa)...)
	reader := NewReader(stream)
	for index := 0; index < 2; index++ {
		tlvPacket, ok := reader.Next()
		if !ok {
			t.Fatalf("packet %d was not read", index)
		}
		datagram, ok := reader.Datagram(tlvPacket)
		if !ok {
			t.Fatalf("packet %d did not parse", index)
		}
		if index == 1 {
			if datagram.SourcePort != 0x1234 || datagram.DestinationPort != 0x5678 {
				t.Fatalf("ports = %#x -> %#x, want 0x1234 -> 0x5678", datagram.SourcePort, datagram.DestinationPort)
			}
			if len(datagram.Source) != 16 || datagram.Source[0] != 1 {
				t.Fatalf("the context address was not applied: %x", datagram.Source)
			}
		}
	}
	if reader.Stats().CompressedNoContext != 0 {
		t.Errorf("a context that was seen should not be counted as missing: %+v", reader.Stats())
	}
}

// A sample begins wherever the tuner happened to be: what comes before the
// first packet is counted, but is not an error.
func TestARunThatBeginsMidPacketIsNotAnError(t *testing.T) {
	buffer := append([]byte{0x12, 0x34, 0x56}, packet(TypeNull, 0, 0)...)
	reader := NewReader(buffer)
	if got, ok := reader.Next(); !ok || got.Type != TypeNull {
		t.Fatalf("packet = %#v, ok = %t", got, ok)
	}
	if stats := reader.Stats(); stats.InvalidPackets != 0 || stats.BeforeSync != 3 {
		t.Fatalf("stats = %+v, want no invalid and 3 bytes before sync", stats)
	}
}

// Once the framing has been found, bytes that break it are an error.
func TestLosingTheFramingAfterItWasFoundIsAnError(t *testing.T) {
	buffer := packet(TypeNull, 0, 0)
	buffer = append(buffer, packet(TypeControl, 0x40, 0x01)...)
	buffer = append(buffer, 0x12, 0x34, 0x56)
	buffer = append(buffer, packet(TypeNull, 0, 0)...)
	reader := NewReader(buffer)
	for {
		if _, ok := reader.Next(); !ok {
			break
		}
	}
	if stats := reader.Stats(); stats.Packets != 3 || stats.InvalidPackets != 1 || stats.BeforeSync != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

// A sync byte alone is no evidence: taken for a start, it would read a length
// out of a payload and step over the real packet after it.
func TestASyncByteInAPayloadIsNotTakenForAStart(t *testing.T) {
	buffer := []byte{SyncByte, TypeIPv4, 0x00, 0x02, 0xaa, 0xbb, 0xcc}
	buffer = append(buffer, packet(TypeNull, 0, 0)...)
	buffer = append(buffer, packet(TypeControl, 0x40, 0x01)...)
	reader := NewReader(buffer)
	if got, ok := reader.Next(); !ok || got.Type != TypeNull {
		t.Fatalf("the false start was taken: %#v, %t", got, ok)
	}
	if got, ok := reader.Next(); !ok || got.Type != TypeControl {
		t.Fatalf("second packet = %#v, %t", got, ok)
	}
	if reader.Stats().InvalidPackets != 0 {
		t.Fatalf("stats = %+v", reader.Stats())
	}
}

// A candidate whose confirmation has not arrived is not stepped over.
func TestAnUnconfirmedStartWaitsForTheBytesThatWouldConfirmIt(t *testing.T) {
	buffer := append(packet(TypeNull, 0, 0), SyncByte)
	reader := NewReader(buffer)
	if got, ok := reader.Next(); ok {
		t.Fatalf("a packet was taken before it could be confirmed: %#v", got)
	}
	if stats := reader.Stats(); stats.Packets != 0 || stats.BeforeSync != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}
