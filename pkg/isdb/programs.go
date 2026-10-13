// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"maps"
	"slices"

	"github.com/Till0196/coax/pkg/mpegts"
)

// programEntry is one program the association table names.
type programEntry struct {
	number uint16
	pid    uint16
}

// associatePrograms reads the program association table: the stream's
// identifier and every program with where its map is.
//
// The table is taken whole or not at all. Each version is gathered section by
// section, and the last version to arrive complete is the one that stands: a
// program is taken off the air by no longer being named, and nothing else says
// so.
func associatePrograms(sections [][]byte, info *TransportInfo) []programEntry {
	var (
		standing []programEntry
		version  = -1
		last     byte
		parts    map[byte][]programEntry
	)
	for _, section := range sections {
		if len(section) < 12 || section[0] != 0x00 || !mpegts.ValidSection(section) {
			continue
		}
		info.TransportStreamID = uint16(section[3])<<8 | uint16(section[4])
		key := mpegts.SectionKeyOf(section)
		if int(key.Version) != version {
			version, parts = int(key.Version), map[byte][]programEntry{}
		}
		last = section[7]
		var listed []programEntry
		end := len(section) - 4
		for at := 8; at+4 <= end; at += 4 {
			number := uint16(section[at])<<8 | uint16(section[at+1])
			pid := uint16(section[at+2]&0x1f)<<8 | uint16(section[at+3])
			// Program nought is not a program: it says where the network table is.
			if number == 0 {
				continue
			}
			listed = append(listed, programEntry{number: number, pid: pid})
		}
		parts[key.Number] = listed
		if len(parts) > int(last) {
			standing = nil
			for _, number := range slices.Sorted(maps.Keys(parts)) {
				standing = append(standing, parts[number]...)
			}
		}
	}
	return standing
}

// mapPrograms reads the map of every program the association table named. The
// first map of each program is kept: every repeat says the same thing.
func mapPrograms(packets [][]byte, programs []programEntry) []ProgramInfo {
	if len(programs) == 0 {
		return nil
	}
	collectors := map[uint16]*mpegts.SectionCollector{}
	for _, program := range programs {
		collectors[program.pid] = &mpegts.SectionCollector{}
	}
	for _, packet := range packets {
		pid, payload, start, ok := mpegts.Payload(packet)
		collector := collectors[pid]
		if !ok || collector == nil {
			continue
		}
		collector.Push(payload, start)
	}
	read := map[uint16]*ProgramMap{}
	for _, collector := range collectors {
		for _, section := range collector.Sections() {
			if !mpegts.ValidSection(section) {
				continue
			}
			program := ParseProgramMap(section)
			if program == nil {
				continue
			}
			if _, seen := read[program.ProgramNumber]; !seen {
				read[program.ProgramNumber] = program
			}
		}
	}
	found := make([]ProgramInfo, 0, len(programs))
	for _, program := range programs {
		info := ProgramInfo{ProgramNumber: program.number, MapPID: program.pid}
		if mapped := read[program.number]; mapped != nil {
			info.Mapped = true
			info.PCRPID = mapped.PCRPID
			if descriptor, ok := FindDescriptor(mapped.Descriptors, 0xfe); ok {
				info.System = ParseSystemManagement(descriptor.Body)
			}
			for _, stream := range mapped.Streams {
				info.Streams = append(info.Streams, ProgramStream{Type: stream.Type, PID: stream.PID})
			}
		}
		found = append(found, info)
	}
	slices.SortFunc(found, func(a, b ProgramInfo) int { return int(a.ProgramNumber) - int(b.ProgramNumber) })
	return found
}
