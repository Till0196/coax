// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import (
	"bytes"
	"encoding/binary"
	"testing"
)

type mpuUnit struct {
	sample uint32
	data   []byte
}

// mpuPacket is an MMTP packet carrying one MPU payload, built the way the
// standard says rather than the way the reader happens to read it.
func mpuPacket(packetID uint16, sequence uint32, rap bool, fragmentType byte, timed bool, fragmentation byte, aggregation bool, mpu uint32, units ...mpuUnit) []byte {
	var body []byte
	for _, unit := range units {
		var one []byte
		if fragmentType == FragmentMFU {
			if timed {
				one = binary.BigEndian.AppendUint32(one, 0)
				one = binary.BigEndian.AppendUint32(one, unit.sample)
				one = binary.BigEndian.AppendUint32(one, 0)
				one = append(one, 0, 0)
			} else {
				one = binary.BigEndian.AppendUint32(one, unit.sample)
			}
		}
		one = append(one, unit.data...)
		if aggregation {
			body = binary.BigEndian.AppendUint16(body, uint16(len(one)))
		}
		body = append(body, one...)
	}
	flags := fragmentType<<4 | fragmentation<<1
	if timed {
		flags |= 0x08
	}
	if aggregation {
		flags |= 0x01
	}
	payload := []byte{0, 0, flags, 0}
	payload = binary.BigEndian.AppendUint32(payload, mpu)
	payload = append(payload, body...)
	binary.BigEndian.PutUint16(payload, uint16(len(payload)-2))

	first := byte(0)
	if rap {
		first = 1
	}
	out := []byte{first, PayloadTypeMPU}
	out = binary.BigEndian.AppendUint16(out, packetID)
	out = binary.BigEndian.AppendUint32(out, 0)
	out = binary.BigEndian.AppendUint32(out, sequence)
	return append(out, payload...)
}

func wholeUnit(packetID uint16, sequence, mpu uint32, data string) []byte {
	return mpuPacket(packetID, sequence, true, FragmentMFU, true, FragmentationComplete, false, mpu, mpuUnit{0, []byte(data)})
}

func pushMPU(t *testing.T, reassembler *MPUReassembler, raw []byte) []AccessUnit {
	t.Helper()
	packet, ok := Parse(raw)
	if !ok {
		t.Fatal("the packet is whole")
	}
	return reassembler.Push(packet)
}

func TestATimedFragmentUnitIsReadWithItsHeader(t *testing.T) {
	unit := binary.BigEndian.AppendUint32(nil, 1)
	unit = binary.BigEndian.AppendUint32(unit, 5)
	unit = binary.BigEndian.AppendUint32(unit, 2)
	unit = append(unit, 3, 4)
	unit = append(unit, "picture"...)
	payload := []byte{0, 0, FragmentMFU<<4 | 0x08, 0}
	payload = binary.BigEndian.AppendUint32(payload, 9)
	payload = append(payload, unit...)
	binary.BigEndian.PutUint16(payload, uint16(len(payload)-2))
	read, ok := ParseMPU(payload)
	if !ok || read.FragmentType != FragmentMFU || !read.Timed || read.Aggregation || read.MPUSequenceNumber != 9 || len(read.Units) != 1 {
		t.Fatalf("read %+v %v", read, ok)
	}
	got := read.Units[0]
	if got.MovieFragmentSequenceNumber != 1 || got.SampleNumber != 5 || got.Offset != 2 || got.Priority != 3 || got.DependencyCounter != 4 || string(got.Data) != "picture" {
		t.Fatalf("unit %+v", got)
	}
}

func TestAPayloadLongerThanItsBytesIsNotAPayload(t *testing.T) {
	raw := mpuPacket(1, 0, false, FragmentMFU, true, FragmentationComplete, false, 1, mpuUnit{0, []byte("x")})
	packet, _ := Parse(raw)
	payload := append([]byte(nil), packet.Payload...)
	binary.BigEndian.PutUint16(payload, 0xffff)
	if _, ok := ParseMPU(payload); ok {
		t.Fatal("a length past the end read")
	}
	if _, ok := ParseMPU(payload[:4]); ok {
		t.Fatal("four bytes read as a payload")
	}
}

