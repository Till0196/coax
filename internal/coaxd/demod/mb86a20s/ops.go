// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86a20s

// Three of the chip's registers are windows onto indexed banks rather than
// settings of their own: a write to the index register picks an entry, and the
// register after it carries the value. The register tables are written as these
// operations and flattened, so that what the sequences do stays visible.
type operation struct {
	index byte
	value uint32
	// data is the register the value goes to, or zero for a plain write to
	// index. width is how many registers from data the value spans.
	data  byte
	width byte
}

// set is a plain write to one register.
func set(register, value byte) operation {
	return operation{index: register, value: uint32(value)}
}

// coefficient writes a 24-bit entry of the bank behind register 0x28.
func coefficient(index byte, value uint32) operation {
	return operation{index: index, value: value, data: 0x29, width: 3}
}

// coefficientLow writes only the low byte of such an entry, which the tables do
// where the rest of the entry is already what it has to be.
func coefficientLow(index, value byte) operation {
	return operation{index: index, value: uint32(value), data: 0x2b, width: 1}
}

// paged04 and paged50 write a byte of the two single-byte banks.
func paged04(index, value byte) operation {
	return operation{index: index, value: uint32(value), data: 0x05, width: 1}
}

func paged50(index, value byte) operation {
	return operation{index: index, value: uint32(value), data: 0x51, width: 1}
}

// indexRegister reports which register selects the bank an operation writes to.
func (op operation) indexRegister() byte {
	switch op.data {
	case 0x29, 0x2b:
		return 0x28
	case 0x05:
		return 0x04
	case 0x51:
		return 0x50
	}
	return 0
}

// flatten expands operations into the register writes they stand for.
func flatten(operations []operation) [][2]byte {
	writes := make([][2]byte, 0, len(operations)*2)
	for _, op := range operations {
		if op.width == 0 {
			writes = append(writes, [2]byte{op.index, byte(op.value)})
			continue
		}
		writes = append(writes, [2]byte{op.indexRegister(), op.index})
		for shift := int(op.width) - 1; shift >= 0; shift-- {
			writes = append(writes, [2]byte{op.data + byte(int(op.width)-1-shift), byte(op.value >> (8 * shift))})
		}
	}
	return writes
}

// The error counters are grouped per hierarchical layer, three of them, and
// the demodulator collects a reading once a counter reaches the preset below.
// The presets are spelt out here rather than left as bare bank writes.
const (
	layerA = 0
	layerB = 1
	layerC = 2
)

func countBitErrorsBeforeViterbi() operation { return set(0x52, 0x01) }

func countBitErrorsAfterViterbi() operation { return set(0x5e, 0x07) }

func countPacketErrors() operation { return paged50(0xb0, 0x07) }

// bitsPerReading presets one layer's pre-Viterbi bit counter, 24 bits wide.
func bitsPerReading(layer int, bits uint32) []operation {
	base := byte(0xa7 + layer*3)
	return []operation{
		paged50(base, byte(bits>>16)),
		paged50(base+1, byte(bits>>8)),
		paged50(base+2, byte(bits)),
	}
}

// blocksPerReading presets one layer's post-Viterbi block counter, and
// packetsPerReading the packet error counter, both 16 bits wide.
func blocksPerReading(layer int, blocks uint16) []operation {
	return counter16(byte(0xdc+layer*2), blocks)
}

func packetsPerReading(layer int, packets uint16) []operation {
	return counter16(byte(0xb2+layer*2), packets)
}

func counter16(base byte, count uint16) []operation {
	return []operation{paged50(base, byte(count>>8)), paged50(base+1, byte(count))}
}

// group flattens nested operation lists, so a table can mix single operations
// with the multi-register ones above.
func group(items ...any) []operation {
	var operations []operation
	for _, item := range items {
		switch value := item.(type) {
		case operation:
			operations = append(operations, value)
		case []operation:
			operations = append(operations, value...)
		default:
			panic("mb86a20s: register tables take operations only")
		}
	}
	return operations
}
