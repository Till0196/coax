// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package regs names the registers of the MB86H47A's encoder units, as the
// programs on the chip and the host both reach them.
//
// A unit that comes more than once (the row rate control of each coding
// unit, the register file the encoder core and its helper both see) is a
// struct of its registers built from a base address: the same definition
// for each copy and each core's view of it. A register whose use is not
// known is named for its offset (Unk...), so that it is at least in one
// place.
//
// The units are laid out for two coding pipes: pipe 0 is the units at
// 14Exxxx with the helper core 10D, pipe 1 the units at 14Fxxxx (a unit's
// registers + 0x10000) with a second helper core at 11D0000. Where the
// register file has a pair, index 0 is pipe 0. With one pipe only index 0
// is used and pipe 1's registers read back garbage.
package regs

// Reg is a register's address on a core's bus.
type Reg uint32

// Addr is the register's address.
func (r Reg) Addr() uint32 { return uint32(r) }

// The bases of the register file the encoder core (14D) and its helper
// (10D) share, as each sees it: the helper sees 14D4xxx at 10D4xxx. All of
// its registers are 32 bits wide.
const (
	SharedCore   = 0x14d4000
	SharedHelper = 0x10d4000
)

// Shared is the register file the encoder core and its helpers share
// (14D4000-14D4FFF): the unit's reset, three event groups (one per core:
// the encoder core, helper 0, helper 1), a DMA engine, the macroblock data
// streams, the row rate control of each pipe and the mailbox.
//
// Event groups: a write to a Set register sets bits in that core's
// latched Status (a core rings another by writing the other's Set); a read
// of Set gives the raw events. Status is the latched events that Enable
// lets through; they raise the core's interrupt (14D0014 bit 9 for the
// encoder core, 10D0010 bit 4 for helper 0). Writing bits to Ack clears
// them.
type Shared struct {
	// Reset holds the unit: Ports=0x1F, Reset=1, Ports=0 at the unit's
	// reset; Ports=0x1F (two pipes) or 0x1D (one pipe, bit 1 off), then
	// Reset=0 to release it.
	Reset Reg // 0x000
	// Ports is the unit's port enables (5 bits); bit 1 is pipe 1's
	// helper (cleared with one pipe). See Reset.
	Ports Reg // 0x004

	// Set rings a core: [0] the encoder core, [1] helper 0, [2] helper 1.
	// Helper 0 writes 10D4010=0x100 when its answer is in the mailbox;
	// the core writes 14D4014/14D4018=0x100 when a command is. Read by its
	// own core: the raw events; the core polls 14D4010 bits 0/1 for the
	// DMA engine, helper 0 polls 10D4014 bit 5 for the pipe's next
	// macroblock, which Enable does not let through.
	Set [3]Reg // 0x010, 0x014, 0x018
	// Status is each core's latched, enabled events: the core reads
	// 14D4020 in its interrupt (bit 8 helper 0 answered, bit 9 helper 1),
	// helper 0 reads 10D4024 (bit 8 a command).
	Status [3]Reg // 0x020, 0x024, 0x028
	// Enable is each core's event enables: 0xF00 (bits 8-11, the mail
	// events) for the core and helper 0. 14D4038 (helper 1's) is left
	// unwritten.
	Enable [3]Reg // 0x030, 0x034, 0x038
	// Ack clears each core's events: the core writes back what it read
	// of Status, 3 after the DMA engine, 0xFFFF at start; helper 0 writes
	// back 10D4024, 0x20 after each pipe step, 0xFFFFFFFF at its start.
	Ack [3]Reg // 0x040, 0x044, 0x048
	// Unk0A0 is set to 1 as the unit comes out of reset, the last write
	// (our boot).
	Unk0A0 Reg // 0x0a0

	// The DMA engine that copies from SDRAM into a core's local RAM (for
	// example a block of bits to put out through the stream unit's bit
	// writer). A copy waits for DMABusy&3 == 0, sets it up, writes
	// DMAStart=3 and polls 14D4010: bit 1 done, bit 0 (with Ack=1 and
	// DMANext=1) wants to go on; then Ack=3.
	DMAStart    Reg // 0x200: 3 starts a copy
	DMANext     Reg // 0x210: 1 lets the copy go on (after event bit 0)
	DMABusy     Reg // 0x230: bits 0-1 nonzero while a copy runs
	DMALocal    Reg // 0x240: the local RAM address the copy goes to
	DMALocalEnd Reg // 0x244: the last local RAM byte (DMALocal+DMALength-1)
	DMALength   Reg // 0x24c: bytes, in 8
	DMAMode     Reg // 0x254: 7
	DMASource   Reg // 0x258: the source, as its address - 0x402000
	DMAUnk25C   Reg // 0x25c: 0

	// MBData is each pipe's four macroblock data streams (24, 2, 1/4 and 8
	// bytes a macroblock; core.go).
	MBData [2][4]MBStream

	// The row rate control of each pipe, and what each pipe's helper
	// reported of the last picture.
	RowRC  [2]RowRC
	Report [2]Report

	// Command is the mailbox command to each helper: the core writes
	// (kind << 16) | SharedMailFromCore after the words, then rings
	// (Set[1+i] = 0x100); the helper answers (kind << 16) |
	// SharedMailFromHelper in the same register and the core reads it
	// back in its interrupt.
	Command [2]Reg // 0xa00, 0xa04
	// Urgent is each helper's second command slot, which it looks at
	// first: the core puts SharedMailStop | FromCore there to stop a
	// picture and 0 when it resets the helper; the helper clears it when
	// it has stopped.
	Urgent [2]Reg // 0xa08, 0xa0c
	// MailWords is the mail's first word: a helper command's parameters
	// follow on 4 bytes up to 14D4BA4 (up to 0x66 words; the parser is
	// mailbox_test.go). Our core puts a job's address there. Only helper
	// 0 has words.
	MailWords Reg // 0xa10
}

