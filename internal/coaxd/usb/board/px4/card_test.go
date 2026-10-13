// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px4

import "testing"

// TestDeviceNumberParsing pins the serial format an eight-tuner enclosure uses:
// fifteen decimal digits whose last numbers the device within the enclosure.
func TestDeviceNumberParsing(t *testing.T) {
	cases := []struct {
		serial string
		number int
		ok     bool
		what   string
	}{
		{"000000000000011", 1, true, "the device the slot is wired to"},
		{"000000000000012", 2, true, "the other device"},
		{"000000000000013", 0, false, "an enclosure holds two devices, not three"},
		{"000000000000010", 0, false, "zero numbers nothing"},
		{"00000000000001", 0, false, "too short"},
		{"0000000000000111", 0, false, "too long"},
		{"00000000000001A", 0, false, "not decimal"},
		{"", 0, false, "no serial at all"},
	}
	for _, test := range cases {
		number, ok := parseDeviceNumber(test.serial)
		if number != test.number || ok != test.ok {
			t.Errorf("%q: got %d, %v; want %d, %v (%s)", test.serial, number, ok, test.number, test.ok, test.what)
		}
	}
}
