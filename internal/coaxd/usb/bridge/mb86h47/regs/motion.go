// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// The motion unit: the pre-analysis (0x400..) and the motion estimation
// (0x800..) of a coding unit, with their common reset, clock and interrupt
// registers (0x000..). Only the encoder core (14D) reaches it; the helper and
// the main CPU never do. Every register here is 16 bits wide, accessed as a
// halfword (STH/LDUH); a 32-bit count is read as two halves (see
// MotionSum).
//
// There is one unit per coding unit: unit 0 at 0x14E6000, unit 1 at
// 0x14F6000, so unit n's registers are at (n << 16) | 0x14E6xxx. With two
// coding units both are set up, started and read, and unit 1's statistics
// are added to unit 0's.

// The bases of the two motion units, as the encoder core sees them.
const (
	MotionBase0 = 0x14e6000
	MotionBase1 = 0x14f6000
)

// MotionUnit is a coding unit's pre-analysis and motion estimation.
type MotionUnit struct {
	// Reset holds the unit in reset while 1. To hold the unit: Clock=1,
	// Unk008=2, Pre.Enable=2, then Reset=1, Pre.Reset=1, then the three
	// back to 0. To release it: the same with Reset=0, Pre.Reset=0, then
	// Unk008=1, Pre.Enable=1 (Clock left 1).
	Reset Reg
	// Clock is 1 while the unit runs: 1 around both reset sequences, left
	// 1 by the release and 0 by the hold. Probably the unit's clock.
	Clock Reg
	// Unk008 is 2 during either reset sequence, 1 after the release, 0
	// after the hold.
	Unk008 Reg

	// The interrupt: Status shows the events (MotionStatus*), Ack clears
	// the bits written (0xFFFF at open; an interrupt handler writes back
	// what it read), IRQEnable passes them on to the core's interrupt
	// (bit 11 of 14D0014): 0x16B (MotionIRQMask) at open.
	Ack       Reg
	IRQEnable Reg
	Status    Reg

	// Unk080 are six registers set to 0x10 at open.
	Unk080 [6]Reg

	Pre PreAnalysis
	ME  MotionEstimation
}

// The events of MotionUnit.Status.
const (
	MotionStatusPassDone   = 0x001 // a pre-analysis pass is over (then Pre.Enable = 0)
	MotionStatusPassPart   = 0x002 // a pre-analysis pass's intermediate event; comes with or before PassDone
	MotionStatusCoarseDone = 0x008 // the coarse pass is over: its result waits in the FIFO
	MotionStatusBandDone   = 0x020 // a band engine is done: the next band may be given to it
	MotionStatusSearchDone = 0x040 // the search over the picture is over (then statistics, ME.Clock = 0)
	MotionStatusBandWait   = 0x100 // the search waits for a band: ME.Search = 1 once one is given

	// MotionIRQMask are the events enabled at open (all the above).
	MotionIRQMask = 0x16b
)

// MotionSum is a 32-bit count the unit keeps as two halfword registers.
// The value is Hi << 16 | Lo; with two coding units unit 1's is added.
type MotionSum struct {
	Hi, Lo Reg
}