// MailWord is the mailbox's word i (0 at 0xA10, up to 0x65 at 0xBA4).
func (s Shared) MailWord(i int) Reg { return s.MailWords + Reg(4*i) }

// Bits of the event groups (Set, Status, Enable, Ack).
const (
	SharedEventDMAMore = 1 << 0 // the encoder core's: the DMA engine wants DMANext
	SharedEventDMADone = 1 << 1 // the encoder core's: the DMA copy is done
	SharedEventPipe    = 1 << 5 // helper 0's: the pipe has the next macroblock (raw, in 10D4014)
	SharedEventMail    = 1 << 8 // a mail: from helper 0 (core's), a command (helper's)
	SharedEventMail1   = 1 << 9 // the encoder core's: helper 1 answered
	SharedEnableMail   = 0xf00  // the Enable value in use
)

// Ports values.
const (
	SharedPortsTwoPipes = 0x1f
	SharedPortsOnePipe  = 0x1d
)

// The mailbox command word: who sent it (bits 24-25) and the kind (bits
// 16-18).
const (
	SharedMailFromCore   = 0x01000000
	SharedMailFromHelper = 0x02000000
	SharedMailFromMask   = 0x03000000
	SharedMailKindShift  = 16
	SharedMailKindMask   = 7
)

// The mail kinds.
const (
	SharedMailReset    = 0 // the helper's answer after it (re)started
	SharedMailSequence = 1 // a sequence's parameters
	SharedMailKind2    = 2 // one byte in word 0; use unknown
	SharedMailPicture  = 3 // a picture's parameters; the answer carries the report
	SharedMailStop     = 4 // in Urgent: stop the picture
	SharedMailKind7    = 7 // no words; use unknown
)

// SharedAt is the shared register file at base.
func SharedAt(base uint32) Shared {
	r := func(o uint32) Reg { return Reg(base + o) }
	s := Shared{
		Reset: r(0x000), Ports: r(0x004),
		Set:    [3]Reg{r(0x010), r(0x014), r(0x018)},
		Status: [3]Reg{r(0x020), r(0x024), r(0x028)},
		Enable: [3]Reg{r(0x030), r(0x034), r(0x038)},
		Ack:    [3]Reg{r(0x040), r(0x044), r(0x048)},
		Unk0A0: r(0x0a0),

		DMAStart: r(0x200), DMANext: r(0x210), DMABusy: r(0x230), DMALocal: r(0x240),
		DMALocalEnd: r(0x244), DMALength: r(0x24c), DMAMode: r(0x254), DMASource: r(0x258),
		DMAUnk25C: r(0x25c),

		RowRC:  [2]RowRC{RowRCAt(base + 0x600), RowRCAt(base + 0x800)},
		Report: [2]Report{ReportAt(base + 0xba8), ReportAt(base + 0xbc8)},

		Command: [2]Reg{r(0xa00), r(0xa04)},
		Urgent:  [2]Reg{r(0xa08), r(0xa0c)},

		MailWords: r(0xa10),
	}
	for pipe := range s.MBData {
		for stream := range s.MBData[pipe] {
			s.MBData[pipe][stream] = MBStreamAt(base + 0x300 + uint32(pipe)*0x80 + uint32(stream)*0x20)
		}
	}
	return s
}

// The shared register file as the core and as the helper see it.
var (
	Core   = SharedAt(SharedCore)
	Helper = SharedAt(SharedHelper)
)

