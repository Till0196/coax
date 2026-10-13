// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package si2183

import "testing"

// secBus answers every command as ready and keeps what was written; its
// WriteRead ignores the write, which nothing here exercises.
type secBus struct{ writes [][]byte }

func (bus *secBus) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *secBus) Read(address byte, length int) ([]byte, error) {
	out := make([]byte, length)
	out[0] = statusReady
	return out, nil
}

func (bus *secBus) WriteRead(address byte, write []byte, length int) ([]byte, error) {
	return bus.Read(address, length)
}

// The tone is a DiSEqC command with no message: the block enabled, the tone bit
// set the way it should end up, the sequence marked finished. This chip has no
// separate tone command.
func TestSetToneIsADiSEqCCommandCarryingNothing(t *testing.T) {
	for _, one := range []struct {
		on    bool
		flags byte
	}{
		{false, secEnable | secEndOfSeq},
		{true, secEnable | secEndOfSeq | secToneOn},
	} {
		bus := &secBus{}
		demod := New(bus, "", Config{})
		if err := demod.SetTone(one.on); err != nil {
			t.Fatalf("SetTone(%v): %v", one.on, err)
		}
		if len(bus.writes) != 1 {
			t.Fatalf("SetTone(%v) made %d writes, want 1", one.on, len(bus.writes))
		}
		write := bus.writes[0]
		if len(write) != secBytes {
			t.Errorf("the command is %d bytes, want %d: %v", len(write), secBytes, write)
		}
		if write[0] != secCommand {
			t.Errorf("opcode %#02x, want %#02x", write[0], secCommand)
		}
		if write[1] != one.flags {
			t.Errorf("SetTone(%v) flags %#02x, want %#02x", one.on, write[1], one.flags)
		}
	}
}

// A message goes in the same command, its length in the top three bits of the
// flags, and the command still occupies its full width on the wire.
func TestSendDiSEqCCarriesTheLengthInTheFlags(t *testing.T) {
	bus := &secBus{}
	demod := New(bus, "", Config{})
	message := []byte{0xe0, 0x10, 0x38, 0xf1}
	if err := demod.SendDiSEqC(message, true); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	write := bus.writes[0]
	if len(write) != secBytes {
		t.Fatalf("the command is %d bytes, want %d", len(write), secBytes)
	}
	wanted := byte(secEnable | secEndOfSeq | secToneOn | byte(len(message))<<secLengthAt)
	if write[1] != wanted {
		t.Errorf("flags %#02x, want %#02x", write[1], wanted)
	}
	for index, want := range message {
		if write[2+index] != want {
			t.Errorf("message[%d] = %#02x, want %#02x", index, write[2+index], want)
		}
	}
}

// Room for six message bytes and no more: a longer one is refused, not cut.
func TestSendDiSEqCRefusesWhatWillNotFit(t *testing.T) {
	demod := New(&secBus{}, "", Config{})
	if err := demod.SendDiSEqC(make([]byte, secMessageBytes+1), false); err == nil {
		t.Error("an oversized DiSEqC message should be refused")
	}
	if err := demod.SendDiSEqC(nil, false); err == nil {
		t.Error("an empty DiSEqC message should be refused")
	}
}

// A chip another driver has already booted is resumed, not started again: the
// cold sequence would put it back into its bootloader and, on Windows, take
// down a transport stream the vendor driver is carrying.
func TestAlreadyBootedResumesInsteadOfLoading(t *testing.T) {
	bus := &secBus{}
	if err := New(bus, "", Config{AlreadyBooted: true}).Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if len(bus.writes) != 3 {
		t.Fatalf("the warm path made %d writes, want 3: %v", len(bus.writes), bus.writes)
	}
	// The clock, then the resume, then the state read.
	if bus.writes[0][0] != 0xc0 || bus.writes[0][1] != 0x12 {
		t.Errorf("first write %v, want the clock command", bus.writes[0])
	}
	want := []byte{0xc0, 0x06, 0x08, 0x0f, 0x00, 0x20, 0x21, 0x01}
	for index, value := range want {
		if bus.writes[1][index] != value {
			t.Fatalf("resume command %v, want %v", bus.writes[1], want)
		}
	}
	if len(bus.writes[2]) != 1 || bus.writes[2][0] != 0x85 {
		t.Errorf("third write %v, want the one-byte state read", bus.writes[2])
	}
	// And nothing that would reset the chip: the cold power-up differs in its
	// third byte and must not appear.
	for _, write := range bus.writes {
		if len(write) >= 3 && write[0] == 0xc0 && write[1] == 0x06 && write[2] == 0x01 {
			t.Errorf("the cold power-up was sent to a chip that was already running: %v", write)
		}
	}
}
