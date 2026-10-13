// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/pkg/mpegts"
)

func TestParseHeader(t *testing.T) {
	packet := make([]byte, mpegts.PacketSize)
	packet[0], packet[2], packet[3] = 0x47, HeaderPID, 0x10
	payload := packet[4:]
	payload[0], payload[1], payload[2] = 0x1a, 0x86, 0x01
	payload[5], payload[6], payload[7], payload[8] = 0x40, 0x20, 0, 4
	for index := 0; index < 26; index++ {
		payload[69+index] = 0x11
	}
	payload[121], payload[126] = 0x80, 0x11
	binary.BigEndian.PutUint32(payload[180:], mpegts.CRC32(payload[:180]))
	header := Parse(packet, 7)
	if header == nil || header.PacketIndex != 7 || len(header.RelativeStreams) != 1 || header.RelativeStreams[0].StreamID != 0x4020 {
		t.Fatalf("header = %#v", header)
	}
}

// tlvFrameHeader builds a frame header whose only relative stream is TLV and
// owns every slot.
func tlvFrameHeader(t *testing.T) []byte {
	t.Helper()
	packet := make([]byte, mpegts.PacketSize)
	packet[0], packet[2], packet[3] = 0x47, HeaderPID, 0x10
	payload := packet[4:]
	payload[0], payload[1], payload[2] = 0x1a, 0x86, 0x01
	payload[5], payload[6], payload[7], payload[8] = 0x40, 0x20, 0, 4
	for index := 0; index < 26; index++ {
		payload[69+index] = 0x11 // every slot belongs to relative stream 1
	}
	// Leaving the stream-type bit clear marks relative stream 1 as TLV.
	payload[121], payload[126] = 0x00, 0x11
	binary.BigEndian.PutUint32(payload[180:], mpegts.CRC32(payload[:180]))
	return packet
}

// tlvSlotPacket builds a TSMF slot packet carrying TLV bytes.
func tlvSlotPacket(start bool, fill byte) []byte {
	packet := make([]byte, mpegts.PacketSize)
	packet[0] = 0x47
	packet[1] = byte(TLVPID >> 8)
	if start {
		packet[1] |= 0x40
	}
	packet[2] = byte(TLVPID)
	packet[3] = 0x10
	for index := 4; index < len(packet); index++ {
		packet[index] = fill
	}
	return packet
}

// TestTLVPayloadReportsTheUnitStart pins the byte layout, which differs by one
// between a packet that starts a TLV unit and one that continues it.
func TestTLVPayloadReportsTheUnitStart(t *testing.T) {
	payload, start := TLVPayload(tlvSlotPacket(true, 0xaa))
	if !start {
		t.Error("a packet with the unit-start flag should report it")
	}
	if len(payload) != mpegts.PacketSize-4 {
		t.Errorf("starting payload is %d bytes, want %d", len(payload), mpegts.PacketSize-4)
	}
	payload, start = TLVPayload(tlvSlotPacket(false, 0xaa))
	if start {
		t.Error("a continuing packet should not report a unit start")
	}
	if len(payload) != mpegts.PacketSize-3 {
		t.Errorf("continuing payload is %d bytes, want %d", len(payload), mpegts.PacketSize-3)
	}
	if payload, _ := TLVPayload(make([]byte, mpegts.PacketSize)); payload != nil {
		t.Error("a packet without the sync byte should yield nothing")
	}
}

// TestDemultiplexSkipsTLVBytesBeforeAUnitStart checks that a frame whose TLV
// packets begin mid-unit contributes nothing until a start arrives.
func TestDemultiplexSkipsTLVBytesBeforeAUnitStart(t *testing.T) {
	header := tlvFrameHeader(t)
	packets := [][]byte{
		header,
		tlvSlotPacket(false, 0x11),
		tlvSlotPacket(false, 0x22),
		tlvSlotPacket(true, 0x33),
		tlvSlotPacket(false, 0x44),
	}
	parsed := Parse(header, 0)
	if parsed == nil {
		t.Fatal("the frame header did not parse")
	}
	_, streams := Demultiplex(packets, parsed)
	want := (mpegts.PacketSize - 4) + (mpegts.PacketSize - 3)
	if got := len(streams.TLV[1]); got != want {
		t.Fatalf("collected %d TLV bytes, want %d (only from the unit start onwards)", got, want)
	}
}

