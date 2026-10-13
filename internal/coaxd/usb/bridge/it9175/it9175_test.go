// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it9175

import (
	"encoding/hex"
	"errors"
	"testing"
)

// TestChecksum covers the frame check on a register read of 0x800047, which is
// the command the tuning loop repeats most.
func TestChecksum(t *testing.T) {
	// Mailbox, command, sequence, then the read's arguments.
	frame := []byte{0x80, 0x00, 0x00, 0x01, 0x02, 0x00, 0x00, 0x00, 0x47}
	if got, want := checksum(frame), uint16(0x36fe); got != want {
		t.Errorf("checksum = %#04x, want %#04x", got, want)
	}
	// An odd trailing byte is padded into the high half of the last word.
	if got, want := checksum([]byte{0x12}), uint16(^uint16(0x1200)); got != want {
		t.Errorf("odd-length checksum = %#04x, want %#04x", got, want)
	}
}

// TestLRC covers the T=1 error check byte.
func TestLRC(t *testing.T) {
	if got := lrc([]byte{0x00, 0x01, 0x02}); got != 0x03 {
		t.Errorf("lrc = %#02x, want 0x03", got)
	}
	if got := lrc(nil); got != 0 {
		t.Errorf("lrc of nothing = %#02x, want 0", got)
	}
}

// synthImage is a firmware image built to exercise the packer: a bank whose
// blocks straddle both the byte budget and the three-segment limit, then a
// second bank with a single short block.
func synthImage() []byte {
	image := []byte{0x00}
	blocks := []struct {
		address uint16
		length  int
		first   byte
	}{{0x4100, 3, 0}, {0x4200, 40, 10}, {0x4300, 90, 60}, {0x5000, 1, 0xaa}}
	for _, block := range blocks {
		image = append(image, byte(block.length>>8), byte(block.length), byte(block.address>>8), byte(block.address))
		for index := 0; index < block.length; index++ {
			if block.length == 1 {
				image = append(image, block.first)
				continue
			}
			image = append(image, block.first+byte(index))
		}
	}
	image = append(image, 0x00, 0x00, 0x01)
	image = append(image, 0x00, 0x05, 0x60, 0x00, 1, 2, 3, 4, 5)
	return append(image, 0x00, 0x00, 0x00)
}

// TestPackFirmware checks the packed commands byte for byte, including their
// ordering: the data pieces follow their descriptors in reverse.
func TestPackFirmware(t *testing.T) {
	want := []string{
		"030000024100034200230a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c000102",
		"030000024223054300213c3d3e3f404142434445464748494a4b4c4d4e4f505152535455565758595a5b5c2d2e2f3031",
		"030000014321295d5e5f606162636465666768696a6b6c6d6e6f707172737475767778797a7b7c7d7e7f808182838485",
		"03000002434a10500001aa868788898a8b8c8d8e8f909192939495",
		"030100016000050102030405",
	}
	frames, err := packFirmware(synthImage())
	if err != nil {
		t.Fatalf("packFirmware: %v", err)
	}
	if len(frames) != len(want) {
		t.Fatalf("packed %d commands, want %d", len(frames), len(want))
	}
	for index, frame := range frames {
		if got := hex.EncodeToString(frame); got != want[index] {
			t.Errorf("command %d:\n got %s\nwant %s", index, got, want[index])
		}
		// Every command has to fit the chip's frame alongside its own header
		// and checksum, or the download silently truncates.
		if len(frame) > maxWriteLength {
			t.Errorf("command %d is %d bytes, over the %d a frame holds", index, len(frame), maxWriteLength)
		}
	}
}

// TestPackFirmwareRejectsMalformed covers the malformed images the packer must
// reject rather than read past.
func TestPackFirmwareRejectsMalformed(t *testing.T) {
	cases := map[string][]byte{
		"empty":              {},
		"no blocks":          {0x00},
		"truncated header":   {0x00, 0x00, 0x05, 0x60},
		"block past the end": {0x00, 0x00, 0x40, 0x60, 0x00, 1, 2, 3},
		"empty block":        {0x00, 0x00, 0x00, 0x60, 0x00},
	}
	for name, image := range cases {
		if _, err := packFirmware(image); err == nil {
			t.Errorf("%s: packFirmware accepted a malformed image", name)
		} else if !errors.Is(err, errFirmwareFormat) {
			t.Errorf("%s: error is %v, want one wrapping errFirmwareFormat", name, err)
		}
	}
}

