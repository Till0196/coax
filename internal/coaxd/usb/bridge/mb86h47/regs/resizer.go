// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// ResizerBase is the base of the resizer, which scales a decoded picture
// (through window MemoryWindowResizeSource) into another buffer (through
// MemoryWindowResizeTarget), a plane of a field or frame a pass.
const ResizerBase = 0xf14000

// ResizerUnit is the resizer at F14000. Its registers are 16 bits wide, in
// the high half of their word.
//
// A job is set up (from the formats and the crop, the filter mode by the
// ratio) and started with the set-up pass; the passes after it run one at
// a time, the next once Command no longer shows bit 2. Our
// transcode/resize.go and the loop's passes drive it.
type ResizerUnit struct {
	// Reset: our code pulses 1 then 0, with the clock running, before
	// each picture and each pass after the set-up, which is when a pass
	// takes its settings.
	Reset Reg // 0x008
	// Command starts a pass (ResizerCommand...); reads bit 2 set while a
	// pass runs.
	Command Reg // 0x00c
	// IRQEnable enables Status to the main CPU (as Events2 bit 15 of the
	// system unit); 0 after power-up.
	IRQEnable Reg // 0x014
	// Status: bit 0 a pass done, write 1 to clear.
	Status Reg // 0x018

	// The source: its width, its first and last line (of a field, for
	// field passes), the field (ResizerField...), its first and last
	// pixel. 020/024 are halved for the passes of the second field. Our
	// code writes: 01C the width, 02C 0, 030 width - 1, 020 0, 024 lines
	// - 1.
	SourceWidth  Reg // 0x01c
	SourceTop    Reg // 0x020
	SourceBottom Reg // 0x024
	SourceField  Reg // 0x028
	SourceLeft   Reg // 0x02c
	SourceRight  Reg // 0x030
	// Unk034 is 0.
	Unk034 Reg // 0x034
	// Unk038 and Unk03C are 0.
	Unk038, Unk03C Reg // 0x038, 0x03c
	// StartLine is the whole source lines the first output line starts
	// on, StartPhase the phases beyond (our code: from the geometry).
	StartLine  Reg // 0x040
	StartPhase Reg // 0x044

	// The destination: its width, first and last line, and field
	// (ResizerField...). 0x780/0/0x43F/3 for a 1920x1088 fill.
	DestWidth  Reg // 0x048
	DestTop    Reg // 0x04c
	DestBottom Reg // 0x050
	DestField  Reg // 0x054
	// Unk058 is 0.
	Unk058 Reg // 0x058
	// HStep and VStep are the horizontal and vertical steps, source over
	// output samples: whole samples, the phases, whole phases and 1/4096
	// of a phase.
	HStep [4]Reg // 0x05c..0x068
	VStep [4]Reg // 0x06c..0x078
	// Unk07C is 2 or 3 by the filter mode; our code writes 2.
	Unk07C Reg // 0x07c
	// TapShift: the three lines' taps of a filter record add up to
	// 2^(TapShift+1) (7, 8 or 9 by the filter mode; our code writes 8).
	TapShift Reg // 0x080
	// FilterMode picks the block of filter records in the filter window
	// (1..0x10 by the ratio, 0 none); our code writes 2 and puts its own
	// records in that block.
	FilterMode Reg // 0x084
	// Unk088 is 2 (our set-up).
	Unk088 Reg // 0x088
	// PadLuma and PadChroma are the padding colour: 0x10, 0x8080.
	PadLuma   Reg // 0x08c
	PadChroma Reg // 0x090
	// The padding: lines above and below, pixels left and right (a full
	// fill uses PadTop = 0x440).
	PadTop    Reg // 0x094
	PadBottom Reg // 0x098
	PadLeft   Reg // 0x09c
	PadRight  Reg // 0x0a0
	// FieldD0, FieldD4 and FieldD8 are set per source field: D0 a byte
	// offset (param * 0x88, + a line for the bottom field), D4 a
	// computed phase (or 0), D8 a byte << 8 | a field code 1, 2 or 3.
	// Our code writes D0 0/0x44, D4 0x22/0x66, D8 3.
	FieldD0, FieldD4, FieldD8 Reg // 0x0d0, 0x0d4, 0x0d8
	// Unk0DC is 0x11 (our set-up).
	Unk0DC Reg // 0x0dc
	// CoefMode is 0 when CoefA/CoefB were loaded, 3 when not.
	CoefMode Reg // 0x0ec
	// CoefA and CoefB are the filter table: 11 triples of 5-bit taps
	// each (our resizeCoefficients, the same in both).
	CoefA [11]Reg // 0x0f0..0x118
	CoefB [11]Reg // 0x11c..0x144
	// Sum are four 32-bit statistics read after a pass of a picture
	// over 0x13F lines: ((lo >> 5) | hi << 11), added up over the passes.
	Sum [4]ResizerSum // 0x148..0x164
	// Unk168 is a byte setting; our code writes 0.
	Unk168 Reg // 0x168
	// Count is a fifth statistic: (lo >> 11) | hi << 5.
	Count ResizerSum // 0x16c, 0x170
}