// PreAnalysis is the pre-analysis at the unit's base + 0x400: a pass over
// a region of the input picture (a whole frame, a frame's half, or a field)
// that makes the motion estimation's reduced pictures and the picture's
// statistics. A pass is set up (AnalysisPasses) and started by Start;
// MotionStatusPassDone ends it.
type PreAnalysis struct {
	// Reset holds the pre-analysis in reset while 1 (see MotionUnit.Reset).
	Reset Reg
	// Enable is 1 while a pass is set up and runs: written 1 first, 0 by
	// the pass-done handler; 2 during the resets.
	Enable Reg
	// Start starts the pass set up: 1 then 0.
	Start Reg
	// Mode is the region's kind: PreModeFull | kind << PreModeKindShift |
	// field << PreModeFieldShift.
	Mode Reg
	// Height is the picture's height in lines (halved for field coding),
	// 11 bits.
	Height Reg
	// StartLine is the region's first line (11 bits), StartRow its first
	// macroblock row (9 bits), StartQuarter its first row times four
	// (9 bits): 0 but for the lower half of a tall frame.
	StartLine    Reg
	StartRow     Reg
	StartQuarter Reg
	// Offsets are where the region's output starts in each of the four
	// output areas: 0 and rows*width*3 for the first region; see
	// regions() in the encoder for the rest.
	Offsets [4]Reg
	// Unk430 is 1 at open and 3 each pass.
	Unk430 Reg
	// Width and RegionHeight are the region's size in pixels (11 bits).
	Width        Reg
	RegionHeight Reg
	// Rows is the region's last macroblock row << 8 | its first (7 bits
	// each).
	Rows Reg
	// RowMask is a bit per macroblock row of the region, rows 0..15 in
	// [0], 16..31 in [1], 32..44 in [2]; probably the rows that start a
	// slice. Our code writes 0.
	RowMask [3]Reg
	// Unk454 (0x1010), Unk458 (0x30), Unk45C (0xC) are written at open
	// only.
	Unk454, Unk458, Unk45C Reg
	// Unk48C are 18 registers written at open only: three times
	// 0x1005, 0x201B, 0x101B, 0xD05, 0x281F, 0xD1F (byte pairs; a table
	// of three sets of six).
	Unk48C [18]Reg
	// Unk4DC (0x62) and Unk4E0 (0xF) are written at open only.
	Unk4DC, Unk4E0 Reg
	// Activity and Cost are the pass's statistics, summed over the
	// picture (read at the pass's end; summed over both halves of a tall
	// frame and over both units). The rate control takes them per
	// macroblock: Activity's for the activity step's gain (the helper's
	// mail +0x10), Cost's for the mail's +0x18. Cost is most likely the
	// picture's intra cost, comparable with ME.Cost for scene detection.
	// Note the order: Lo below Hi here.
	Activity MotionSum // Hi 0x4E8, Lo 0x4E4
	Cost     MotionSum // Hi 0x4F0, Lo 0x4EC
	// Unk534 (0x1008) and Unk538 (0x810) are written each pass, always
	// the same.
	Unk534, Unk538 Reg
	// Lambda is the motion cost weight of the picture's QP, in both
	// bytes (lambda(QP) * 0x101).
	Lambda Reg
	// Intra is 0x303 for the first pass of an I or IDR picture, else 0.
	Intra Reg
	// Unk544 is 0 at open and in the usual pass; in a full-mode pass 0
	// for an I or IDR picture, else 3.
	Unk544 Reg
	// Unk548 and Unk54C are 0 at open only.
	Unk548, Unk54C Reg
	// Unk550 and Unk554 are 0 at open and in the usual pass, 0x88 in a
	// full-mode pass.
	Unk550, Unk554 Reg
}

// The fields of PreAnalysis.Mode.
const (
	// PreModeFull selects the full-mode pass, which also sets Unk544 and
	// Unk550/554; our code does not use it.
	PreModeFull = 0x100
	// PreModeKindShift: 2 bits, 1 a tall frame's upper half, 2 its lower
	// half, 3 the whole picture (a frame, or a field).
	PreModeKindShift = 4
	// PreModeFieldShift: 2 bits, 0 a frame, 2 the top field, 3 the
	// bottom field.
	PreModeFieldShift = 1
)