// testReference is the frequency a stick on a 20.480 MHz crystal derives from
// the 1670 its measurement register reports.
const testReference = uint32(1670<<2) * 20480 / 18

// newTestDevice builds a device with the clock figures a real stick reports and
// its band table calibrated from the reference it measures, so the tuning
// arithmetic can be exercised without hardware.
func newTestDevice() *Device {
	device := &Device{clockMode: 1, xtal: 20480, fdiv: 18}
	device.boundaries = defaultBoundaries
	device.calibrateBoundaries(testReference, boundaryCount)
	return device
}

// TestCalibrateBoundaries checks the band table is rebuilt from the chip's own
// measurement, and that a chip asking for more bands than there are cannot run
// off the end of the table.
func TestCalibrateBoundaries(t *testing.T) {
	device := newTestDevice()
	want := [boundaryCount]uint32{59377, 79170, 118755, 158340, 237511, 316681, 475022, 950044}
	if device.boundaries != want {
		t.Errorf("boundaries = %v, want %v", device.boundaries, want)
	}
	// A count beyond the table is clamped rather than panicking.
	device.calibrateBoundaries(testReference, 255)
	if device.boundaries != want {
		t.Errorf("clamped boundaries = %v, want %v", device.boundaries, want)
	}
}

// TestFreqIndex covers the band lookup either side of an edge, and above the
// highest one where the extra divider entry is used.
func TestFreqIndex(t *testing.T) {
	device := newTestDevice()
	cases := []struct {
		frequency uint32
		index     int
		ndiv      uint8
	}{
		{59377, 0, 48},
		{59378, 1, 32},
		{475022, 6, 6},
		{475023, 7, 4},
		{950044, 7, 4},
		{950045, 8, 2},
	}
	for _, test := range cases {
		index, ndiv := device.freqIndex(test.frequency)
		if index != test.index || ndiv != test.ndiv {
			t.Errorf("freqIndex(%d) = %d, %d; want %d, %d", test.frequency, index, ndiv, test.index, test.ndiv)
		}
	}
}

// TestSynthesiser pins the frequency words at real Japanese channel centres,
// over the range of calibration values the chip's sign-magnitude register can
// hold.
func TestSynthesiser(t *testing.T) {
	device := newTestDevice()
	cases := []struct {
		frequency   uint32
		calibration byte
		lnaBand     byte
		word        uint32
		corrected   uint32
		shifted     uint32
		channel     string
	}{
		{93143, 0x06, 0, 18349, 18421, 1965, "VHF 2"},
		{93143, 0x1f, 0, 18349, 18721, 1965, "VHF 2"},
		{93143, 0x3f, 0, 18349, 18337, 1965, "VHF 2"},
		{205143, 0x06, 0, 34932, 34968, 18548, "VHF 9"},
		{205143, 0x3f, 0, 34932, 34926, 18548, "VHF 9"},
		{473143, 0x06, 1, 51647, 51665, 35263, "UHF 13"},
		{473143, 0x1f, 1, 51647, 51740, 35263, "UHF 13"},
		{473143, 0x3f, 1, 51647, 51644, 35263, "UHF 13"},
		{551143, 0x06, 3, 59282, 59294, 42898, "UHF 26"},
		{767143, 0x06, 6, 60041, 60053, 43657, "UHF 62"},
		{767143, 0x3f, 6, 60041, 60039, 43657, "UHF 62"},
	}
	for _, test := range cases {
		got := device.synthesiser(test.frequency, test.calibration)
		if got.lnaBand != test.lnaBand || got.word != test.word || got.corrected != test.corrected || got.shifted != test.shifted {
			t.Errorf("%s at calibration %#02x:\n got band=%d word=%d corrected=%d shifted=%d\nwant band=%d word=%d corrected=%d shifted=%d",
				test.channel, test.calibration, got.lnaBand, got.word, got.corrected, got.shifted,
				test.lnaBand, test.word, test.corrected, test.shifted)
		}
	}
}

