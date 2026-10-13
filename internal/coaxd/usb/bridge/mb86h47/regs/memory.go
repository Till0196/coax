// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// MemoryBase is the base of the frame-memory controller: the barrier the
// main CPU writes before it rings a cluster, the picture layouts of the
// engines' clients and the window table through which the engines reach
// SDRAM.
const MemoryBase = 0xf12000

// MemoryUnit is the frame-memory controller at F12000. Its registers are
// 16 bits wide, in the high half of their word, and take single-word
// writes only: a longer write stores its first word alone (transcode
// session.go).
//
// Windows: an engine reaches SDRAM through a slot of the window table,
// which holds the window's base in 1 KiB units of the bus (SDRAM from 0;
// bus address >> 10). Left at 0 a window lands at the bottom of SDRAM,
// where the power-up code keeps its variables and stack. A picture
// buffer's layout (its row format and where its chroma starts) is given
// per client by Format and Planes. Everything from 0x100 to 0x7D0 is set to
// 0 before a set-up (transcode Prepare).
type MemoryUnit struct {
	// Barrier is written 0 before every doorbell to a cluster (our loop
	// and session): the main CPU's writes before it reach SDRAM first.
	Barrier Reg // 0x000
	// Unk004 is written 1 as the power-up code closes, after Unk0B0 = 0.
	Unk004 Reg // 0x004
	// Unk024 and Unk038 are set to 1 and Unk03C to 0 after the table
	// is cleared (transcode Prepare).
	Unk024, Unk038, Unk03C Reg // 0x024, 0x038, 0x03c
	// Unk058 is written 1 and Unk064 0 by the power-up code at start.
	Unk058, Unk064 Reg // 0x058, 0x064
	// Unk094 is copied into the resizer's attribute
	// Attr[MemoryAttrResize1].
	Unk094 Reg // 0x094
	// Unk0B0 is written 0 as the power-up code closes.
	Unk0B0 Reg // 0x0b0

	// Format is the row formats of the clients' pictures, a 4-bit code a
	// client (MemoryRow...): Format[1] bits 3-0 the encoder's windows
	// (slots 0-9 and 0x90-0x99, Planes[0]), bits 11-8 the resizer's
	// target (Planes2[1]), bits 15-12 its source (Planes2[2]);
	// Format[2] bits 15-12 = 7 and Format[3] bits 15-12 = 3 for the
	// decoder's side information and the resizer's filter records.
	// Format[0] and Format[3] are 0 at set-up.
	Format [4]Reg // 0x100..0x10c
	// Planes are the layouts of the first clients: Planes[0] the
	// encoder's pictures (Chroma = the luma's size in KiB: 0x330 for
	// 1088 rows of 768 bytes, 0x1B0 for 576), Planes[1] 0x110 or 0x50
	// alongside (the encoder's channel 0 motion buffers, slots 0x24..),
	// Planes[2] 0.
	Planes [3]WindowPlanes // 0x110, 0x120, 0x130
	// Format2 is a second set of row formats: Format2[1] is 3 (our clock
	// set-up), its bits 3-0 the encoder's channel 1 windows (slots
	// 0x10-0x1F; transcode layout.go), and Format2[2] is 4 with the video
	// output unit (FEE000).
	Format2 [4]Reg // 0x140..0x14c
	// Planes2 are the layouts of more clients: Planes2[1] the resizer's
	// target and Planes2[2] its source (0x880 is 1088 rows of 2048
	// bytes); our clock set-up writes 0x880 to Planes2[2] and
	// Planes2[4].
	Planes2 [8]WindowPlanes // 0x150..0x1c0
	// Unk1D0 are eight registers of which 0x1D4, 0x1DC, 0x1E4 and 0x1EC
	// are set to 0xFFFF after the table is cleared (transcode Prepare).
	Unk1D0 [8]Reg // 0x1d0..0x1ec
	// Planes3 are the layouts of the last clients: Planes3[0] (0x1B0)
	// the encoder's channel 1 pictures, slots 0x10-0x1F, and Planes3[1]
	// (0x20) its motion buffers, slots 0xAD... [3], [4] and [5] hold
	// other values: 0x244 = 0xFFFF, 0x258/0x25C = 0x3F, 0x268 = 0x80
	// with the video output unit.
	Planes3 [6]WindowPlanes // 0x210..0x260
	Unk300  Reg             // 0x300

	// Window is the window table (0x400..0x6FC); see MemoryWindow... for
	// the slots known.
	Window [0xc0]Reg
	// Attr are the clients' window attributes (0x700..0x7D0): each is
	// written 0 with its client's windows (Attr[0x11] with slots
	// 0x30-0x31, [0x13] 0x38-0x39, [0x16]/[0x17] 0x41-0x46, [0x18]/[0x19]
	// 0x47-0x4B, [0x1A]/[0x1B] 0x4C-0x50, [0x1C] 0x51-0x54, [0x1D]
	// 0x55-0x56, [0x1E] 0x57-0x58, [0x1F] 0x59, [0x2E]/[0x2F] 0x62-0x66,
	// [0x30] 0x67-0x6A).
	Attr [0x35]Reg
}

