// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

// The programme association table is the first table of any transport stream
// and the shortest: the stream's own identifier, and then a programme number
// and the packet identifier its description is on. That is enough to say a
// stream is a stream and not noise that happened to check out, and enough to
// count what is on it without waiting for the service table.

// PATPID is the packet identifier the association table rides on.
const PATPID = 0x0000

// TablePAT and TablePMT are the association table and the programme map.
const (
	TablePAT = 0x00
	TablePMT = 0x02
)

// Program is one programme of a stream.
type Program struct {
	// Number selects it, which for a broadcast is the service identifier.
	Number uint16
	// PID is where its map is.
	PID uint16
}

// PATReader reads the association table out of a stream as its packets arrive.
type PATReader struct {
	collector     SectionCollector
	streamID      uint16
	hasStreamID   bool
	programs      []Program
	networkPID    uint16
	hasNetworkPID bool
}

// Push takes one transport packet; packets of other identifiers are ignored.
func (reader *PATReader) Push(packet []byte) {
	pid, payload, start, ok := Payload(packet)
	if !ok || pid != PATPID {
		return
	}
	reader.collector.Push(payload, start)
	for _, section := range reader.collector.Take() {
		if section[0] != TablePAT || len(section) < 12 || !ValidSection(section) {
			continue
		}
		if !reader.hasStreamID {
			reader.streamID, reader.hasStreamID = uint16(section[3])<<8|uint16(section[4]), true
		}
		end := len(section) - 4
		for at := 8; at+4 <= end; at += 4 {
			number := uint16(section[at])<<8 | uint16(section[at+1])
			pid := uint16(section[at+2]&0x1f)<<8 | uint16(section[at+3])
			// Programme nought is not a programme: it says where the network's
			// own table is.
			if number == 0 {
				if !reader.hasNetworkPID {
					reader.networkPID, reader.hasNetworkPID = pid, true
				}
				continue
			}
			known := false
			for _, program := range reader.programs {
				if program.Number == number {
					known = true
					break
				}
			}
			if !known {
				reader.programs = append(reader.programs, Program{Number: number, PID: pid})
			}
		}
	}
}

// StreamID is what the stream says it is; false where the table has not
// arrived.
func (reader *PATReader) StreamID() (uint16, bool) { return reader.streamID, reader.hasStreamID }

// Programs is the programmes named so far, in the order they were named.
func (reader *PATReader) Programs() []Program { return reader.programs }

// NetworkPID is where the network's own table is, where the stream said.
func (reader *PATReader) NetworkPID() (uint16, bool) {
	return reader.networkPID, reader.hasNetworkPID
}
