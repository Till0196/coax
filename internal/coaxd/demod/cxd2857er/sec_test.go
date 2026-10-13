// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2857er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// recordingBus keeps every write, so a sequence can be checked without a chip.
type recordingBus struct {
	writes [][]byte
	read   []byte
}

func (bus *recordingBus) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	return nil
}

func (bus *recordingBus) Read(address byte, length int) ([]byte, error) {
	out := make([]byte, length)
	copy(out, bus.read)
	return out, nil
}

func (bus *recordingBus) WriteRead(address byte, write []byte, length int) ([]byte, error) {
	out := make([]byte, length)
	copy(out, bus.read)
	return out, nil
}

func secDemod(bus *recordingBus, system sony.System) *Demod {
	demod := New(bus, sony.Config{DemodAddress: 0x64, TunerAddress: 0x60})
	demod.System = system
	return demod
}

// The tone is one bank select and one register write, which is what the
// family's other driver does and what was seen to hold on a real chip.
func TestSetToneSelectsTheBankAndWritesTheRegister(t *testing.T) {
	for _, one := range []struct {
		on   bool
		want byte
	}{{true, 1}, {false, 0}} {
		bus := &recordingBus{}
		if err := secDemod(bus, sony.SystemDVBS2).SetTone(one.on); err != nil {
			t.Fatalf("SetTone(%v): %v", one.on, err)
		}
		if len(bus.writes) != 2 {
			t.Fatalf("SetTone(%v) made %d writes, want 2: %v", one.on, len(bus.writes), bus.writes)
		}
		// The bank, then the tone.
		if got := bus.writes[0]; got[1] != 0x00 || got[2] != sony.SECBank {
			t.Errorf("first write %v, want the bank select to %#02x", got, sony.SECBank)
		}
		if got := bus.writes[1]; got[1] != sony.SECTone || got[2] != one.want {
			t.Errorf("second write %v, want register %#02x = %d", got, sony.SECTone, one.want)
		}
	}
}

// The block is on the chip's satellite side, so asking for it anywhere else is
// a mistake worth naming rather than a write into whichever bank happens to be
// selected.
func TestSetToneRefusesOffTheSatelliteSide(t *testing.T) {
	for _, system := range []sony.System{
		sony.SystemISDBT, sony.SystemDVBT2, sony.SystemJ83B, sony.SystemUnspecified,
	} {
		bus := &recordingBus{}
		if err := secDemod(bus, system).SetTone(true); err == nil {
			t.Errorf("SetTone should be refused while receiving %s", system)
		}
		if len(bus.writes) != 0 {
			t.Errorf("%s: nothing should have been written, got %v", system, bus.writes)
		}
	}
}

// Every satellite standard the chip receives can carry a tone, including the
// Japanese pair -- they do not use one, but the block is the same.
func TestSetToneWorksForEverySatelliteStandard(t *testing.T) {
	for _, system := range []sony.System{
		sony.SystemDVBS, sony.SystemDVBS2, sony.SystemISDBS, sony.SystemISDBS3,
	} {
		bus := &recordingBus{}
		if err := secDemod(bus, system).SetTone(true); err != nil {
			t.Errorf("SetTone while receiving %s: %v", system, err)
		}
	}
}

// A DiSEqC message is padded to the register block's width, and the length is
// the message's own.
func TestSendDiSEqCFillsTheBlock(t *testing.T) {
	bus := &recordingBus{}
	message := []byte{0xe0, 0x10, 0x38, 0xf1}
	if err := secDemod(bus, sony.SystemDVBS2).SendDiSEqC(message); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	var wroteLength, wroteMessage bool
	for _, write := range bus.writes {
		switch write[1] {
		case sony.SECMessageLength:
			wroteLength = write[2] == byte(len(message))
		case sony.SECMessage:
			wroteMessage = len(write)-2 == sony.SECMessageBytes &&
				write[2] == 0xe0 && write[5] == 0xf1
		}
	}
	if !wroteLength {
		t.Errorf("the message length was not written: %v", bus.writes)
	}
	if !wroteMessage {
		t.Errorf("the message was not written padded to %d bytes: %v", sony.SECMessageBytes, bus.writes)
	}
	// Too long, and too short, are refused rather than truncated.
	if err := secDemod(bus, sony.SystemDVBS2).SendDiSEqC(make([]byte, sony.SECMessageBytes+1)); err == nil {
		t.Error("an oversized DiSEqC message should be refused")
	}
	if err := secDemod(bus, sony.SystemDVBS2).SendDiSEqC(nil); err == nil {
		t.Error("an empty DiSEqC message should be refused")
	}
}
