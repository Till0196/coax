// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import "sort"

// A multiplex is a single run of fixed-length packets carrying every stream of
// every programme interleaved. A Demuxer sorts them back out by packet
// identifier, puts the tables back together section by section and the
// elementary streams back together packet by packet, and hands each whole thing
// on as it completes.
//
// What it does not do is read the tables it hands on: the map of a programme
// means one thing under one broadcasting standard and another under the next.
// The association table is the exception, because without reading it the
// demultiplexer would not know where anything else is.

// IsFixedTablePID reports whether the identifier is one the standards fix in
// advance, so that its sections can be read without waiting for a table to say
// they are there: the association table, conditional access, the network and
// service information tables, the time and date, and the identifiers a
// broadcaster's own tables are pinned to.
func IsFixedTablePID(pid uint16) bool {
	return pid <= 0x001f || (pid >= 0x0023 && pid <= 0x0029) || (pid >= 0x1fc8 && pid <= 0x1fcf)
}

// CarriesSections reports whether a programme map's stream type means the
// identifier carries sections rather than an elementary stream: DSM-CC in its
// four shapes, of which a broadcast here uses the carousel. A programme map does
// not say so directly, so the caller reads the type and tells the demultiplexer
// with Expect.
func CarriesSections(streamType byte) bool { return streamType >= 0x0a && streamType <= 0x0d }

// PCR is the clock a programme is timed against, as the adaptation field sends
// it: a count of the 90kHz clock and, under it, one of the 27MHz clock.
type PCR struct {
	Base      uint64
	Extension uint16
}

// Ticks27MHz is the clock in the units it is really kept in.
func (pcr PCR) Ticks27MHz() uint64 { return pcr.Base*300 + uint64(pcr.Extension) }

// Ticks90kHz is the clock in the units the timestamps of an elementary stream
// use, so that the two can be compared.
func (pcr PCR) Ticks90kHz() uint64 { return pcr.Base }

// Section is one whole section of a table, handed on undecoded. A section with
// section_syntax_indicator set has had its CRC checked; one without (the time
// and date table) carries no CRC to check.
type Section struct {
	PID     uint16
	TableID byte
	// ProgramMap says the association table named this identifier as carrying
	// a programme map and this is a section of one.
	ProgramMap bool
	// Data is the section as sent, header and check included. It is the
	// handler's to keep.
	Data []byte
}

// PAT is the association table, which is the one table read here.
type PAT struct {
	StreamID uint16
	// Programs is the programmes, and where each one's map is.
	Programs []Program
	// NetworkPID is where the network's own table is, where HasNetworkPID
	// says the stream said.
	NetworkPID    uint16
	HasNetworkPID bool
}

// PES is one whole packet of an elementary stream, put back together from as
// many transport packets as it took.
type PES struct {
	PID    uint16
	Header PESHeader
	// RandomAccess says the transport packet it started in was marked as a
	// point the decoder may start from.
	RandomAccess bool
	// Discontinuity says the transmitter broke the count deliberately before
	// this packet, so what came before it does not join to it.
	Discontinuity bool
	// Lost is how many packets of this identifier were missed since the last
	// one was handed on. Anything above nought means the payload has a hole.
	Lost uint32
	// Complete says the packet ran to the length it declared. False where it
	// was cut short -- the stream ended, or a new packet started first.
	Complete bool
	// Payload is the bytes of the elementary stream, header stripped. It is
	// the handler's to keep.
	Payload []byte
}

// Handler is what a Demuxer hands its output to. Embed IgnoreHandler to write
// only the methods wanted.
type Handler interface {
	// OnPAT is the association table, each time a whole one has arrived. A
	// table sent in several sections is handed over once, when the last of
	// them lands. What arrives is the list as it now stands: a programme no
	// longer named has left.
	OnPAT(PAT)
	// OnSection is a section of any other table.
	OnSection(Section)
	// OnPES is a whole packet of an elementary stream.
	OnPES(PES)
	// OnPCR is the programme clock, each time a packet carries it.
	OnPCR(pid uint16, pcr PCR)
}

