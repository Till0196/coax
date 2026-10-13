// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// The MPEG-2 decoder cluster's units (12D0000..12DFFFF), as its core (12D)
// and the main CPU see them. The core's program is given coded pictures by
// the main CPU, sets the units up for each and sees them through: an ES
// reader that fetches the picture's slices from the stream ring (12D2xxx),
// the decoding core that parses and reconstructs the macroblocks (12D4xxx),
// its quantiser matrices (12D7xxx), the frame-store unit that says where
// the picture and its references are (12D8xxx) and the side-information
// unit that writes per-macroblock data for the encoder (12DAxxx). The
// control block (12D0000..12D0FFF) has the layout of Cluster
// (ClusterDecoder); DecoderControl adds the registers of it Cluster does
// not name.
//
// Width: every register here is 16 bits wide, in the high half of its
// word; the core accesses them with STH/LDUH only.

// DecoderBase is the decoder cluster's register space.
const DecoderBase = ClusterDecoderBase

// DecoderUnits is the decoder cluster's register space.
type DecoderUnits struct {
	Control DecoderControl // 0x0000: the control block's registers Cluster lacks
	Input   DecoderInput   // 0x2000: the ES reader
	Core    DecoderCore    // 0x4000: the decoding core
	Quant   DecoderQuant   // 0x7000: status and quantiser matrices
	Frames  DecoderFrames  // 0x8000: the frame-store unit
	Side    DecoderSide    // 0xA000: the side-information unit
}

// DecoderAt is the decoder cluster's units at base.
func DecoderAt(base uint32) DecoderUnits {
	return DecoderUnits{
		Control: DecoderControlAt(base),
		Input:   DecoderInputAt(base + 0x2000),
		Core:    DecoderCoreAt(base + 0x4000),
		Quant:   DecoderQuantAt(base + 0x7000),
		Frames:  DecoderFramesAt(base + 0x8000),
		Side:    DecoderSideAt(base + 0xa000),
	}
}

// Decoder is the decoder cluster's units; ClusterDecoder is its control
// block.
var Decoder = DecoderAt(DecoderBase)

// DecoderControl is the registers of the decoder cluster's control block
// that Cluster does not name. In 12D the control block's interrupt
// IRQStatus[0] (12D0010) gathers three event groups (enabled 0x33 in
// IRQEnable[0]): bit 0 the DMA's (Cluster.Unk030 latched, DMAEnable
// enabling), bit 1 the doorbells (BellStatus[0] & BellEnable[0], 0x1B80),
// bit 4 the decoding core's (DecoderCore.Events & EventEnable). A handler
// writes back what it found of each to clear it.
type DecoderControl struct {
	// DMAEnable enables Cluster.Unk030 (12D0030), which latches the DMA's
	// done event in bit 8 (a handler writes back 0030 & 0040; bit 8 is
	// cleared after a copy when it was not set before).
	DMAEnable Reg // 0x040
	// Unk0A0 is written 2 then 1 at the units' reset (our hardwareReset).
	Unk0A0 Reg // 0x0a0

	// The bus DMA between SDRAM and the core's RAM. A copy saves
	// IRQEnable[0..3] and clears them, waits for DMACommand bit 1 to
	// clear, sets Cluster.Run bit 3, sets the copy up, writes DMACommand,
	// waits for bit 1 to clear, then restores Run and IRQEnable. Our
	// program does not use it (the host gives it everything in the
	// Params).

	// DMACommand is 3 to read SDRAM into the core's RAM, 2 to write the
	// core's RAM out; bit 1 reads 1 while a copy runs.
	DMACommand Reg // 0x100
	// DMAMode is 0xC for every copy.
	DMAMode Reg // 0x104
	// DMALocal is the core's RAM address (offset from 0xC0000).
	DMALocal Reg // 0x108
	// DMAFarHi and DMAFarLo are the SDRAM address, high and low halves.
	DMAFarHi, DMAFarLo Reg // 0x10c, 0x110
	// DMALength is the copy's bytes less 0x20: 0x20 for 0x40 bytes, 0 for
	// 0x20 (our bench models it so).
	DMALength Reg // 0x114
}

// DecoderControlAt is the control block's extra registers at base (the
// cluster's base).
func DecoderControlAt(base uint32) DecoderControl {
	r := func(o uint32) Reg { return Reg(base + o) }
	return DecoderControl{
		DMAEnable: r(0x040), Unk0A0: r(0x0a0),
		DMACommand: r(0x100), DMAMode: r(0x104), DMALocal: r(0x108),
		DMAFarHi: r(0x10c), DMAFarLo: r(0x110), DMALength: r(0x114),
	}
}

