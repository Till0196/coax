// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// The stream unit of a coding pipe (14E4000 for pipe 0 as the encoder core
// sees it) takes the coded macroblock rows from the macroblock data FIFO,
// codes the slice data, writes NAL units with its bit writer and moves the
// stream into a ring in SDRAM. Pipe ch's unit is at ch<<16 | 0x14e4xxx, so
// pipe 1's is at 14F4000.
//
// Every register is 16 bits wide, accessed with STH/LDUH only; wider
// values are split into a high and a low register (high first in the
// address map).
//
// A width of "hi/lo" pairs below means the value is (hi << 16) | lo.

// StreamUnit is a coding pipe's stream unit.
type StreamUnit struct {
	// Resets. Each of the unit's blocks has a reset and a companion
	// register written around it: the companion is set to 2, the reset
	// changed, then the companion set to 1 when the block is to run
	// (channel open) or 0 when it is left held (start). The companion may
	// be a clock or bus enable.
	ControlReset  Reg // 000: 1 holds the slice control block (0x000..0x1FF) in reset
	CoderReset    Reg // 004: 1 holds a second block in reset (which one is not known)
	ControlEnable Reg // 008: companion of ControlReset (2 around the change, then 1 or 0)
	CoderEnable   Reg // 00C: companion of CoderReset

	// Interrupt.
	ErrorCause Reg // 024: read when Status has StreamStatusError: bit 0x40 (StreamErrorBenign) set = the error can be ignored, else coding must stop
	Ack        Reg // 030: write Status bits to clear them (0xFFFF at channel open; the interrupt writes back what it read)
	IRQEnable  Reg // 040: the Status bits that raise the core's interrupt (14D0014 bit 0x8000); 0xAC0D (StreamIRQMask) at channel open
	Status     Reg // 044: StreamStatus... bits; cleared by Ack

	// The slice.
	Unk080     Reg // 080: written 0 at channel open, right after ControlReset is released
	SliceStart Reg // 084: 1 starts coding the slice set up in 0A0..0B8, after the first row windows
	Format     Reg // 0A0: picture format: MB width - 1 (bits 6:0) | StreamFormat... bits
	SliceMBs   Reg // 0A4: the slice's macroblocks (rows * width - the first macroblock's offset)
	SliceKind  Reg // 0A8: slice type code (bits 1:0, StreamSlice...) | L0 refs (min 2) << 2 | L1 refs (min 2) << 4 | QP << 6 | a picture byte << 12
	Unk0AC     Reg // 0AC: 0xFFFF for every slice; perhaps a limit
	MaxMBBits  Reg // 0B0: 0xC80 = 3200 = 128 + RawMbBits (4:2:0), the H.264 limit on a macroblock's bits
	Unk0B4     Reg // 0B4: 0 for every slice
	Unk0B8     Reg // 0B8: the picture's width in macroblocks for every slice

	// What a slice came to. On StreamStatusSliceCounts read Unk104 and
	// one of the counts (CABAC or CAVLC by the channel's entropy coding),
	// then write CountsAck.
	CountsAck  Reg // 100: 1 after StreamStatusSliceCounts was handled
	Unk104     Reg // 104: 16 bits read on StreamStatusSliceCounts
	CABACHi    Reg // 108: hi/lo count read on StreamStatusSliceCounts for a CABAC channel; meaning not known
	CABACLo    Reg // 10C
	CAVLCHi    Reg // 110: hi/lo count read on StreamStatusSliceCounts for a CAVLC channel; meaning not known
	CAVLCLo    Reg // 114
	NALBytesHi Reg // 120: bytes of the NAL unit (or slice) just closed: (hi & 0x3F) << 16 | lo; add 4 for the start code
	NALBytesLo Reg // 124
	BinsHi     Reg // 128: the slice's CABAC bins: (hi & 0xFF) << 16 | lo; summed with the bytes into the cabac_zero_words count
	BinsLo     Reg // 12C

	// The macroblock data FIFO the unit reads (the coding pipe writes rows
	// to it, 14E1C58/14E1C5C tell where), and the row windows it may code.
	InputReset   Reg // 400: 1 holds the input block (0x400..0x7FF) in reset
	InputEnable  Reg // 404: companion of InputReset
	FIFOSizeHi   Reg // 410: hi/lo size of the macroblock data FIFO in bytes: 0x12 / 0x4E00 = 0x124E00 at channel open
	FIFOSizeLo   Reg // 414
	WindowFromHi Reg // 418: hi/lo FIFO position the next row window starts at (the last row's end + 1, 0 at the FIFO's size)
	WindowFromLo Reg // 41C
	WindowToHi   Reg // 428: hi/lo FIFO position the window ends at (a row's last byte). Written 41C, 418, 42C, 428 in that order; each window gives the unit one more row and answers StreamStatusWantsWindow
	WindowToLo   Reg // 42C
	Unk44C       Reg // 44C: 7 at channel open

	// The output: the stream ring in SDRAM, in 8-byte units.
	OutputReset  Reg // 800: 1 holds the output block (0x800..0x8FF) in reset
	OutputEnable Reg // 804: companion of OutputReset
	Unk820       Reg // 820: 0 at channel open
	BitLevel     Reg // 830: the bit writer's FIFO level: push bits only while it is below 0x601; fill chunks are sized from (level - 0x800) * 3/4 (filler data) and (level - 0x600) * 5/8 (cabac_zero_words)
	RingSizeHi   Reg // 880: hi/lo ring size in 8-byte units: 7 / 0 = 0x70000 (ring positions wrap at 0x70000) at channel open
	RingSizeLo   Reg // 884
	StartHi      Reg // 888: hi/lo unit the picture's stream starts at; latched by Latch
	StartLo      Reg // 88C
	LimitHi      Reg // 894: hi/lo last unit the unit may write (the unit before the oldest one not given back); latched by Latch or LatchLimit
	LimitLo      Reg // 898
	Latch        Reg // 8A4: 1 takes Start and Limit: a picture's stream begins
	LatchLimit   Reg // 8A8: 1 takes Limit only: more room (also on StreamStatusAtLimit)
	WPHi         Reg // 8AC: hi/lo last unit written (read; the next picture starts 64 bytes on, on 64 bytes)
	WPLo         Reg // 8B0

	// The bit writer: NAL units written by the core (headers, SEI, the
	// slice header, filler), each opened and closed with BitCommand.
	BitCommand Reg // 900: StreamBit... commands
	BitStatus  Reg // 904: StreamBitBusy while a fill runs; waited on before a fill or a close
	Unk90C     Reg // 90C: 1 before every StreamBitOpen; perhaps start code / emulation prevention on
	CloseMode  Reg // 910: how StreamBitClose closes: StreamCloseEnd or StreamCloseAlign
	FillByte   Reg // 914: the byte StreamBitFill writes: 0xFF for filler data, 0 for cabac_zero_words
	FillCount  Reg // 918: how many bytes StreamBitFill writes
	Unk91C     Reg // 91C: 1 before every fill; perhaps a fill mode
	PadBytes   Reg // 920: the unused bytes of the stream's last 8-byte unit: size = units * 8 - PadBytes
	BitsHi     Reg // 994: bits 31:16 of the value to write
	BitsLo     Reg // 998: bits 15:0 of the value to write
	BitsCount  Reg // 99C: writes the low n bits (1..32) of BitsHi:BitsLo, n the value written
	BitsGolomb Reg // 9A0: writes BitsHi:BitsLo (or BitsLo alone) as an Exp-Golomb code: StreamGolomb...
}

