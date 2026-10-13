// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package mpegts provides MPEG-2 TS packet framing, payload extraction,
// section reassembly, and CRC.
package mpegts

const (
	// PacketSize is the length of one transport packet.
	PacketSize = 188
	// SyncByte begins every transport packet.
	SyncByte = 0x47
)

// SplitPackets splits a capture into transport packets, locking on again after
// a discontinuity. A capture taken over USB is not continuous: the device drops
// data whenever its buffer overruns, and what arrives next resumes in the
// middle of a packet. Stopping at the first non-sync byte would silently throw
// away the whole remainder.
func SplitPackets(buffer []byte) [][]byte {
	packets := make([][]byte, 0, len(buffer)/PacketSize)
	for offset := 0; ; {
		start := findSync(buffer, offset)
		if start < 0 {
			return packets
		}
		for start+PacketSize <= len(buffer) && buffer[start] == SyncByte {
			// Alignment has to hold going forward: the truncated packet a gap
			// leaves behind still begins with a sync byte, and taking it would
			// hand back 188 bytes straddling the gap.
			if start+2*PacketSize <= len(buffer) && buffer[start+PacketSize] != SyncByte {
				break
			}
			packets = append(packets, buffer[start:start+PacketSize])
			start += PacketSize
		}
		offset = start + 1
	}
}

// findSync reports the next offset at which several packets line up. A single
// sync byte proves nothing -- 0x47 occurs freely inside a payload -- so a run
// of them one packet apart is required, which is why no lock is attempted
// within the last few packets.
func findSync(buffer []byte, from int) int {
	const confirm = 4
	if from < 0 {
		from = 0
	}
	for offset := from; offset+PacketSize*confirm <= len(buffer); offset++ {
		aligned := true
		for index := 0; index < confirm; index++ {
			if buffer[offset+PacketSize*index] != SyncByte {
				aligned = false
				break
			}
		}
		if aligned {
			return offset
		}
	}
	return -1
}

func Payload(packet []byte) (uint16, []byte, bool, bool) {
	if len(packet) != PacketSize || packet[0] != 0x47 || packet[1]&0x80 != 0 {
		return 0, nil, false, false
	}
	pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
	start := packet[1]&0x40 != 0
	afc := (packet[3] >> 4) & 0x03
	if afc != 1 && afc != 3 {
		return pid, nil, start, false
	}
	offset := 4
	if afc == 3 {
		offset += 1 + int(packet[4])
	}
	if offset >= len(packet) {
		return pid, nil, start, false
	}
	return pid, packet[offset:], start, true
}

// ValidSection reports whether a section arrived whole, which its own CRC is
// what says: running the check over a section including its CRC leaves zero.
func ValidSection(section []byte) bool {
	return len(section) >= 4 && CRC32(section) == 0
}

// SectionKey identifies one section of one version of a sub-table, so that a
// repeat of it is recognisable: a sub-table is split across numbered sections
// and every one is retransmitted for as long as the stream runs. The layout is
// the same for the tables carried in transport packets and in TLV, which is why
// it lives here.
type SectionKey struct {
	TableID  byte
	SubTable uint16
	Version  byte
	Number   byte
}

// SectionKeyOf reads a section's identity out of its header. The section must
// carry the seven header bytes this reads; anything shorter is not a section.
func SectionKeyOf(section []byte) SectionKey {
	return SectionKey{
		TableID:  section[0],
		SubTable: uint16(section[3])<<8 | uint16(section[4]),
		Version:  (section[5] >> 1) & 0x1f,
		Number:   section[6],
	}
}

func CRC32(data []byte) uint32 {
	crc := uint32(0xffffffff)
	for _, value := range data {
		crc ^= uint32(value) << 24
		for bit := 0; bit < 8; bit++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// pcrClockHz is the 27MHz clock a programme clock reference counts in.
const pcrClockHz = 27_000_000

// pcrWrap is where the reference runs out and starts again: the base counts
// 90kHz ticks in 33 bits and the extension the 27MHz ticks between them.
const pcrWrap = (int64(1) << 33) * 300

// Bitrate measures how fast a transport stream is running, in bits per second,
// off the stream's own clock rather than off the wall: the programme clock
// references say when the packets between them were meant to be delivered.
// Timing the capture instead would measure the reader.
//
// The references of a single PID are used, since each programme keeps its own
// clock, but the packets counted between them are all of them, so what comes
// out is the rate of the whole multiplex. The PID with the most references is
// taken.
//
// It reports false when the stream carries no clock, or too little of one to
// divide by.
func Bitrate(packets [][]byte) (float64, bool) {
	type span struct {
		firstIndex, lastIndex int
		firstPCR, lastPCR     int64
		count                 int
	}
	spans := map[uint16]*span{}
	for index, packet := range packets {
		pid, value, ok := programClock(packet)
		if !ok {
			continue
		}
		seen := spans[pid]
		if seen == nil {
			spans[pid] = &span{firstIndex: index, lastIndex: index, firstPCR: value, lastPCR: value, count: 1}
			continue
		}
		seen.lastIndex, seen.lastPCR, seen.count = index, value, seen.count+1
	}
	var best *span
	for _, seen := range spans {
		if best == nil || seen.count > best.count {
			best = seen
		}
	}
	if best == nil || best.count < 2 {
		return 0, false
	}
	ticks := best.lastPCR - best.firstPCR
	if ticks < 0 {
		// The clock ran out and started again somewhere in the middle.
		ticks += pcrWrap
	}
	if ticks < pcrClockHz/10 {
		return 0, false
	}
	bytes := float64(best.lastIndex-best.firstIndex) * PacketSize
	return bytes * 8 * pcrClockHz / float64(ticks), true
}

// programClock reads a packet's programme clock reference, when it carries one.
func programClock(packet []byte) (uint16, int64, bool) {
	if len(packet) != PacketSize || packet[0] != 0x47 || packet[1]&0x80 != 0 {
		return 0, 0, false
	}
	if afc := (packet[3] >> 4) & 0x03; afc != 2 && afc != 3 {
		return 0, 0, false // no adaptation field, so nowhere to carry one
	}
	length := int(packet[4])
	if length < 7 || 5+length > PacketSize {
		return 0, 0, false
	}
	if packet[5]&0x10 == 0 {
		return 0, 0, false // the adaptation field is there but the flag is not set
	}
	base := int64(packet[6])<<25 | int64(packet[7])<<17 | int64(packet[8])<<9 |
		int64(packet[9])<<1 | int64(packet[10])>>7
	extension := int64(packet[10]&0x01)<<8 | int64(packet[11])
	pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
	return pid, base*300 + extension, true
}