// IgnoreHandler does nothing with anything, for pushing packets through only to
// count what went past, or to embed in a handler that wants one method.
type IgnoreHandler struct{}

func (IgnoreHandler) OnPAT(PAT)         {}
func (IgnoreHandler) OnSection(Section) {}
func (IgnoreHandler) OnPES(PES)         {}
func (IgnoreHandler) OnPCR(uint16, PCR) {}

// Carries is what an identifier is expected to carry.
type Carries int

const (
	// CarriesTables is sections of a table.
	CarriesTables Carries = iota
	// CarriesPES is packets of an elementary stream.
	CarriesPES
	// CarriesCount is packets nobody is reading: counted (arrivals, losses,
	// damage, scrambling) but not assembled.
	CarriesCount
)

// demuxMostBody is the most a unit is gathered to. A unit that declares no
// length -- which video units may -- ends when the next one starts; the largest
// picture a broadcast carries is a few megabytes.
const demuxMostBody = 32 * 1024 * 1024

// openPES is a packetised elementary stream packet that has begun and not yet
// ended.
type openPES struct {
	header        PESHeader
	randomAccess  bool
	discontinuity bool
	lost          uint32
	wanted        int
	hasWanted     bool
	body          []byte
}

// Counted is what arrived on one packet identifier.
type Counted struct {
	// Arrived is packets that arrived at all, damaged ones included.
	Arrived uint64
	// Passed is packets handed on to be assembled.
	Passed uint64
	// Lost is packets the continuity counters say were missed.
	Lost uint64
	// Errors is packets the transmitter marked as known to be damaged.
	Errors uint64
	// Scrambled is packets that arrived scrambled and were not read.
	Scrambled uint64
}

type demuxStream struct {
	sections    *SectionCollector
	programMap  bool
	counter     int8
	counted     Counted
	pendingLost uint32
	countOnly   bool
	open        *openPES
}

func newDemuxStream(pid uint16) *demuxStream {
	stream := &demuxStream{counter: -1}
	if IsFixedTablePID(pid) {
		stream.sections = &SectionCollector{}
	}
	return stream
}

// continuity says how many packets the counter says were missed between the
// last one and this. A packet with no payload does not step the counter, a
// packet sent twice repeats it, and neither is a loss; nor is a step the
// transmitter announced.
func (stream *demuxStream) continuity(counter byte, payload, discontinuity bool) uint32 {
	previous := stream.counter
	stream.counter = int8(counter)
	if previous < 0 || discontinuity || !payload || int8(counter) == previous {
		return 0
	}
	return uint32((counter - byte(previous) - 1) & 0x0f)
}

// Demuxer sorts a transport stream back out into the tables and streams it
// carries. The zero value is ready to use.
type Demuxer struct {
	streams        map[uint16]*demuxStream
	programMapPIDs []uint16
	programs       []Program
	// patVersion is which version of the association table is being put
	// back together, or -1.
	patVersion int
	patPrimed  bool
	// patParts holds the programmes of each section of it by number. A table
	// sent in several sections cannot be taken a section at a time: replacing
	// what is known from one of them would drop every programme the others
	// named.
	patParts      map[byte][]Program
	patNetwork    uint16
	patHasNetwork bool
	packets       uint64
	lost          uint64
	scrambled     uint64
	errors        uint64
}

func (demuxer *Demuxer) stream(pid uint16) *demuxStream {
	if demuxer.streams == nil {
		demuxer.streams = map[uint16]*demuxStream{}
	}
	stream := demuxer.streams[pid]
	if stream == nil {
		stream = newDemuxStream(pid)
		demuxer.streams[pid] = stream
	}
	return stream
}

