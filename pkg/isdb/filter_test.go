// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Till0196/coax/pkg/mpegts"
)

// frame builds one multiplexing frame: a header naming two relative streams,
// then SlotCount slot packets divided between them.
func frame(t *testing.T, tsidA, tsidB uint16, mark byte) []byte {
	t.Helper()
	payload := make([]byte, 184)
	payload[0], payload[1] = 0x1a, 0x86 // the frame sync word
	payload[2] = 1                      // frame type 1
	put := func(offset int, value uint16) {
		payload[offset], payload[offset+1] = byte(value>>8), byte(value)
	}
	put(5, tsidA)       // relative stream 1
	put(7, 4)           // its original network
	put(9, tsidB)       // relative stream 2
	put(11, 11)         // its original network
	payload[121] = 0xc0 // both streams are transport streams
	payload[122] = 0
	// Half the slots to each stream, in the map's two-per-byte packing.
	for slot := range SlotCount {
		relative := byte(1)
		if slot >= SlotCount/2 {
			relative = 2
		}
		if slot%2 == 0 {
			payload[69+slot/2] |= relative << 4
		} else {
			payload[69+slot/2] |= relative
		}
	}
	payload[126] = 0x11
	crc := mpegts.CRC32(payload[:180])
	copy(payload[180:], []byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)})

	stream := make([]byte, 0, (SlotCount+1)*mpegts.PacketSize)
	header := []byte{0x47, 0x40, HeaderPID, 0x10}
	stream = append(stream, append(header, payload...)...)
	for slot := range SlotCount {
		packet := make([]byte, mpegts.PacketSize)
		packet[0], packet[1], packet[2], packet[3] = 0x47, 0x00, 0x64, 0x10
		packet[4] = mark
		packet[5] = byte(slot)
		stream = append(stream, packet...)
	}
	return stream
}

func slotsOf(t *testing.T, written []byte) []byte {
	t.Helper()
	if len(written)%mpegts.PacketSize != 0 {
		t.Fatalf("wrote %d bytes, not a whole number of packets", len(written))
	}
	marks := make([]byte, 0, len(written)/mpegts.PacketSize)
	for offset := 0; offset < len(written); offset += mpegts.PacketSize {
		marks = append(marks, written[offset+5])
	}
	return marks
}

// The frame gives each of its two relative streams half the slots, and only the
// half belonging to the stream asked for comes out.
func TestFilterKeepsOnlyTheStreamAskedFor(t *testing.T) {
	var out bytes.Buffer
	filter := NewFilter(&out, 4321, nil)
	if _, err := filter.Write(frame(t, 1234, 4321, 0xaa)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	marks := slotsOf(t, out.Bytes())
	if len(marks) != SlotCount/2 {
		t.Fatalf("kept %d slots, want the %d belonging to that stream", len(marks), SlotCount/2)
	}
	// The second stream owns the second half of the frame.
	for index, slot := range marks {
		if int(slot) != SlotCount/2+index {
			t.Fatalf("kept slot %d at position %d, want %d", slot, index, SlotCount/2+index)
		}
	}
}

// A stream that is not multiplexed passes through untouched, which is what makes
// the flag safe to give whatever the delivery system: a satellite demodulator
// has already chosen by the time the bytes arrive.
func TestFilterPassesAStreamThatIsNotMultiplexed(t *testing.T) {
	plain := make([]byte, 0, decideBudget*2*mpegts.PacketSize)
	for index := range decideBudget * 2 {
		packet := make([]byte, mpegts.PacketSize)
		packet[0], packet[2], packet[5] = 0x47, 0x21, byte(index)
		plain = append(plain, packet...)
	}
	var out bytes.Buffer
	filter := NewFilter(&out, 4321, nil)
	if _, err := filter.Write(plain); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := filter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Fatalf("passed %d of %d bytes through unchanged", len(out.Bytes()), len(plain))
	}
}

