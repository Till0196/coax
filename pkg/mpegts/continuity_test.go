// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import "testing"

func counted(pid uint16, counter byte) []byte {
	packet := make([]byte, PacketSize)
	packet[0] = SyncByte
	packet[1], packet[2] = byte(pid>>8), byte(pid)
	packet[3] = 0x10 | counter&0x0f
	return packet
}

func countedStream(pid uint16, counters ...byte) []byte {
	var out []byte
	for _, counter := range counters {
		out = append(out, counted(pid, counter)...)
	}
	return out
}

func TestAStreamThatArrivedWholeHasNothingMissing(t *testing.T) {
	var counting Continuity
	counting.Feed(countedStream(0x100, 0, 1, 2, 3, 4, 5, 6, 7))
	if counting.Packets() != 8 || counting.Errors() != 0 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

func TestASkippedCounterIsAsManyPacketsAsItSkipped(t *testing.T) {
	var counting Continuity
	counting.Feed(countedStream(0x100, 0, 1, 5, 6))
	if counting.Packets() != 4 || counting.Errors() != 3 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

func TestTheCounterWrapsRoundWithoutThatBeingALoss(t *testing.T) {
	var counting Continuity
	counting.Feed(countedStream(0x100, 14, 15, 0, 1))
	if counting.Errors() != 0 {
		t.Errorf("errors %d", counting.Errors())
	}
}

func TestEachStreamIsCountedOnItsOwn(t *testing.T) {
	var counting Continuity
	for step := range byte(4) {
		counting.Feed(counted(0x100, step))
		counting.Feed(counted(0x101, step))
	}
	if counting.Packets() != 8 || counting.Errors() != 0 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

func TestStuffingIsNotCounted(t *testing.T) {
	var counting Continuity
	counting.Feed(countedStream(NullPID, 0, 0, 0, 0))
	if counting.Packets() != 0 || counting.Errors() != 0 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

func TestAPacketSplitAcrossReadsIsStillOnePacket(t *testing.T) {
	whole := countedStream(0x100, 0, 1, 2, 3, 4)
	var counting Continuity
	counting.Feed(whole[:100])
	counting.Feed(whole[100:300])
	counting.Feed(whole[300:])
	if counting.Packets() != 5 || counting.Errors() != 0 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

// A stream picked up part way begins wherever it begins, and the lost boundary
// is one break.
func TestAReaderThatJoinedMidPacketFindsTheNextOne(t *testing.T) {
	whole := countedStream(0x100, 0, 1, 2, 3, 4, 5)
	var counting Continuity
	counting.Feed(whole[40:])
	if counting.Packets() != 5 || counting.Errors() != 1 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

// A 0x47 in a payload is not taken for a boundary: the packets that would
// follow it do not line up.
func TestASyncByteInAPayloadIsNotTakenForABoundary(t *testing.T) {
	first := counted(0x100, 0)
	first[10] = SyncByte
	stream := append(first[10:], countedStream(0x100, 1, 2, 3, 4)...)
	var counting Continuity
	counting.Feed(stream)
	if counting.Packets() != 4 || counting.Errors() != 1 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

// Losing the boundary forgets the counters: the packet after a break does not
// follow the packet before it, and the break is counted once.
func TestLosingTheBoundaryForgetsTheCounters(t *testing.T) {
	stream := countedStream(0x100, 0, 1, 2, 3)
	stream = append(stream, 0x00, 0x11, 0x22)
	stream = append(stream, countedStream(0x100, 9, 10, 11, 12)...)
	var counting Continuity
	counting.Feed(stream)
	if counting.Packets() != 8 || counting.Errors() != 1 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

func TestAPacketMarkedInErrorCountsAgainstTheCarrier(t *testing.T) {
	broken := counted(0x100, 1)
	broken[1] |= 0x80
	stream := append(counted(0x100, 0), broken...)
	stream = append(stream, countedStream(0x100, 2, 3)...)
	var counting Continuity
	counting.Feed(stream)
	if counting.Packets() != 4 || counting.Errors() != 1 {
		t.Errorf("packets %d, errors %d", counting.Packets(), counting.Errors())
	}
}

func TestAHandfulOfPacketsSaysNothingAboutTheSignal(t *testing.T) {
	var counting Continuity
	counting.Feed(countedStream(0x100, 0, 1, 2, 3))
	if _, ok := counting.Losses(); ok {
		t.Error("a share was given from a handful of packets")
	}
}
