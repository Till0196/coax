// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// The macroblock coding engine (10E) that the helper core (10D) drives. The
// helper sees it at 0x10E0000; the encoder core sees the same block at
// 0x14E0000 (the helper enables bit 0x200 of Status for the core, which
// waits for it in CoreStatus; both write the FIFO's limit; both acknowledge
// at 0x014) and uses its row FIFO side. Pipe 1 (the second coding unit) is
// at base + 0x10000. It is programmed per sequence (sequenceOpenWrites),
// per picture (pictureWrites) and per row of macroblocks (SliceStart,
// ListWrites, WeightWrites, deblockWrites), then run one macroblock at a
// time (helperprog "picture").
//
// Width: everything at 0x10E0000..0x10E1FFF is 16 bits wide (STH/LDUH;
// values above 0xFFFF are cut). The motion hint unit at 0x10E8000 has
// 16-bit unit controls (0x8000/0x8004) and 32-bit registers from 0x8010
// (LD/ST).
//
// The engine is made of sub-units, each with a reset register at its base
// and a control register at base+4 (0x1808's at 0x180C). They are stopped
// (holdWrites) with control = 2 (the top one 6, the hint unit 0x22),
// reset = 1, control = 0, and released (sequenceOpenWrites) with control =
// 2/6/0x22, reset = 0, control = 1 (the hint unit 0x11).
//
// Registers whose meaning is not known are named for their offset.
const (
	MBEngineHelper = 0x10e0000 // the engine's base as the helper sees it
	MBEngineCore   = 0x14e0000 // as the core sees it
	MBEnginePipe1  = 0x10000   // pipe 1's engine is at base + this
)

// MBEngineUnit is a sub-unit's reset and control pair.
type MBEngineUnit struct {
	Reset   Reg // 1 holds the unit in reset, 0 releases it
	Control Reg // 2 stop (6 the top unit, 0x22 the hint unit), 0 held, 1 run (0x11 the hint unit)
}

// Values of the sub-unit registers.
const (
	MBEngineUnitReset   = 1 // Reset: held
	MBEngineUnitRelease = 0 // Reset: released
	MBEngineUnitStop    = 2 // Control: stop, before a reset change (top unit: 6, hint unit: 0x22)
	MBEngineUnitHeld    = 0 // Control: after a stop
	MBEngineUnitRun     = 1 // Control: run (hint unit: 0x11)

	MBEngineTopStop  = 6    // Control of the top unit: stop
	MBEngineTopSetUp = 5    // Control of the top unit: taking the picture registers (Size..PipeLag)
	MBEngineTopRun   = 1    // Control of the top unit: run
	MBEngineHintStop = 0x22 // Control of the hint unit: stop
	MBEngineHintRun  = 0x11 // Control of the hint unit: run
)

// Status (0x10E0010) / Ack (0x10E0014) / IntEnable bits.
const (
	MBEngineStarted  = 0x20  // the engine took a picture start (Command = 8)
	MBEngineMBDone   = 0x80  // the engine finished a macroblock (Command = 0x20)
	MBEngineRowReady = 0x200 // a macroblock row is in the FIFO: the one bit the helper lets reach the core (IntEnable), which waits for it in CoreStatus
	MBEngineAckAll   = 0xffff
)

// Command (0x10E0030) values.
const (
	MBEngineCmdPicture = 8    // start a picture (after its set-up writes)
	MBEngineCmdMB      = 0x20 // code the next macroblock
)

// Slice types (SliceType, 0x10E0078): H.264's slice_type numbers.
const (
	MBEngineSliceP = 0
	MBEngineSliceB = 1
	MBEngineSliceI = 2
)

