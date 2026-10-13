// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// MMT signaling messages, which arrive fragmented across MMTP packets or
// several to a packet, and have to be put back together before they parse.
package mmttlv

import "encoding/binary"

const maxBuffer = 4 << 20

type Message struct {
	ID      uint16
	Version byte
	Payload []byte
}

type Reassembler struct {
	buffers map[uint16][]byte
}

func NewReassembler() *Reassembler {
	return &Reassembler{buffers: make(map[uint16][]byte)}
}

func (assembler *Reassembler) Push(packetID uint16, payload []byte) []Message {
	if len(payload) < 2 {
		return nil
	}
	fragmentation := payload[0] >> 6
	aggregated := payload[0]&1 != 0
	lengthExtension := payload[0]&2 != 0
	body := payload[2:]
	if aggregated {
		if fragmentation != 0 {
			return nil
		}
		return parseAggregated(body, lengthExtension)
	}
	switch fragmentation {
	case 0:
		delete(assembler.buffers, packetID)
		if message, ok := parseMessage(body); ok {
			return []Message{message}
		}
	case 1:
		assembler.buffers[packetID] = append([]byte(nil), body...)
	case 2:
		if current, ok := assembler.buffers[packetID]; ok && len(current)+len(body) <= maxBuffer {
			assembler.buffers[packetID] = append(current, body...)
		}
	case 3:
		if current, ok := assembler.buffers[packetID]; ok {
			delete(assembler.buffers, packetID)
			if message, valid := parseMessage(append(current, body...)); valid {
				return []Message{message}
			}
		}
	}
	return nil
}

func parseAggregated(buffer []byte, lengthExtension bool) []Message {
	width := 2
	if lengthExtension {
		width = 4
	}
	var messages []Message
	for len(buffer) >= width {
		length := int(binary.BigEndian.Uint16(buffer[:2]))
		if width == 4 {
			length = int(binary.BigEndian.Uint32(buffer[:4]))
		}
		buffer = buffer[width:]
		if length > len(buffer) {
			break
		}
		if message, ok := parseMessage(buffer[:length]); ok {
			messages = append(messages, message)
		}
		buffer = buffer[length:]
	}
	return messages
}

func parseMessage(buffer []byte) (Message, bool) {
	if len(buffer) < 5 {
		return Message{}, false
	}
	id := binary.BigEndian.Uint16(buffer[:2])
	header := 5
	length := int(binary.BigEndian.Uint16(buffer[3:5]))
	if id == 0 || id == 0x8003 || id >= 0xf000 || id >= 0x7000 && id <= 0x7fff {
		header = 7
		if len(buffer) < header {
			return Message{}, false
		}
		length = int(binary.BigEndian.Uint32(buffer[3:7]))
	}
	if length < 0 || header+length > len(buffer) {
		return Message{}, false
	}
	return Message{ID: id, Version: buffer[2], Payload: buffer[header : header+length]}, true
}
