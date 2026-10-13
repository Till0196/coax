// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package tlv reads an ARIB TLV stream as it goes past and says what was wrong with it:
// the ISDB-S3 half of what internal/coaxd/mpegts does for everything else. A demodulator
// locked to noise reports a lock like any other.
//
// Unlike a transport packet, a TLV packet carries its own length, so the next header's
// position is stated rather than assumed:
//
//	byte 0     0x7f
//	byte 1     '11' then a six-bit packet type
//	bytes 2-3  the payload length, most significant byte first
//	then       that many bytes
//
// TLV has no continuity counter of its own. The MMTP packet inside carries a 32-bit
// packet_sequence_number per packet identifier, which unlike a transport stream's four
// bits does not wrap every sixteen packets.
package tlv

import "encoding/binary"

// SyncByte begins every TLV packet.
const SyncByte = 0x7f

// The packet types ARIB defines. Anything else in that byte is not a TLV
// header, which is most of what makes the boundary findable.
const (
	TypeIPv4         = 0x01
	TypeIPv6         = 0x02
	TypeCompressedIP = 0x03
	TypeControl      = 0xfe
	TypeNull         = 0xff
)

// headerSize is the sync byte, the type and the two length bytes.
const headerSize = 4

// syncConfirm is how many headers have to agree before a boundary is believed.
// Each is a 0x7f and one of five types -- better than one part in eight hundred
// per header before the length has to land on the next one.
const syncConfirm = 4

// maxRuns bounds the sequence table. A real multiplex has a few dozen runs, all
// established in its first second, but a stream read wrongly invents runs as
// fast as it invents packets.
const maxRuns = 4096

// maxPacket is the largest a TLV packet can be given the 16-bit length field,
// and huntLimit how far a chain may reach before a boundary can be judged.
// Nothing real is this big -- a TLV packet is an IP datagram -- but noise is.
const (
	maxPacket = headerSize + 0xffff
	huntLimit = syncConfirm * maxPacket
)

// Stats is what an Analyser saw. Nothing is ever reset; a caller wanting a rate
// takes two readings and subtracts.
type Stats struct {
	// Bytes is everything fed in, packet or not.
	Bytes uint64
	// Packets is the TLV packets recognised, stuffing included.
	Packets uint64
	// Skipped is bytes that were not part of a packet.
	Skipped uint64
	// SyncLosses counts breaks in alignment, not packets.
	SyncLosses uint64
	// Nulls is stuffing, and Control is the TLV signalling that describes what
	// the multiplex carries.
	Nulls   uint64
	Control uint64
	// IPv4, IPv6 and CompressedIP are how the datagrams inside are carried.
	// Almost all of a Japanese TLV multiplex is CompressedIP.
	IPv4         uint64
	IPv6         uint64
	CompressedIP uint64
	// Discontinuities is MMTP packets whose sequence number did not follow the
	// last with the same packet identifier: data missing, not data damaged.
	Discontinuities uint64
	// Undecodable is packets that were framed as TLV but whose MMTP header
	// could not be reached. Counted so a stream being read wrongly
	// cannot pass for a clean one.
	Undecodable uint64
	// Forgotten is how many times more than maxRuns sequences appeared at once
	// and the table was emptied. It should be zero; anything else means
	// non-MMTP is being read as MMTP and Discontinuities cannot be believed.
	Forgotten uint64
}

// run is one MMTP sequence: a packet identifier within the flow carrying it.
// The flow is part of the key because an MMTP packet identifier is unique only
// inside its own package -- on a real ISDB-S3 recording, keying on the
// identifier alone invented eighty-one breaks in sixteen megabytes that had
// none.
type run struct {
	flow       flow
	identifier uint16
}

// flow is which datagram stream a packet belongs to: a compressed packet by its
// context, all the fully compressed form carries; a plain one by its address.
type flow struct {
	context    uint16
	address    uint32
	port       uint16
	compressed bool
}

// Analyser reads bytes and keeps the counts. Not safe for concurrent use.
type Analyser struct {
	synced bool
	buffer []byte
	// sequences is the last MMTP packet_sequence_number seen in each run.
	sequences map[run]uint32
	stats     Stats
}

// New returns an empty Analyser.
func New() *Analyser {
	return &Analyser{
		buffer:    make([]byte, 0, 64*1024),
		sequences: make(map[run]uint32),
	}
}

// Synced reports whether the stream is currently lining up on TLV packets.
func (a *Analyser) Synced() bool { return a.synced }