// The decoder cluster's IRQStatus[0] (12D0010) bits.
const (
	DecoderIRQDMA    = 1 << 0 // Cluster.Unk030 & DecoderControl.DMAEnable
	DecoderIRQBell   = 1 << 1 // BellStatus[0] & BellEnable[0]
	DecoderIRQCore   = 1 << 4 // DecoderCore.Events & DecoderCore.EventEnable
	DecoderIRQEnable = 0x33   // IRQEnable[0]; bit 5 not handled
	DecoderDMADone   = 0x100  // Cluster.Unk030: the DMA copy is done
	DecoderBellMask  = 0x1b80 // BellEnable[0]: the doorbells the core takes
)

// DMACommand values.
const (
	DecoderDMARead  = 3 // SDRAM to the core's RAM
	DecoderDMAWrite = 2 // the core's RAM to SDRAM
	DecoderDMABusy  = 2 // bit 1 while a copy runs
)

// DecoderInput is the ES reader (12D2000..): it fetches a picture's data
// from the stream ring, from the first slice on, for the decoding core.
// Positions and counts are in 32-byte units, wider values split into a
// high and a low register.
type DecoderInput struct {
	// Reset is pulsed 1 then 0, with Enable 0x3F3F around it (our
	// hardwareReset and each picture's start); 0 when the core's program
	// searches the stream itself and at a stop.
	Reset Reg // 0x010
	// Enable is 0x3F3F around Reset, 0 after, and 0x3F as the picture's
	// decode starts; 0 with Reset at a stop.
	Enable Reg // 0x014
	// Unk018 is written 0xFFFF at the units' reset with the event groups'
	// clears.
	Unk018 Reg // 0x018
	// Start is written 3 as the picture's decode starts.
	Start Reg // 0x030
	// RingStartHi/Lo are written 0 at the units' reset with the ring's
	// size: the ring's start, it seems. The ring's address itself
	// (0x88C000) is not written here.
	RingStartHi, RingStartLo Reg // 0x100, 0x104
	// RingSizeHi/Lo are the stream ring's size in bytes: 0x1F / 0xFFE0 =
	// 0x1FFFE0.
	RingSizeHi, RingSizeLo Reg // 0x110, 0x114
	// ReadHi/Lo are where the first slice is in the ring, in 32-byte
	// units (pos >> 21, pos >> 5); the byte in the unit goes to
	// DecoderCore.ESByte.
	ReadHi, ReadLo Reg // 0x200, 0x204
	// Units is the picture's data left from there, in 32-byte units, plus
	// 0x20.
	Units Reg // 0x210
}

// DecoderInputAt is the ES reader at base.
func DecoderInputAt(base uint32) DecoderInput {
	r := func(o uint32) Reg { return Reg(base + o) }
	return DecoderInput{
		Reset: r(0x010), Enable: r(0x014), Unk018: r(0x018), Start: r(0x030),
		RingStartHi: r(0x100), RingStartLo: r(0x104), RingSizeHi: r(0x110), RingSizeLo: r(0x114),
		ReadHi: r(0x200), ReadLo: r(0x204), Units: r(0x210),
	}
}

// DecoderInput.Enable values.
const (
	DecoderInputHold = 0x3f3f // around Reset
	DecoderInputRun  = 0x3f   // as the decode starts
)