// The macroblock data streams, in the order of their registers.
const (
	MBStream24      = 0 // 24 bytes a macroblock
	MBStream2       = 1 // 2 bytes a macroblock
	MBStreamQuarter = 2 // 1/4 byte a macroblock (rows padded to 8 bytes a 32)
	MBStream8       = 3 // 8 bytes a macroblock
)

// MBStream is a macroblock data stream of a pipe: where its data go, set
// per picture (core.go). With two pipes each takes every other row: pipe 0
// the first half (rounded up) of the rows, pipe 1 the rest, starting a row
// further on.
type MBStream struct {
	// Row is the stream's bytes a row; only the 1/4-byte stream's is
	// written (14D4348, 14D43C8).
	Row Reg // +0x08
	// Size is the stream's bytes for the picture (rows times the row's).
	Size Reg // +0x0c
	// Start is where the picture's data start: 0 for a frame or first
	// field, past the first field's for a second field (the 24-byte
	// stream: 24 bytes + 1/4 byte streams of a field; the 1/4-byte stream:
	// its own field's size plus twice the 24-byte stream's).
	Start Reg // +0x18
	// Skip is 0 with one pipe; with two, a row of the stream (twice that
	// for the 1/4-byte stream): the other pipe's row to leap.
	Skip Reg // +0x1c
}

// MBStreamAt is the macroblock data stream at base.
func MBStreamAt(base uint32) MBStream {
	return MBStream{Row: Reg(base + 0x08), Size: Reg(base + 0x0c), Start: Reg(base + 0x18), Skip: Reg(base + 0x1c)}
}

// RowRC is a pipe's row rate control: the picture's settings (the same
// values for both pipes), the helper's macroblock set-up, the QP the unit
// computes for each macroblock and what the unit counts as it codes (both
// pipes' are added up after the picture). With two pipes each takes half
// of the level and budget.
type RowRC struct {
	// Start is written 1 by the helper as the picture's macroblock loop
	// starts, after 10E0030=8 (helper.go).
	Start Reg // +0x00
	// Unk004 is written 1 by the helper in its engine set-up with
	// 10E0014=0xFFFF, 10E0018=0x200 (job.go).
	Unk004 Reg // +0x04
	// MBWidth is the picture's width in macroblocks (set by the helper).
	MBWidth Reg // +0x10
	// MBCount is the macroblocks the helper codes: width times height.
	MBCount Reg // +0x18

	// Gain is the gain the rate control learnt from the last picture's
	// sums: 0..256, 256*(Unk080+SumB/..)/(Unk080+SumA/..).
	Gain Reg // +0x20
	// GainRest is 256 - Gain (0 at 256).
	GainRest Reg // +0x24
	// Target is the bits the picture is to take; with the smooth rate
	// control the helper rotates it among the job's three targets each
	// macroblock.
	Target Reg // +0x28
	// LowQPScaled is the low QP (the picture QP - 20, at least 10, at
	// most 24 intra / 26 inter) times the macroblocks over the divider
	// (60).
	LowQPScaled Reg // +0x2c
	// MBs is the picture's macroblocks.
	MBs Reg // +0x30
	// InvMBs is their reciprocal, 2^32 units.
	InvMBs Reg // +0x34
	// DividerQP is divider << 8 | low QP (the divider 60).
	DividerQP Reg // +0x38
	// QPScaled is the picture's QP times the macroblocks over the
	// divider.
	QPScaled Reg // +0x3c
	// Inter is 1 for a P or B picture, 0 for I/IDR.
	Inter Reg // +0x40
	// InvRate is the reciprocal (2^32 units) of the bits a picture
	// period (rate >> fields).
	InvRate Reg // +0x44
	// InvSize is the reciprocal (2^32 units) of the picture's expected
	// size (inter or intra).
	InvSize Reg // +0x48
	// Unk050 is 5.
	Unk050 Reg // +0x50
	// Spent is a count of the bits spent: set to the level (halved with
	// two pipes), or 0, at the picture's start; the unit takes the bits
	// off it as the picture is coded (from 0 it ends at minus about the
	// picture's bits). Our helper holds the macroblocks' QPs to it
	// (encoder helperprog.go).
	Spent Reg // +0x54
	// Budget is the bits the picture may take before the helper steps in:
	// min(level/4, max(level/32, 32)+6000) + 5*rate (one pipe; with two
	// half).
	Budget Reg // +0x58
	// Unk05C is 0x1000 (both pipes).
	Unk05C Reg // +0x5c

	// Unk06C is read after the picture, halved with two pipes like the
	// level written to Spent.
	Unk06C Reg // +0x6c
	// QP is the QP the unit computed for the macroblock (low byte): the
	// helper reads it after each pipe step as the macroblock's base QP.
	QP Reg // +0x70
	// Bits is the picture's coded bits, for the HRD entry and the rate
	// control.
	Bits Reg // +0x7c
	// Unk080 is a count the gain is formed with: 256*(Unk080 +
	// SumB/(Bits/rate*2))/(Unk080 + SumA/..).
	Unk080 Reg // +0x80
	// SumA is a 64-bit sum (+0x84 high word, +0x88 low) the gain is formed
	// from (the pipes' are added).
	SumAHi, SumALo Reg // +0x84, +0x88
	// SumB is the other 64-bit sum.
	SumBHi, SumBLo Reg // +0x8c, +0x90
	// Unk094 is a second bit count, at most Bits: Bits - Unk094 is the
	// overshoot the next picture's size is estimated from.
	Unk094 Reg // +0x94
	Unk098 Reg // +0x98
	Unk09C Reg // +0x9c
	// Unk0A0 is a cost-like sum, added to 3 times the helper's cost.
	Unk0A0 Reg // +0xa0
	// QPSum is the macroblocks' QPs summed (low 19 bits): the helper puts
	// it in its report (Report.QPSum) and the core takes the mean QP
	// from it.
	QPSum Reg // +0xa4
	// MBStat0 is the macroblock's statistics, two halfwords, the helper
	// reads after each pipe step (the high one is the activity it
	// compares and puts in 10E1020).
	MBStat0 Reg // +0xc0
	// MBStat1 is two more.
	MBStat1 Reg // +0xcc
}