// Expect says in advance what an identifier carries, for what the
// demultiplexer cannot work out on its own: the stream types of a programme map
// say which identifiers carry data sections, and reading the map is the
// caller's job.
func (demuxer *Demuxer) Expect(pid uint16, carries Carries) {
	stream := demuxer.stream(pid)
	switch carries {
	case CarriesTables:
		stream.countOnly = false
		if stream.sections == nil {
			stream.sections = &SectionCollector{}
		}
	case CarriesPES:
		stream.countOnly = false
		stream.sections = nil
	case CarriesCount:
		stream.countOnly = true
		stream.sections = nil
		stream.open = nil
	}
}

// Push takes one transport packet and hands on whatever it completed.
func (demuxer *Demuxer) Push(packet []byte, handler Handler) {
	if len(packet) != PacketSize || packet[0] != SyncByte {
		return
	}
	pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
	if pid == NullPID {
		return
	}
	stream := demuxer.stream(pid)
	stream.counted.Arrived++
	// The transmitter saying the packet is known to be damaged. It is counted
	// against the identifier its header names before it is dropped: which
	// identifier is taking the damage is what a receiver showing why a
	// picture breaks up needs.
	if packet[1]&0x80 != 0 {
		stream.counted.Errors++
		demuxer.errors++
		// Its counter cannot be read, so it is taken to have used the next
		// one: the packet after it is not counted as missing a second time.
		if stream.counter >= 0 {
			stream.counter = (stream.counter + 1) & 0x0f
		}
		return
	}
	demuxer.packets++
	control := packet[3]
	payload := control&0x10 != 0
	at := 4
	discontinuity, randomAccess := false, false
	if control&0x20 != 0 {
		length := int(packet[4])
		if 5+length > PacketSize {
			return
		}
		if length > 0 {
			flags := packet[5]
			discontinuity = flags&0x80 != 0
			randomAccess = flags&0x40 != 0
			if flags&0x10 != 0 && length >= 7 {
				handler.OnPCR(pid, readPCR(packet[6:12]))
			}
		}
		at = 5 + length
	}
	if lost := stream.continuity(control&0x0f, payload, discontinuity); lost > 0 {
		stream.counted.Lost += uint64(lost)
		stream.pendingLost += lost
		demuxer.lost += uint64(lost)
	}
	if !payload || at >= PacketSize {
		return
	}
	// A scrambled packet does not carry readable bytes, so it is neither
	// assembled nor read, only counted. A unit being assembled loses itself:
	// the counter still advances over such a packet, so nothing else would
	// notice the hole, and the clear packets after it would be appended to a
	// body missing bytes in the middle. The next unit starts afresh and the
	// dropped one is counted as lost.
	if control&0xc0 != 0 {
		stream.counted.Scrambled++
		demuxer.scrambled++
		if stream.open != nil {
			stream.open = nil
			stream.pendingLost++
		}
		return
	}
	stream.counted.Passed++
	if stream.countOnly {
		return
	}
	starts := packet[1]&0x40 != 0
	if stream.sections != nil {
		demuxer.sections(pid, stream, packet[at:], starts, handler)
		return
	}
	demuxer.pes(pid, stream, packet[at:], starts, discontinuity, randomAccess, handler)
}

// Flush hands on every elementary stream packet still open, which is what the
// end of a stream leaves behind: a packet that declared no length ends where
// the next one starts, and the last one has no next one.
func (demuxer *Demuxer) Flush(handler Handler) {
	for _, pid := range demuxer.sortedPIDs(false) {
		demuxer.close(pid, demuxer.streams[pid], handler)
	}
}

// Packets is how many packets went past, stuffing and damaged ones aside.
func (demuxer *Demuxer) Packets() uint64 { return demuxer.packets }

// Lost is how many packets the counters say were missed, over the whole stream.
func (demuxer *Demuxer) Lost() uint64 { return demuxer.lost }

// Scrambled is how many packets were scrambled, over the whole stream.
func (demuxer *Demuxer) Scrambled() uint64 { return demuxer.scrambled }