// MotionEstimation is the motion estimation at the unit's base + 0x800:
// set up at open and each picture, started with ME.Clock = 0x3E. A
// picture's search is an optional coarse pass (CoarseStart, over FIFO
// words), whose result is read out and turned into bands; three band
// engines take the bands' words (Band) while the search (Search) runs over
// the picture, asking for bands as it goes.
type MotionEstimation struct {
	// Reset holds the motion estimation in reset while 1: at boot Clock =
	// 0x3E, Reset = 1, Clock = 0; at open Clock = 0x3E, Reset = 0, Clock =
	// 0x3E.
	Reset Reg
	// Clock enables the motion estimation's parts: 0x3E (bits 1..5) at a
	// picture's start, 0 when its search is over. Probably a clock gate
	// per part.
	Clock Reg
	// WidthMBs and HeightMBs are the picture's size in macroblocks (7
	// bits; the height halved for field coding).
	WidthMBs  Reg
	HeightMBs Reg
	// Frame is 1 for frame coding, 0 for field coding.
	Frame Reg
	// Type is the picture's kind for the search (METype*): P 0, B 1,
	// intra 2; the second field of an intra frame (searched from the
	// first) is P.
	Type Reg
	// FirstField is 1 for a field picture's first field, else 0.
	FirstField Reg
	// Refs are the references searched, a bit each (MERef*): 3 for P and
	// intra (the two last), 1 or 2 for the bottom field of an intra frame
	// (its first field; 2 when it is the second field), 0xF for a B
	// picture before any intra picture since the IDR, else 0xC.
	Refs Reg
	// Unk834 (1) and Unk838 (0) are written at open only.
	Unk834, Unk838 Reg
	// CoarseStart starts the coarse pass (1) over the three words given
	// to the FIFO with FifoSelect = MEFifoCoarse. It ends with
	// MotionStatusCoarseDone.
	CoarseStart Reg
	// Unk884 (0x303), Unk888..Unk894 and Unk89C (0) are written at open
	// only: the coarse pass's settings.
	Unk884                         Reg
	Unk888, Unk88C, Unk890, Unk894 Reg
	// Unk898 is 0x3FFF for the first IDR picture after the picture size
	// changed, else 0; written each picture. With Unk918.
	Unk898 Reg
	Unk89C Reg
	// Search starts the search over the picture (3) and lets it go on
	// after MotionStatusBandWait once a band is given (1). It ends with
	// MotionStatusSearchDone.
	Search Reg
	// Unk904 (0x9303), Unk908..Unk914 (0) are written at open only.
	Unk904                         Reg
	Unk908, Unk90C, Unk910, Unk914 Reg
	// Unk918 is 0x3FFF when Unk898 is, else 1; written each picture.
	Unk918 Reg
	// Written at open only: Unk91C 1, Unk920 0x8000, Unk924 8, Unk928 8,
	// Unk92C 1, Unk934 3, Unk938 1.
	Unk91C, Unk920, Unk924, Unk928, Unk92C Reg
	// Unk930 is written 1 at open.
	Unk930         Reg
	Unk934, Unk938 Reg
	// Unk93C, Unk940, Unk944 are written 0 at open and when a picture's
	// search starts without a coarse pass.
	Unk93C, Unk940, Unk944 Reg
	// Unk948, Unk94C, Unk950 are written 0 each picture.
	Unk948, Unk94C, Unk950 Reg
	// Unk954 is 0 at open.
	Unk954 Reg
	// Range is written at open, 4..40; most likely the search range. Our
	// code writes 0x20.
	Range Reg
	// Written at open only: Unk95C 3000, Unk960 1, Unk964 0, Unk968 0x28
	// (40, Range's upper limit), Unk96C 8, Unk970 1.
	Unk95C, Unk960, Unk964, Unk968, Unk96C, Unk970 Reg
	// Band are the three band engines' go registers: 1 starts engine n
	// on the words given with FifoSelect = MEFifoBand(n) (engine = band
	// number mod 3); a read gives 1 while the engine is busy, 0 when it
	// may take a band.
	Band [3]Reg
	// FifoDir is the FIFO's way: 0 to give words (coarse or band
	// set-up), 1 to read the coarse pass's result.
	FifoDir Reg
	// FifoSelect is what the FIFO is for (MEFifo*): 0 the coarse pass's
	// words, 8 its result, 0x10/0x20/0x30 band engine 0/1/2's words.
	FifoSelect Reg
	// Fifo is the FIFO: written with FifoDir 0, read with FifoDir 1.
	// The coarse pass takes 3 words (b<<11 | c<<6 | d<<5 | e; three
	// nibbles; three bits 12..14 over three nibbles); a band 10, or 16
	// for a B picture (row<<6 | col, then three times a<<10 | b<<6 | c
	// and two words lo8 | hi6<<8, four for B); the result is 28 words
	// (two times two words, then 2 x 6 words of a 6-bit and a 4-bit
	// field).
	Fifo Reg
	// FifoBusy is 1 while the result being read out is not ready (after
	// FifoDir = 1, FifoSelect = 8).
	FifoBusy Reg
	// Cost is the search's cost summed over the picture (the rate
	// control's picture +8 and the helper's mail +0x18 per macroblock;
	// comparable with Pre.Cost for scene detection). Note the order: Hi
	// below Lo here.
	Cost MotionSum // Hi 0xA10, Lo 0xA14
	// Vector are sums readable after a field-coded B picture: [0] at
	// 0xA48/0xA4C and 0xA50/0xA54, [1] at 0xA88/0xA8C and 0xA90/0xA94.
	// Most likely the motion vectors' sizes, horizontal and vertical, of
	// forward and backward prediction.
	Vector [2][2]MotionSum
	// Counts are four sets of five counts: [0] 0xB50.., [1] 0xB64.., and
	// for a B picture also [2] 0xB78.. and [3] 0xB8C... Their meaning is
	// not known.
	Counts [4][5]Reg
}

