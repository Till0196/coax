// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// ClockBase is the base of the main CPU's system unit: the chip's
// configuration pins, the PLL and the clock dividers, the clock gates of
// the engines, and three groups of interrupt sources that go to the main
// CPU.
const ClockBase = 0xf10000

// ClockUnit is the system unit at F10000. Its registers are 16 bits wide,
// in the high half of their word: the CPU writes them with STH, the host
// with a 16-bit write.
//
// Interrupts: each group has a latched Status (write 1 to clear) and an
// Enable. Events (F10034/F10038) is the first level: its bit 15 stands for
// Events1 (F10024/F10028), and within that, bit 15 of Events2
// (F10244/F10248) for the resizer's status. The main CPU sees Events as
// bit 2 of the interrupt unit's group A (FD0010).
type ClockUnit struct {
	// Unk000 is a count the power-up code uses in its USB time-out.
	Unk000 Reg // 0x000
	// Strap is the chip's configuration as it came up: bits 10-11 pick
	// how the power-up code serves USB (0 none, 1, 2 the configuration
	// this board uses, 3).
	Strap Reg // 0x004
	// Unk010 is an interrupt enable of the Events1 kind: 0 at start; the
	// power-up code sets bits 0-1 on a USB event.
	Unk010 Reg // 0x010
	// Unk014 is a mode register with fields in bits 5-6, 7 and 8-9.
	Unk014 Reg // 0x014
	Unk018 Reg // 0x018
	Unk020 Reg // 0x020

	// Events1Status is the second group's latched events, write 1 to
	// clear: bit 6 a USB event, bits 8-9 the configuration's own
	// sources.
	Events1Status Reg // 0x024
	// Events1Enable enables Events1Status (0 at start; bit 6 the USB
	// event).
	Events1Enable Reg // 0x028
	// Unk030 is written 0x200 after the power-up code puts a message for
	// the host at F00020.
	Unk030 Reg // 0x030
	// EventsStatus is the first group's latched events, write 1 to clear.
	// Bit 15: Events1 or Events2 has one.
	EventsStatus Reg // 0x034
	// EventsEnable enables EventsStatus; bit 15 passes Events1 and
	// Events2 (the resizer).
	EventsEnable Reg // 0x038
	// Unk040 is written with Unk0DC's kind in the power-up code's signal
	// to the host.
	Unk040 Reg // 0x040

	// Unk070, Unk074 and Unk078 are the power-up code's signals to the
	// host: it writes 4 (a word at F00004) or 8 (at F00008) to Unk070,
	// and 8 to Unk074 after an endpoint 0x83 packet; it waits for bit 3
	// of Unk074 and Unk078 before it writes a message. Unk078 is a mask
	// the other signals are checked against.
	Unk070 Reg // 0x070
	Unk074 Reg // 0x074
	Unk078 Reg // 0x078
	// Busy is a lock the power-up code shares with the host: bit 0 set
	// while the host holds it; the code waits for 0 before it touches
	// the USB core's registers and writes 0 after.
	Busy   Reg // 0x080
	Unk09C Reg // 0x09c
	// Unk0DC is the kind of the power-up code's signal to the host
	// (0x80, 0x100, 0x200, 0x400, 0x800).
	Unk0DC         Reg // 0x0dc
	Unk0E0         Reg // 0x0e0
	Unk0E4, Unk0E8 Reg // 0x0e4, 0x0e8
	// Unk0F0 is written 1 by the power-up code when Strap's bits 10-11
	// are not 0.
	Unk0F0 Reg // 0x0f0
	// Unk104 is the power-up code's state: (state & 0xFF) | 0x8000, or
	// 0; a USB request is served differently when it is 0.
	Unk104 Reg // 0x104
	// Unk108: bits 1-3 are cleared when the power-up code closes.
	Unk108 Reg // 0x108

	// PLL: bit 15 runs the PLL, bits 8-14 are its divider, bit 0 is set
	// from the start. The divider is set with bit 15 off, then bit 15;
	// our clock set-up writes 0x8201 (mb86h47.go).
	PLL Reg // 0x10c
	// Unk114 is 0x8008 in this board's configuration (0x8001 or 0x8009
	// in others); the I2C set-up sets bits 2-3.
	Unk114 Reg // 0x114
	// Gates is the engines' clock gates, a bit a block (1 runs its
	// clock). Bit 8 is the resizer's: set before each of its runs,
	// cleared after the last pass. Our loop keeps ClockGatesAll.
	Gates  Reg // 0x118
	Unk11C Reg // 0x11c
	// Unk120, Unk124, Unk128 and Unk12C are the clock dividers of the
	// configuration: 0x100, 0x128A, 0x6000 and 0x4007 in this one. Bits
	// of 120, 124 and 128 depend on the output formats.
	Unk120, Unk124, Unk128, Unk12C Reg // 0x120, 0x124, 0x128, 0x12c
	// Unk180 is written 0x8000 after Unk184 = 0x5EAF as the power-up
	// code closes.
	Unk180, Unk184 Reg // 0x180, 0x184
	// Unk1B0 is a mask the power-up code checks its signals against.
	Unk1B0 Reg // 0x1b0
	// Unk1B4 and Unk1B8 are a second signal mask and its kind.
	Unk1B4, Unk1B8 Reg // 0x1b4, 0x1b8

	// Events2Status is the third group's latched events: bit 15 the
	// resizer, bit 10 a source of another configuration.
	Events2Status Reg // 0x244
	// Events2Enable enables Events2Status (bit 15 the resizer); our
	// resizer set-up writes 0 because the loop polls.
	Events2Enable Reg // 0x248
}

// ClockAt is the system unit at base.
func ClockAt(base uint32) ClockUnit {
	r := func(o uint32) Reg { return Reg(base + o) }
	return ClockUnit{
		Unk000: r(0x000), Strap: r(0x004), Unk010: r(0x010), Unk014: r(0x014), Unk018: r(0x018), Unk020: r(0x020),
		Events1Status: r(0x024), Events1Enable: r(0x028), Unk030: r(0x030),
		EventsStatus: r(0x034), EventsEnable: r(0x038), Unk040: r(0x040),
		Unk070: r(0x070), Unk074: r(0x074), Unk078: r(0x078), Busy: r(0x080), Unk09C: r(0x09c),
		Unk0DC: r(0x0dc), Unk0E0: r(0x0e0), Unk0E4: r(0x0e4), Unk0E8: r(0x0e8), Unk0F0: r(0x0f0),
		Unk104: r(0x104), Unk108: r(0x108),
		PLL: r(0x10c), Unk114: r(0x114), Gates: r(0x118), Unk11C: r(0x11c),
		Unk120: r(0x120), Unk124: r(0x124), Unk128: r(0x128), Unk12C: r(0x12c),
		Unk180: r(0x180), Unk184: r(0x184), Unk1B0: r(0x1b0), Unk1B4: r(0x1b4), Unk1B8: r(0x1b8),
		Events2Status: r(0x244), Events2Enable: r(0x248),
	}
}

// Clock is the system unit.
var Clock = ClockAt(ClockBase)

// Bits and values of the system unit.
const (
	ClockPLLOn       = 0x8000 // PLL: the PLL runs
	ClockGateResizer = 0x100  // Gates: the resizer's clock
	// ClockGatesAll is the gates our loop keeps on (bits 3 and 11 off).
	ClockGatesAll       = 0xf7f7
	ClockEventsCascade  = 0x8000 // EventsStatus: Events1/Events2 have an event
	ClockEvents2Resizer = 0x8000 // Events2Status/Enable: the resizer
)
