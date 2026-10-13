// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2857er

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// The symbol rate register is the one number DVB-S2 carries that the Japanese
// standards do not. The ISDB-S constant already in this package settles the
// arithmetic: that rate through this function must come out as the three bytes
// the wake-up sequence writes literally.
func TestSymbolRateRegisterReproducesTheISDBSConstant(t *testing.T) {
	written, err := symbolRateRegister(isdbsSymbolRate)
	if err != nil {
		t.Fatalf("symbolRateRegister: %v", err)
	}
	want := []byte{0x07, 0x37, 0x0a}
	for index := range want {
		if written[index] != want[index] {
			t.Fatalf("28.86 MSps became %#v, want %#v", written, want)
		}
	}
}

// And the other fixed rate this chip receives, for a second point on the line.
func TestSymbolRateRegisterHandlesTheISDBS3Rate(t *testing.T) {
	written, err := symbolRateRegister(isdbs3SymbolRate)
	if err != nil {
		t.Fatalf("symbolRateRegister: %v", err)
	}
	// 33750 kSps x 16384 / 1000 = 552960 = 0x087000.
	want := []byte{0x08, 0x70, 0x00}
	for index := range want {
		if written[index] != want[index] {
			t.Fatalf("33.75 MSps became %#v, want %#v", written, want)
		}
	}
}

// A channel that gave no symbol rate is refused rather than tuned at zero,
// and one that gave an impossible one is refused rather than truncated.
func TestSymbolRateRegisterRefusesWhatItCannotCarry(t *testing.T) {
	if _, err := symbolRateRegister(0); err == nil {
		t.Error("a missing symbol rate should be refused")
	}
	// The register is twenty bits, which runs out above about 64 MSps.
	if _, err := symbolRateRegister(200_000_000); err == nil {
		t.Error("a symbol rate past the register's width should be refused")
	}
}

// A real transponder at 23303 kSps: 23303 x 16384 / 1000 = 381,796 = 0x05D364.
func TestSymbolRateRegisterOnARealTransponder(t *testing.T) {
	written, err := symbolRateRegister(23_303_000)
	if err != nil {
		t.Fatalf("symbolRateRegister: %v", err)
	}
	want := []byte{0x05, 0xd3, 0x64}
	for index := range want {
		if written[index] != want[index] {
			t.Fatalf("23.303 MSps became %#v, want %#v", written, want)
		}
	}
}

func TestIsISDBS3StreamID(t *testing.T) {
	// The advanced-wideband networks are the ones whose identifier starts
	// 0xB or 0xC; the BS and CS networks that came before them do not.
	for _, streamID := range []uint32{0xb110, 0xc001, 0xbfff} {
		if !IsISDBS3StreamID(streamID) {
			t.Errorf("stream ID %#x should select ISDB-S3", streamID)
		}
	}
	for _, streamID := range []uint32{0x0000, 0x4010, 0x7fe0, 0xd000} {
		if IsISDBS3StreamID(streamID) {
			t.Errorf("stream ID %#x should not select ISDB-S3", streamID)
		}
	}
}

func TestIsSatellite(t *testing.T) {
	for _, system := range []sony.System{sony.SystemISDBS, sony.SystemISDBS3} {
		if !system.IsSatellite() {
			t.Errorf("%s should be satellite", system)
		}
	}
	for _, system := range []sony.System{
		sony.SystemUnspecified, sony.SystemDVBT, sony.SystemDVBT2, sony.SystemDVBC,
		sony.SystemISDBT, sony.SystemJ83B, sony.SystemATSC,
	} {
		if system.IsSatellite() {
			t.Errorf("%s should not be satellite", system)
		}
	}
}

// TestCNRFromTable checks the lookup at both ends and in the middle. The tables
// run from the largest raw reading (the worst CNR) to the smallest, and a
// reading outside either end is clamped.
func TestCNRFromTable(t *testing.T) {
	for _, table := range []struct {
		name    string
		entries sony.CNCurve
	}{
		{"the shared satellite curve", satelliteCN},
		{"ISDB-S3", isdbs3CN},
	} {
		entries := table.entries
		if len(entries)%2 != 0 {
			t.Fatalf("%s curve has an odd number of values, so a pair was lost", table.name)
		}
		if entries.Len() < 3 {
			t.Fatalf("%s table is too short to be the reference driver's", table.name)
		}
		firstRaw, firstMdB := entries.At(0)
		lastRaw, lastMdB := entries.At(entries.Len() - 1)
		if firstRaw <= lastRaw {
			t.Errorf("%s table should run from the largest raw reading down", table.name)
		}
		if firstMdB >= lastMdB {
			t.Errorf("%s table should run from the worst ratio up", table.name)
		}
		if got := entries.MdB(firstRaw + 1000); got != firstMdB {
			t.Errorf("%s below the table: got %d, want %d", table.name, got, firstMdB)
		}
		if got := entries.MdB(0); got != lastMdB {
			t.Errorf("%s above the table: got %d, want %d", table.name, got, lastMdB)
		}
		middleRaw, _ := entries.At(entries.Len() / 2)
		got := entries.MdB(middleRaw)
		if got < firstMdB || got > lastMdB {
			t.Errorf("%s middle reading %d is outside the table", table.name, got)
		}
	}
}

