// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// The encoder cluster's buffer selection (0x14D2000..0x14D3FFF), as the
// encoder core (14D) sees it; the helper sees it at 0x10D2000. Most
// registers pick a window slot (the index of a window in the table at
// F12400, 4 bytes a slot; see transcode/layout.go) or a buffer number for a
// client of the frame memory: the pipe's input, reconstruction and
// references, the motion estimation's pictures and work areas, the
// pre-analysis, the decimator. The decoder core (12D) writes two of them
// (0x2C40, 0x3040); the host sets the block up (transcode session).
//
// Width: everything here is 16 bits wide (STH/LDUH).

// The buffer selection's bases.
const (
	BuffersCore   = 0x14d2000 // as the core (and the 12D and main CPU) sees it
	BuffersHelper = 0x10d2000 // as the helper sees it
)

// BufferSelect is the encoder cluster's buffer selection (14D2000..14D3FFF).
//
// Kinds of values, per channel ch (0..3):
//   - input window slot: ch*16 + input buffer (0..3), the windows at
//     F12400 + 4*slot that the resizer fills;
//   - recon window slot: ch*16 + 8 + reconstructed picture (0..7);
//   - recon number: ch*8 + reconstructed picture;
//   - motion buffer: ch*6 + motion estimation picture (the pictures the
//     pre-analysis makes and the motion search reads);
//   - work buffer: ch*3 + n; info buffer: ch*5 + n; colocated buffer:
//     ch*4 + n (0..3).
//
// Two references go in one register as first | second << 8 (list 0 and
// list 1, or top and bottom field).
type BufferSelect struct {
	// Set-up (transcode encoderInputStart; the core's
	// bootWrites/ChannelWrites keep the order they need).

	// 0x2000: 1 holds the frame memory clients in reset, 0 runs them
	// (1 then 0 in our boot and channel writes, around Unk2010).
	Reset Reg
	// 0x2010..0x201C: four words of four 4-bit fields each, written
	// 0x7713, 0x1577, 0xFF11, 0xF0FF around a Reset change and then
	// cleared to 0 (frameMemoryWrites/ChannelWrites); 0 at set-up.
	Unk2010 [4]Reg
	// 0x2020..0x202C: the same four values, kept: 0x7713, 0x1577,
	// 0xFF11, 0xF0FF (0xFF in the last at set-up).
	Unk2020 [4]Reg
	Unk2060 Reg // 0x2060: 0 at set-up
	Unk2064 Reg // 0x2064: 0x202 at set-up
	// 0x2070: bits 3:0 the picture size code (0 1440x1088, 1 1920x1088
	// or 960x1088, 2 1280x720, 4 720x480/576, 6 704x480/576, 7 640x480,
	// 8 320x240, 352 wide 0, 0xFF other), bit 4 (BuffersSizeFrame) frame
	// coding. Written per channel start (sizeCode); 1 at set-up. The
	// helper reads it to pick its per-size table.
	SizeCode Reg
	Unk2074  Reg // 0x2074
	// 0x2080: busy flags: bit 4 (BuffersFlushing0) pipe 0's output is
	// still being written out, bit 5 (BuffersFlushing1) pipe 1's. Wait
	// for the pipe's bit to clear before reading its stream write
	// pointer (0x14E48AC/48B0); our coreprog waits for bit 4 after the
	// stream unit's "flushed" interrupt (0x14E4044 bit 4).
	Busy    Reg
	Unk2090 Reg // 0x2090: 0x10 at set-up
	Unk2800 Reg // 0x2800: 0 at set-up

	// The coding pipe's buffers, per picture (PictureWrites).

	// 0x2830: a B picture's references' recon numbers, list 0 | list 1
	// << 8 (0x3F each).
	RefRecons Reg
	// 0x2850: an I/P picture's own recon number (0x3F).
	Recon Reg
	// 0x2854: the motion estimation's work buffer (0x1F); a B picture
	// puts it in both bytes (our Motion set-up).
	MEWork Reg
	// 0x2858: the pre-analysis's info buffer (ch*5 + n, 0x1F), set by
	// our pre-analysis passes.
	PrepInfo Reg
	// 0x285C: the colocated buffer the pre-analysis reads: ch*4, or
	// ch*4 + the reference's colocated buffer when there is a reference
	// (our code always writes ch*4).
	PrepColocated Reg
	Unk2860       Reg // 0x2860: 0 with every pre-analysis pass
	// 0x2864: the pipe's info buffer << 8 | its work buffer (ch*5 + n,
	// ch*3 + n).
	PipeInfoWork Reg
	// 0x2868: the pipe's colocated buffer (ch*4 + n, n 0..3).
	PipeColocated Reg
	// 0x2C20: the pipe's reference windows (recon window slots):
	// a frame's reference; a field's first field: ref | ref << 8; a
	// second field: ref | own << 8; B: list 0 | list 1 << 8. Always the
	// same value as PipeRefs2.
	PipeRefs Reg
	// 0x2C40: the decoder's picture being decoded (window slot 0x20 +
	// frame store); written by the decoder core 12D (our decoder
	// program), 8 at set-up.
	DecoderPicture Reg
	// 0x2C44: the pipe's input window slot (ch*16 + input buffer, 0x3F),
	// set by PictureWrites and coreprog.
	PipeInput Reg
	// 0x2C48: an I/P picture's reconstruction window slot (ch*16 + 8 +
	// recon).
	PipeRecon Reg
	// 0x2C4C/0x2C50: the motion buffer the pre-analysis writes (ch*6 +
	// the picture's motion buffer, 0x3F), both the same.
	PrepMotion, PrepMotion2 Reg

	// 0x3020: 1 when the channel codes fields (ChannelWrites); 1 at
	// set-up.
	Field Reg
	// 0x3030: the pipe's reference windows, the same value as PipeRefs.
	PipeRefs2 Reg
	// 0x3040: the decoder's reference windows: past | second << 8
	// (window slots 0x20 + frame store), written by the decoder core 12D;
	// 0x89 at set-up.
	DecoderRefs Reg

	// 0x3400..0x3454: a unit the helper can wait on. Purpose unknown
	// ("Loader").
	//
	// 0x3400: four 4-bit fields, bit 0 of each a flag (the helper can
	// read bits 0, 4, 8, 12). 0 at set-up (ChannelWrites).
	Unk3400 Reg
	Unk3404 Reg // 0x3404
	// 0x3408: the pipe's reference mode: 0 a frame or first field, 1 a
	// second field that references its own first field (P), 0x1100 a B
	// picture (PictureWrites).
	RefMode Reg
	Unk3410 Reg // 0x3410
	Unk3414 Reg // 0x3414
	// 0x3418: a 14-bit count (read & 0x3FFF).
	Unk3418 Reg
	Unk341C Reg    // 0x341C
	Unk3424 [4]Reg // 0x3424, 0x3434, 0x3444, 0x3454

	// The pre-analysis and motion estimation's pictures, per picture (our
	// pre-analysis passes and Motion set-up).

	// 0x3820: the pre-analysis's input window slot (ch*16 + input buffer,
	// 0x3F); our coreprog writes it from cvPrepSlot.
	PrepInput Reg
	// 0x3824: the pre-analysis's reference motion buffer (ch*6 [+ the
	// reference's], 0x1F).
	PrepRef Reg
	// 0x3828/0x3834: the motion search's list 0 reference motion buffers
	// (0x1F each): a frame's reference ref | ref << 8; a bottom field
	// mixes in the current (cur | ref << 8, or ref | cur << 8 bottom
	// first). Always the same value (two readers).
	MERef0, MERef0b Reg
	// 0x3830/0x3838: the current picture's motion buffer (ch*6 + n, 0x1F),
	// both the same.
	MECurrent, MECurrentb Reg
	// 0x3848/0x3854: a B picture's list 1 reference motion buffers
	// (r | r << 8), both the same.
	MERef1, MERef1b Reg
	// 0x3860/0x3864: the references' field parity set-up, both the same:
	// P/I 0x3838 same structure as the reference, 0x202 top, 0x0A0A
	// bottom (top first), 0x3232 bottom (bottom first); B: list 0 in the
	// low byte (0x38 same structure, else 2), list 1 in the high byte
	// (0x38 / 2).
	MEParity, MEParityb Reg

	// The decimator: makes the quarter picture the coarse motion search
	// reads.

	// 0x3C00: BuffersDecimatorOn (1) for the sizes it takes (1440/1920
	// x1088, 1920x512/576, 1280x720, 720/704x480/576, 640x480, 320x240),
	// BuffersDecimatorB (3) for a B picture; 0 off (and at set-up).
	DecimatorMode Reg
	// 0x3C04: the quarter picture's width (min(width/4, 0x88), 8-aligned)
	// | (band rows: 0x30, 0x28 at 1920 wide) >> 3 << 11.
	DecimatorSize Reg
	// 0x3C0C: our code writes 0.
	Unk3C0C Reg
	// 0x3C10: write 1 to start; reads non-zero while busy (wait for 0
	// before and after).
	DecimatorStart Reg
	// 0x3C14: a second start for 1920x512/576 pictures (once
	// DecimatorStart is busy: wait for 0, write 1).
	DecimatorStart2 Reg
}

