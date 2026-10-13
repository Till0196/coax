// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tlv

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// wrap builds one TLV packet around a payload.
func wrap(kind byte, payload []byte) []byte {
	built := []byte{SyncByte, kind, 0, 0}
	binary.BigEndian.PutUint16(built[2:4], uint16(len(payload)))
	return append(built, payload...)
}

// mmtp builds the fixed part of an MMTP header and some payload after it.
func mmtp(identifier uint16, number uint32) []byte {
	packet := make([]byte, 32)
	packet[0] = 0x00 // no counter, no extension
	packet[1] = 0x00 // payload type 0, media
	binary.BigEndian.PutUint16(packet[2:4], identifier)
	binary.BigEndian.PutUint32(packet[4:8], 0x11223344) // the delivery timestamp
	binary.BigEndian.PutUint32(packet[8:12], number)
	return packet
}

// compressed wraps an MMTP packet the way almost every packet of a Japanese
// TLV multiplex is wrapped: a context identifier and nothing else.
func compressed(identifier uint16, number uint32) []byte {
	header := []byte{0x00, 0x10, 0x21} // context 1, no compressed header
	return wrap(TypeCompressedIP, append(header, mmtp(identifier, number)...))
}

// datagramV6 wraps an MMTP packet in a plain IPv6 UDP datagram addressed to a
// port, which is how the parts of a multiplex that are not header-compressed
// arrive.
func datagramV6(address byte, port uint16, payload []byte) []byte {
	header := make([]byte, 48)
	header[0], header[6] = 0x60, 17 // IPv6, next header UDP
	header[24], header[39] = 0xff, address
	binary.BigEndian.PutUint16(header[42:44], port)
	return wrap(TypeIPv6, append(header, payload...))
}

// null is stuffing.
func null(size int) []byte { return wrap(TypeNull, bytes.Repeat([]byte{0xff}, size)) }

// stream builds a stream of compressed MMTP packets with the sequence advancing.
func stream(identifier uint16, count int) []byte {
	var built []byte
	for index := range count {
		built = append(built, compressed(identifier, uint32(index))...)
	}
	return built
}

func TestACleanStreamHasNothingWrongWithIt(t *testing.T) {
	analyser := New()
	analyser.Feed(stream(0x1000, 50))

	if !analyser.Synced() {
		t.Fatal("a stream of whole packets did not line up")
	}
	stats := analyser.Stats()
	if stats.Packets != 50 || stats.CompressedIP != 50 {
		t.Errorf("saw %d packets (%d compressed), wanted 50", stats.Packets, stats.CompressedIP)
	}
	if stats.Skipped != 0 || stats.SyncLosses != 0 || stats.Discontinuities != 0 || stats.Undecodable != 0 {
		t.Errorf("a clean stream reported %+v", stats)
	}
}

// A read boundary is not a packet boundary, and TLV packets are not all the
// same length, so both halves of the carry have to work.
func TestPacketsSplitAcrossReadsAreStillPackets(t *testing.T) {
	data := stream(0x1000, 40)
	analyser := New()
	for offset := 0; offset < len(data); offset += 13 {
		analyser.Feed(data[offset:min(offset+13, len(data))])
	}
	stats := analyser.Stats()
	if stats.Packets != 40 {
		t.Errorf("saw %d packets across ragged reads, wanted 40", stats.Packets)
	}
	if stats.SyncLosses != 0 || stats.Discontinuities != 0 {
		t.Errorf("ragged reads reported %+v", stats)
	}
}

func TestAStreamThatStartsMidPacketFindsTheBoundary(t *testing.T) {
	data := append([]byte{0x00, 0x7f, 0x01, 0xff, 0x7f}, stream(0x1000, 20)...)
	analyser := New()
	analyser.Feed(data)

	if !analyser.Synced() {
		t.Fatal("a stream with rubbish in front of it did not line up")
	}
	if got := analyser.Stats().Skipped; got != 5 {
		t.Errorf("skipped %d bytes, wanted the 5 in front", got)
	}
	if got := analyser.Stats().Packets; got != 20 {
		t.Errorf("saw %d packets, wanted 20", got)
	}
}