// PictureFlags (0x10E005C) bits.
const (
	MBEngineFlagField        = 1 << 0  // coded as fields (sequence w0 bit 16)
	MBEngineFlagBottom       = 1 << 1  // the bottom field (S50 == 1)
	MBEngineFlagS20          = 1 << 2  // sequence flag S+0x20 (w2 bit 16)
	MBEngineFlagChromaShift  = 3       // bits 7:3: chroma_qp_index_offset (5 bits, S+0x61)
	MBEngineFlagChroma2Shift = 8       // bits 12:8: S+0x60 (5 bits; likely second_chroma_qp_index_offset)
	MBEngineFlagS5F          = 1 << 13 // picture flag S+0x5F (always 1 in our use; likely transform_8x8_mode)
	MBEngineFlagS1C          = 1 << 14 // sequence flag S+0x1C (w2 bit 24; the rate control's K = 1 - it)
)

// RowSync (0x10E0074) bits, two pipes only (0 with one pipe).
const (
	MBEngineRowPrevStart = 0x80   // the row before starts a slice / at the last row: the field ends
	MBEngineRowLink      = 0x8000 // a row in a slice whose previous row started one; bits 6:0 = row-1
	MBEngineRowMask      = 0x7f
	MBEngineRowLastField = 0x80 // written after the last macroblock of the last row
)

// QP2 (0x10E009C) bits.
const (
	MBEngineQP2Same = 0x40 // with it bits 5:0 are the macroblock QP itself; without, a lower QP
)

// ScalingSelect (0x10E0410) fields.
const (
	MBEngineScalingLoad      = 1 // load the list selected in bits 7:4
	MBEngineScalingListShift = 4 // 0..5 the 4x4 lists (intra Y Cb Cr, inter Y Cb Cr), 6/7 the 8x8 intra/inter
)

// ListStrobe (0x10E0844) / DistanceStrobe (0x10E0880) bits.
const (
	MBEngineListWrite     = 0x40 // ListStrobe: write the entry; bit 5 the list (0/1), bits 4:0 its index
	MBEngineListL1        = 0x20
	MBEngineDistanceWrite = 0x80 // DistanceStrobe: write the entry; bit 6 top, bits 5:0 the buffer
	MBEngineDistanceTop   = 0x40
)