// DecoderCore is the decoding core (12D4000..): the picture's parameters
// from its headers, the commands that start and resume the decode, its
// events and the concealment of damaged macroblocks. The values are the
// MPEG-2 (or MPEG-1) header fields; our Params carries them from the host.
type DecoderCore struct {
	// Reset is pulsed 0x7FE then 0 at the units' reset, written 0 at each
	// picture's set-up and at a stop.
	Reset Reg // 0x010
	// Events is the core's latched events (DecoderEvent...), write 1 to
	// clear; wait for Frames.Status bit 8 before clearing them. 0xFFFF at
	// the reset.
	Events Reg // 0x014
	// Position is the macroblock the core stopped at: row << 8 | column
	// (7 bits each), 0x7F7F for none.
	Position Reg // 0x018
	// EventEnable enables Events into IRQStatus[0] bit 4: 1 at the reset,
	// 0x29 (end, error, row error) for a picture.
	EventEnable Reg // 0x01c
	// Mode is the core's state: 0x1F at the reset (then 0 once
	// Quant.Status is idle), 0x16 as a picture is set up (before the
	// matrices), 0x14 while it decodes, 4 to conceal a missing field, 0 at
	// the picture's end.
	Mode Reg // 0x030
	// Version is 1 for MPEG-1, 2 for MPEG-2; written before the matrices
	// are loaded and with each picture.
	Version Reg // 0x040
	// Unk044 is written 0x10 as the decode starts.
	Unk044 Reg // 0x044
	// ESByte is the first slice's byte in its 32-byte unit.
	ESByte Reg // 0x04c
	// Tools is 0xFF for an I picture, 0xFC for P, 0xFE for B, each bit
	// replaced by an override where its mask is set.
	Tools Reg // 0x050
	// Command starts the core: DecoderCommandReset at the units' reset
	// (then wait for Quant.Status), DecoderCommandStart for a picture,
	// DecoderCommandResume at Resume/ResumeColumn.
	Command Reg // 0x054
	// Remaining is the macroblocks left in the row from the resume point:
	// width - column, 8 bits.
	Remaining Reg // 0x058
	// Mode70 is 0x1013 for MPEG-1, 0x1010 + picture_structure for
	// MPEG-2.
	Mode70 Reg // 0x070
	// Unk074 and Unk078 are 0xFFFF for every picture.
	Unk074, Unk078 Reg // 0x074, 0x078
	// Resume is the row to resume at, | 0x200.
	Resume Reg // 0x090
	// ResumeColumn is the column to resume at.
	ResumeColumn Reg // 0x094
	// Conceal is how damaged macroblocks are concealed: 0x11 for an
	// MPEG-2 frame, 0x15 an MPEG-2 field, 0x18 MPEG-1.
	Conceal Reg // 0x098
	// ConcealRef is two groups of eight written with each resume:
	// [k][1] (12D40A0, 12D40C0) selects the field, 0x1000 the bottom
	// field (a bottom field picture, or the missing field being
	// concealed), else 0; the others 0.
	ConcealRef [2][8]Reg // 0x09c + 0x20*k + 4*i
	// Width and Height are the picture's size in pixels, 16-aligned (the
	// height 32-aligned for an interlaced MPEG-2 sequence).
	Width, Height Reg // 0x0e8, 0x0ec
	// Unk0FC is 0 for every picture.
	Unk0FC Reg // 0x0fc
	// MBWidth is the width in macroblocks.
	MBWidth Reg // 0x100
	// MBHeight is the height in macroblocks (a field's for a field
	// picture); the side-information unit's set-up reads it back.
	MBHeight Reg // 0x104
	// Unk108 is 0xFFFF and Unk10C 0 for every picture.
	Unk108, Unk10C Reg // 0x108, 0x10c
	// Coding is picture_coding_type << 8 | frame_pred_frame_dct << 4 |
	// concealment_motion_vectors << 3 | intra_vlc_format << 2 |
	// picture_structure (3 for MPEG-1).
	Coding Reg // 0x110
	// Timing is top_field_first << 15 | repeat_first_field << 14 |
	// intra_dc_precision << 12 | progressive_frame << 11 |
	// temporal_reference.
	Timing Reg // 0x114
	// FCode1 is the MPEG-1 motion codes: full_pel_forward << 12 |
	// forward_f_code << 8 | full_pel_backward << 4 | backward_f_code.
	FCode1 Reg // 0x118
	// FCode2 is the MPEG-2 f_codes (16 bits, from the picture coding
	// extension); FCode1 again for MPEG-1.
	FCode2 Reg // 0x11c
	// Quant is q_scale_type << 9 | alternate_scan << 8 | intra matrix
	// loaded << 4 | non-intra matrix loaded.
	Quant Reg // 0x120
	// Unk204 is read at a picture's end: bits 2 and 4 set when
	// macroblocks wanted the past reference a B picture of a closed GOP
	// decodes without.
	Unk204 Reg // 0x204
}

