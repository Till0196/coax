// Copyright 2026 KASHIWAYA Rikuto
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package ut100b

import (
	"bytes"
	"testing"
)

// A null packet off the board: PID 0x1fff, every payload byte 0xff before the
// IC enciphered it. The head key turns each eight 0xff into one block and the
// tail key into another, so the payload is two blocks repeated.
var (
	nullHeadBlock = []byte{0x4e, 0x8c, 0x28, 0x31, 0x35, 0x43, 0xbf, 0x56}
	nullTailBlock = []byte{0xe9, 0x12, 0xe3, 0x59, 0x9d, 0x94, 0xd7, 0x8c}
)

func enciphered(header []byte) []byte {
	packet := append([]byte(nil), header...)
	packet = append(packet, bytes.Repeat(nullHeadBlock, 16)...)
	return append(packet, bytes.Repeat(nullTailBlock, 7)...)
}

func TestDescrambleNullPacket(t *testing.T) {
	packet := enciphered([]byte{0x47, 0x1f, 0xff, 0x10})
	newDescrambler().packet(packet)
	if !bytes.Equal(packet[:4], []byte{0x47, 0x1f, 0xff, 0x10}) {
		t.Fatalf("the header changed: % x", packet[:4])
	}
	if !bytes.Equal(packet[4:], bytes.Repeat([]byte{0xff}, packetSize-4)) {
		t.Fatalf("payload: % x", packet[4:])
	}
}

// recfriioDecrypt is what recfriio's decrypt.c, which spelled the same DES out
// by hand, made of a packet whose byte i is i*7+3 after the sync byte.
var recfriioDecrypt = []byte{
	0x47, 0x0a, 0x11, 0x18, 0x68, 0xbb, 0xf6, 0x74, 0xe6, 0x78, 0x16, 0xe7, 0xed, 0x4b, 0x6e, 0x0e,
	0x14, 0x88, 0xba, 0xa2, 0xe0, 0xdb, 0x30, 0x5a, 0x45, 0xba, 0xe7, 0x4e, 0x08, 0x61, 0xe2, 0x0d,
	0xe8, 0x0c, 0x24, 0x83, 0xa8, 0x4b, 0x6d, 0xdc, 0xc4, 0x12, 0xab, 0x4d, 0xed, 0xa1, 0xbd, 0xb6,
	0x4d, 0xad, 0x0b, 0x61, 0x65, 0xc5, 0xfd, 0x07, 0xd2, 0x8a, 0x32, 0x4b, 0x36, 0xcf, 0x62, 0x83,
	0x46, 0xa8, 0x3f, 0xae, 0xb9, 0x95, 0x48, 0xf5, 0xeb, 0x5e, 0xce, 0x70, 0x45, 0x8c, 0xb2, 0x15,
	0xef, 0xa3, 0x5a, 0x69, 0x42, 0xce, 0x6c, 0x20, 0x2a, 0xab, 0x64, 0x3c, 0xe1, 0x75, 0x76, 0xc2,
	0x23, 0xed, 0x55, 0x93, 0xcd, 0xf7, 0x93, 0x85, 0x70, 0x5a, 0xf9, 0x57, 0x54, 0x6f, 0x38, 0x92,
	0xbf, 0x8d, 0xe1, 0x1b, 0x4b, 0xd7, 0x95, 0xe0, 0x15, 0x46, 0x14, 0xe9, 0x84, 0x37, 0x7a, 0x1b,
	0xae, 0xce, 0xbd, 0x3c, 0x53, 0x34, 0x44, 0x28, 0xef, 0x5a, 0xa0, 0x2f, 0x40, 0x64, 0x15, 0x83,
	0xfa, 0xaf, 0x3c, 0xbc, 0x7e, 0x5d, 0xa1, 0x49, 0xa1, 0x93, 0xe1, 0xf2, 0xf6, 0x80, 0xf1, 0xdb,
	0x7a, 0x97, 0x32, 0xdc, 0xbe, 0xe5, 0x76, 0xa1, 0xb0, 0x3b, 0xf0, 0xc7, 0x31, 0x09, 0x36, 0xce,
	0x4e, 0x68, 0xb1, 0x85, 0xaa, 0x7b, 0x01, 0x0c, 0x3b, 0x18, 0xa2, 0x13,
}

func TestDescrambleAsRecfriio(t *testing.T) {
	packet := make([]byte, packetSize)
	for index := range packet {
		packet[index] = byte(index*7 + 3)
	}
	packet[0] = syncByte
	newDescrambler().packet(packet)
	if !bytes.Equal(packet, recfriioDecrypt) {
		t.Fatalf("got % x", packet)
	}
}

// stream is count enciphered null packets with their continuity counters
// running, as the bridge would deliver them.
func stream(count int) []byte {
	var data []byte
	for index := range count {
		data = append(data, enciphered([]byte{0x47, 0x1f, 0xff, 0x10 | byte(index&0x0f)})...)
	}
	return data
}

// checkNulls checks that data is whole deciphered null packets, counters
// running on from first.
func checkNulls(t *testing.T, data []byte, first int) {
	t.Helper()
	if len(data)%packetSize != 0 {
		t.Fatalf("%d bytes is not whole packets", len(data))
	}
	for at := 0; at < len(data); at += packetSize {
		packet := data[at : at+packetSize]
		want := []byte{0x47, 0x1f, 0xff, 0x10 | byte((first+at/packetSize)&0x0f)}
		if !bytes.Equal(packet[:4], want) || !bytes.Equal(packet[4:], bytes.Repeat([]byte{0xff}, packetSize-4)) {
			t.Fatalf("packet %d: % x", at/packetSize, packet[:8])
		}
	}
}

func TestPacketizerDropsStaleAndSplits(t *testing.T) {
	packets := newPacketizer()
	// The stale part is not a whole number of packets, so what follows it starts
	// part of the way into one.
	stalePackets := staleBytes/packetSize + 1
	data := stream(stalePackets + 200)
	// Arrives in reads that do not line up with packets.
	var out []byte
	buffer := make([]byte, 1000)
	for at := 0; at < len(data); at += 777 {
		packets.feed(data[at:min(at+777, len(data))])
		for {
			moved := packets.take(buffer)
			if moved == 0 {
				break
			}
			out = append(out, buffer[:moved]...)
		}
	}
	checkNulls(t, out, stalePackets)
	if len(out) < 190*packetSize {
		t.Fatalf("only %d packets came through", len(out)/packetSize)
	}
}

func TestPacketizerResynchronises(t *testing.T) {
	packets := newPacketizer()
	packets.stale = 0
	data := stream(20)
	// A few bytes lost in the middle of packet 5.
	broken := append(append([]byte(nil), data[:5*packetSize+50]...), data[5*packetSize+60:]...)
	packets.feed(broken)
	out := make([]byte, len(broken))
	moved := packets.take(out)
	out = out[:moved]
	// Packets 0 to 4 come through, packet 5 is lost, and the rest from 6 on.
	checkNulls(t, out[:5*packetSize], 0)
	checkNulls(t, out[5*packetSize:], 6)
	if packets.resyncs != 1 {
		t.Fatalf("%d resyncs", packets.resyncs)
	}
}
