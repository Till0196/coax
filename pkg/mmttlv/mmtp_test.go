// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

import "testing"

func TestParseSignalingPacket(t *testing.T) {
	buffer := make([]byte, 12)
	buffer[1], buffer[2], buffer[3] = PayloadTypeSignaling, 0x80, 0x04
	buffer = append(buffer, 1, 2, 3)
	packet, ok := Parse(buffer)
	if !ok || packet.PacketID != 0x8004 || packet.PayloadType != PayloadTypeSignaling || len(packet.Payload) != 3 {
		t.Fatalf("packet = %#v, ok = %t", packet, ok)
	}
}

func TestParseReadsTheSequenceNumberAndRAP(t *testing.T) {
	buffer := make([]byte, 12)
	buffer[0] = 0x41 // version 1, RAP
	buffer[1], buffer[2], buffer[3] = 0x00, 0x01, 0x00
	buffer[8], buffer[9], buffer[10], buffer[11] = 0x00, 0x01, 0x02, 0x03
	packet, ok := Parse(buffer)
	if !ok || packet.SequenceNumber != 0x00010203 || !packet.RAP {
		t.Fatalf("packet = %#v, ok = %t", packet, ok)
	}
}