// DecoderCoreAt is the decoding core at base.
func DecoderCoreAt(base uint32) DecoderCore {
	r := func(o uint32) Reg { return Reg(base + o) }
	c := DecoderCore{
		Reset: r(0x010), Events: r(0x014), Position: r(0x018), EventEnable: r(0x01c),
		Mode: r(0x030), Version: r(0x040), Unk044: r(0x044), ESByte: r(0x04c),
		Tools: r(0x050), Command: r(0x054), Remaining: r(0x058), Mode70: r(0x070),
		Unk074: r(0x074), Unk078: r(0x078), Resume: r(0x090), ResumeColumn: r(0x094),
		Conceal: r(0x098), Width: r(0x0e8), Height: r(0x0ec), Unk0FC: r(0x0fc),
		MBWidth: r(0x100), MBHeight: r(0x104), Unk108: r(0x108), Unk10C: r(0x10c),
		Coding: r(0x110), Timing: r(0x114), FCode1: r(0x118), FCode2: r(0x11c),
		Quant: r(0x120), Unk204: r(0x204),
	}
	for k := range c.ConcealRef {
		for i := range c.ConcealRef[k] {
			c.ConcealRef[k][i] = r(0x09c + 0x20*uint32(k) + 4*uint32(i))
		}
	}
	return c
}

// Values and bits of the decoding core.
const (
	// Events and EventEnable.
	DecoderEventEnd      = 1 << 0 // stopped: at the last macroblock, the picture's end; elsewhere resume there
	DecoderEventError    = 1 << 3 // an error at Position: resume there
	DecoderEventRowError = 1 << 5 // an error: resume at the next row (or end at the last)
	DecoderEventsPicture = 0x29   // EventEnable for a picture

	DecoderNoPosition   = 0x7f7f // Position: none
	DecoderResumeRow    = 0x200  // or'd into Resume
	DecoderMissingRef   = 0x14   // Unk204: the missing past reference was wanted
	DecoderConcealField = 0x1000 // ConcealRef[k][1]: the bottom field

	// Command.
	DecoderCommandStart  = 4
	DecoderCommandResume = 0x400
	DecoderCommandReset  = 0x2000

	// Mode.
	DecoderModeReset   = 0x1f
	DecoderModeIdle    = 0
	DecoderModeSetUp   = 0x16
	DecoderModeDecode  = 0x14
	DecoderModeConceal = 4
)

// DecoderQuant is the decoding core's status and quantiser matrices
// (12D7000..).
type DecoderQuant struct {
	// Status bit 4 (DecoderQuantBusy) is set while the core resets: wait
	// for it to clear after Core.Command = reset.
	Status Reg // 0x000
	// Intra and NonIntra are the quantiser matrices, one 8-bit entry a
	// register, in the stream's (zigzag) order: loaded from the sequence
	// header or the quant matrix extension, and readable back.
	Intra    [64]Reg // 0x400 + 4*i
	NonIntra [64]Reg // 0x500 + 4*i
}

// DecoderQuantBusy is DecoderQuant.Status's reset busy bit.
const DecoderQuantBusy = 0x10

// DecoderQuantAt is the status and matrices at base.
func DecoderQuantAt(base uint32) DecoderQuant {
	q := DecoderQuant{Status: Reg(base)}
	for i := range q.Intra {
		q.Intra[i] = Reg(base + 0x400 + 4*uint32(i))
		q.NonIntra[i] = Reg(base + 0x500 + 4*uint32(i))
	}
	return q
}

// DecoderFrames is the frame-store unit (12D8000..): the picture's
// structure and where it and its references are, as window slots (0x20 +
// frame store, the windows at F12400; see BufferSelect).
type DecoderFrames struct {
	// Reset is two registers pulsed 0x102, 0x103, 0x100 together; 1 in
	// both holds the unit.
	Reset [2]Reg // 0x000, 0x004
	// Structure is the picture_structure, | 0x10 or 0x20 for a P second
	// field that refers to its own first field (0x20 when the second is
	// the top field).
	Structure Reg // 0x00c
	// Status bit 8 (DecoderFramesReady) must be set before the decoding
	// core's events are cleared.
	Status Reg // 0x010
	// Unk020 is pulsed 0x100, 0x101, 0x100 after Reset and left 0x10.
	Unk020 Reg // 0x020
	// Unk024 is written 0x100 after Unk020.
	Unk024 Reg // 0x024
	// Past is the older reference's slot (the forward reference of a B
	// picture); the newest's when a closed GOP has no older one.
	Past Reg // 0x034
	// Current is the picture's own slot.
	Current Reg // 0x038
	// Newest is the newest reference's slot.
	Newest Reg // 0x03c
}

// DecoderFrames bits.
const (
	DecoderFramesReady      = 0x100 // Status
	DecoderFramesSecondTop  = 0x20  // Structure: a P second field (top) on its first
	DecoderFramesSecondBot  = 0x10  // Structure: a P second field (bottom) on its first
	DecoderFramesSlotOffset = 0x20  // the window slot of frame store 0
)

