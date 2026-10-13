// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import (
	"bytes"
	"slices"
	"testing"
)

// kept is what the demultiplexer handed on, kept for the test to look at.
type kept struct {
	pats     []PAT
	sections []Section
	pes      []PES
	pcrs     []PCR
	pcrPIDs  []uint16
}

func (k *kept) OnPAT(pat PAT)             { k.pats = append(k.pats, pat) }
func (k *kept) OnSection(section Section) { k.sections = append(k.sections, section) }
func (k *kept) OnPES(unit PES)            { k.pes = append(k.pes, unit) }
func (k *kept) OnPCR(pid uint16, pcr PCR) {
	k.pcrs, k.pcrPIDs = append(k.pcrs, pcr), append(k.pcrPIDs, pid)
}

// tsPacket builds one transport packet: the identifier, whether it starts a
// unit, the counter, and as much of the payload as fits, stuffed out with an
// adaptation field the way a real multiplexer does.
func tsPacket(pid uint16, starts bool, counter byte, payload []byte) []byte {
	out := bytes.Repeat([]byte{0xff}, PacketSize)
	out[0] = SyncByte
	out[1] = byte(pid>>8) & 0x1f
	if starts {
		out[1] |= 0x40
	}
	out[2] = byte(pid)
	out[3] = 0x10 | counter&0x0f
	if len(payload) < PacketSize-4 {
		stuffing := PacketSize - 4 - len(payload)
		out[3] |= 0x20
		out[4] = byte(stuffing - 1)
		if stuffing >= 2 {
			out[5] = 0
		}
		copy(out[4+stuffing:], payload)
	} else {
		copy(out[4:], payload)
	}
	return out
}

// carry cuts a unit up into transport packets of one identifier.
func carry(pid uint16, counter *byte, unit []byte) [][]byte {
	var out [][]byte
	starts := true
	for rest := unit; len(rest) > 0; {
		take := min(len(rest), PacketSize-4)
		out = append(out, tsPacket(pid, starts, *counter, rest[:take]))
		*counter = (*counter + 1) & 0x0f
		starts = false
		rest = rest[take:]
	}
	return out
}

func patSectionAt(streamID uint16, version, number, last byte, entries [][2]uint16) []byte {
	section := []byte{0, 0, 0, byte(streamID >> 8), byte(streamID), 0xc1 | version<<1, number, last}
	for _, entry := range entries {
		pid := entry[1] | 0xe000
		section = append(section, byte(entry[0]>>8), byte(entry[0]), byte(pid>>8), byte(pid))
	}
	return sealSection(section)
}

// anySection is any section at all, under a table identifier, with its check.
func anySection(tableID byte, body []byte) []byte {
	return sealSection(append([]byte{tableID, 0, 0}, body...))
}

func sealSection(section []byte) []byte {
	length := len(section) + 4 - 3
	section[1] = 0xb0 | byte(length>>8)&0x0f
	section[2] = byte(length)
	crc := CRC32(section)
	return append(section, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
}

func sectionPacket(pid uint16, counter byte, section []byte) []byte {
	return tsPacket(pid, true, counter, append([]byte{0}, section...))
}

func ramp(n int) []byte {
	out := make([]byte, n)
	for index := range out {
		out[index] = byte(index)
	}
	return out
}

func TestAUnitSplitAcrossPacketsComesBackWhole(t *testing.T) {
	body := ramp(500)
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesPES)
	counter := byte(0)
	for _, packet := range carry(0x0100, &counter, pesUnit(0xe0, 90_000, 87_000, body)) {
		demuxer.Push(packet, &got)
	}
	if len(got.pes) != 1 {
		t.Fatalf("%d units", len(got.pes))
	}
	unit := got.pes[0]
	if unit.PID != 0x0100 || unit.Header.StreamID != 0xe0 || unit.Header.PTS != 90_000 || unit.Header.DTS != 87_000 ||
		!unit.Header.DataAlignment || unit.Lost != 0 || !unit.Complete || !bytes.Equal(unit.Payload, body) {
		t.Errorf("unit %+v", unit.Header)
	}
}