// TestISDBSAgainstCapturedHardware checks the ISDB-S readings against a capture
// from a TBS 6812 locked to stream 16400, where these register values meant
// -68.44 dBm and 17.90 dB.
func TestISDBSAgainstCapturedHardware(t *testing.T) {
	// bank 0xA1, register 0x10: valid flag, then the raw figure.
	raw := uint32(0x0021) & 0x1fff
	if got := satelliteCN.MdB(raw); got != 17900 {
		t.Errorf("ISDB-S CNR: got %d mdB, want 17900", got)
	}
	// bank 0xA0, register 0x1F: the IF AGC reading.
	ifAGC := int32(0x23&0x1f)<<8 | 0x36
	hundredths := -divideRound(ifAGC*1400, 403)
	if dBm := -(float64(hundredths)/100 + 97); dBm != -68.44 {
		t.Errorf("satellite level: got %.2f dBm, want -68.44", dBm)
	}
	// bank 0xA0, register 0x10: the lock flag sits in the last byte.
	if data := []byte{0x3d, 0x00, 0x7c}; data[len(data)-1]&0x40 == 0 {
		t.Error("captured status should read as locked")
	}
}

// TestTerrestrialRSSIAgainstCapturedHardware checks the terrestrial level
// against a reading from a TBS 6812 locked to ISDB-T channel 21 at
// 521142857 Hz, which was -49.46 dBm.
//
// The registers behind it: the tuner answered 0x63 for the IF AGC, 0x0F for
// the RF AGC, 0x87 for the filter setting and 0x11 for the gain trim.
func TestTerrestrialRSSIAgainstCapturedHardware(t *testing.T) {
	const frequencyKHz = 521142857 / 1000
	rssi := rssiFromGains(frequencyKHz, 0x63, 0x0f, 0x87&0x0f, sony.TwosComplement(0x11&0x0f, 4))
	if want := int32(-4496); rssi != want {
		t.Fatalf("RSSI = %d, want %d", rssi, want)
	}
	if dBm := float64(rssi)/100 - 4.5; dBm != -49.46 {
		t.Errorf("level = %.2f dBm, want -49.46", dBm)
	}
}

// TestTwosComplement covers the four-bit gain trim at both signs.
func TestTwosComplement(t *testing.T) {
	for value, want := range map[byte]int32{0x00: 0, 0x01: 1, 0x07: 7, 0x08: -8, 0x0f: -1} {
		if got := sony.TwosComplement(value, 4); got != want {
			t.Errorf("sony.TwosComplement(%#x, 4) = %d, want %d", value, got, want)
		}
	}
}

// TestISDBS3AgainstCapturedHardware checks the ISDB-S3 readings against a
// capture from a TBS 6812 locked to stream 45168, which read 17.90 dB and
// -69.87 dBm.
func TestISDBS3AgainstCapturedHardware(t *testing.T) {
	// bank 0xD0 register 0xF3: the valid flag, then the raw figure.
	raw := (uint32(0x00)<<16 | uint32(0x02)<<8 | uint32(0x43)) & 0x1ffff
	if got := isdbs3CN.MdB(raw); got != 17900 {
		t.Errorf("ISDB-S3 CNR = %d mdB, want 17900", got)
	}
	// bank 0xA0 register 0x1F: the IF AGC reading.
	ifAGC := int32(0x23&0x1f)<<8 | 0x0d
	hundredths := -divideRound(ifAGC*1400, 403)
	if dBm := -(float64(hundredths)/100 + 97); dBm != -69.87 {
		t.Errorf("ISDB-S3 level = %.2f dBm, want -69.87", dBm)
	}
	// bank 0xA0 register 0x10, two bytes for ISDB-S3: the lock flag is bit 6
	// of the last one.
	if data := []byte{0xfd, 0xc0}; data[len(data)-1]&0x40 == 0 {
		t.Error("captured ISDB-S3 status should read as locked")
	}
}

// TestJ83BLevelAgainstCapturedHardware checks the cable input level against a
// capture from the same card locked at 314.25 MHz, which read -40.05 dBm. Cable
// goes through the terrestrial reading, which covers both cable annexes as well
// as ISDB-T.
func TestJ83BLevelAgainstCapturedHardware(t *testing.T) {
	rssi := rssiFromGains(314250, 0x6f, 0x0f, 0x83&0x0f, sony.TwosComplement(0x11&0x0f, 4))
	if want := int32(-3555); rssi != want {
		t.Fatalf("RSSI = %d, want %d", rssi, want)
	}
	if dBm := float64(rssi)/100 - 4.5; dBm != -40.05 {
		t.Errorf("level = %.2f dBm, want -40.05", dBm)
	}
}