// DecoderFramesAt is the frame-store unit at base.
func DecoderFramesAt(base uint32) DecoderFrames {
	r := func(o uint32) Reg { return Reg(base + o) }
	return DecoderFrames{
		Reset:     [2]Reg{r(0x000), r(0x004)},
		Structure: r(0x00c), Status: r(0x010), Unk020: r(0x020), Unk024: r(0x024),
		Past: r(0x034), Current: r(0x038), Newest: r(0x03c),
	}
}

// DecoderSide is the side-information unit (12DA000..): it writes data of
// each macroblock to a side buffer in SDRAM (0x80154000 + 0x22000 * side
// buffer) that goes out with the picture, and counts statistics that can
// be read after the picture.
type DecoderSide struct {
	// Reset is two registers pulsed 0x103 / 3 then 0x100 / 0 (also at a
	// stop); 1 in both holds the unit.
	Reset [2]Reg // 0x000, 0x004
	// Start is two registers written 1 as the decode starts.
	Start [2]Reg // 0x100, 0x104
	// MBSize is the width | height << 8 in macroblocks, 7 bits each
	// (from Core.MBWidth/MBHeight).
	MBSize Reg // 0x200
	// Structure is picture_structure (3 for 0) | frame_pred_frame_dct
	// << 8.
	Structure Reg // 0x204
	// B is 1 for a B picture.
	B Reg // 0x208
	// Unk20C is 0 for every picture.
	Unk20C Reg // 0x20c
	// AreaHi is bit 26 of the side buffer's address; Area its address
	// >> 10.
	AreaHi, Area Reg // 0x300, 0x304
	// Rows is the rows the unit writes: MBHeight, doubled for a field
	// picture of an interlaced sequence, else rounded up to even.
	Rows Reg // 0x308
	// Weights is four weights, 0x4040 each but for a P frame of an
	// interlaced sequence, where [1] (bottom field first) or [2] (top
	// field first) is (0x40 / (2d + 1)) << 8 | 0x40, d the temporal
	// distance to the reference (0x15 for 1).
	Weights [4]Reg // 0x400 + 4*i
	// Predicted is two registers, 1 for a P or B picture.
	Predicted [2]Reg // 0x500, 0x504
	// Unk508 is six registers written 0 for every picture.
	Unk508 [6]Reg // 0x508 + 4*i
	// The statistics, readable after each picture (in the order of
	// Stats).
	Unk700 [2]Reg    // 0x700, 0x704
	Unk800 [6]Reg    // 0x800 + 4*i
	Unk900 [5][8]Reg // 0x900 + 0x100*k + 4*i (0x900..0xD1C)
	UnkE00 [2]Reg    // 0xe00, 0xe04
}

// Stats is the side-information unit's statistics, in their read order
// (50 registers).
func (s DecoderSide) Stats() []Reg {
	r := append([]Reg{}, s.Unk700[:]...)
	r = append(r, s.Unk800[:]...)
	for k := range s.Unk900 {
		r = append(r, s.Unk900[k][:]...)
	}
	return append(r, s.UnkE00[:]...)
}

// DecoderSide values.
const (
	DecoderSideWeight = 0x4040
	DecoderSideStride = 0x22000 // a side buffer's bytes
)

// DecoderSideAt is the side-information unit at base.
func DecoderSideAt(base uint32) DecoderSide {
	r := func(o uint32) Reg { return Reg(base + o) }
	s := DecoderSide{
		Reset: [2]Reg{r(0x000), r(0x004)}, Start: [2]Reg{r(0x100), r(0x104)},
		MBSize: r(0x200), Structure: r(0x204), B: r(0x208), Unk20C: r(0x20c),
		AreaHi: r(0x300), Area: r(0x304), Rows: r(0x308),
		Predicted: [2]Reg{r(0x500), r(0x504)},
		Unk700:    [2]Reg{r(0x700), r(0x704)}, UnkE00: [2]Reg{r(0xe00), r(0xe04)},
	}
	for i := range s.Weights {
		s.Weights[i] = r(0x400 + 4*uint32(i))
	}
	for i := range s.Unk508 {
		s.Unk508[i] = r(0x508 + 4*uint32(i))
		s.Unk800[i] = r(0x800 + 4*uint32(i))
	}
	for k := range s.Unk900 {
		for i := range s.Unk900[k] {
			s.Unk900[k][i] = r(0x900 + 0x100*uint32(k) + 4*uint32(i))
		}
	}
	return s
}
