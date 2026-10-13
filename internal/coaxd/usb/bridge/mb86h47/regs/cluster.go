// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// The control blocks of the clusters the encoder uses (the first 0x1000
// bytes of each cluster's register space): 14D0000 the encoder core's, 10D0000
// its helper's, and 11D0000 a second helper's, which the MB86H47A does not
// have. The same layout is in 12D0000 and 13D0000.
//
// The registers are 16 bits wide, in the high half of their word: the
// cores write them with STH, the host with a 16-bit write.
const (
	ClusterCoreBase    = 0x14d0000
	ClusterHelperBase  = 0x10d0000
	ClusterHelper1Base = 0x11d0000
	ClusterDecoderBase = 0x12d0000 // the MPEG-2 decoder's (see Decoder)
	ClusterSpareBase   = 0x13d0000 // the fourth cluster's (core13)
)

// Cluster is a cluster's control block.
//
// Interrupts: each of IRQStatus[n] (latched, write 1 to clear) is let
// through by IRQEnable[n] and raises the core's interrupt n: vector 16+n.
// A handler reads Enable & Status and writes it back to clear.
//
// Doorbells: a write to Bell[n] (by the main CPU) sets bits in
// BellStatus[n]; enabled by BellEnable[n], they raise IRQStatus[1] bit 1
// (Bell 1) and, it seems, bit 0 (Bell 0).
type Cluster struct {
	// Reset holds the core and its units: 3 holds, 0 lets it run; the
	// host writes 0xFFFF (our Hold). A helper is reset with Run=0,
	// Reset=3, Reset=0, Run=0x1F.
	Reset Reg // 0x000
	// Run is written with Reset: 0x8000 while held, 0 (14D) or 0x1F (10D)
	// to run.
	Run Reg // 0x004
	// IRQStatus is the latched interrupt sources, write 1 to clear. Core:
	// 0x10 bit 8 (vector 16 has nothing to do), 0x14 the units (the
	// ClusterIRQ bits), 0x18 (not enabled; only cleared). Helper: 0x10
	// (the ClusterHelperIRQ bits). All four are set to 0xFFFF at the
	// core's start.
	IRQStatus [4]Reg // 0x010, 0x014, 0x018, 0x01c
	// IRQEnable enables IRQStatus: core 0x100, 0xFE03, 0, 0; helper 0x10,
	// 0, 0, 0.
	IRQEnable [4]Reg // 0x020, 0x024, 0x028, 0x02c
	// Unk030 is written 0xFFFF by the host as it lets a cluster run,
	// with BellStatus, to clear stale latches.
	Unk030 Reg // 0x030
	// BellStatus is the doorbells rung, write 1 to clear: the core sets
	// both to 0xFFFF at start and reads 14D0038 & 14D0048 in its
	// interrupt (the ClusterBell bits); our core clears 14D0038 each loop
	// pass.
	BellStatus [2]Reg // 0x034, 0x038
	// BellEnable enables BellStatus: 0x3780 and 0x3F80.
	BellEnable [2]Reg // 0x044, 0x048
	// Bell rings the cluster (after F12000=0): 14D0054 sets 14D0038
	// (start 0x80, new 0x800, return 0x1000, reset 0x100; layout.go
	// regEncodeBell); 14D0050/12D0050 set BellStatus[0] (0x80, 0x200,
	// 0x100, 0x800/0x1000; the decoder's bells).
	Bell [2]Reg // 0x050, 0x054
	// Unk080 is written 1 just before the core enables interrupts
	// (ORCCR 0x10).
	Unk080 Reg // 0x080
	// Unk090 is written 3 at the core's start, before the SDRAM set-up;
	// our boot writes it.
	Unk090 Reg // 0x090
	// Sample reads back a changing value while the core runs: the
	// program counter.
	Sample [2]Reg // 0x0b0, 0x0b4
	// Standby is written 1 at the start and after each idle wait, and four
	// times in a row (then NOPs) to wait for an interrupt.
	Standby Reg // 0x400
	// StandbyMode is 4 while the core waits (set before Standby, 0
	// after). The host sets the same bit (bit 2) in its clock set-up
	// (mb86h47.go clocksLocked).
	StandbyMode Reg // 0x404
}

// ClusterAt is the control block at base.
func ClusterAt(base uint32) Cluster {
	r := func(o uint32) Reg { return Reg(base + o) }
	return Cluster{
		Reset: r(0x000), Run: r(0x004),
		IRQStatus:  [4]Reg{r(0x010), r(0x014), r(0x018), r(0x01c)},
		IRQEnable:  [4]Reg{r(0x020), r(0x024), r(0x028), r(0x02c)},
		Unk030:     r(0x030),
		BellStatus: [2]Reg{r(0x034), r(0x038)},
		BellEnable: [2]Reg{r(0x044), r(0x048)},
		Bell:       [2]Reg{r(0x050), r(0x054)},
		Unk080:     r(0x080), Unk090: r(0x090),
		Sample:  [2]Reg{r(0x0b0), r(0x0b4)},
		Standby: r(0x400), StandbyMode: r(0x404),
	}
}

// The control blocks.
var (
	ClusterCore    = ClusterAt(ClusterCoreBase)
	ClusterHelper  = ClusterAt(ClusterHelperBase)
	ClusterHelper1 = ClusterAt(ClusterHelper1Base)
	ClusterDecoder = ClusterAt(ClusterDecoderBase)
	ClusterSpare   = ClusterAt(ClusterSpareBase)
)

// Reset and Run values.
const (
	ClusterResetHold   = 3      // Reset: hold
	ClusterResetRun    = 0      // Reset: run
	ClusterRunHeld     = 0x8000 // Run while held
	ClusterRunHelper   = 0x1f   // Run for the helper
	ClusterStandbyWait = 4      // StandbyMode while waiting for an interrupt
)

// The encoder core's IRQStatus[1] (14D0014) bits: the units of pipe 0
// (14Exxxx) and pipe 1 (14Fxxxx). Enabled: 0xFE03.
const (
	ClusterIRQStream0    = 1 << 15 // stream unit 0 (14E4044)
	ClusterIRQStream1    = 1 << 14 // stream unit 1 (14F4044)
	ClusterIRQRow0       = 1 << 13 // coding unit 0's macroblock row (14E001C bit 9)
	ClusterIRQRow1       = 1 << 12 // coding unit 1's
	ClusterIRQMotion0    = 1 << 11 // motion unit 0 (14E6048 & 14E6044)
	ClusterIRQMotion1    = 1 << 10 // motion unit 1
	ClusterIRQShared     = 1 << 9  // the shared register file's events (14D4020)
	ClusterIRQBell1      = 1 << 1  // BellStatus[1] & BellEnable[1] (14D0038)
	ClusterIRQBell0      = 1 << 0  // enabled; BellStatus[0], it seems
	ClusterIRQCoreEnable = 0xfe03
)

// The helper's IRQStatus[0] (10D0010) bits. Enabled: 0x10
// (our helper polls bit 4).
const (
	ClusterHelperIRQInfo   = 1 << 8 // 10E8010 (macroblock info ready)
	ClusterHelperIRQEngine = 1 << 5 // the 10E macroblock engine (10E0010)
	ClusterHelperIRQShared = 1 << 4 // the shared register file's events (10D4024)
)

// Doorbell bits (BellStatus, Bell): as the decoder's (layout.go).
const (
	ClusterBellStart  = 0x80   // start
	ClusterBellReset  = 0x100  // reset
	ClusterBellStop   = 0x200  // stop
	ClusterBellNew    = 0x800  // a new input
	ClusterBellReturn = 0x1000 // an output returned
)
