// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

// TestTheTerrestrialDescriptorNamesEveryTransmitter. ARIB gives the
// terrestrial band a descriptor and a tag of its own rather than DVB's, and
// counts its frequencies in sevenths of a megahertz -- which is how the
// Japanese plan is spaced, and why channel 13 is at 473142857 rather than a
// round number.
func TestTheTerrestrialDescriptorNamesEveryTransmitter(t *testing.T) {
	// Area 1310, and two sites: 3312 sevenths is channel 13, 3354 is 14.
	body := []byte{0x51, 0xe0, 0x0c, 0xf0, 0x0d, 0x1a}
	described := ParseTerrestrialDelivery(body)
	if described == nil {
		t.Fatal("the descriptor was refused")
	}
	if described.AreaCode != 1310 {
		t.Errorf("area code %d, want 1310", described.AreaCode)
	}
	want := []uint32{473142857, 479142857}
	if len(described.CarriersHz) != len(want) {
		t.Fatalf("named %d transmitters, want %d: %v", len(described.CarriersHz), len(want), described.CarriersHz)
	}
	for index, frequency := range want {
		if described.CarriersHz[index] != frequency {
			t.Errorf("transmitter %d is at %d, want %d", index, described.CarriersHz[index], frequency)
		}
	}

	// Too short to hold even the area code, which is a descriptor that is not
	// the one it claims to be.
	if ParseTerrestrialDelivery([]byte{0x51}) != nil {
		t.Error("a truncated descriptor was read")
	}
	// An odd tail is a frequency that did not arrive whole, and is left off
	// rather than half-read.
	if named := ParseTerrestrialDelivery([]byte{0x51, 0xe0, 0x0c, 0xf0, 0x0d}); len(named.CarriersHz) != 1 {
		t.Errorf("a half-arrived frequency was read: %v", named.CarriersHz)
	}
}