// A video access unit is too long for the length field, so the field is left
// empty and the unit ends when the next one begins.
func TestAUnitOfUnstatedLengthEndsWhenTheNextOneStarts(t *testing.T) {
	body := ramp(400)
	sent := pesUnit(0xe0, 1000, -1, body)
	sent[4], sent[5] = 0, 0
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesPES)
	counter := byte(0)
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	if len(got.pes) != 0 {
		t.Fatal("a unit of unstated length came out before the next one began")
	}
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	if len(got.pes) != 1 || !bytes.Equal(got.pes[0].Payload, body) {
		t.Fatalf("%d units", len(got.pes))
	}
	demuxer.Flush(&got)
	if len(got.pes) != 2 || !bytes.Equal(got.pes[1].Payload, body) {
		t.Errorf("the last unit did not come out at the end: %d units", len(got.pes))
	}
}

func TestASkippedCounterIsReportedWithTheNextUnit(t *testing.T) {
	sent := pesUnit(0xe0, 1, -1, bytes.Repeat([]byte{7}, 40))
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesPES)
	counter := byte(0)
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	counter = (counter + 3) & 0x0f
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	if demuxer.Lost() != 3 || demuxer.LostOn(0x0100) != 3 || len(got.pes) != 2 || got.pes[0].Lost != 0 || got.pes[1].Lost != 3 {
		t.Errorf("lost %d on %d, units %d", demuxer.Lost(), demuxer.LostOn(0x0100), len(got.pes))
	}
}

func TestACountedOnlyIdentifierIsCountedAndNotAssembled(t *testing.T) {
	sent := pesUnit(0xe0, 1, -1, bytes.Repeat([]byte{7}, 40))
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesCount)
	counter := byte(0)
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	counter = (counter + 2) & 0x0f
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	if len(got.pes) != 0 || demuxer.LostOn(0x0100) != 2 {
		t.Fatalf("units %d, lost %d", len(got.pes), demuxer.LostOn(0x0100))
	}
	if counted, ok := demuxer.CountedOn(0x0100); !ok || counted.Arrived == 0 {
		t.Fatal("nothing was counted")
	}
	// Once it is read, the next unit is assembled.
	demuxer.Expect(0x0100, CarriesPES)
	for _, packet := range carry(0x0100, &counter, sent) {
		demuxer.Push(packet, &got)
	}
	demuxer.Flush(&got)
	if len(got.pes) != 1 {
		t.Errorf("%d units", len(got.pes))
	}
}

func TestARepeatedCounterIsNotALoss(t *testing.T) {
	var demuxer Demuxer
	demuxer.Expect(0x0100, CarriesPES)
	for _, counter := range []byte{0, 1, 1, 2} {
		demuxer.Push(tsPacket(0x0100, false, counter, make([]byte, 20)), IgnoreHandler{})
	}
	if demuxer.Lost() != 0 {
		t.Errorf("lost %d", demuxer.Lost())
	}
}

func TestAPacketWithoutPayloadDoesNotStepTheCounter(t *testing.T) {
	var demuxer Demuxer
	empty := tsPacket(0x0100, false, 0, make([]byte, 20))
	empty[3], empty[4] = 0x20, 183
	demuxer.Push(tsPacket(0x0100, false, 0, make([]byte, 20)), IgnoreHandler{})
	demuxer.Push(empty, IgnoreHandler{})
	demuxer.Push(tsPacket(0x0100, false, 1, make([]byte, 20)), IgnoreHandler{})
	if demuxer.Lost() != 0 {
		t.Errorf("lost %d", demuxer.Lost())
	}
}