// ResizerSum is a statistic in two registers.
type ResizerSum struct{ Hi, Lo Reg }

// ResizerAt is the resizer at base.
func ResizerAt(base uint32) ResizerUnit {
	r := func(o uint32) Reg { return Reg(base + o) }
	u := ResizerUnit{
		Reset: r(0x008), Command: r(0x00c), IRQEnable: r(0x014), Status: r(0x018),
		SourceWidth: r(0x01c), SourceTop: r(0x020), SourceBottom: r(0x024), SourceField: r(0x028),
		SourceLeft: r(0x02c), SourceRight: r(0x030), Unk034: r(0x034), Unk038: r(0x038), Unk03C: r(0x03c),
		StartLine: r(0x040), StartPhase: r(0x044),
		DestWidth: r(0x048), DestTop: r(0x04c), DestBottom: r(0x050), DestField: r(0x054), Unk058: r(0x058),
		Unk07C: r(0x07c), TapShift: r(0x080), FilterMode: r(0x084), Unk088: r(0x088),
		PadLuma: r(0x08c), PadChroma: r(0x090), PadTop: r(0x094), PadBottom: r(0x098), PadLeft: r(0x09c), PadRight: r(0x0a0),
		FieldD0: r(0x0d0), FieldD4: r(0x0d4), FieldD8: r(0x0d8), Unk0DC: r(0x0dc), CoefMode: r(0x0ec),
		Unk168: r(0x168), Count: ResizerSum{r(0x16c), r(0x170)},
	}
	for i := range u.HStep {
		u.HStep[i] = r(0x05c + 4*uint32(i))
		u.VStep[i] = r(0x06c + 4*uint32(i))
	}
	for i := range u.CoefA {
		u.CoefA[i] = r(0x0f0 + 4*uint32(i))
		u.CoefB[i] = r(0x11c + 4*uint32(i))
	}
	for i := range u.Sum {
		u.Sum[i] = ResizerSum{r(0x148 + 8*uint32(i)), r(0x14c + 8*uint32(i))}
	}
	return u
}

// Resizer is the resizer.
var Resizer = ResizerAt(ResizerBase)

// Command values and bits.
const (
	ResizerIdle   = 0
	ResizerLuma   = 4 // a luma pass
	ResizerChroma = 5 // a chroma pass
	ResizerSetUp  = 6 // the set-up pass, first
	ResizerBusy   = 4 // Command read back: a pass runs
	ResizerDone   = 1 // Status: a pass done
)

// Field codes (SourceField, DestField).
const (
	ResizerFieldTop    = 1
	ResizerFieldBottom = 2
	ResizerFieldFrame  = 3
)