// A capture that ends before it could say what it was must not lose what it did
// carry.
func TestFilterKeepsTheBeginningOfAShortStream(t *testing.T) {
	packet := make([]byte, mpegts.PacketSize)
	packet[0], packet[2] = 0x47, 0x21
	var out bytes.Buffer
	filter := NewFilter(&out, 4321, nil)
	if _, err := filter.Write(packet); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("a packet was written before the question was settled")
	}
	if err := filter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(out.Bytes(), packet) {
		t.Fatalf("Close wrote %d bytes, want the %d held back", out.Len(), len(packet))
	}
}

// Being handed an empty file and no reason is the worst answer available, so the
// error names the identifiers that are there to try instead.
func TestFilterSaysWhatIsThereWhenTheStreamIsNot(t *testing.T) {
	var out bytes.Buffer
	filter := NewFilter(&out, 9999, nil)
	_, err := filter.Write(frame(t, 1234, 4321, 0xaa))
	if err == nil {
		t.Fatal("asking for a stream that is not in the multiplex must fail")
	}
	for _, want := range []string{"9999", "TSID=1234", "TSID=4321"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %s: %v", want, err)
		}
	}
	// Closing on the way down must not leave a file of the streams nobody
	// asked for behind an error saying there is no such stream.
	if err := filter.Close(); err != nil {
		t.Fatalf("Close after a failure: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %d bytes of a multiplex that did not carry what was asked for", out.Len())
	}
}

// A file handed to something else should begin where a TLV packet begins, and a
// sync byte alone is not enough to go on: it is one value out of 256, and
// payloads are full of them.
func TestAlignTLVStartsOnAPacketBoundary(t *testing.T) {
	if got := alignTLV([]byte{0x11, 0x7f, 0x7f, 0xff, 0x00, 0x04}); len(got) != 4 || got[0] != 0x7f || got[1] != 0xff {
		t.Errorf("alignTLV cut to %#v, want the sync byte followed by a type", got)
	}
	if got := alignTLV([]byte{0x7f, 0x55, 0x11, 0x22}); got != nil {
		t.Errorf("a sync byte followed by no known type is not a boundary, got %#v", got)
	}
	if got := alignTLV([]byte{0x11, 0x22, 0x33}); got != nil {
		t.Errorf("a payload with no boundary in it gave %#v", got)
	}
}

// A stream arrives in whatever sizes the network chose, almost never in whole
// packets.
func TestFilterSurvivesWritesThatSplitPackets(t *testing.T) {
	whole := frame(t, 1234, 4321, 0xaa)
	var out bytes.Buffer
	filter := NewFilter(&out, 4321, nil)
	for offset := 0; offset < len(whole); offset += 77 {
		end := min(offset+77, len(whole))
		if _, err := filter.Write(whole[offset:end]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got, want := len(slotsOf(t, out.Bytes())), SlotCount/2; got != want {
		t.Fatalf("kept %d slots across split writes, want %d", got, want)
	}
}

// A satellite carrying MMT/TLV hands back TLV, which has no 188-byte packets and no
// sync bytes except where its own bytes happen to fall. Reframing it on those
// would destroy it, and the identifier the caller gave was meant for the
// demodulator rather than for anything here.
func TestFilterLeavesAStreamThatIsNotTransportAlone(t *testing.T) {
	// A stretch of TLV: sync byte, type, length, payload.
	stream := make([]byte, 0, 8192)
	for index := range 64 {
		packet := make([]byte, 0, 128)
		packet = append(packet, 0x7f, 0x03, 0x00, 120)
		for offset := range 120 {
			// Includes bytes that are 0x47, which is the whole difficulty.
			packet = append(packet, byte((index*120+offset)%256))
		}
		stream = append(stream, packet...)
	}
	var out bytes.Buffer
	filter := NewFilter(&out, 45168, nil)
	if _, err := filter.Write(stream); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := filter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(out.Bytes(), stream) {
		t.Fatalf("passed %d of %d bytes through, and not unchanged", out.Len(), len(stream))
	}
}