// Stats is what has been seen so far.
func (a *Analyser) Stats() Stats { return a.stats }

// Feed accounts for the next bytes of the stream, in any sizes.
func (a *Analyser) Feed(data []byte) {
	a.stats.Bytes += uint64(len(data))
	a.buffer = append(a.buffer, data...)
	for {
		if !a.synced {
			if !a.hunt() {
				return
			}
		}
		if !a.consume() {
			return
		}
	}
}

// validType reports whether a byte is a TLV packet type. The top two bits are
// reserved and transmitted as ones, which is already in the constants.
func validType(value byte) bool {
	switch value {
	case TypeIPv4, TypeIPv6, TypeCompressedIP, TypeControl, TypeNull:
		return true
	default:
		return false
	}
}

// hunt looks for the packet boundary and reports whether it found one.
//
// An offset that cannot be judged yet does not stop the search: a length taken
// out of noise can claim 65535 bytes that have not arrived, and waiting would
// let one bad byte hold up a stream that lines up four bytes further on.
func (a *Analyser) hunt() bool {
	undecided := -1
	for offset := 0; offset < len(a.buffer); offset++ {
		lined, judged := a.chain(offset)
		if lined {
			a.discard(offset)
			a.synced = true
			return true
		}
		if !judged && undecided < 0 {
			undecided = offset
		}
	}
	if undecided < 0 {
		a.discard(len(a.buffer))
		return false
	}
	a.discard(undecided)
	return false
}

// chain follows the lengths from an offset and reports whether enough headers
// agreed, and whether there were enough bytes to reach a verdict at all.
func (a *Analyser) chain(offset int) (lined, judged bool) {
	at := offset
	for range syncConfirm {
		if at-offset > huntLimit {
			return false, true
		}
		if at+headerSize > len(a.buffer) {
			return false, false
		}
		if a.buffer[at] != SyncByte || !validType(a.buffer[at+1]) {
			return false, true
		}
		at += headerSize + int(binary.BigEndian.Uint16(a.buffer[at+2:at+4]))
	}
	return true, true
}

// consume takes whole packets off the front while the stream lines up, and
// reports whether the caller has to go back to hunting.
func (a *Analyser) consume() bool {
	for {
		if len(a.buffer) < headerSize {
			return false
		}
		if a.buffer[0] != SyncByte || !validType(a.buffer[1]) {
			// One byte goes so that the search starts somewhere new, and the
			// sequence numbers go with it: what follows a break does not
			// follow what came before it.
			a.stats.SyncLosses++
			a.synced = false
			clear(a.sequences)
			a.discard(1)
			return true
		}
		length := int(binary.BigEndian.Uint16(a.buffer[2:4]))
		if headerSize+length > len(a.buffer) {
			return false
		}
		a.packet(a.buffer[1], a.buffer[headerSize:headerSize+length])
		a.advance(headerSize + length)
	}
}

// discard drops bytes off the front and counts them as skipped.
func (a *Analyser) discard(count int) {
	if count <= 0 {
		return
	}
	a.stats.Skipped += uint64(count)
	a.advance(count)
}

func (a *Analyser) advance(count int) {
	if count <= 0 {
		return
	}
	a.buffer = append(a.buffer[:0], a.buffer[count:]...)
}

func (a *Analyser) packet(kind byte, payload []byte) {
	a.stats.Packets++
	switch kind {
	case TypeNull:
		a.stats.Nulls++
		return
	case TypeControl:
		// TLV signalling is not carried over MMTP and has no sequence number.
		a.stats.Control++
		return
	case TypeIPv4:
		a.stats.IPv4++
	case TypeIPv6:
		a.stats.IPv6++
	case TypeCompressedIP:
		a.stats.CompressedIP++
	}
	carried, packet, ok := mmtpOf(kind, payload)
	if !ok {
		a.stats.Undecodable++
		return
	}
	if carried.port == ntpPort {
		// The time signal an ISDB-S3 multiplex carries beside the programmes
		// is not MMTP; reading a sequence number out of it would be reading a
		// timestamp as a counter.
		return
	}
	a.sequence(carried, packet)
}

// ntpPort carries the transmission time rather than a programme.
const ntpPort = 123