func TestScrambledPayloadsAreCountedAndNotAssembled(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesPES)
	counter := byte(0)
	for _, packet := range carry(0x0100, &counter, pesUnit(0xe0, 1, -1, bytes.Repeat([]byte{7}, 40))) {
		packet[3] |= 0x80
		demuxer.Push(packet, &got)
	}
	if len(got.pes) != 0 || demuxer.Scrambled() != 1 || demuxer.ScrambledOn(0x0100) != 1 || demuxer.Lost() != 0 {
		t.Errorf("units %d, scrambled %d, lost %d", len(got.pes), demuxer.Scrambled(), demuxer.Lost())
	}
}

// A scrambled packet in the middle of a unit takes the unit with it: the clear
// packets after it are not appended to a body with a hole, and the next unit
// starts afresh, carrying the loss.
func TestAScrambledPacketInTheMiddleDropsTheUnitBeingAssembled(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesPES)
	counter := byte(0)
	for index, packet := range carry(0x0100, &counter, pesUnit(0xe0, 1, -1, bytes.Repeat([]byte{7}, 400))) {
		if index == 1 {
			packet[3] |= 0x80
		}
		demuxer.Push(packet, &got)
	}
	for _, packet := range carry(0x0100, &counter, pesUnit(0xe0, 2, -1, bytes.Repeat([]byte{8}, 400))) {
		demuxer.Push(packet, &got)
	}
	demuxer.Flush(&got)
	if len(got.pes) != 1 || got.pes[0].Header.PTS != 2 || got.pes[0].Lost != 1 || demuxer.ScrambledOn(0x0100) != 1 {
		t.Errorf("units %+v", got.pes)
	}
}

// A programme the table stops naming has left the transmission. Nothing else
// says so, and a demultiplexer that only added would go on assembling the map
// of a programme that is off the air.
func TestAProgrammeTheTableStopsNamingIsDropped(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Push(sectionPacket(0, 0, patSectionAt(1, 0, 0, 0, [][2]uint16{{101, 0x0101}, {102, 0x0102}})), &got)
	if !slices.Equal(demuxer.ProgramMapPIDs(), []uint16{0x0101, 0x0102}) {
		t.Fatalf("maps %v", demuxer.ProgramMapPIDs())
	}
	demuxer.Push(sectionPacket(0x0102, 0, anySection(TablePMT, []byte{0, 102})), &got)
	if len(got.sections) != 1 || got.sections[0].PID != 0x0102 || !got.sections[0].ProgramMap {
		t.Fatalf("sections %+v", got.sections)
	}
	got.sections = nil
	demuxer.Push(sectionPacket(0, 1, patSectionAt(1, 1, 0, 0, [][2]uint16{{101, 0x0101}})), &got)
	if !slices.Equal(demuxer.ProgramMapPIDs(), []uint16{0x0101}) || len(demuxer.Programs()) != 1 ||
		len(got.pats[len(got.pats)-1].Programs) != 1 {
		t.Fatalf("maps %v", demuxer.ProgramMapPIDs())
	}
	demuxer.Push(sectionPacket(0x0102, 1, anySection(TablePMT, []byte{0, 102})), &got)
	if len(got.sections) != 0 {
		t.Errorf("a map was still read on an identifier the table dropped: %+v", got.sections)
	}
}

// A table sent in several sections is taken once it is whole.
func TestATableInTwoSectionsIsTakenWhole(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Push(sectionPacket(0, 0, patSectionAt(1, 0, 0, 1, [][2]uint16{{101, 0x0101}})), &got)
	if len(got.pats) != 0 || len(demuxer.ProgramMapPIDs()) != 0 {
		t.Fatal("half a table was taken as a whole one")
	}
	demuxer.Push(sectionPacket(0, 1, patSectionAt(1, 0, 1, 1, [][2]uint16{{102, 0x0102}})), &got)
	if len(got.pats) != 1 || len(got.pats[0].Programs) != 2 || !slices.Equal(demuxer.ProgramMapPIDs(), []uint16{0x0101, 0x0102}) {
		t.Errorf("tables %+v", got.pats)
	}
}