// StreamUnitAt is the stream unit at base.
func StreamUnitAt(base uint32) StreamUnit {
	r := func(off uint32) Reg { return Reg(base + off) }
	return StreamUnit{
		ControlReset: r(0x000), CoderReset: r(0x004), ControlEnable: r(0x008), CoderEnable: r(0x00c),
		ErrorCause: r(0x024), Ack: r(0x030), IRQEnable: r(0x040), Status: r(0x044),
		Unk080: r(0x080), SliceStart: r(0x084),
		Format: r(0x0a0), SliceMBs: r(0x0a4), SliceKind: r(0x0a8), Unk0AC: r(0x0ac),
		MaxMBBits: r(0x0b0), Unk0B4: r(0x0b4), Unk0B8: r(0x0b8),
		CountsAck: r(0x100), Unk104: r(0x104), CABACHi: r(0x108), CABACLo: r(0x10c),
		CAVLCHi: r(0x110), CAVLCLo: r(0x114), NALBytesHi: r(0x120), NALBytesLo: r(0x124),
		BinsHi: r(0x128), BinsLo: r(0x12c),
		InputReset: r(0x400), InputEnable: r(0x404), FIFOSizeHi: r(0x410), FIFOSizeLo: r(0x414),
		WindowFromHi: r(0x418), WindowFromLo: r(0x41c), WindowToHi: r(0x428), WindowToLo: r(0x42c),
		Unk44C:      r(0x44c),
		OutputReset: r(0x800), OutputEnable: r(0x804), Unk820: r(0x820), BitLevel: r(0x830),
		RingSizeHi: r(0x880), RingSizeLo: r(0x884), StartHi: r(0x888), StartLo: r(0x88c),
		LimitHi: r(0x894), LimitLo: r(0x898), Latch: r(0x8a4), LatchLimit: r(0x8a8),
		WPHi: r(0x8ac), WPLo: r(0x8b0),
		BitCommand: r(0x900), BitStatus: r(0x904), Unk90C: r(0x90c), CloseMode: r(0x910),
		FillByte: r(0x914), FillCount: r(0x918), Unk91C: r(0x91c), PadBytes: r(0x920),
		BitsHi: r(0x994), BitsLo: r(0x998), BitsCount: r(0x99c), BitsGolomb: r(0x9a0),
	}
}

