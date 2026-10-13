// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

// BS-1, whose downlink is 11.72748 GHz at 110.0 degrees east, right-hand
// circular, 28.86 Msymbol/s. Subtracting the 10678 MHz oscillator gives 1049480
// kHz, which is where a receiver tunes it and what the channel files write.
func TestParseSatelliteDelivery(t *testing.T) {
	body := []byte{
		0x01, 0x17, 0x27, 0x48, // 011.72748 GHz
		0x11, 0x00, // 110.0 degrees
		0x80 | (0x03 << 5) | 0x08, // east, right-hand circular, modulation 8
		0x02, 0x88, 0x60, 0x03,    // 28.8600 Msymbol/s, FEC 3
	}
	got := ParseSatelliteDelivery(body)
	if got == nil {
		t.Fatal("a well-formed descriptor must parse")
	}
	if got.FrequencyKHz != 11727480 {
		t.Errorf("FrequencyKHz = %d, want 11727480", got.FrequencyKHz)
	}
	if got.FrequencyKHz-10678000 != 1049480 {
		t.Errorf("the intermediate frequency works out to %d, want 1049480", got.FrequencyKHz-10678000)
	}
	if got.OrbitalPosition != 1100 || !got.East {
		t.Errorf("orbital position = %d east=%t, want 1100 east", got.OrbitalPosition, got.East)
	}
	if got.Polarisation != "R" {
		t.Errorf("Polarisation = %q, want R", got.Polarisation)
	}
	if got.SymbolRate != 28860000 {
		t.Errorf("SymbolRate = %d, want 28860000 (28.86 Msymbol/s)", got.SymbolRate)
	}
	if got.Modulation != 8 || got.FECInner != 3 {
		t.Errorf("modulation=%d fec=%d, want 8 and 3", got.Modulation, got.FECInner)
	}
}

// Every field is decimal digits, so a nibble above nine says this is not the
// descriptor it claims to be -- and a frequency believed out of one is a
// transponder that is not there.
func TestParseSatelliteDeliveryRefusesWhatIsNotOne(t *testing.T) {
	if got := ParseSatelliteDelivery([]byte{0x01, 0x17}); got != nil {
		t.Errorf("a short descriptor gave %#v", got)
	}
	body := []byte{0x0a, 0x17, 0x27, 0x48, 0x11, 0x00, 0xe8, 0x02, 0x88, 0x60, 0x03}
	if got := ParseSatelliteDelivery(body); got != nil {
		t.Errorf("a frequency with a non-digit gave %#v", got)
	}
}

// A Japanese cable carrier: 111 MHz, 256QAM, 5.274 Msymbol/s, which is what the
// channel files write for C13.
func TestParseCableDelivery(t *testing.T) {
	body := []byte{
		0x01, 0x11, 0x00, 0x00, // 0111.0000 MHz
		0x00, 0x00, // reserved, FEC outer 0
		0x05,                   // 256QAM
		0x00, 0x52, 0x74, 0x00, // 0.052740 -> 5.2740 Msymbol/s, FEC inner 0
	}
	got := ParseCableDelivery(body)
	if got == nil {
		t.Fatal("a well-formed descriptor must parse")
	}
	if got.FrequencyHz != 111000000 {
		t.Errorf("FrequencyHz = %d, want 111000000", got.FrequencyHz)
	}
	if got.Modulation != 5 {
		t.Errorf("Modulation = %d, want 5 (256QAM)", got.Modulation)
	}
	if got.SymbolRate != 5274000 {
		t.Errorf("SymbolRate = %d, want 5274000", got.SymbolRate)
	}
	if got := ParseCableDelivery([]byte{0x01, 0x11}); got != nil {
		t.Errorf("a short descriptor gave %#v", got)
	}
}