// WindowPlanes is a client's picture layout: four registers, the last
// two the chroma's start (the luma's size) in KiB for the client's two
// kinds of window.
type WindowPlanes struct {
	// Unk00 and Unk04 are 0.
	Unk00, Unk04 Reg
	// Chroma is where the chroma starts in each of the client's
	// buffers, in KiB from the buffer's base; both the same.
	Chroma [2]Reg
}

func windowPlanesAt(base uint32) WindowPlanes {
	return WindowPlanes{Unk00: Reg(base), Unk04: Reg(base + 4), Chroma: [2]Reg{Reg(base + 8), Reg(base + 0xc)}}
}

// MemoryAt is the frame-memory controller at base.
func MemoryAt(base uint32) MemoryUnit {
	r := func(o uint32) Reg { return Reg(base + o) }
	m := MemoryUnit{
		Barrier: r(0x000), Unk004: r(0x004), Unk024: r(0x024), Unk038: r(0x038), Unk03C: r(0x03c),
		Unk058: r(0x058), Unk064: r(0x064), Unk094: r(0x094), Unk0B0: r(0x0b0),
		Unk300: r(0x300),
	}
	for i := range m.Format {
		m.Format[i] = r(0x100 + 4*uint32(i))
		m.Format2[i] = r(0x140 + 4*uint32(i))
	}
	for i := range m.Planes {
		m.Planes[i] = windowPlanesAt(base + 0x110 + 0x10*uint32(i))
	}
	for i := range m.Planes2 {
		m.Planes2[i] = windowPlanesAt(base + 0x150 + 0x10*uint32(i))
	}
	for i := range m.Unk1D0 {
		m.Unk1D0[i] = r(0x1d0 + 4*uint32(i))
	}
	for i := range m.Planes3 {
		m.Planes3[i] = windowPlanesAt(base + 0x210 + 0x10*uint32(i))
	}
	for i := range m.Window {
		m.Window[i] = r(0x400 + 4*uint32(i))
	}
	for i := range m.Attr {
		m.Attr[i] = r(0x700 + 4*uint32(i))
	}
	return m
}

// Memory is the frame-memory controller.
var Memory = MemoryAt(MemoryBase)

// Window table slots.
const (
	// MemoryWindowEncoderInput is the encoder's four input pictures
	// (slots 0-3), which the resizer fills.
	MemoryWindowEncoderInput = 0x00
	// MemoryWindowEncoderRecon is the encoder's two reconstructed
	// pictures (slots 8 and 9).
	MemoryWindowEncoderRecon = 0x08
	// MemoryWindowEncoderWork is the encoder's channel 1 pictures (slots
	// 0x10-0x19): its inputs (0x10..) and reconstructed pictures
	// (0x18..), ch*16 as channel 0's. Channel 1's motion buffers are slots 0xAD.., their small
	// areas 0xB3.., as channel 0's are 0x24.. and 0x2A...
	MemoryWindowEncoderWork = 0x10
	// MemoryWindowDecoderFrames is the decoder's four frame stores
	// (slots 0x20-0x23: 0x82020000 + i*0x330000).
	MemoryWindowDecoderFrames = 0x20
	// MemoryWindowEncoderStream is the encoder's stream ring (slot 0x55:
	// 0x1430, bus 0x50C000).
	MemoryWindowEncoderStream = 0x55
	// MemoryWindowDecoderStream is the decoder's elementary-stream ring
	// (slot 0x56: 0x2230, bus 0x88C000).
	MemoryWindowDecoderStream = 0x56
	// MemoryWindowEP2 is endpoint 0x02's DMA destination window (slot
	// 0x81: 0x8000 = bus 0x2000000 while it runs, 0 when it stops); the endpoint's destination register is an offset into
	// it (USBEP2DMA.Dest).
	MemoryWindowEP2 = 0x81
	// MemoryWindowResizeTarget is the window the resizer writes (slot
	// 0x85).
	MemoryWindowResizeTarget = 0x85
	// MemoryWindowResizeSource is the window it reads (slot 0x89).
	MemoryWindowResizeSource = 0x89
	// MemoryWindowDecoderSide is the decoder's per-macroblock side
	// information (slot 0x8A: 0x550).
	MemoryWindowDecoderSide = 0x8a
	// MemoryWindowResizeFilter is where the resizer reads its filter
	// records, at +0x1400 (slot 0x8B: 0xBD3).
	MemoryWindowResizeFilter = 0x8b
	// MemoryWindowVideoOut is the video output unit's picture (slot
	// 0xAC).
	MemoryWindowVideoOut = 0xac
	// MemoryWindowEncoderInput2 is a second set of the encoder's windows,
	// set to the same pictures as slots 0-9 (slots 0x90-0x99).
	MemoryWindowEncoderInput2 = 0x90
)

// Window attributes known.
const (
	MemoryAttrEP2     = 0x22 // 0x788: 0 without USB
	MemoryAttrResize0 = 0x24 // 0x790: the resizer's (0)
	MemoryAttrResize1 = 0x25 // 0x794: the resizer's (Unk094)
)

// Row formats (Format's codes): the row pitch of a client's pictures.
const (
	MemoryRow2048 = 0
	MemoryRow1536 = 1
	MemoryRow1024 = 2
	MemoryRow768  = 3
	MemoryRow512  = 4
	MemoryRow256  = 5
)
