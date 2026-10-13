// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it930x

import "testing"

func TestChecksum(t *testing.T) {
	t.Parallel()
	// The bridge sums big-endian 16-bit words and complements the result; an
	// odd trailing byte counts as its high half.
	tests := []struct {
		data []byte
		want uint16
	}{
		{[]byte{0x00, 0x00}, 0xffff},
		{[]byte{0x00, 0x01}, 0xfffe},
		{[]byte{0x12, 0x34, 0x56, 0x78}, ^uint16(0x1234 + 0x5678)},
		{[]byte{0x12}, ^uint16(0x1200)},
		{[]byte{0x12, 0x34, 0x56}, ^uint16(0x1234 + 0x5600)},
	}
	for _, test := range tests {
		if got := checksum(test.data); got != test.want {
			t.Errorf("checksum(%x) = %#04x; want %#04x", test.data, got, test.want)
		}
	}
}

func TestRegisterLength(t *testing.T) {
	t.Parallel()
	for register, want := range map[uint32]byte{
		0x00000012: 1,
		0x00001234: 2,
		0x00123456: 3,
		0x12345678: 4,
	} {
		if got := registerLength(register); got != want {
			t.Errorf("registerLength(%#08x) = %d; want %d", register, got, want)
		}
	}
}

func TestRegisterHeader(t *testing.T) {
	t.Parallel()
	got := registerHeader(0xda4c, 3)
	want := []byte{3, 2, 0x00, 0x00, 0xda, 0x4c}
	if len(got) != len(want) {
		t.Fatalf("header = %x; want %x", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("header = %x; want %x", got, want)
		}
	}
}

func TestGPIOIndex(t *testing.T) {
	t.Parallel()
	// The boards name pins from one; the register banks are indexed from zero.
	if index, err := gpioIndex(1); err != nil || index != 0 {
		t.Errorf("gpioIndex(1) = %d, %v", index, err)
	}
	if index, err := gpioIndex(16); err != nil || index != 15 {
		t.Errorf("gpioIndex(16) = %d, %v", index, err)
	}
	for _, gpio := range []int{0, -1, 17} {
		if _, err := gpioIndex(gpio); err == nil {
			t.Errorf("gpioIndex(%d) accepted an out-of-range pin", gpio)
		}
	}
}

func TestGPIOBanksAreComplete(t *testing.T) {
	t.Parallel()
	// A mode register is always followed by its enable register, and every pin
	// has an output register.
	for index := 0; index < gpioCount; index++ {
		if gpioEnableRegisters[index] != gpioModeRegisters[index]+1 {
			t.Errorf("GPIO %d: enable register %#04x does not follow mode register %#04x",
				index+1, gpioEnableRegisters[index], gpioModeRegisters[index])
		}
		if gpioOutputRegisters[index] == 0 {
			t.Errorf("GPIO %d has an empty output register", index+1)
		}
	}
}
