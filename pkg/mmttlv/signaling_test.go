// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import "testing"

func TestReassemblesFragmentedMessage(t *testing.T) {
	message := []byte{0x80, 0x00, 1, 0, 3, 1, 2, 3}
	assembler := NewReassembler()
	if got := assembler.Push(0x8004, append([]byte{0x40, 0}, message[:4]...)); len(got) != 0 {
		t.Fatalf("first fragment returned %#v", got)
	}
	got := assembler.Push(0x8004, append([]byte{0xc0, 1}, message[4:]...))
	if len(got) != 1 || got[0].ID != 0x8000 || len(got[0].Payload) != 3 {
		t.Fatalf("messages = %#v", got)
	}
}