// RowRCQPSumMask is the valid bits of RowRC.QPSum.
const RowRCQPSumMask = 0x7ffff

// RowRCAt is the row rate control at base.
func RowRCAt(base uint32) RowRC {
	r := func(o uint32) Reg { return Reg(base + o) }
	return RowRC{
		Start: r(0x00), Unk004: r(0x04), MBWidth: r(0x10), MBCount: r(0x18),
		Gain: r(0x20), GainRest: r(0x24), Target: r(0x28), LowQPScaled: r(0x2c),
		MBs: r(0x30), InvMBs: r(0x34), DividerQP: r(0x38), QPScaled: r(0x3c),
		Inter: r(0x40), InvRate: r(0x44), InvSize: r(0x48), Unk050: r(0x50),
		Spent: r(0x54), Budget: r(0x58), Unk05C: r(0x5c),
		Unk06C: r(0x6c), QP: r(0x70), Bits: r(0x7c), Unk080: r(0x80),
		SumAHi: r(0x84), SumALo: r(0x88), SumBHi: r(0x8c), SumBLo: r(0x90),
		Unk094: r(0x94), Unk098: r(0x98), Unk09C: r(0x9c), Unk0A0: r(0xa0),
		QPSum: r(0xa4), MBStat0: r(0xc0), MBStat1: r(0xcc),
	}
}

// Report is what a helper answers of a command, after the mailbox words:
// the header always, the rest for a picture. The core takes the cost of
// both pipes.
type Report struct {
	// Header: bits 24-31 and 16-23 two bytes of the answer (0, 2 for a
	// picture; 0, 1 at the helper's start), bits 8-15 the channel, bits
	// 0-7 a byte (0).
	Header Reg // +0x00
	// QPSum is the macroblocks' QPs summed (RowRC.QPSum); the core divides
	// it by the macroblocks for the class's mean QP (clamped 1..51).
	QPSum Reg // +0x04
	// Count is 10E0D90 & 0x1FFF << 16 | 10E0D94; the core keeps it over
	// the macroblocks as the gain statistic.
	Count Reg // +0x08
	// RatioSum is the helper's ratio statistic summed (0 from our
	// helper).
	RatioSum Reg // +0x0c
	// Counter is the helper's counter (0 from our helper).
	Counter Reg // +0x18
	// Cost is the sum over the macroblocks of a cost by their QP (0 from
	// our helper).
	Cost Reg // +0x1c
}

// ReportChannelShift is where the channel lies in Report.Header.
const ReportChannelShift = 8

// ReportAt is the report at base.
func ReportAt(base uint32) Report {
	return Report{Header: Reg(base), QPSum: Reg(base + 4), Count: Reg(base + 8), RatioSum: Reg(base + 0xc),
		Counter: Reg(base + 0x18), Cost: Reg(base + 0x1c)}
}

// The register space of a cluster's units (its control, shared file,
// buffers and the engines behind them), as the core and as the helper see
// it.
const (
	CoreUnits   = 0x14d0000
	HelperUnits = 0x10d0000
	UnitsSpan   = 0x20000
)