// Values and bits of the buffer selection.
const (
	BuffersSizeFrame   = 0x10 // SizeCode: frame coding
	BuffersFlushing0   = 0x10 // Busy: pipe 0's output is being written out
	BuffersFlushing1   = 0x20 // Busy: pipe 1's
	BuffersDecimatorOn = 1    // DecimatorMode: on (I/P)
	BuffersDecimatorB  = 3    // DecimatorMode: on, a B picture
	BuffersRefModeB    = 0x1100
	BuffersRefModeSelf = 1 // RefMode: a second field referencing its first
)

// BuffersAt is the buffer selection at base (0x14D2000 or 0x10D2000).
func BuffersAt(base uint32) BufferSelect {
	r := func(off uint32) Reg { return Reg(base - 0x2000 + off) }
	b := BufferSelect{
		Reset:   r(0x2000),
		Unk2060: r(0x2060), Unk2064: r(0x2064), SizeCode: r(0x2070), Unk2074: r(0x2074),
		Busy: r(0x2080), Unk2090: r(0x2090), Unk2800: r(0x2800),
		RefRecons: r(0x2830), Recon: r(0x2850), MEWork: r(0x2854), PrepInfo: r(0x2858),
		PrepColocated: r(0x285c), Unk2860: r(0x2860), PipeInfoWork: r(0x2864), PipeColocated: r(0x2868),
		PipeRefs: r(0x2c20), DecoderPicture: r(0x2c40), PipeInput: r(0x2c44), PipeRecon: r(0x2c48),
		PrepMotion: r(0x2c4c), PrepMotion2: r(0x2c50),
		Field: r(0x3020), PipeRefs2: r(0x3030), DecoderRefs: r(0x3040),
		Unk3400: r(0x3400), Unk3404: r(0x3404), RefMode: r(0x3408), Unk3410: r(0x3410),
		Unk3414: r(0x3414), Unk3418: r(0x3418), Unk341C: r(0x341c),
		PrepInput: r(0x3820), PrepRef: r(0x3824),
		MERef0: r(0x3828), MERef0b: r(0x3834), MECurrent: r(0x3830), MECurrentb: r(0x3838),
		MERef1: r(0x3848), MERef1b: r(0x3854), MEParity: r(0x3860), MEParityb: r(0x3864),
		DecimatorMode: r(0x3c00), DecimatorSize: r(0x3c04), Unk3C0C: r(0x3c0c),
		DecimatorStart: r(0x3c10), DecimatorStart2: r(0x3c14),
	}
	for i := range 4 {
		b.Unk2010[i] = r(0x2010 + 4*uint32(i))
		b.Unk2020[i] = r(0x2020 + 4*uint32(i))
		b.Unk3424[i] = r(0x3424 + 0x10*uint32(i))
	}
	return b
}

// The buffer selection as the core and as the helper see it.
var (
	Buffers       = BuffersAt(BuffersCore)
	HelperBuffers = BuffersAt(BuffersHelper)
)