// The stream units of pipe 0 and pipe 1 as the encoder core sees them.
var (
	Stream      = StreamUnitAt(0x14e4000)
	StreamPipe1 = StreamUnitAt(0x14f4000)
)

// Status (044) bits; IRQEnable and Ack take the same bits.
const (
	StreamStatusSliceEnd    = 0x0001 // the slice is coded and its NAL unit closed (NALBytes, Bins valid)
	StreamStatusFlushed     = 0x0004 // the picture's stream is out after StreamBitFlush (WP final)
	StreamStatusSliceCounts = 0x0008 // the slice's counts (104..114) are ready: read them, write CountsAck = 1
	StreamStatusAtLimit     = 0x0400 // the writer reached Limit: give a new one (LatchLimit) when there is room
	StreamStatusWantsWindow = 0x0800 // the unit has coded its row windows: write the next (WindowTo)
	StreamStatusFlushAgain  = 0x2000 // write StreamBitFlush again; with StreamStatusAtLimit and no limit ready, wait for the new limit first
	StreamStatusError       = 0x8000 // an error: see ErrorCause

	// StreamIRQMask is IRQEnable at channel open: exactly the bits above.
	StreamIRQMask = 0xac0d

	StreamErrorBenign = 0x40 // ErrorCause bit: an error that can be ignored
)

// Format (0A0) bits above the width (bits 6:0, MB width - 1).
const (
	StreamFormatWidthMask = 0x007f
	StreamFormatField     = 0x0080 // field picture
	StreamFormatCABAC     = 0x0200 // CABAC (else CAVLC)
	StreamFormatBit10     = 0x0400 // meaning not known; set when StreamFormatBit11 is not
	StreamFormatBit11     = 0x0800 // meaning not known; set when StreamFormatBit10 is not
	StreamFormatBit12     = 0x1000 // always set
	StreamFormatMono      = 0x2000 // 4:0:0 (RawMbBits then 2048 instead of 3072)
)

// SliceKind (0A8) fields.
const (
	StreamSliceP        = 0 // slice type code (bits 1:0); also the second field of an I frame
	StreamSliceB        = 1
	StreamSliceI        = 2 // IDR or I
	StreamSliceTypeMask = 0x3
	StreamSliceL0Shift  = 2 // L0 references, at most 2 (2 bits)
	StreamSliceL1Shift  = 4 // L1 references, at most 2 (2 bits)
	StreamSliceQPShift  = 6 // slice QP (6 bits)
	StreamSliceQPMask   = 0x3f << StreamSliceQPShift
	StreamSliceUnkShift = 12 // a byte of the picture's state, 0 in our use
)

// BitCommand (900) commands.
const (
	StreamBitOpen  = 7  // open a NAL unit (Unk90C = 1 first); the NAL header byte is then written as 8 bits
	StreamBitClose = 8  // close it per CloseMode; wait for BitStatus not busy first
	StreamBitFill  = 9  // write FillCount bytes of FillByte (Unk91C = 1 first)
	StreamBitFlush = 10 // the picture's stream is complete: move it out (then StreamStatusFlushed)
)

// BitStatus (904) bits.
const StreamBitBusy = 0x8 // a fill (or close) is running

// CloseMode (910) values.
const (
	StreamCloseEnd   = 0 // end the NAL unit (the program writes the rbsp stop bit first)
	StreamCloseAlign = 1 // byte-align with 1 bits (cabac_alignment_one_bit) and keep the NAL open: the slice data follows
)

// BitsGolomb (9A0) codes.
const (
	StreamGolombUE32 = 1 // ue(v) of BitsHi:BitsLo
	StreamGolombSE32 = 2 // se(v) of BitsHi:BitsLo
	StreamGolombUE16 = 5 // ue(v) of BitsLo (BitsHi need not be set)
	StreamGolombSE16 = 6 // se(v) of BitsLo as a signed 16-bit value
)
