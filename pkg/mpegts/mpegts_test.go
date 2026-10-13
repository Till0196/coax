// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import "testing"

func TestSplitPacketsRejectsUnsynchronizedData(t *testing.T) {
	if packets := SplitPackets(make([]byte, PacketSize*4)); len(packets) != 0 {
		t.Fatalf("packets = %d", len(packets))
	}
}

func TestPayload(t *testing.T) {
	packet := make([]byte, PacketSize)
	packet[0], packet[1], packet[2], packet[3], packet[4] = 0x47, 0x40, 0x11, 0x10, 0xaa
	pid, payload, start, ok := Payload(packet)
	if !ok || pid != 0x11 || !start || len(payload) != 184 || payload[0] != 0xaa {
		t.Fatalf("pid=%#x payload=%d start=%t ok=%t", pid, len(payload), start, ok)
	}
}

// samplePacket builds one transport packet with a recognisable payload.
func samplePacket(marker byte) []byte {
	packet := make([]byte, PacketSize)
	packet[0] = SyncByte
	packet[1], packet[2] = 0x00, 0x2f
	for index := 3; index < PacketSize; index++ {
		packet[index] = marker
	}
	return packet
}

// The behaviour a USB capture depends on: the device drops data when its buffer
// overruns, so a long capture contains discontinuities and resumes mid-packet.
// Stopping at the first one would throw away everything after it -- silently,
// because what survives still parses.
func TestSplitPacketsResynchronisesAfterAGap(t *testing.T) {
	var buffer []byte
	for index := 0; index < 6; index++ {
		buffer = append(buffer, samplePacket(0x11)...)
	}
	// A partial packet, as if the device dropped the rest of it.
	buffer = append(buffer, samplePacket(0x22)[:100]...)
	for index := 0; index < 6; index++ {
		buffer = append(buffer, samplePacket(0x33)...)
	}

	packets := SplitPackets(buffer)
	if len(packets) != 12 {
		t.Fatalf("split into %d packets, want 12 (six on each side of the gap)", len(packets))
	}
	for _, packet := range packets {
		if packet[0] != SyncByte {
			t.Fatalf("a packet does not begin with the sync byte: %x", packet[:4])
		}
	}
	// The packets after the gap have to be the ones that followed it, not a
	// misaligned reading of them.
	if packets[6][3] != 0x33 {
		t.Fatalf("the first packet after the gap is %#02x, want 0x33", packets[6][3])
	}
}

// TestSplitPacketsIgnoresASyncByteInsideAPayload checks that locking on takes
// more than one 0x47, which occurs freely inside payloads.
func TestSplitPacketsIgnoresASyncByteInsideAPayload(t *testing.T) {
	packet := samplePacket(SyncByte) // every payload byte is a sync byte
	var buffer []byte
	for index := 0; index < 8; index++ {
		buffer = append(buffer, packet...)
	}
	packets := SplitPackets(buffer)
	if len(packets) != 8 {
		t.Fatalf("split into %d packets, want 8", len(packets))
	}
}

// TestSplitPacketsHandlesACaptureThatStartsMidPacket covers the ordinary case
// of a sample beginning wherever the tuner happened to be.
func TestSplitPacketsHandlesACaptureThatStartsMidPacket(t *testing.T) {
	var buffer []byte
	buffer = append(buffer, samplePacket(0x44)[90:]...)
	for index := 0; index < 5; index++ {
		buffer = append(buffer, samplePacket(0x55)...)
	}
	packets := SplitPackets(buffer)
	if len(packets) != 5 {
		t.Fatalf("split into %d packets, want 5", len(packets))
	}
	if packets[0][3] != 0x55 {
		t.Fatalf("the first packet is %#02x, want 0x55", packets[0][3])
	}
}