// MBEngine is the macroblock coding engine (10E).
type MBEngine struct {
	// Units are the sub-units in their reset order: 0x0000
	// (the top), 0x0180, 0x0C00, 0x0800, 0x0900, 0x1808, 0x1800, 0x1000,
	// 0x1200, 0x0400, 0x1C00, 0x1CB0, 0x1400, 0x8000 (the hint unit).
	Units [14]MBEngineUnit

	// Top unit (0x0000..0x00FF).
	Reset     Reg // 0x000: the top unit's reset (Units[0].Reset)
	Control   Reg // 0x004: 6 stop, 5 set-up (around Size..PipeLag each picture), 1 run
	Unk008    Reg // 0x008: 0 at a sequence's open
	Unk00C    Reg // 0x00C: 1 at a sequence's open
	Status    Reg // 0x010: MBEngineStarted, MBEngineMBDone (polled by wait16)
	Ack       Reg // 0x014: write bits to acknowledge them; 0xFFFF at open; an interrupt handler writes IntStatus
	IntEnable Reg // 0x018: which Status bits reach the core's CoreStatus: MBEngineRowReady at open
	// 0x01C: the enabled status bits pending, the core's view: bit
	// MBEngineRowReady; it also raises 0x14D0014 bit 0x2000 (pipe 0).
	// Polled by our core's frame loop.
	CoreStatus Reg
	Unk020     Reg // 0x020: 0 at open
	IntStatus  Reg // 0x024: the enabled bits pending: an interrupt handler acknowledges them (Ack = IntStatus)
	Command    Reg // 0x030: MBEngineCmdPicture at a picture's start, MBEngineCmdMB per macroblock
	Unk044     Reg // 0x044: 0 at open
	Unk050     Reg // 0x050: 0 at open
	Size       Reg // 0x054: MB width << 8 | MB height (7 bits each), per picture
	Unk058     Reg // 0x058: 0 per picture
	// 0x05C: the picture's flags (MBEngineFlag*).
	PictureFlags Reg
	SliceFirst   Reg // 0x070: 1 when the row starts a slice (SliceStart)
	RowSync      Reg // 0x074: two-pipe row link (MBEngineRow*); 0x80 at the last row of a field
	SliceType    Reg // 0x078: MBEngineSliceI/P/B, per row start
	// 0x080: 0 for the first picture after the channel's open or with
	// S+0x328; else 1 for IDR/I/P, 0 for B.
	Unk080 Reg
	QP     Reg // 0x084: the macroblock's QP (bits 5:0), per macroblock before Command's next 0x20
	// 0x09C: QP base | 0x40 at a picture's start ((S+0x1BF & 0x3F) + 0x40);
	// per macroblock QP|0x40, or for a flat macroblock (activity < 0x800,
	// QP > 32) a lower QP max(QP-5, act>>7 + 32) without 0x40.
	QP2       Reg
	PipeMode  Reg // 0x0B0: 1 one pipe, 2 two pipes (walking row pairs), at open
	PipeLink  Reg // 0x0B4: 0 one pipe, 0x8006 two pipes, at open
	PipeLag   Reg // 0x0B8: two pipes: (MB width - 6) & 0x7F (the second pipe's lag); one pipe 0
	StatReady Reg // 0x0F8: nonzero when the macroblock's statistics (Stat0/Stat1) are ready

	// Unit 0x0180.
	Unk190 Reg    // 0x190: 0 at open
	Unk1A0 [7]Reg // 0x1A0..0x1B8: 0x10 each at open

	// Scaling lists (unit 0x0400).
	ScalingSelect Reg // 0x410: list << 4 | 1 (MBEngineScaling*), before its data
	// 0x480..0x4FC: a list's data, two coefficients a word: the low byte
	// coefficient i, the high byte i + n/2 (n = 16 or 64, raster order);
	// 8 words for a 4x4 list, 32 for an 8x8 one. 0x1010 each: flat 16.
	ScalingData [32]Reg

	// References (unit 0x0800).
	Unk808 Reg // 0x808: 0 at open
	// 0x80C/0x810: fields: the index of the top (bits 4:0) and bottom (bits
	// 9:5) field in list 0 / list 1 (mail w2 bytes); frames 0.
	FieldRefIdx [2]Reg
	Reference   Reg    // 0x814: 0x100 a reference picture (nal_ref_idc != 0), else 0
	TwoPipes    Reg    // 0x818: 1 with two pipes and MB width >= 7
	SpatialPred Reg    // 0x81C: H.264's spatial mv prediction flag of B slices (bit 0), per row start
	POC         [4]Reg // 0x820..0x82C: top POC high, low, bottom POC high, low (16 bits each)
	Unk830      Reg    // 0x830: 1 per picture
	WeightMode  Reg    // 0x834: (mail w5 bits 25:24) << 1 | bit 28; weighted prediction idc pair
	WeightDenom Reg    // 0x838: (w5 bits 10:8) << 3 | bits 2:0
	ListSizes   Reg    // 0x840: num_ref_idx_l0_active-1 (bits 4:0) | l1 (bits 9:5)
	ListStrobe  Reg    // 0x844: writes ListEntry/ListWords as entry index of a list (MBEngineList*)
	// 0x848: the list entry: bits 5:0 the picture's buffer (recon + 8 *
	// channel), bit 6 the bottom field, bits 14 and 15 mail flags.
	ListEntry Reg
	ListWords [3]Reg // 0x84C..0x854: six more bytes of the entry, high byte first (0 in our use)
	// 0x880: writes the distance (DPB) entry below: 0x80 | top << 6 |
	// buffer (recon + 8 * channel), one for each of S+0x74 entries.
	DistanceStrobe Reg
	DistanceA      Reg    // 0x884: 4 bits; 1 when the reference's POC >= the current one
	DistanceB      Reg    // 0x888: B << 1 | C; B = 1 for fields
	DistancePOC    [4]Reg // 0x88C..0x898: the reference's top POC high, low, bottom POC high, low
	Unk8D0         Reg    // 0x8D0: (MB width * 9 + 63) >> 6 & 0x3F per picture

	// Mode decision and statistics (unit 0x0C00).
	// 0xC20/0xC24: a macroblock's list 0 / list 1 motion: bits 7:0 x (7 bits
	// sign-extended), bit 8 the reference's parity, bits 15:9 y. The hint
	// unit's vectors are written here; read back after the QP write they
	// tell what the last macroblock was coded with (0 = no motion, with
	// bit 8 masked: the "flat" test).
	Motion [2]Reg
	RCMode Reg    // 0xC38: 0x30, or 0x100 for the flat rate control (mail w0 bit 28)
	UnkC3C Reg    // 0xC3C: 0x30 at open
	UnkC48 Reg    // 0xC48: 0x7F7F per picture
	UnkC4C [5]Reg // 0xC4C..0xC5C: 0 at open
	// 0xC60: which references to search, 2 bits: IDR/I 3, P frame 1, P
	// field 3 (2 for the second field of an I frame), B 3 (2 with a negative
	// top POC).
	RefSearch Reg
	// 0xC64: S+0x54 << 4 | 2 | (P fields: the list 0 index of the field's
	// own parity) & 1.
	UnkC64  Reg
	UnkC68  Reg    // 0xC68: 8 at open
	UnkC6C  Reg    // 0xC6C: 1000 at open
	PredC70 Reg    // 0xC70: B prediction from the colocated block: 0 spatial, 0x10 temporal
	UnkC74  Reg    // 0xC74: 1000 at open
	UnkC78  Reg    // 0xC78: 200 at open
	PredC7C Reg    // 0xC7C: the same: 0 spatial, 0x28 temporal
	UnkC80  [4]Reg // 0xC80..0xC8C: 1, 3, 3, 9 at open
	UnkCA0  [5]Reg // 0xCA0..0xCB0: 3, 6, 7, 0xC, 1 at open
	ModeQP  Reg    // 0xCC0: the QP base clamped to 12..51 (& 0x3F) per picture: the mode decision's QP
	UnkCC4  Reg    // 0xCC4: 0x8601 at open and per picture
	UnkCC8  Reg    // 0xCC8: 0xF at open
	UnkCCC  Reg    // 0xCCC: 0xF at open
	// 0xCD0: a mode decision bias: 0x60 with scaling lists, 0xA0 without
	// (0x60 at open); 0xFFF when the bits run over (emergency).
	ModeBias0 Reg
	UnkCD4    Reg // 0xCD4: 0x80 at open
	// 0xCD8: a second bias: 0x32 at open; per picture from the picture's
	// second activity (S+0x1D0 << (field - 4) >> 4 for B, >> 6 else, used
	// when > 50, max 255), 0xFF at QP >= 50 or in an emergency.
	ModeBias1 Reg
	// 0xCDC: a search effort level, 0..2 (0 at open): B pictures 1 (0 at
	// QP >= 52); the first field / a frame 2 (1 at QP >= 52); a second field
	// 2 below QP 46, 1 below 52, else 0.
	ModeEffort     Reg
	ThresholdIndex Reg // 0xCE0: the threshold RAM's index for ThresholdData
	// 0xCE4: threshold RAM data, two bytes a word (high first): 0..9 the 20
	// QP weights, 10..22/23..35/36..48 three activity tables (13 words each,
	// raised by the weights for P and B), 49..57 the threshold table (18
	// bytes, AltTable picks one of two).
	ThresholdData Reg
	Stat0         Reg     // 0xD10: macroblock statistic (>> 8 used: the rate control's stat 0)
	Stat1         Reg     // 0xD14: macroblock statistic (the rate control's stat 1)
	BitsHigh      Reg     // 0xD90: bits counted, bits 12:0 the high half
	BitsLow       Reg     // 0xD94: bits counted, the low half: (BitsHigh & 0x1FFF) << 16 | BitsLow
	UnkDD0        [12]Reg // 0xDD0..0xDFC: 0 at open

	// Quantiser (unit 0x1000).
	ScalingOn Reg // 0x1008: 1 with scaling lists
	// 0x1010..0x101C: two pairs of per-diagonal 4-bit values like the
	// rounding below (7 nibbles a pair, from the low nibble up): 0x7600,
	// 0x887 and 0x8800, 0x888 at open. Thresholds of the 4x4 coefficients.
	CoefThreshold [4]Reg
	// 0x1020: the macroblock's activity for the quantiser: (stat + 0x80) >>
	// 1, at most 2000 (2000 at open), per macroblock.
	Activity Reg
	// Quantiser rounding: 4-bit offsets, one per anti-diagonal of the
	// block from the low nibble of the first word up (4x4: 7 over a pair
	// of words, 8x8: 15 over four). Scaling lists: v*0x1111, v*0x111 from
	// the type and QP base (roundingWrites); without: fixed falling values
	// (0xACDF, 0x567, ...).
	RoundIntra4x4 [4]Reg // 0x1030..0x103C: two pairs (the second a copy)
	RoundInter4x4 [4]Reg // 0x1040..0x104C: two pairs
	RoundIntraDC  Reg    // 0x1050: 4 bits, the intra 4x4 value (10 without scaling)
	RoundIntra8x8 [4]Reg // 0x1060..0x106C
	RoundInter8x8 [4]Reg // 0x1070..0x107C
	// 0x1090: emergency coding: bits 11:8 2t+1, 6:4 t, 2:0 t (t = 7, 6 or
	// 5 as the bits spent run over the budget), else 0.
	Emergency Reg
	Unk1110   Reg    // 0x1110: 0 at open
	Unk1120   [3]Reg // 0x1120..0x1128: 0 at open
	Unk1130   [3]Reg // 0x1130..0x1138: 0 at open
	Unk1140   Reg    // 0x1140: 0 at open
	Unk1150   Reg    // 0x1150: 1 at open

	// Unit 0x1200.
	// 0x1210: (Weights[min(max(QPbase-12, 0)/2, 19)] + 1) >> 2 per picture:
	// the QP weight of the QP base.
	QPWeight Reg
	Unk1214  Reg // 0x1214: 0 at open

	// Unit 0x1800 / 0x1808.
	Unk1828 Reg // 0x1828: 0 at open
	Unk182C Reg // 0x182C: 1 at open

	// Unit 0x1C00: deblocking and output.
	// 0x1C08: bit 0 AltTable (S+0x28), bits 9:4 the slice QP (S5E), bit 12
	// list 0 has more than one entry, bit 13 list 1.
	SliceFlags  Reg
	TableSelect Reg // 0x1C20: scaling lists << 1 | AltTable
	RowPulse    Reg // 0x1C24: 1 at the 3rd macroblock of each row but the first, and 2 before the end
	Unk1C2C     Reg // 0x1C2C: 0x6060 at open
	// The row FIFO: the engine writes each row's coded macroblock data into
	// a ring in SDRAM, and the core hands the rows on to the stream unit.
	FIFOSizeHi, FIFOSizeLo   Reg // 0x1C30/0x1C34: its size in bytes, bits 23:16 / 15:0 (0x124E00 at open)
	FIFOStartHi, FIFOStartLo Reg // 0x1C40/0x1C44: its start offset (0 at open)
	// 0x1C50/0x1C54: the last byte the engine may write. The helper sets
	// the ring's end at open (0x12/0x4DFF); the core moves it each picture
	// to the last row the stream unit took (after the picture's stream).
	// The low half is written first.
	FIFOLimitHi, FIFOLimitLo Reg
	// 0x1C58/0x1C5C: where the row just finished starts in the FIFO, bits
	// 23:16 (read & 0xFF) / 15:0: read by the core right after
	// MBEngineRowReady and handed to the stream unit.
	RowAddrHi, RowAddrLo Reg
	RowTaken             Reg    // 0x1C68: write 1 once the row's address is read: the engine may flag the next
	Unk1C74              Reg    // 0x1C74: 7 at open
	DCShift              [2]Reg // 0x1C78/0x1C7C: intra/inter: DC value of the 8x8 list << 8 | the 4x4 list's (6 bits)
	Unk1C80              Reg    // 0x1C80: 3 with two pipes and not a B picture, else 0
	NonRef               Reg    // 0x1CB8: 0 a reference picture, 0x11 not (unit 0x1CB0)
	// 0x1CBC: deblocking: beta offset << 12 | (alpha offset & 0xF) << 8 |
	// disable_deblocking_filter_idc & 3.
	Deblock Reg

	// The motion hint unit (0x8000): it reads per-macroblock vectors of a
	// picture from SDRAM for Motion (used when the picture's mail w35 bit
	// 1 is set, which our code never does).
	Hint MBEngineHint
}

