// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"errors"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/trace"
)

// A tuner driven from here is left in whatever standard it was last asked for,
// and the vendor driver cannot pick it up from there: it initialises its
// demodulator once, when the card's mode is written, and every later tune only
// retunes what it believes is set up. Closing puts it back, but a killed
// process never closes anything. So the repair happens later and the hardware
// answers whether it is needed: a demodulator whose standard disagrees with the
// card's own mode is a takeover never handed back.

// systemSelect is SLVX 0x17, holding the standard the demodulator was last
// woken for. The values are the ones the wake-up sequences write.
const systemSelect = 0x17

const (
	selectDVBT   = 0x01
	selectDVBT2  = 0x02
	selectDVBC   = 0x04
	selectISDBT  = 0x06
	selectJ83B   = 0x08
	selectISDBS  = 0x0c
	selectISDBS3 = 0x0d
	selectATSC   = 0x0f
)

// selectForSystem is what systemSelect holds for each standard.
func selectForSystem(system sony.System) (byte, bool) {
	switch system {
	case sony.SystemDVBT:
		return selectDVBT, true
	case sony.SystemDVBT2:
		return selectDVBT2, true
	case sony.SystemDVBC:
		return selectDVBC, true
	case sony.SystemISDBT:
		return selectISDBT, true
	case sony.SystemJ83B:
		return selectJ83B, true
	case sony.SystemISDBS:
		return selectISDBS, true
	case sony.SystemISDBS3:
		return selectISDBS3, true
	case sony.SystemATSC:
		return selectATSC, true
	}
	return 0, false
}

func knownSelect(value byte) bool {
	switch value {
	case selectDVBT, selectDVBT2, selectDVBC, selectISDBT,
		selectJ83B, selectISDBS, selectISDBS3, selectATSC:
		return true
	}
	return false
}

func (device *Device) currentSystem() (byte, error) {
	slvx := device.config.DemodAddress + 2
	if err := device.bus.Write(slvx, []byte{0x00, 0x00}); err != nil {
		return 0, err
	}
	value, err := device.bus.WriteRead(slvx, []byte{systemSelect}, 1)
	if err != nil {
		return 0, err
	}
	if len(value) == 0 {
		return 0, errors.New("the demodulator answered with nothing")
	}
	return value[0], nil
}

// Recover puts back any tuner left in a standard the vendor driver does not
// expect, and reports how many it repaired. It runs at start-up, after a
// previous run was killed without handing anything back. A tuner that cannot be
// opened is left alone, and so is a demodulator never woken at all: that is how
// a card looks after a cold start.
func Recover() int {
	cards, err := Devices()
	if err != nil {
		return 0
	}
	repaired := 0
	for _, card := range cards {
		if recoverOne(card.Selector) {
			repaired++
		}
	}
	return repaired
}

func recoverOne(selector string) bool {
	began := time.Now()
	defer func() { trace.Printf("TBS recover selector=%s elapsed=%s", selector, time.Since(began)) }()
	device, err := Open(selector, "")
	if err != nil {
		return false
	}
	defer device.Close()

	// The question is a Sony chip's: which standard its register says it was
	// woken for. A satellite-only card has nothing to answer it, and asking
	// would put a transfer on an address where nothing is listening.
	if device.model.newDemod == nil {
		return false
	}
	if err := device.attach(sony.SystemUnspecified); err != nil {
		return false
	}

	current, err := device.currentSystem()
	if err != nil {
		return false
	}
	if !knownSelect(current) {
		return false
	}
	// A demodulator sitting in a standard the card's mode covers can still be
	// picked up by the vendor driver; only one left outside them needs repair.
	state, ok := device.model.states[device.vendorMode]
	if !ok {
		return false
	}
	for _, system := range state.systems {
		wanted, ok := selectForSystem(system)
		if ok && current == wanted {
			return false
		}
	}
	// Close is what hands back, so taking the tuner is the whole repair.
	if _, err := device.startTransport(sony.SystemUnspecified); err != nil {
		return false
	}
	return true
}
