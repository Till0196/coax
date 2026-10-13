// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// recorder stands in for the property set, keeping what was written.
type recorder struct {
	id      uint32
	written []byte
}

func (r *recorder) Get(set vendors.GUID, id uint32, data []byte) (uint32, error) {
	return 0, nil
}

func (r *recorder) GetWithInstance(set vendors.GUID, id uint32, instance, data []byte) (uint32, error) {
	return 0, nil
}

func (r *recorder) Set(set vendors.GUID, id uint32, data []byte) error {
	r.id = id
	r.written = append([]byte(nil), data...)
	return nil
}

// The two voltages are numbered the other way round from the order they are
// written in -- 18 V is one and 13 V is two -- which is the numbering the
// property uses and the easiest thing here to get backwards.
func TestSupplyEncodesTheDriversNumbering(t *testing.T) {
	for _, one := range []struct {
		name      string
		high, off bool
		want      uint32
	}{
		{"the higher voltage", true, false, LNB18V},
		{"the lower one", false, false, LNB13V},
		{"and none at all", false, true, LNBOff},
		{"which wins over the voltage", true, true, LNBOff},
	} {
		t.Run(one.name, func(t *testing.T) {
			into := &recorder{}
			supply := &Supply{Properties: into, Name: "test"}
			if err := supply.Set(one.high, one.off); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if into.id != PropLNB {
				t.Errorf("property = %#x, want %#x", into.id, PropLNB)
			}
			if len(into.written) != LNBStructBytes {
				t.Fatalf("wrote %d bytes, want %d", len(into.written), LNBStructBytes)
			}
			if command := binary.LittleEndian.Uint32(into.written[LNBCommandAt:]); command != LNBCommand {
				t.Errorf("command = %d, want %d", command, LNBCommand)
			}
			if got := binary.LittleEndian.Uint32(into.written[LNBValueAt:]); got != one.want {
				t.Errorf("value = %d, want %d", got, one.want)
			}
		})
	}
}

// A DiSEqC message goes in the same structure under the other command, with
// its length in the last word.
func TestDiSEqCUsesTheOtherCommand(t *testing.T) {
	into := &recorder{}
	supply := &Supply{Properties: into, Name: "test"}
	message := []byte{0xe0, 0x10, 0x38, 0xf1}
	if err := supply.SendDiSEqC(message); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	if command := binary.LittleEndian.Uint32(into.written[LNBCommandAt:]); command != DiSEqCCommand {
		t.Errorf("command = %d, want %d", command, DiSEqCCommand)
	}
	for index, want := range message {
		if got := into.written[DiSEqCAt+index]; got != want {
			t.Errorf("message[%d] = %#02x, want %#02x", index, got, want)
		}
	}
	if length := binary.LittleEndian.Uint32(into.written[DiSEqCLengthAt:]); length != uint32(len(message)) {
		t.Errorf("length = %d, want %d", length, len(message))
	}
	// A message that cannot fit is refused rather than truncated.
	if err := supply.SendDiSEqC(make([]byte, DiSEqCLengthAt-DiSEqCAt+1)); err == nil {
		t.Error("an oversized DiSEqC message should be refused")
	}
}