// This is what the whole package is for: a demodulator locked to a lead with
// nothing on it hands over noise, and noise has plenty of 0x7f in it.
func TestNoiseNeverLinesUp(t *testing.T) {
	noise := make([]byte, 512*1024)
	source := rand.New(rand.NewSource(1))
	source.Read(noise)

	analyser := New()
	analyser.Feed(noise)

	if analyser.Synced() {
		t.Error("noise was taken for a TLV stream")
	}
	if got := analyser.Stats().Packets; got != 0 {
		t.Errorf("found %d packets in noise", got)
	}
}

// A length that came out of noise must not send the search off into the
// distance, and must not be believed just because the byte in front of it was
// 0x7f.
func TestALengthOutOfNoiseDoesNotCarryTheSearchAway(t *testing.T) {
	data := []byte{SyncByte, TypeIPv4, 0xff, 0xff}
	data = append(data, bytes.Repeat([]byte{0x00}, 8192)...)
	data = append(data, stream(0x1000, 10)...)

	analyser := New()
	analyser.Feed(data)

	if !analyser.Synced() {
		t.Fatal("the real packets after the bad length were never found")
	}
	if got := analyser.Stats().Packets; got != 10 {
		t.Errorf("saw %d packets, wanted the 10 real ones", got)
	}
}

func TestABreakInTheMiddleIsOneSyncLoss(t *testing.T) {
	data := stream(0x1000, 20)
	data = append(data, bytes.Repeat([]byte{0xa5}, 1000)...)
	data = append(data, stream(0x1000, 20)...)

	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.SyncLosses != 1 {
		t.Errorf("a single burst of rubbish was reported as %d sync losses", stats.SyncLosses)
	}
	if stats.Packets != 40 {
		t.Errorf("saw %d packets around the break, wanted 40", stats.Packets)
	}
	// The sequence numbers restart at the break rather than being compared
	// across it, so the run after it is not a discontinuity.
	if stats.Discontinuities != 0 {
		t.Errorf("the break was counted as %d discontinuities as well", stats.Discontinuities)
	}
}

func TestAMissingMMTPPacketIsADiscontinuity(t *testing.T) {
	var data []byte
	for number := range uint32(10) {
		if number == 4 {
			continue // the packet that went missing
		}
		data = append(data, compressed(0x1000, number)...)
	}
	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 1 {
		t.Errorf("a missing packet gave %d discontinuities, wanted 1", got)
	}
}

// Two identifiers are two runs, and one is not checked against the other.
func TestSequencesAreKeptPerPacketIdentifier(t *testing.T) {
	var data []byte
	for number := range uint32(10) {
		data = append(data, compressed(0x1000, number)...)
		data = append(data, compressed(0x2000, number+5000)...)
	}
	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("interleaved identifiers gave %d discontinuities", got)
	}
}

func TestADuplicatedPacketIsNotADiscontinuity(t *testing.T) {
	data := compressed(0x1000, 0)
	data = append(data, compressed(0x1000, 1)...)
	data = append(data, compressed(0x1000, 1)...)
	data = append(data, compressed(0x1000, 2)...)

	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("a duplicated packet gave %d discontinuities", got)
	}
}

// The counter is 32 bits, so it wraps once every four thousand million
// packets rather than every sixteen.
func TestTheSequenceNumberWrapsWithoutBeingABreak(t *testing.T) {
	data := compressed(0x1000, 0xfffffffe)
	data = append(data, compressed(0x1000, 0xffffffff)...)
	data = append(data, compressed(0x1000, 0)...)
	data = append(data, compressed(0x1000, 1)...)

	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("the counter wrapping gave %d discontinuities", got)
	}
}

func TestStuffingAndSignallingAreCountedButNotFollowed(t *testing.T) {
	var data []byte
	for range 4 {
		data = append(data, null(100)...)
		data = append(data, wrap(TypeControl, bytes.Repeat([]byte{0x11}, 40))...)
	}
	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.Nulls != 4 || stats.Control != 4 {
		t.Errorf("counted %d nulls and %d control packets, wanted 4 of each", stats.Nulls, stats.Control)
	}
	if stats.Undecodable != 0 || stats.Discontinuities != 0 {
		t.Errorf("stuffing was walked as though it carried MMTP: %+v", stats)
	}
}