// frameHeader builds a header whose slots are all relative stream 1, a
// transport stream, with what the caller wants changed before the check is
// worked out.
func frameHeader(change func(payload []byte)) []byte {
	packet := make([]byte, mpegts.PacketSize)
	packet[0], packet[2], packet[3] = 0x47, HeaderPID, 0x10
	payload := packet[4:]
	payload[0], payload[1], payload[2] = 0x1a, 0x86, 0x01
	payload[5], payload[6], payload[7], payload[8] = 0x40, 0x20, 0, 4
	for index := 0; index < 26; index++ {
		payload[69+index] = 0x11
	}
	payload[121], payload[126] = 0x80, 0x11
	if change != nil {
		change(payload)
	}
	binary.BigEndian.PutUint32(payload[180:], mpegts.CRC32(payload[:180]))
	return packet
}

// Everything the header says is read, whether or not the demultiplexing uses
// it.
func TestTheWholeOfAFrameHeaderIsRead(t *testing.T) {
	header := Parse(frameHeader(func(payload []byte) {
		payload[2] = 0x2<<5 | 0x10 | 0x01 // version 2, relative mode 1, frame type 1
		payload[65] = 0x40                // relative stream 1: poor reception
		payload[68] = 0x01                // emergency warning
	}), 0)
	if header == nil {
		t.Fatal("the header was refused")
	}
	if header.Version != 2 || header.RelativeMode != 1 || header.FrameType != 1 || !header.Emergency || header.Sync != 0x1a86 {
		t.Fatalf("header = %#v", header)
	}
	if header.RelativeStreams[0].ReceiveStatus != 1 {
		t.Fatalf("receive status = %d, want 1", header.RelativeStreams[0].ReceiveStatus)
	}
}

// A split frame naming a group that cannot be keeps its header; only the
// group is left unsaid.
func TestAGroupOutOfRangeLeavesTheHeader(t *testing.T) {
	header := Parse(frameHeader(func(payload []byte) {
		payload[2] = 0x02
		payload[123], payload[124], payload[125] = 9, 3, 5 // fifth of three
	}), 0)
	if header == nil {
		t.Fatal("the header was refused for its group")
	}
	if header.Grouped || header.NumberOfCarriers != 1 || header.CarrierSequence != 1 || header.GroupID != 0 {
		t.Fatalf("header = %#v", header)
	}
	if len(header.RelativeStreams) != 1 {
		t.Fatalf("streams = %#v", header.RelativeStreams)
	}
}

// A header that arrives before the slots run out starts the frame again, and
// a frame that runs out without one places nothing more.
func TestAHeaderMidFrameStartsTheFrameAgain(t *testing.T) {
	slot := func(marker byte) []byte {
		packet := make([]byte, mpegts.PacketSize)
		packet[0], packet[1], packet[2], packet[3] = 0x47, 0x01, 0x00, 0x10
		packet[4] = marker
		return packet
	}
	packets := [][]byte{frameHeader(nil), slot(1), slot(2), frameHeader(nil)}
	for range SlotCount {
		packets = append(packets, slot(3))
	}
	packets = append(packets, slot(4)) // past the end of the frame, no header
	_, streams := Demultiplex(packets, Parse(packets[0], 0))
	kept := streams.TS[1]
	if streams.Frames != 2 || len(kept) != 2+SlotCount {
		t.Fatalf("frames = %d, slots kept = %d", streams.Frames, len(kept))
	}
	if kept[len(kept)-1][4] != 3 {
		t.Fatal("a packet past the end of the frame was placed")
	}
}