func TestASplitUnitIsPutBackTogetherFromItsPieces(t *testing.T) {
	r := NewMPUReassembler()
	if len(pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationFirst, false, 7, mpuUnit{0, []byte("one ")}))) != 0 {
		t.Fatal("first piece completed something")
	}
	pushMPU(t, r, mpuPacket(1, 1, false, FragmentMFU, true, FragmentationMiddle, false, 7, mpuUnit{0, []byte("two ")}))
	out := pushMPU(t, r, mpuPacket(1, 2, false, FragmentMFU, true, FragmentationLast, false, 7, mpuUnit{0, []byte("three")}))
	if len(out) != 1 || string(out[0].Data) != "one two three" || out[0].MPUSequenceNumber != 7 || out[0].Number != 1 {
		t.Fatalf("out %+v", out)
	}
	// The random access point is the one the unit opened with.
	if !out[0].RAP || out[0].AfterLoss {
		t.Fatalf("flags %+v", out[0])
	}
	if s := r.Stats(); s.AccessUnits != 1 || s.FragmentErrors != 0 {
		t.Fatalf("stats %+v", s)
	}
}

func TestSeveralUnitsShareOneAggregatingPayload(t *testing.T) {
	r := NewMPUReassembler()
	out := pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationComplete, true, 3,
		mpuUnit{0, []byte("a")}, mpuUnit{0, []byte("bb")}, mpuUnit{0, []byte("ccc")}))
	if len(out) != 3 || string(out[0].Data) != "a" || string(out[1].Data) != "bb" || string(out[2].Data) != "ccc" {
		t.Fatalf("out %+v", out)
	}
	for i, unit := range out {
		if unit.Number != uint32(i+1) {
			t.Fatalf("unit %d numbered %d", i, unit.Number)
		}
	}
}

func TestUnitsAreNumberedHereBecauseTheStreamLeavesItAtNought(t *testing.T) {
	r := NewMPUReassembler()
	type numbered struct{ mpu, number uint32 }
	var got []numbered
	sequence := uint32(0)
	for mpu := uint32(100); mpu < 103; mpu++ {
		for range 3 {
			for _, unit := range pushMPU(t, r, wholeUnit(1, sequence, mpu, "x")) {
				if unit.SampleNumber != 0 {
					t.Fatal("the stream numbers nothing")
				}
				got = append(got, numbered{unit.MPUSequenceNumber, unit.Number})
			}
			sequence++
		}
	}
	want := []numbered{{100, 1}, {100, 2}, {100, 3}, {101, 1}, {101, 2}, {101, 3}, {102, 1}, {102, 2}, {102, 3}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
	if r.Stats().NonZeroSamples != 0 {
		t.Fatal("non-zero samples")
	}
}

func TestAssetsAreKeptApartByTheirPacketIdentifier(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationFirst, false, 1, mpuUnit{0, []byte("aa")}))
	out := pushMPU(t, r, wholeUnit(2, 0, 1, "zz"))
	if len(out) != 1 || out[0].PacketID != 2 {
		t.Fatalf("out %+v", out)
	}
	out = pushMPU(t, r, mpuPacket(1, 1, false, FragmentMFU, true, FragmentationLast, false, 1, mpuUnit{0, []byte("bb")}))
	if len(out) != 1 || string(out[0].Data) != "aabb" || r.Stats().FragmentErrors != 0 {
		t.Fatalf("out %+v stats %+v", out, r.Stats())
	}
}

func TestAGapInThePacketsThrowsAwayWhatWasBeingHeld(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationFirst, false, 1, mpuUnit{0, []byte("aa")}))
	if len(pushMPU(t, r, mpuPacket(1, 2, false, FragmentMFU, true, FragmentationLast, false, 1, mpuUnit{0, []byte("cc")}))) != 0 {
		t.Fatal("half a unit")
	}
	s := r.Stats()
	if s.SequenceGaps != 1 || s.LostPackets != 1 || s.FragmentErrors != 2 {
		t.Fatalf("stats %+v", s)
	}
	out := pushMPU(t, r, wholeUnit(1, 3, 1, "dd"))
	if len(out) != 1 || !out[0].AfterLoss || out[0].Number != 1 {
		t.Fatalf("out %+v", out)
	}
	out = pushMPU(t, r, wholeUnit(1, 4, 1, "ee"))
	if out[0].AfterLoss || out[0].Number != 2 {
		t.Fatalf("the loss is reported once: %+v", out)
	}
}

