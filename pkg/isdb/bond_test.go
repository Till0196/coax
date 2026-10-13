// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"testing"

	"github.com/Till0196/coax/pkg/mpegts"
)

// bondFrame is a frame of one carrier whose slots hold the packets given.
func bondFrame(position, of, sequence, carriers byte, packets ...[]byte) Frame {
	frame := Frame{Grouped: true, GroupID: 7, Position: position, Of: of, Sequence: sequence, Carriers: carriers}
	for at, packet := range packets {
		frame.Slots[at] = packet
	}
	return frame
}

// countedPacket is a transport packet on one identifier, carrying the count
// given.
func countedPacket(counter byte) []byte {
	packet := make([]byte, mpegts.PacketSize)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x01, 0x00, 0x10|counter&0x0f
	return packet
}

// A superframe is a run of frames starting at nought, and a run that is not
// whole waits for the rest rather than being handed on.
func TestFramesAreMadeIntoSuperframesOnlyWhenTheRunIsWhole(t *testing.T) {
	carrier := &bondCarrier{}
	carrier.waiting = append(carrier.waiting, bondFrame(0, 2, 1, 1, countedPacket(0)))
	carrier.pack()
	if len(carrier.superframes) != 0 {
		t.Fatal("half a run is not a run")
	}
	carrier.waiting = append(carrier.waiting, bondFrame(1, 2, 1, 1, countedPacket(1)))
	carrier.pack()
	if len(carrier.superframes) != 1 || len(carrier.waiting) != 0 {
		t.Fatalf("superframes %d waiting %d", len(carrier.superframes), len(carrier.waiting))
	}
}

// Two carriers, each holding every other packet of one stream. Put back
// together in the right order the counts run on; in any other order they jump,
// which is what the bonding decides by.
func TestTwoCarriersAreLinedUpByWhatReadsAsAStream(t *testing.T) {
	bond := NewBond(CarriageTransport)
	var out []byte
	counter := byte(0)
	for range 40 {
		first := bondFrame(0, 1, 1, 2, countedPacket(counter))
		second := bondFrame(0, 1, 2, 2, countedPacket(counter+1))
		counter = (counter + 2) & 0x0f
		out = append(out, bond.Feed([]Frame{first})...)
		out = append(out, bond.Feed([]Frame{second})...)
	}
	if bond.Carriers() != 2 || len(out) == 0 || len(out)%mpegts.PacketSize != 0 {
		t.Fatalf("carriers %d, %d bytes", bond.Carriers(), len(out))
	}
	previous := -1
	for at := 0; at < len(out); at += mpegts.PacketSize {
		count := int(out[at+3] & 0x0f)
		if previous >= 0 && count != (previous+1)&0x0f {
			t.Fatalf("the count jumped from %d to %d", previous, count)
		}
		previous = count
	}
}

// A group says nothing until every carrier of it has been heard from.
func TestNothingComesOutUntilEveryCarrierHasSpoken(t *testing.T) {
	bond := NewBond(CarriageTransport)
	for counter := range byte(40) {
		if out := bond.Feed([]Frame{bondFrame(0, 1, 1, 2, countedPacket(counter))}); len(out) != 0 {
			t.Fatal("one carrier of two said something")
		}
	}
	if bond.Carriers() != 2 {
		t.Fatalf("carriers %d", bond.Carriers())
	}
}

// A carrier that stops once the group is lined up does not leave the others
// piling up for ever.
func TestACarrierThatStopsDoesNotLeaveTheOthersPilingUp(t *testing.T) {
	bond := NewBond(CarriageTransport)
	counter := byte(0)
	for range 40 {
		bond.Feed([]Frame{bondFrame(0, 1, 1, 2, countedPacket(counter))})
		bond.Feed([]Frame{bondFrame(0, 1, 2, 2, countedPacket(counter+1))})
		counter = (counter + 2) & 0x0f
	}
	if bond.offsets == nil {
		t.Fatal("the carriers were not lined up")
	}
	for range 2000 {
		bond.Feed([]Frame{bondFrame(0, 1, 1, 2, countedPacket(counter))})
		counter = (counter + 2) & 0x0f
	}
	for _, carrier := range bond.held {
		if len(carrier.superframes) > mostUnaligned {
			t.Fatalf("%d superframes held", len(carrier.superframes))
		}
	}
}

// A damaged frame naming a carrier the group does not have is not a new
// carrier.
func TestAFrameNamingACarrierOutOfRangeIsIgnored(t *testing.T) {
	bond := NewBond(CarriageTransport)
	bond.Feed([]Frame{bondFrame(0, 1, 9, 2, countedPacket(0))})
	bond.Feed([]Frame{bondFrame(0, 1, 1, 200, countedPacket(0))})
	if len(bond.held) != 0 || bond.Carriers() != 0 {
		t.Fatalf("held %d carriers %d", len(bond.held), bond.Carriers())
	}
}

// The frame reader keeps the wanted stream's slots in the places they sat,
// and hands a frame on when the next header arrives.
func TestAFrameReaderKeepsTheSlotsInTheirPlaces(t *testing.T) {
	split := frameHeader(func(payload []byte) {
		payload[2] = 0x02
		payload[69] = 0x12 // slot 0 is stream 1, slot 1 is stream 2
		payload[123], payload[124], payload[125] = 7, 2, 2
		payload[126] = 0x10 // one frame, at position 0
	})
	var stream []byte
	stream = append(stream, split...)
	for slot := range SlotCount {
		stream = append(stream, countedPacket(byte(slot))...)
	}
	stream = append(stream, split...)

	reader := NewFrameReader(1)
	frames := reader.Feed(stream[:1000])
	frames = append(frames, reader.Feed(stream[1000:])...)
	if len(frames) != 1 {
		t.Fatalf("%d frames", len(frames))
	}
	frame := frames[0]
	if !frame.Grouped || frame.GroupID != 7 || frame.Carriers != 2 || frame.Sequence != 2 || frame.Of != 1 || frame.Position != 0 {
		t.Fatalf("frame %+v", frame)
	}
	if frame.Slots[0] == nil || frame.Slots[0][3]&0x0f != 0 || frame.Slots[1] != nil {
		t.Fatal("the slots were not kept by place")
	}
}
