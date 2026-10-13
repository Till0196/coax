// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

// SectionCollector reassembles the PSI/SI sections carried on one PID. A
// section is not bounded by the transport packet that carries it: it begins at
// the pointer field of a packet whose payload_unit_start_indicator is set,
// states its own length, and runs on through as many following packets as it
// needs. Feeding every packet of one PID through a collector turns that back
// into whole sections.
//
// Nothing here is specific to any one table.

type SectionCollector struct {
	buffer   []byte
	expected int
	sections [][]byte
}

// Push takes one packet's payload for this PID. payloadStart is the packet's
// payload_unit_start_indicator, which is what says a new section begins here
// and puts a pointer field at the front.
func (collector *SectionCollector) Push(payload []byte, payloadStart bool) {
	if len(payload) == 0 {
		return
	}
	if payloadStart {
		pointer := int(payload[0])
		if 1+pointer > len(payload) {
			collector.buffer, collector.expected = nil, 0
			return
		}
		if pointer > 0 && len(collector.buffer) > 0 {
			collector.feed(payload[1 : 1+pointer])
		}
		collector.buffer, collector.expected = nil, 0
		collector.feed(payload[1+pointer:])
		return
	}
	collector.feed(payload)
}

func (collector *SectionCollector) feed(data []byte) {
	for len(data) > 0 {
		if len(collector.buffer) == 0 && data[0] == 0xff {
			return
		}
		if collector.expected == 0 && len(collector.buffer)+len(data) >= 3 {
			need := 3 - len(collector.buffer)
			collector.buffer = append(collector.buffer, data[:need]...)
			data = data[need:]
			collector.expected = 3 + (int(collector.buffer[1]&0x0f)<<8 | int(collector.buffer[2]))
			if collector.expected < 3 || collector.expected > 4096 {
				collector.buffer, collector.expected = nil, 0
				continue
			}
		}
		if collector.expected == 0 {
			collector.buffer = append(collector.buffer, data...)
			return
		}
		need := collector.expected - len(collector.buffer)
		if need > len(data) {
			collector.buffer = append(collector.buffer, data...)
			return
		}
		collector.buffer = append(collector.buffer, data[:need]...)
		data = data[need:]
		collector.sections = append(collector.sections, append([]byte(nil), collector.buffer...))
		collector.buffer, collector.expected = nil, 0
	}
}

// Sections returns the sections completed so far, in the order they finished.
func (collector *SectionCollector) Sections() [][]byte { return collector.sections }

// Take returns the sections completed since the last Take and forgets them, for
// a reader that runs for as long as the stream does and must not hold every
// section it has ever seen.
func (collector *SectionCollector) Take() [][]byte {
	sections := collector.sections
	collector.sections = nil
	return sections
}
