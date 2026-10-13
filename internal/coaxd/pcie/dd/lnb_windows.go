// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"fmt"
	"time"
)

// The card's own supply for the aerial lead, outside the command mailbox the rest
// of this backend uses: the bridge FPGA has a small block of registers per tuner
// driving the supply chip and the 22 kHz tone. From ddbridge-max.c and
// ddbridge-regs.h in the vendor's Linux tree.
const (
	// lnbBase is where the block starts on a MAX M4; it is a property of the
	// card.
	lnbBase = 0x400
	// lnbStride is the distance between one tuner's registers and the next's.
	lnbStride = 0x20
	// lnbBlocks is how many tuners' registers fit before the command mailbox
	// begins at mciControl. A demodulator numbered past them would be writing
	// the mailbox's control register.
	lnbBlocks = (mciControl - lnbBase) / lnbStride
)

// The commands the control register takes, in its lowest three bits.
const (
	lnbCommandNone = 0
	lnbCommandInit = 1
	lnbCommandLow  = 3 // 13 V
	lnbCommandHigh = 4 // 18 V
	lnbCommandOff  = 5
)

const (
	// lnbBusy is set while the block is carrying a command out.
	lnbBusy = 1 << 4
	// lnbTone puts the continuous 22 kHz tone on the lead. It rides along with
	// whatever command is written, so changing the tone alone is a command of
	// "none" with this bit set as it should end up.
	lnbTone = 1 << 15
)

// lnbSettle is how long to wait between looks at the busy bit, and how many
// looks to take.
const (
	lnbSettle = 20 * time.Millisecond
	lnbLooks  = 10
)

func lnbControl(demod byte) uint32 { return lnbBase + uint32(demod)*lnbStride }

// lnbCommand writes one command and waits for the block to finish it. A command
// that never finishes is an error: the caller is a tune about to depend on the
// supply being up.
func (device *Device) lnbCommand(command uint32, tone bool) error {
	value := command
	if tone {
		value |= lnbTone
	}
	if device.demod >= lnbBlocks {
		return fmt.Errorf("demodulator %d has no LNB control register; the card has %d", device.demod, lnbBlocks)
	}
	register := lnbControl(device.demod)
	if err := device.bridge.writeRegister(register, value); err != nil {
		return fmt.Errorf("write the LNB control register: %w", err)
	}
	for look := 0; look < lnbLooks; look++ {
		state, err := device.bridge.readRegister(register)
		if err != nil {
			return fmt.Errorf("read the LNB control register: %w", err)
		}
		if state&lnbBusy == 0 {
			return nil
		}
		time.Sleep(lnbSettle)
	}
	return fmt.Errorf("the LNB supply did not finish command %d", command)
}

// prepareLNB brings the block up once per tuner, on the first satellite tune
// rather than at open, so a tuner only used for terrestrial never touches the
// supply. The supply itself is left off.
func (device *Device) prepareLNB() error {
	if device.lnbReady {
		return nil
	}
	if err := device.lnbCommand(lnbCommandInit, false); err != nil {
		return err
	}
	device.lnbReady = true
	device.lnbTone = false
	return nil
}

// setLNB puts the supply and the tone where a satellite channel asked for them.
// The two go together because the tone bit rides along with the command: setting
// a voltage would drop the tone if it were not carried across.
func (device *Device) setLNB(high, off, tone bool) error {
	if err := device.prepareLNB(); err != nil {
		return err
	}
	command := uint32(lnbCommandLow)
	switch {
	case off:
		command = lnbCommandOff
	case high:
		command = lnbCommandHigh
	}
	if err := device.lnbCommand(command, tone); err != nil {
		return err
	}
	device.lnbTone = tone
	return nil
}

// releaseLNB drops the supply when a tuner is finished with. Nothing else will:
// the card holds the last command until told otherwise or powered down, so a gone
// tuner would leave 18 V on the lead indefinitely.
func (device *Device) releaseLNB() {
	if !device.lnbReady {
		return
	}
	_ = device.lnbCommand(lnbCommandOff, false)
	device.lnbTone = false
}
