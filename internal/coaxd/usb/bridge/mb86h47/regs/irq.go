// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// IRQBase is the base of the main CPU's interrupt unit and the small
// peripherals beside it (a timer at 0x100, a UART at 0x500).
const IRQBase = 0xfd0000

// IRQUnit is the interrupt unit at FD0000. Its registers are 16 bits wide,
// in the high half of their word.
//
// The main CPU's interrupt reads two groups, A (Status A & Enable A) and
// B, and from them the units' own latches. Most latches are in the units;
// the ones of the clusters' replies and of the timer and UART are here:
// each latch is a Status (write 1 to clear; a cluster sets bits in its
// reply latch) and an Enable.
type IRQUnit struct {
	Unk004 Reg // 0x004
	// StatusA is group A's sources (IRQA...), let through by EnableA.
	StatusA Reg // 0x010
	// EnableA enables StatusA (IRQAEnabled).
	EnableA                Reg // 0x020
	Unk024, Unk028, Unk02C Reg // 0x024, 0x028, 0x02c

	// Latch is the timer's and the UART's latch (IRQA bit 0): bit 8 the
	// timer, bit 0 a byte received and bit 3 room to send. 0xFFFF clears
	// it.
	Latch Reg // 0x030
	// Reply is the latch of each role's cluster (IRQA bit 1): [0] the
	// cluster in the decoder's role, [1] the one in the encoder's role
	// (layout.go). A cluster answers a doorbell by setting bits here
	// (ReplyReady, ReplyStarted); bit 14 of Reply[0] is the fourth
	// cluster's (13D). The main CPU clears the bits it will wait for
	// before it rings (0x3880 for Reply[0], 0x3A80 for Reply[1]) and the
	// host clears them as it takes them (transcode WaitReply).
	Reply [2]Reg // 0x034, 0x038
	// LatchEnable enables Latch.
	LatchEnable Reg // 0x040
	// ReplyEnable enables Reply: 0 at start; the power-up code enables
	// 0x4000 for Reply[0]. The bits of a doorbell's answer are enabled as
	// it is rung.
	ReplyEnable [2]Reg // 0x044, 0x048
	// Unk090: the power-up code sets bit 0, waits while bit 4 reads busy,
	// then clears bit 1.
	Unk090 Reg // 0x090
	Unk0A0 Reg // 0x0a0
	// StatusB is group B's sources (IRQB...), let through by EnableB.
	StatusB Reg // 0x0c0
	// EnableB enables StatusB (IRQBEnabled).
	EnableB                Reg // 0x0d0
	Unk0D4, Unk0D8, Unk0DC Reg // 0x0d4, 0x0d8, 0x0dc

	// Timer is the timer's registers at 0x100..0x11C: 0x100 = 2 starts
	// it, 0x104..0x110 are two 32-bit counts as high and low halves.
	Timer [8]Reg // 0x100..0x11c
	// Unk400 is written 1 while the power-up code waits in its USB
	// loops.
	Unk400         Reg // 0x400
	Unk404         Reg // 0x404
	Unk484, Unk488 Reg // 0x484, 0x488
	Unk494, Unk498 Reg // 0x494, 0x498
	// UART is the UART's registers at 0x500..0x528: 0x500 = 3 and 0x504 =
	// 0x83D set it up, 0x514 is the byte received, 0x524 the byte to
	// send.
	UART [11]Reg // 0x500..0x528
}

// IRQAt is the interrupt unit at base.
func IRQAt(base uint32) IRQUnit {
	r := func(o uint32) Reg { return Reg(base + o) }
	u := IRQUnit{
		Unk004: r(0x004), StatusA: r(0x010), EnableA: r(0x020),
		Unk024: r(0x024), Unk028: r(0x028), Unk02C: r(0x02c),
		Latch: r(0x030), Reply: [2]Reg{r(0x034), r(0x038)},
		LatchEnable: r(0x040), ReplyEnable: [2]Reg{r(0x044), r(0x048)},
		Unk090: r(0x090), Unk0A0: r(0x0a0), StatusB: r(0x0c0), EnableB: r(0x0d0),
		Unk0D4: r(0x0d4), Unk0D8: r(0x0d8), Unk0DC: r(0x0dc),
		Unk400: r(0x400), Unk404: r(0x404), Unk484: r(0x484), Unk488: r(0x488),
		Unk494: r(0x494), Unk498: r(0x498),
	}
	for i := range u.Timer {
		u.Timer[i] = r(0x100 + 4*uint32(i))
	}
	for i := range u.UART {
		u.UART[i] = r(0x500 + 4*uint32(i))
	}
	return u
}

// IRQ is the interrupt unit.
var IRQ = IRQAt(IRQBase)

// The roles of the reply latches.
const (
	IRQReplyDecoder = 0 // the cluster in the decoder's role
	IRQReplyEncoder = 1 // the cluster in the encoder's role
)

// Group A's sources.
const (
	IRQALatch   = 1 << 0  // Latch
	IRQAReply   = 1 << 1  // Reply[0], Reply[1]
	IRQASystem  = 1 << 2  // the system unit's Events (F10034)
	IRQAFD4000  = 1 << 7  // the unit at FD4000 (FD4050 & FD4040)
	IRQAFFA000  = 1 << 12 // the unit at FFA000 (FFA010 & FFA014)
	IRQAFF9000  = 1 << 13 // the unit at FF9000
	IRQAFF8000  = 1 << 14 // the unit at FF8000
	IRQAFFC000  = 1 << 15 // the unit at FFC000
	IRQAEnabled = 0x1f    // EnableA
)

// Group B's sources.
const (
	IRQBVideoOut = 1 << 8  // the video output unit (FEE010 & FEE014)
	IRQBCore1    = 1 << 9  // codec core 1 (17000C8)
	IRQBCore0    = 1 << 10 // codec core 0 (16000C8)
	IRQBUSB      = 1 << 11 // the USB unit (FD8080)
	IRQBFEA000   = 1 << 12 // the unit at FEA000 (FEA018 & FEA01C)
	IRQBTS       = 1 << 15 // the TS engine (D000C8)
	IRQBEnabled  = 0x9f00  // EnableB
)