func TestTheAssociationTableMarksWhereTheProgrammeMapsAre(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Push(sectionPacket(0, 0, patSectionAt(16625, 0, 0, 0, [][2]uint16{{0, 0x0010}, {1024, 0x1fc8}})), &got)
	if len(got.pats) != 1 || got.pats[0].StreamID != 16625 || !got.pats[0].HasNetworkPID || got.pats[0].NetworkPID != 0x0010 ||
		len(got.pats[0].Programs) != 1 || !slices.Equal(demuxer.ProgramMapPIDs(), []uint16{0x1fc8}) {
		t.Fatalf("table %+v", got.pats)
	}
	demuxer.Push(sectionPacket(0x1fc8, 0, anySection(TablePMT, []byte{0x04, 0x00, 0xe1, 0x00, 0xf0, 0x00})), &got)
	if len(got.sections) != 1 || got.sections[0].PID != 0x1fc8 || got.sections[0].TableID != TablePMT || !got.sections[0].ProgramMap {
		t.Errorf("sections %+v", got.sections)
	}
}

func TestAFixedTableIdentifierIsReadWithoutBeingAnnounced(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Push(sectionPacket(0x0011, 0, anySection(0x42, []byte{1, 2, 3, 4})), &got)
	if len(got.sections) != 1 || got.sections[0].TableID != 0x42 || got.sections[0].ProgramMap {
		t.Errorf("sections %+v", got.sections)
	}
}

func TestADamagedSectionIsNotHandedOn(t *testing.T) {
	var demuxer Demuxer
	var got kept
	damaged := anySection(0x42, []byte{1, 2, 3, 4})
	damaged[4] ^= 0xff
	demuxer.Push(sectionPacket(0x0011, 0, damaged), &got)
	if len(got.sections) != 0 {
		t.Error("a section whose check failed was handed on")
	}
}

// The time and date table has no CRC to check: its syntax indicator is clear.
// Demanding one would drop every one of them.
func TestASectionWithoutACheckIsHandedOn(t *testing.T) {
	var demuxer Demuxer
	var got kept
	tdt := []byte{0x70, 0x70, 0x05, 0xc0, 0x79, 0x12, 0x45, 0x00}
	demuxer.Push(sectionPacket(0x0014, 0, tdt), &got)
	if len(got.sections) != 1 || got.sections[0].TableID != 0x70 || !bytes.Equal(got.sections[0].Data, tdt) {
		t.Errorf("sections %+v", got.sections)
	}
}

func TestTheClockComesOutOfTheAdaptationField(t *testing.T) {
	packet := tsPacket(0x0100, false, 0, make([]byte, 20))
	packet[3], packet[4], packet[5] = 0x30, 7, 0x10
	copy(packet[6:12], []byte{0, 0, 0, 0, 0x80 | 0x7e, 0x02}) // a base of one, an extension of two
	var demuxer Demuxer
	var got kept
	demuxer.Push(packet, &got)
	if len(got.pcrs) != 1 || got.pcrPIDs[0] != 0x0100 || got.pcrs[0].Ticks90kHz() != 1 || got.pcrs[0].Ticks27MHz() != 302 {
		t.Errorf("clocks %+v", got.pcrs)
	}
}

func TestTheNullIdentifierIsDropped(t *testing.T) {
	var demuxer Demuxer
	demuxer.Push(tsPacket(NullPID, false, 0, make([]byte, 20)), IgnoreHandler{})
	if demuxer.Packets() != 0 {
		t.Errorf("packets %d", demuxer.Packets())
	}
}