// MBEngineHint is the engine's motion hint unit (0x10E8000), 32-bit from
// 0x10.
type MBEngineHint struct {
	Reset   Reg // 0x8000: (Units[13].Reset)
	Control Reg // 0x8004: 0x22 stop, 0 held, 0x11 run
	Status  Reg // 0x8010: nonzero when the macroblock's vectors are in (polled, or acknowledged by an interrupt handler)
	Ack     Reg // 0x8018: 1 after a Status, or Status written back
	Command Reg // 0x8020: 3 at a picture's start (after Command = 8), 1 per macroblock but the last
	Size    Reg // 0x8030: MB width << 8 | MB height (7 bits each)
	Frame   Reg // 0x8034: 1 a frame, 0 fields
	// 0x8038: the vectors' SDRAM address: 0x80154000 + slot * 0x22000, +
	// n << 11 for a bottom field (n = 0x803C's value).
	Address Reg
	Unk803C Reg // 0x803C: a byte of the channel's state (S+0x325), also the bottom field's offset
	// 0x8054/0x8060: top/bottom (or the one frame) candidate: bits 20:16 a
	// 5-bit cost class (averaged), bits 5:4 the lists used (1 list 0, 2
	// list 1, 3 both).
	Kind [2]Reg
	// 0x8058/0x8064: its vector: bit 24 parity, bits 15:9 y, bit 8 a flag,
	// bits 6:0 x (7 bits signed).
	Vector [2]Reg
}