// TestSynthesiserAppliesNegativeCalibration checks the sign-magnitude decode:
// values from 0x20 up are negative and pull the word down.
func TestSynthesiserAppliesNegativeCalibration(t *testing.T) {
	device := newTestDevice()
	zero := device.synthesiser(473143, 0x00)
	positive := device.synthesiser(473143, 0x1f)
	negative := device.synthesiser(473143, 0x3f)
	if positive.corrected <= zero.corrected {
		t.Errorf("a positive calibration did not raise the word: %d then %d", zero.corrected, positive.corrected)
	}
	if negative.corrected >= zero.corrected {
		t.Errorf("a negative calibration did not lower the word: %d then %d", zero.corrected, negative.corrected)
	}
	// The uncorrected word is the same whatever the calibration says.
	if positive.word != zero.word || negative.word != zero.word {
		t.Error("the calibration changed the uncorrected word")
	}
}

// TestSdramClock covers each band's first and last channel, a frequency below
// every band, and one above the top channel.
func TestSdramClock(t *testing.T) {
	cases := []struct {
		frequency uint32
		clock     byte
		what      string
	}{
		{50000, 73, "below every band"},
		{90143, 73, "first of band 0"},
		{156143, 61, "last of band 0"},
		{164143, 65, "first of band 1"},
		{192143, 75, "first of band 2"},
		{230143, 68, "first of band 3"},
		{470143, 64, "first of band 4"},
		{767143, 61, "last of band 4"},
		{900000, 61, "above the top channel, clamped"},
	}
	for _, test := range cases {
		if got := sdramClock(test.frequency); got != test.clock {
			t.Errorf("sdramClock(%d) = %d, want %d (%s)", test.frequency, got, test.clock, test.what)
		}
	}
}

// TestTunerWordRoundsToNearest checks the word is rounded rather than truncated,
// which is the point of the doubled multiply.
func TestTunerWordRoundsToNearest(t *testing.T) {
	// A divisor of 4 with a multiplier of 1 makes the half-step visible.
	if word, _ := tunerWord(9, 1, 4); word != 2 {
		t.Errorf("tunerWord rounded 9/4 to %d, want 2", word)
	}
	if word, _ := tunerWord(10, 1, 4); word != 3 {
		t.Errorf("tunerWord rounded 10/4 to %d, want 3", word)
	}
	// A zero divisor cannot divide, and must not panic.
	if word, band := tunerWord(473143, 1, 0); word != 0 || band != 1 {
		t.Errorf("tunerWord with no divisor = %d, band %d; want 0, band 1", word, band)
	}
}

// TestScaled covers the fixed-point divide the clock programming uses.
func TestScaled(t *testing.T) {
	if got, want := scaled(12_000_000, 1_000_000, 19), uint32(0x600000); got != want {
		t.Errorf("core clock word = %#x, want %#x", got, want)
	}
	if got, want := scaled(20_250_000, 1_000_000, 19), uint32(0xa20000); got != want {
		t.Errorf("ADC clock word = %#x, want %#x", got, want)
	}
}

// TestInitTablesAddressDemodulator checks the bring-up tables reach the
// demodulator half of the register space and stay inside a frame.
func TestInitTablesAddressDemodulator(t *testing.T) {
	for name, table := range map[string][]regBlock{"demodInit1": demodInit1, "demodInit2": demodInit2} {
		if len(table) == 0 {
			t.Fatalf("%s is empty", name)
		}
		for _, block := range table {
			if block.register>>16 == 0 {
				t.Errorf("%s: register %#06x is not in the demodulator's mailbox", name, block.register)
			}
			if len(block.data) == 0 || 6+len(block.data) > maxWriteLength {
				t.Errorf("%s: register %#06x carries %d bytes, which no frame holds", name, block.register, len(block.data))
			}
		}
	}
}