func TestADamagedPacketIsCountedAgainstItsIdentifier(t *testing.T) {
	var demuxer Demuxer
	demuxer.Expect(0x0100, CarriesPES)
	broken := tsPacket(0x0100, false, 0, make([]byte, 20))
	broken[1] |= 0x80
	demuxer.Push(broken, IgnoreHandler{})
	if demuxer.Packets() != 0 {
		t.Fatal("a damaged packet was read")
	}
	demuxer.Push(tsPacket(0x0100, true, 1, make([]byte, 20)), IgnoreHandler{})
	counted, ok := demuxer.CountedOn(0x0100)
	if !ok || counted.Arrived != 2 || counted.Errors != 1 || counted.Passed != 1 || demuxer.Errors() != 1 {
		t.Errorf("counted %+v", counted)
	}
	if _, ok := demuxer.CountedOn(0x0200); ok {
		t.Error("an identifier nothing arrived on was counted")
	}
}

func TestTheIdentifiersComeOutSmallestFirst(t *testing.T) {
	var demuxer Demuxer
	for _, pid := range []uint16{0x0200, 0x0100, 0x0150} {
		demuxer.Expect(pid, CarriesPES)
		demuxer.Push(tsPacket(pid, true, 0, make([]byte, 20)), IgnoreHandler{})
	}
	if !slices.Equal(demuxer.Identifiers(), []uint16{0x0100, 0x0150, 0x0200}) {
		t.Errorf("identifiers %v", demuxer.Identifiers())
	}
}

func TestUnitsThatDeclareTheirLengthComeOutAsTheyFinish(t *testing.T) {
	var demuxer Demuxer
	var got kept
	demuxer.Expect(0x0100, CarriesPES)
	counter := byte(0)
	for step := range 3 {
		for _, packet := range carry(0x0100, &counter, pesUnit(0xc0, int64(step)*90_000, -1, bytes.Repeat([]byte{byte(step)}, 30))) {
			demuxer.Push(packet, &got)
		}
	}
	if len(got.pes) != 3 {
		t.Fatalf("%d units", len(got.pes))
	}
	for step, unit := range got.pes {
		if unit.Header.PTS != uint64(step)*90_000 || !bytes.Equal(unit.Payload, bytes.Repeat([]byte{byte(step)}, 30)) || !unit.Complete {
			t.Errorf("unit %d: %+v", step, unit.Header)
		}
	}
}

func TestAPATReaderSaysWhichProgrammesAStreamCarries(t *testing.T) {
	var reader PATReader
	sent := sectionPacket(0, 0, patSectionAt(16625, 0, 0, 0, [][2]uint16{{0, 0x0010}, {1024, 0x1fc8}}))
	reader.Push(sent)
	reader.Push(sent)
	id, ok := reader.StreamID()
	network, hasNetwork := reader.NetworkPID()
	if !ok || id != 16625 || !hasNetwork || network != 0x0010 || len(reader.Programs()) != 1 ||
		reader.Programs()[0] != (Program{Number: 1024, PID: 0x1fc8}) {
		t.Errorf("stream %d, network %#x, programmes %+v", id, network, reader.Programs())
	}
}

// TestADamagedPacketIsNotCountedTwice: the damaged packet used the counter
// value between its neighbours, so the one after it has lost nothing.
func TestADamagedPacketIsNotCountedTwice(t *testing.T) {
	var demuxer Demuxer
	demuxer.Expect(0x0100, CarriesPES)
	demuxer.Push(tsPacket(0x0100, true, 0, make([]byte, 20)), IgnoreHandler{})
	broken := tsPacket(0x0100, false, 1, make([]byte, 20))
	broken[1] |= 0x80
	demuxer.Push(broken, IgnoreHandler{})
	demuxer.Push(tsPacket(0x0100, false, 2, make([]byte, 20)), IgnoreHandler{})
	counted, _ := demuxer.CountedOn(0x0100)
	if counted.Errors != 1 || counted.Lost != 0 || demuxer.Lost() != 0 {
		t.Errorf("counted %+v, lost %d", counted, demuxer.Lost())
	}
}