// sequence checks an MMTP packet's place in its own run. It reads the fixed
// part of the header: packet identifier at bytes 2-3, packet_sequence_number at
// 8-11. Everything after that is optional and none of it moves those.
func (a *Analyser) sequence(carried flow, packet []byte) {
	const fixedHeader = 12
	if len(packet) < fixedHeader {
		a.stats.Undecodable++
		return
	}
	where := run{flow: carried, identifier: binary.BigEndian.Uint16(packet[2:4])}
	number := binary.BigEndian.Uint32(packet[8:12])
	last, seen := a.sequences[where]
	if !seen && len(a.sequences) >= maxRuns {
		// More runs than any multiplex has, so these are not runs. Start
		// again rather than let an all-night recording grow a table for
		// every number that ever went past.
		clear(a.sequences)
		a.stats.Forgotten++
		last, seen = 0, false
	}
	a.sequences[where] = number
	if !seen || number == last {
		// Nothing to follow yet, or a packet sent twice, which is allowed.
		return
	}
	if number != last+1 {
		a.stats.Discontinuities++
	}
}

// mmtpOf walks a TLV payload down to the MMTP packet inside it and says which
// flow carried it. Where the packet begins does not depend on the header
// compression context, so no context is kept.
func mmtpOf(kind byte, payload []byte) (flow, []byte, bool) {
	switch kind {
	case TypeIPv4:
		return udpPayloadV4(payload)
	case TypeIPv6:
		return udpPayloadV6(payload)
	case TypeCompressedIP:
		return compressedPayload(payload)
	default:
		return flow{}, nil, false
	}
}

// fold reduces an address to a map key (FNV-1a). A collision merges two runs,
// which keying on the identifier alone did routinely; this makes it rare.
func fold(address []byte) uint32 {
	const offset, prime = 2166136261, 16777619
	hash := uint32(offset)
	for _, part := range address {
		hash = (hash ^ uint32(part)) * prime
	}
	return hash
}

// udpPayloadV4 skips an IPv4 header, whose length is in its low nibble in
// 32-bit words, and the 8-byte UDP header after it.
func udpPayloadV4(payload []byte) (flow, []byte, bool) {
	const minimumHeader = 20
	const udpProtocol = 17
	if len(payload) < minimumHeader || payload[0]>>4 != 4 || payload[9] != udpProtocol {
		return flow{}, nil, false
	}
	header := int(payload[0]&0x0f) * 4
	if header < minimumHeader || header+8 > len(payload) {
		return flow{}, nil, false
	}
	carried := flow{
		address: fold(payload[16:20]),
		port:    binary.BigEndian.Uint16(payload[header+2 : header+4]),
	}
	return carried, payload[header+8:], true
}

// udpPayloadV6 skips the 40-byte IPv6 header, the extension headers 0, 43, 51
// and 60 that may follow it, and the UDP header after those.
func udpPayloadV6(payload []byte) (flow, []byte, bool) {
	const fixedHeader = 40
	const udpProtocol = 17
	if len(payload) < fixedHeader || payload[0]>>4 != 6 {
		return flow{}, nil, false
	}
	next, body := payload[6], payload[fixedHeader:]
	for next == 0 || next == 43 || next == 51 || next == 60 {
		if len(body) < 2 {
			return flow{}, nil, false
		}
		// Every extension header but the authentication one (51) measures
		// itself in eight-byte units past the first eight; that one counts
		// fours.
		length := (int(body[1]) + 1) * 8
		if next == 51 {
			length = (int(body[1]) + 2) * 4
		}
		if length > len(body) {
			return flow{}, nil, false
		}
		next, body = body[0], body[length:]
	}
	if next != udpProtocol || len(body) < 8 {
		return flow{}, nil, false
	}
	carried := flow{
		address: fold(payload[24:fixedHeader]),
		port:    binary.BigEndian.Uint16(body[2:4]),
	}
	return carried, body[8:], true
}

// compressedPayload skips a compressed IP header. Its type byte says how much
// of the original headers came with the packet and therefore where the payload
// starts; the fully compressed forms carry no addresses at all.
func compressedPayload(payload []byte) (flow, []byte, bool) {
	const identifierAndType = 3
	if len(payload) < identifierAndType {
		return flow{}, nil, false
	}
	carried := flow{
		context:    binary.BigEndian.Uint16(payload[:2]) >> 4,
		compressed: true,
	}
	var start int
	switch payload[2] {
	case 0x20:
		// A partial IPv4 header with the addresses and ports.
		start = 23
	case 0x60:
		// The same for IPv6, whose addresses are four times the size.
		start = 45
	case 0x21, 0x61:
		// No header beyond the context identifier. Most of a multiplex is these.
		start = identifierAndType
	default:
		return flow{}, nil, false
	}
	if start > len(payload) {
		return flow{}, nil, false
	}
	return carried, payload[start:], true
}