// The values of MotionEstimation.Type.
const (
	METypeP     = 0
	METypeB     = 1
	METypeIntra = 2
)

// The bits of MotionEstimation.Refs.
const (
	MERefForward0  = 0x1
	MERefForward1  = 0x2
	MERefBackward0 = 0x4
	MERefBackward1 = 0x8
)

// The values of MotionEstimation.FifoSelect.
const (
	MEFifoCoarse       = 0x00 // the coarse pass's words (FifoDir 0)
	MEFifoCoarseResult = 0x08 // the coarse pass's result (FifoDir 1)
)

// MEFifoBand is FifoSelect for band engine n's words (FifoDir 0).
func MEFifoBand(n int) uint32 { return uint32(n+1) << 4 }

// MEClockOn is MotionEstimation.Clock while a picture is searched.
const MEClockOn = 0x3e

// MotionUnitAt is the motion unit at base.
func MotionUnitAt(base uint32) MotionUnit {
	u := MotionUnit{
		Reset: Reg(base), Clock: Reg(base + 0x004), Unk008: Reg(base + 0x008),
		Ack: Reg(base + 0x040), IRQEnable: Reg(base + 0x044), Status: Reg(base + 0x048),
		Pre: PreAnalysisAt(base + 0x400),
		ME:  MotionEstimationAt(base + 0x800),
	}
	for i := range u.Unk080 {
		u.Unk080[i] = Reg(base + 0x080 + 4*uint32(i))
	}
	return u
}