// MBEngineAt is the engine at base.
func MBEngineAt(base uint32) MBEngine {
	r := func(off uint32) Reg { return Reg(base + off) }
	arr := func(off uint32, out []Reg) {
		for i := range out {
			out[i] = r(off + 4*uint32(i))
		}
	}
	unit := func(off uint32) MBEngineUnit {
		ctl := off + 4
		if off == 0x1808 {
			ctl = 0x180c
		}
		return MBEngineUnit{Reset: r(off), Control: r(ctl)}
	}
	e := MBEngine{
		Reset: r(0x000), Control: r(0x004), Unk008: r(0x008), Unk00C: r(0x00c),
		Status: r(0x010), Ack: r(0x014), IntEnable: r(0x018), CoreStatus: r(0x01c), Unk020: r(0x020), IntStatus: r(0x024),
		Command: r(0x030), Unk044: r(0x044), Unk050: r(0x050), Size: r(0x054), Unk058: r(0x058),
		PictureFlags: r(0x05c), SliceFirst: r(0x070), RowSync: r(0x074), SliceType: r(0x078),
		Unk080: r(0x080), QP: r(0x084), QP2: r(0x09c),
		PipeMode: r(0x0b0), PipeLink: r(0x0b4), PipeLag: r(0x0b8), StatReady: r(0x0f8),
		Unk190: r(0x190), ScalingSelect: r(0x410),
		Unk808: r(0x808), Reference: r(0x814), TwoPipes: r(0x818), SpatialPred: r(0x81c), Unk830: r(0x830),
		WeightMode: r(0x834), WeightDenom: r(0x838), ListSizes: r(0x840), ListStrobe: r(0x844),
		ListEntry: r(0x848), DistanceStrobe: r(0x880), DistanceA: r(0x884), DistanceB: r(0x888),
		Unk8D0: r(0x8d0),
		RCMode: r(0xc38), UnkC3C: r(0xc3c), UnkC48: r(0xc48), RefSearch: r(0xc60), UnkC64: r(0xc64),
		UnkC68: r(0xc68), UnkC6C: r(0xc6c), PredC70: r(0xc70), UnkC74: r(0xc74), UnkC78: r(0xc78),
		PredC7C: r(0xc7c), ModeQP: r(0xcc0), UnkCC4: r(0xcc4), UnkCC8: r(0xcc8), UnkCCC: r(0xccc),
		ModeBias0: r(0xcd0), UnkCD4: r(0xcd4), ModeBias1: r(0xcd8), ModeEffort: r(0xcdc),
		ThresholdIndex: r(0xce0), ThresholdData: r(0xce4), Stat0: r(0xd10), Stat1: r(0xd14),
		BitsHigh: r(0xd90), BitsLow: r(0xd94),
		ScalingOn: r(0x1008), Activity: r(0x1020), RoundIntraDC: r(0x1050), Emergency: r(0x1090),
		Unk1110: r(0x1110), Unk1140: r(0x1140), Unk1150: r(0x1150),
		QPWeight: r(0x1210), Unk1214: r(0x1214), Unk1828: r(0x1828), Unk182C: r(0x182c),
		SliceFlags: r(0x1c08), TableSelect: r(0x1c20), RowPulse: r(0x1c24),
		Unk1C2C: r(0x1c2c), FIFOSizeHi: r(0x1c30), FIFOSizeLo: r(0x1c34), FIFOStartHi: r(0x1c40), FIFOStartLo: r(0x1c44),
		FIFOLimitHi: r(0x1c50), FIFOLimitLo: r(0x1c54), RowAddrHi: r(0x1c58), RowAddrLo: r(0x1c5c), RowTaken: r(0x1c68),
		Unk1C74: r(0x1c74), Unk1C80: r(0x1c80),
		NonRef: r(0x1cb8), Deblock: r(0x1cbc),
		Hint: MBEngineHint{
			Reset: r(0x8000), Control: r(0x8004), Status: r(0x8010), Ack: r(0x8018), Command: r(0x8020),
			Size: r(0x8030), Frame: r(0x8034), Address: r(0x8038), Unk803C: r(0x803c),
			Kind: [2]Reg{r(0x8054), r(0x8060)}, Vector: [2]Reg{r(0x8058), r(0x8064)},
		},
	}
	for i, off := range []uint32{0x0000, 0x0180, 0x0c00, 0x0800, 0x0900, 0x1808, 0x1800,
		0x1000, 0x1200, 0x0400, 0x1c00, 0x1cb0, 0x1400, 0x8000} {
		e.Units[i] = unit(off)
	}
	arr(0x1a0, e.Unk1A0[:])
	arr(0x480, e.ScalingData[:])
	arr(0x80c, e.FieldRefIdx[:])
	arr(0x820, e.POC[:])
	arr(0x84c, e.ListWords[:])
	arr(0x88c, e.DistancePOC[:])
	arr(0xc20, e.Motion[:])
	arr(0xc4c, e.UnkC4C[:])
	arr(0xc80, e.UnkC80[:])
	arr(0xca0, e.UnkCA0[:])
	arr(0xdd0, e.UnkDD0[:])
	arr(0x1010, e.CoefThreshold[:])
	arr(0x1030, e.RoundIntra4x4[:])
	arr(0x1040, e.RoundInter4x4[:])
	arr(0x1060, e.RoundIntra8x8[:])
	arr(0x1070, e.RoundInter8x8[:])
	arr(0x1120, e.Unk1120[:])
	arr(0x1130, e.Unk1130[:])
	arr(0x1c78, e.DCShift[:])
	return e
}

// The macroblock coding engine as the helper sees it (pipe 0), and each
// pipe's as the core sees it.
var (
	Engine     = MBEngineAt(MBEngineHelper)
	CoreEngine = [2]MBEngine{MBEngineAt(MBEngineCore), MBEngineAt(MBEngineCore + MBEnginePipe1)}
)