// The plain forms have to work too, even though a Japanese multiplex hardly
// uses them.
func TestPlainIPv4AndIPv6CarryTheirMMTPToo(t *testing.T) {
	v4 := make([]byte, 28)
	v4[0], v4[9] = 0x45, 17 // IPv4, twenty-byte header, UDP
	packet := wrap(TypeIPv4, append(v4, mmtp(0x3000, 0)...))
	packet = append(packet, wrap(TypeIPv4, append(v4, mmtp(0x3000, 2)...))...)

	v6 := make([]byte, 48)
	v6[0], v6[6] = 0x60, 17 // IPv6, next header UDP
	packet = append(packet, wrap(TypeIPv6, append(v6, mmtp(0x4000, 0)...))...)
	packet = append(packet, wrap(TypeIPv6, append(v6, mmtp(0x4000, 1)...))...)

	analyser := New()
	analyser.Feed(packet)

	stats := analyser.Stats()
	if stats.IPv4 != 2 || stats.IPv6 != 2 {
		t.Errorf("counted %d IPv4 and %d IPv6 packets, wanted 2 of each", stats.IPv4, stats.IPv6)
	}
	if stats.Undecodable != 0 {
		t.Errorf("%d packets could not be walked", stats.Undecodable)
	}
	if stats.Discontinuities != 1 {
		t.Errorf("the gap in the IPv4 run gave %d discontinuities, wanted 1", stats.Discontinuities)
	}
}

// A datagram this cannot walk is counted rather than passed over, so that a
// stream being read wrongly cannot look like a stream with nothing wrong.
func TestADatagramThatCannotBeWalkedIsCounted(t *testing.T) {
	odd := []byte{0x00, 0x10, 0x7f} // a compressed header type that is not one of ours
	data := wrap(TypeCompressedIP, append(odd, mmtp(0x1000, 0)...))
	data = append(data, stream(0x2000, 4)...)

	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Undecodable; got != 1 {
		t.Errorf("counted %d undecodable packets, wanted 1", got)
	}
}

func TestHuntingDoesNotHoardBytes(t *testing.T) {
	analyser := New()
	for range 100 {
		analyser.Feed(bytes.Repeat([]byte{SyncByte, TypeNull, 0xff, 0xff}, 1024))
	}
	if len(analyser.buffer) > huntLimit+maxPacket {
		t.Errorf("held %d bytes while hunting", len(analyser.buffer))
	}
}

// TestRunsAreKeptPerFlowAsWellAsIdentifier is the bug a real recording found.
//
// An MMTP packet identifier is unique inside its own package, not across the
// multiplex, so two flows using the same numbers are two runs. Keying on the
// identifier alone reported a break every time the multiplex alternated
// between them: eighty-one of them in sixteen megabytes of ISDB-S3 that had
// none.
func TestRunsAreKeptPerFlowAsWellAsIdentifier(t *testing.T) {
	var data []byte
	for number := range uint32(20) {
		// Two flows, the same identifier inside each, interleaved -- and each
		// one's numbers a long way from the other's.
		data = append(data, compressed(0x1000, number)...)
		data = append(data, datagramV6(1, 5000, mmtp(0x1000, number+900000))...)
	}
	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.Undecodable != 0 {
		t.Fatalf("%d packets could not be walked", stats.Undecodable)
	}
	if stats.Discontinuities != 0 {
		t.Errorf("two flows sharing an identifier gave %d discontinuities", stats.Discontinuities)
	}
}

// Two addresses on the same port are still two flows.
func TestTwoAddressesOnOnePortAreTwoRuns(t *testing.T) {
	var data []byte
	for number := range uint32(20) {
		data = append(data, datagramV6(1, 5000, mmtp(0x2000, number))...)
		data = append(data, datagramV6(2, 5000, mmtp(0x2000, number+400000))...)
	}
	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("two addresses gave %d discontinuities", got)
	}
}

// The time signal an ISDB-S3 multiplex carries beside the programmes is a
// datagram like any other and is not MMTP. Reading a sequence number out of it
// is reading a timestamp as a counter.
func TestTheTimeSignalIsNotFollowedAsMMTP(t *testing.T) {
	var data []byte
	for number := range uint32(20) {
		data = append(data, compressed(0x1000, number)...)
		// NTP, whose bytes where the sequence number would be are a reference
		// timestamp that jumps.
		data = append(data, datagramV6(3, ntpPort, mmtp(0, number*7919))...)
	}
	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.Discontinuities != 0 {
		t.Errorf("the time signal was followed as MMTP: %d discontinuities", stats.Discontinuities)
	}
	if stats.IPv6 != 20 {
		t.Errorf("counted %d IPv6 packets, wanted 20", stats.IPv6)
	}
}