// PreAnalysisAt is the pre-analysis at base (the unit's base + 0x400).
func PreAnalysisAt(base uint32) PreAnalysis {
	p := PreAnalysis{
		Reset: Reg(base), Enable: Reg(base + 0x04), Start: Reg(base + 0x08), Mode: Reg(base + 0x0c),
		Height: Reg(base + 0x10), StartLine: Reg(base + 0x14), StartRow: Reg(base + 0x18), StartQuarter: Reg(base + 0x1c),
		Unk430: Reg(base + 0x30), Width: Reg(base + 0x34), RegionHeight: Reg(base + 0x38), Rows: Reg(base + 0x3c),
		Unk454: Reg(base + 0x54), Unk458: Reg(base + 0x58), Unk45C: Reg(base + 0x5c),
		Unk4DC: Reg(base + 0xdc), Unk4E0: Reg(base + 0xe0),
		Activity: MotionSum{Hi: Reg(base + 0xe8), Lo: Reg(base + 0xe4)},
		Cost:     MotionSum{Hi: Reg(base + 0xf0), Lo: Reg(base + 0xec)},
		Unk534:   Reg(base + 0x134), Unk538: Reg(base + 0x138), Lambda: Reg(base + 0x13c), Intra: Reg(base + 0x140),
		Unk544: Reg(base + 0x144), Unk548: Reg(base + 0x148), Unk54C: Reg(base + 0x14c),
		Unk550: Reg(base + 0x150), Unk554: Reg(base + 0x154),
	}
	for i := range p.Offsets {
		p.Offsets[i] = Reg(base + 0x20 + 4*uint32(i))
	}
	for i := range p.RowMask {
		p.RowMask[i] = Reg(base + 0x40 + 4*uint32(i))
	}
	for i := range p.Unk48C {
		p.Unk48C[i] = Reg(base + 0x8c + 4*uint32(i))
	}
	return p
}

// MotionEstimationAt is the motion estimation at base (the unit's base +
// 0x800).
func MotionEstimationAt(base uint32) MotionEstimation {
	r := func(o uint32) Reg { return Reg(base + o) }
	m := MotionEstimation{
		Reset: r(0x00), Clock: r(0x04), WidthMBs: r(0x0c), HeightMBs: r(0x10), Frame: r(0x14),
		Type: r(0x18), FirstField: r(0x1c), Refs: r(0x30), Unk834: r(0x34), Unk838: r(0x38),
		CoarseStart: r(0x80), Unk884: r(0x84), Unk888: r(0x88), Unk88C: r(0x8c), Unk890: r(0x90),
		Unk894: r(0x94), Unk898: r(0x98), Unk89C: r(0x9c),
		Search: r(0x100), Unk904: r(0x104), Unk908: r(0x108), Unk90C: r(0x10c), Unk910: r(0x110),
		Unk914: r(0x114), Unk918: r(0x118), Unk91C: r(0x11c), Unk920: r(0x120), Unk924: r(0x124),
		Unk928: r(0x128), Unk92C: r(0x12c), Unk930: r(0x130), Unk934: r(0x134), Unk938: r(0x138),
		Unk93C: r(0x13c), Unk940: r(0x140), Unk944: r(0x144), Unk948: r(0x148), Unk94C: r(0x14c),
		Unk950: r(0x150), Unk954: r(0x154), Range: r(0x158), Unk95C: r(0x15c), Unk960: r(0x160),
		Unk964: r(0x164), Unk968: r(0x168), Unk96C: r(0x16c), Unk970: r(0x170),
		Band:    [3]Reg{r(0x180), r(0x184), r(0x188)},
		FifoDir: r(0x18c), FifoSelect: r(0x190), Fifo: r(0x194), FifoBusy: r(0x198),
		Cost: MotionSum{Hi: r(0x210), Lo: r(0x214)},
		Vector: [2][2]MotionSum{
			{{Hi: r(0x248), Lo: r(0x24c)}, {Hi: r(0x250), Lo: r(0x254)}},
			{{Hi: r(0x288), Lo: r(0x28c)}, {Hi: r(0x290), Lo: r(0x294)}},
		},
	}
	for i := range m.Counts {
		for j := range m.Counts[i] {
			m.Counts[i][j] = r(0x350 + 0x14*uint32(i) + 4*uint32(j))
		}
	}
	return m
}

// The motion units, and unit 0's (the one a single coding unit uses).
var (
	MotionUnits = [2]MotionUnit{MotionUnitAt(MotionBase0), MotionUnitAt(MotionBase1)}
	Motion      = MotionUnits[0]
)
