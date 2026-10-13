// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// secBus records every write and answers reads from a busy flag that clears
// after the given number of looks.
type secBus struct {
	writes    [][]byte
	busyLooks int
}

func (bus *secBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *secBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	if bus.busyLooks > 0 {
		bus.busyLooks--
		response[0] = 1
	}
	return response, nil
}

func secDemod(bus *secBus, system sony.System) *Demod {
	demod := New(bus, 0xdc, 0xd8, sony.Config{})
	demod.System = system
	demod.State = sony.StateActive
	return demod
}

// wrote reports whether a register took a value, after the bank was selected.
func wrote(writes [][]byte, bank, register, value byte) bool {
	selected := false
	for _, write := range writes {
		if len(write) != 2 {
			continue
		}
		if write[0] == 0x00 {
			selected = write[1] == bank
			continue
		}
		if selected && write[0] == register && write[1] == value {
			return true
		}
	}
	return false
}

// TestSetToneSelectsTheBankFirst: the block is banked like everything else on
// this chip, and a write landing in whatever bank the last tune left selected
// goes somewhere else without failing.
func TestSetToneSelectsTheBankFirst(t *testing.T) {
	for _, on := range []bool{true, false} {
		bus := &secBus{}
		if err := secDemod(bus, sony.SystemISDBS).SetTone(on); err != nil {
			t.Fatalf("SetTone(%v): %v", on, err)
		}
		want := sony.SECToneValue(on)
		if !wrote(bus.writes, sony.SECBank, sony.SECTone, want) {
			t.Errorf("SetTone(%v) did not write %#02x to bank %#02x register %#02x: %v",
				on, want, sony.SECBank, sony.SECTone, bus.writes)
		}
	}
}

// TestSECIsRefusedOffTheSatelliteSide: both calls reach a block that only means
// anything while the chip is demodulating a satellite, so sony.Core refuses
// them outright rather than writing registers that will not do what was asked.
func TestSECIsRefusedOffTheSatelliteSide(t *testing.T) {
	for _, system := range []sony.System{sony.SystemISDBT, sony.SystemDVBC, sony.SystemUnspecified} {
		bus := &secBus{}
		demod := secDemod(bus, system)
		if err := demod.SetTone(true); err == nil {
			t.Errorf("SetTone was accepted while receiving %s", system)
		}
		if err := demod.SendDiSEqC([]byte{0xe0, 0x10, 0x38, 0xf0}); err == nil {
			t.Errorf("SendDiSEqC was accepted while receiving %s", system)
		}
		if len(bus.writes) != 0 {
			t.Errorf("%s: registers were written anyway: %v", system, bus.writes)
		}
	}
}

// TestSendDiSEqCWritesTheWholeMessageRun: the message registers are written as
// one run whatever the message's length, with the length in its own register.
// Writing only the bytes given would leave the tail of a previous, longer
// message in place, differing from what this driver believes it wrote.
func TestSendDiSEqCWritesTheWholeMessageRun(t *testing.T) {
	bus := &secBus{}
	message := []byte{0xe0, 0x10, 0x38, 0xf0}
	if err := secDemod(bus, sony.SystemDVBS2).SendDiSEqC(message); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	var run []byte
	for _, write := range bus.writes {
		if len(write) > 2 && write[0] == sony.SECMessage {
			run = write[1:]
		}
	}
	if len(run) != sony.SECMessageBytes {
		t.Fatalf("the message run is %d bytes, want %d", len(run), sony.SECMessageBytes)
	}
	if !bytes.Equal(run[:len(message)], message) {
		t.Errorf("the message went out as %#v", run)
	}
	for index, value := range run[len(message):] {
		if value != 0 {
			t.Errorf("byte %d of the padding is %#02x, want zero", index+len(message), value)
		}
	}
	if !wrote(bus.writes, sony.SECBank, sony.SECMessageLength, byte(len(message))) {
		t.Errorf("the length register was not set to %d: %v", len(message), bus.writes)
	}
	if !wrote(bus.writes, sony.SECBank, sony.SECTransmit, 0x01) {
		t.Error("the send was never started")
	}
}

// TestSendDiSEqCRefusesAMessageThatWillNotFit, rather than silently sending
// the first twelve bytes of it.
func TestSendDiSEqCRefusesAMessageThatWillNotFit(t *testing.T) {
	for _, length := range []int{0, sony.SECMessageBytes + 1} {
		bus := &secBus{}
		if err := secDemod(bus, sony.SystemDVBS).SendDiSEqC(make([]byte, length)); err == nil {
			t.Errorf("a %d byte message was accepted", length)
		}
	}
}

// TestSendDiSEqCWaitsForTheChipToFinish, and gives up rather than blocking
// for ever on a chip that never lowers the flag.
func TestSendDiSEqCWaitsForTheChipToFinish(t *testing.T) {
	bus := &secBus{busyLooks: 3}
	if err := secDemod(bus, sony.SystemDVBS).SendDiSEqC([]byte{0xe0}); err != nil {
		t.Errorf("a send that finished on the fourth look failed: %v", err)
	}
	bus = &secBus{busyLooks: sony.SECPollAttempts + 1}
	err := secDemod(bus, sony.SystemDVBS).SendDiSEqC([]byte{0xe0})
	if !errors.Is(err, sony.ErrDiSEqCStillGoing) {
		t.Errorf("a chip that stayed busy reported %v", err)
	}
}