// Errors is how many packets the transmitter marked as damaged.
func (demuxer *Demuxer) Errors() uint64 { return demuxer.errors }

// CountedOn is what one identifier carried. It reports false for an identifier
// nothing has arrived on: one a programme map named is known before anything
// arrives, and a row of noughts would read as a stream that is failing.
func (demuxer *Demuxer) CountedOn(pid uint16) (Counted, bool) {
	stream := demuxer.streams[pid]
	if stream == nil || stream.counted.Arrived == 0 {
		return Counted{}, false
	}
	return stream.counted, true
}

// Identifiers is every identifier something has arrived on, smallest first.
func (demuxer *Demuxer) Identifiers() []uint16 { return demuxer.sortedPIDs(true) }

func (demuxer *Demuxer) sortedPIDs(arrivedOnly bool) []uint16 {
	pids := make([]uint16, 0, len(demuxer.streams))
	for pid, stream := range demuxer.streams {
		if !arrivedOnly || stream.counted.Arrived > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	return pids
}

// LostOn is how many packets of one identifier were missed.
func (demuxer *Demuxer) LostOn(pid uint16) uint64 {
	if stream := demuxer.streams[pid]; stream != nil {
		return stream.counted.Lost
	}
	return 0
}

// ScrambledOn is how many packets of one identifier were scrambled.
func (demuxer *Demuxer) ScrambledOn(pid uint16) uint64 {
	if stream := demuxer.streams[pid]; stream != nil {
		return stream.counted.Scrambled
	}
	return 0
}

// ProgramMapPIDs is the identifiers the association table named as carrying
// programme maps, in the order it named them.
func (demuxer *Demuxer) ProgramMapPIDs() []uint16 { return demuxer.programMapPIDs }

// Programs is the programmes the association table named.
func (demuxer *Demuxer) Programs() []Program { return demuxer.programs }

func (demuxer *Demuxer) sections(pid uint16, stream *demuxStream, payload []byte, starts bool, handler Handler) {
	stream.sections.Push(payload, starts)
	for _, section := range stream.sections.Take() {
		// A section with the syntax indicator carries a CRC, and one read
		// from damaged bytes says something that was never sent. One without
		// (the time and date table) carries none to check.
		if section[1]&0x80 != 0 && !ValidSection(section) {
			continue
		}
		tableID := section[0]
		if pid == PATPID && tableID == TablePAT {
			demuxer.pat(section, handler)
			continue
		}
		handler.OnSection(Section{
			PID:        pid,
			TableID:    tableID,
			ProgramMap: stream.programMap && tableID == TablePMT,
			Data:       section,
		})
	}
}

// pat reads the association table. It replaces what was known rather than
// adding to it -- a programme is taken off the air by no longer being named --
// and waits for every section of the version before it does.
func (demuxer *Demuxer) pat(section []byte, handler Handler) {
	if len(section) < 12 {
		return
	}
	version := int((section[5] >> 1) & 0x1f)
	number, last := section[6], section[7]
	var programs []Program
	network, hasNetwork := uint16(0), false
	end := len(section) - 4
	for at := 8; at+4 <= end; at += 4 {
		program := uint16(section[at])<<8 | uint16(section[at+1])
		pid := uint16(section[at+2]&0x1f)<<8 | uint16(section[at+3])
		if program == 0 {
			network, hasNetwork = pid, true
			continue
		}
		programs = append(programs, Program{Number: program, PID: pid})
	}
	if !demuxer.patPrimed || demuxer.patVersion != version {
		demuxer.patPrimed, demuxer.patVersion = true, version
		demuxer.patParts = map[byte][]Program{}
		demuxer.patHasNetwork = false
	}
	if hasNetwork {
		demuxer.patNetwork, demuxer.patHasNetwork = network, true
	}
	demuxer.patParts[number] = programs
	if len(demuxer.patParts) <= int(last) {
		return
	}
	numbers := make([]int, 0, len(demuxer.patParts))
	for number := range demuxer.patParts {
		numbers = append(numbers, int(number))
	}
	sort.Ints(numbers)
	var whole []Program
	for _, number := range numbers {
		whole = append(whole, demuxer.patParts[byte(number)]...)
	}
	named := make([]uint16, 0, len(whole))
	for _, program := range whole {
		named = append(named, program.PID)
	}
	for _, pid := range demuxer.programMapPIDs {
		if containsPID(named, pid) {
			continue
		}
		// This identifier no longer carries a map. What is half-assembled on
		// it belongs to a programme that is no longer named.
		if stream := demuxer.streams[pid]; stream != nil {
			stream.programMap = false
			if !IsFixedTablePID(pid) {
				stream.sections = nil
			}
		}
	}
	for _, pid := range named {
		stream := demuxer.stream(pid)
		if stream.sections == nil {
			stream.sections = &SectionCollector{}
		}
		stream.programMap = true
	}
	demuxer.programMapPIDs = named
	demuxer.programs = append([]Program(nil), whole...)
	handler.OnPAT(PAT{
		StreamID:      uint16(section[3])<<8 | uint16(section[4]),
		Programs:      whole,
		NetworkPID:    demuxer.patNetwork,
		HasNetworkPID: demuxer.patHasNetwork,
	})
}

func containsPID(pids []uint16, pid uint16) bool {
	for _, known := range pids {
		if known == pid {
			return true
		}
	}
	return false
}

func (demuxer *Demuxer) pes(pid uint16, stream *demuxStream, payload []byte, starts, discontinuity, randomAccess bool, handler Handler) {
	if starts {
		// A packet that declared no length ends where the next one begins.
		demuxer.close(pid, stream, handler)
		header, ok := ParsePESHeader(payload)
		if !ok {
			return
		}
		open := &openPES{
			header:        header,
			randomAccess:  randomAccess,
			discontinuity: discontinuity,
			lost:          stream.pendingLost,
			body:          append([]byte(nil), payload[header.PayloadAt:]...),
		}
		open.wanted, open.hasWanted = header.PayloadLength()
		stream.pendingLost = 0
		stream.open = open
	} else {
		if stream.open == nil {
			return
		}
		if len(stream.open.body)+len(payload) > demuxMostBody {
			// A unit that never says where it ends and never ends is not one
			// a decoder could take; it is dropped and counted as lost rather
			// than held without bound.
			stream.open = nil
			stream.pendingLost++
			return
		}
		stream.open.body = append(stream.open.body, payload...)
	}
	// A packet that declared a length is whole as soon as it has that many
	// bytes, and need not wait for the next one to start.
	if open := stream.open; open != nil && open.hasWanted && len(open.body) >= open.wanted {
		demuxer.close(pid, stream, handler)
	}
}

func (demuxer *Demuxer) close(pid uint16, stream *demuxStream, handler Handler) {
	open := stream.open
	if open == nil {
		return
	}
	stream.open = nil
	complete := true
	if open.hasWanted {
		if len(open.body) > open.wanted {
			open.body = open.body[:open.wanted]
		}
		complete = len(open.body) == open.wanted
	}
	handler.OnPES(PES{
		PID:           pid,
		Header:        open.header,
		RandomAccess:  open.randomAccess,
		Discontinuity: open.discontinuity,
		Lost:          open.lost,
		Complete:      complete,
		Payload:       open.body,
	})
}

// readPCR reads the clock out of the six bytes the adaptation field spreads it
// over.
func readPCR(data []byte) PCR {
	base := uint64(data[0])<<25 | uint64(data[1])<<17 | uint64(data[2])<<9 |
		uint64(data[3])<<1 | uint64(data[4]>>7)
	return PCR{Base: base, Extension: uint16(data[4]&0x01)<<8 | uint16(data[5])}
}
