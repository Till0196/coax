// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package regs

// PowerUpVars are variables of the power-up code in the main CPU's RAM
// (F47000..) that the host sets as if the power-up code had started a
// command. They are not registers; the host sets the high byte of each
// word.
type PowerUpVars struct {
	// ClusterRole is the cluster the command is for (1 14D, 2 12D, 3
	// 13D). The power-up code sets it from the host's command word at
	// F00004 (kind 0x20 gives 3, with Mode 0x11). When the power-up code
	// closes it leaves that cluster alone and holds the others; when it
	// starts over it holds that cluster (Reset 0xFFFF). Our code sets 3.
	ClusterRole Reg // 0xf47f20
	// Mode is the command's variant: 1, 2, 0x11, 0x21 or 0x31. Our code
	// sets 0x11.
	Mode Reg // 0xf47f24
}

// PowerUp is the power-up code's variables the host sets.
var PowerUp = PowerUpVars{ClusterRole: 0xf47f20, Mode: 0xf47f24}
