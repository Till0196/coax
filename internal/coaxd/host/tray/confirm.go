// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tray

import (
	"fmt"
)

// The parts of a Windows message box this uses, as their numbers. Here rather
// than beside the call so what is asked can be decided -- and tested -- without
// a message box to put it in.
const (
	promptYesNo       = 0x00000004
	promptIconQuery   = 0x00000020
	promptIconWarn    = 0x00000030
	promptSecondFirst = 0x00000100
	promptTopmost     = 0x00040000
	promptForeground  = 0x00010000
)

// confirmPrompt is what to ask before stopping, and how to ask it. Everything
// the daemon holds goes with it -- a recording in progress, a card session
// mid-conversation -- and the icon sits beside a clock people click at without
// looking, so with something attached the question says how much, warns rather
// than enquires, and puts No under the cursor.
//
// A count below zero means nobody was counting, asked as though nothing were
// attached: saying "0 clients" of a daemon that cannot tell invents a fact.
func confirmPrompt(count int) (text string, flags uintptr) {
	common := uintptr(promptYesNo | promptTopmost | promptForeground)
	switch {
	case count == 1:
		return "Stop the tuner daemon?\n\nOne client is using it, and will lose what it is doing.",
			common | promptIconWarn | promptSecondFirst
	case count > 1:
		return fmt.Sprintf("Stop the tuner daemon?\n\n%d clients are using it, and will lose what they are doing.", count),
			common | promptIconWarn | promptSecondFirst
	case count == 0:
		return "Stop the tuner daemon?\n\nNothing is using it.", common | promptIconQuery
	}
	return "Stop the tuner daemon?", common | promptIconQuery
}