func TestThePiecesBeforeTheFirstOpeningAreAJoinAndNotAnError(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, mpuPacket(1, 0, false, FragmentMFU, true, FragmentationMiddle, false, 1, mpuUnit{0, []byte("bb")}))
	pushMPU(t, r, mpuPacket(1, 1, false, FragmentMFU, true, FragmentationMiddle, false, 1, mpuUnit{0, []byte("cc")}))
	pushMPU(t, r, mpuPacket(1, 2, false, FragmentMFU, true, FragmentationLast, false, 1, mpuUnit{0, []byte("dd")}))
	if s := r.Stats(); s.FragmentErrors != 0 || s.AccessUnits != 0 {
		t.Fatalf("stats %+v", s)
	}
	out := pushMPU(t, r, wholeUnit(1, 3, 1, "ee"))
	if len(out) != 1 || out[0].AfterLoss {
		t.Fatalf("out %+v", out)
	}
}

func TestAPieceWithoutItsOpeningIsComplainedAboutOnce(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, wholeUnit(1, 0, 1, "aa"))
	pushMPU(t, r, mpuPacket(1, 1, false, FragmentMFU, true, FragmentationMiddle, false, 1, mpuUnit{0, []byte("bb")}))
	pushMPU(t, r, mpuPacket(1, 2, false, FragmentMFU, true, FragmentationMiddle, false, 1, mpuUnit{0, []byte("cc")}))
	pushMPU(t, r, mpuPacket(1, 3, false, FragmentMFU, true, FragmentationLast, false, 1, mpuUnit{0, []byte("dd")}))
	if s := r.Stats(); s.FragmentErrors != 1 || s.AccessUnits != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestAnOpeningWhileOneIsHeldAbandonsTheOneBeingHeld(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationFirst, false, 1, mpuUnit{0, []byte("aa")}))
	pushMPU(t, r, mpuPacket(1, 1, true, FragmentMFU, true, FragmentationFirst, false, 1, mpuUnit{0, []byte("xx")}))
	out := pushMPU(t, r, mpuPacket(1, 2, false, FragmentMFU, true, FragmentationLast, false, 1, mpuUnit{0, []byte("yy")}))
	if len(out) != 1 || string(out[0].Data) != "xxyy" || !out[0].AfterLoss || r.Stats().FragmentErrors != 1 {
		t.Fatalf("out %+v stats %+v", out, r.Stats())
	}
}

func TestAPacketThatHasGoneBackwardsIsDropped(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, wholeUnit(1, 5, 1, "aa"))
	pushMPU(t, r, wholeUnit(1, 6, 1, "bb"))
	if out := pushMPU(t, r, wholeUnit(1, 5, 1, "aa")); len(out) != 0 {
		t.Fatalf("out %+v", out)
	}
	if s := r.Stats(); s.OutOfOrderPackets != 1 || s.AccessUnits != 2 {
		t.Fatalf("stats %+v", s)
	}
}

func TestACounterThatStartedOverIsFollowed(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, wholeUnit(1, 1_999_999, 1_000_000, "aa"))
	pushMPU(t, r, wholeUnit(1, 2_000_000, 1_000_000, "bb"))
	out := pushMPU(t, r, wholeUnit(1, 80_000, 80_000, "cc"))
	if len(out) != 1 || string(out[0].Data) != "cc" || !out[0].AfterLoss {
		t.Fatalf("out %+v", out)
	}
	tally, _ := r.Tally(1)
	if s := r.Stats(); s.OutOfOrderPackets != 0 || s.RestartedCounters != 1 || tally.Restarted != 1 || tally.OutOfOrder != 0 {
		t.Fatalf("stats %+v tally %+v", s, tally)
	}
	if len(pushMPU(t, r, wholeUnit(1, 80_001, 80_001, "dd"))) != 1 {
		t.Fatal("the count does not go on")
	}
	// A leap of two million forward is the other counter again, not two
	// million packets lost.
	out = pushMPU(t, r, wholeUnit(1, 2_000_001, 1_000_001, "ee"))
	tally, _ = r.Tally(1)
	if len(out) != 1 || !out[0].AfterLoss || r.Stats().LostPackets != 0 || tally.Lost != 0 {
		t.Fatalf("out %+v stats %+v", out, r.Stats())
	}
	// A short gap is still a loss.
	pushMPU(t, r, wholeUnit(1, 2_000_006, 1_000_002, "ff"))
	if r.Stats().LostPackets != 4 {
		t.Fatalf("lost %d", r.Stats().LostPackets)
	}
}

func TestAScrambledPayloadIsNotReadAndTakesTheHeldUnitWithIt(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationFirst, false, 1, mpuUnit{0, []byte("aa")}))
	packet, _ := Parse(wholeUnit(1, 1, 1, "bb"))
	packet.Scrambled = true
	if len(r.Push(packet)) != 0 {
		t.Fatal("a scrambled payload was read")
	}
	if s := r.Stats(); s.ScrambledPackets != 1 || s.FragmentErrors != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestEachPacketIdentifierKeepsItsOwnTally(t *testing.T) {
	r := NewMPUReassembler()
	for _, one := range []struct {
		id       uint16
		sequence uint32
	}{{1, 0}, {2, 0}, {1, 1}, {1, 3}, {2, 1}} {
		pushMPU(t, r, wholeUnit(one.id, one.sequence, 0, "\x00\x00\x00\x01\x65"))
	}
	ones, _ := r.Tally(1)
	twos, _ := r.Tally(2)
	if ones.Arrived != 3 || ones.Lost != 1 || twos.Arrived != 2 || twos.Lost != 0 {
		t.Fatalf("tallies %+v %+v", ones, twos)
	}
	if _, ok := r.Tally(3); ok {
		t.Fatal("a tally for an identifier nothing arrived on")
	}
	if ids := r.PacketIDs(); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids %v", ids)
	}
	if r.Stats().LostPackets != 1 {
		t.Fatalf("lost %d", r.Stats().LostPackets)
	}
}

func TestAPayloadThatWillNotParseIsCountedAndDropped(t *testing.T) {
	r := NewMPUReassembler()
	raw := []byte{0, PayloadTypeMPU, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0x20, 0, 0, 0, 0, 1}
	if len(pushMPU(t, r, raw)) != 0 || r.Stats().ParseErrors != 1 {
		t.Fatalf("stats %+v", r.Stats())
	}
}

func TestTheBoxesThatDescribeTheMediaAreNotTheMedia(t *testing.T) {
	r := NewMPUReassembler()
	pushMPU(t, r, mpuPacket(1, 0, false, FragmentMPUMetadata, true, FragmentationComplete, false, 1, mpuUnit{0, []byte("moov")}))
	if r.Stats().MetadataFragments != 1 {
		t.Fatalf("stats %+v", r.Stats())
	}
}

func TestAnUntimedAssetArrivesWhole(t *testing.T) {
	r := NewMPUReassembler()
	out := pushMPU(t, r, mpuPacket(1, 0, false, FragmentMFU, false, FragmentationComplete, false, 1, mpuUnit{9, []byte("file")}))
	if len(out) != 1 || !bytes.Equal(out[0].Data, []byte("file")) || out[0].Timed || out[0].ItemID != 9 || r.Stats().UntimedUnits != 1 {
		t.Fatalf("out %+v", out)
	}
}

func TestAUnitLongerThanAnythingAPictureIsGetsAbandoned(t *testing.T) {
	r := NewMPUReassemblerLimit(4)
	pushMPU(t, r, mpuPacket(1, 0, true, FragmentMFU, true, FragmentationFirst, false, 1, mpuUnit{0, []byte("aa")}))
	pushMPU(t, r, mpuPacket(1, 1, false, FragmentMFU, true, FragmentationMiddle, false, 1, mpuUnit{0, []byte("bbb")}))
	pushMPU(t, r, mpuPacket(1, 2, false, FragmentMFU, true, FragmentationLast, false, 1, mpuUnit{0, []byte("cc")}))
	if s := r.Stats(); s.AccessUnits != 0 || s.FragmentErrors < 1 {
		t.Fatalf("stats %+v", s)
	}
	out := pushMPU(t, r, wholeUnit(1, 3, 1, "dd"))
	if len(out) != 1 || !out[0].AfterLoss {
		t.Fatalf("out %+v", out)
	}
}
