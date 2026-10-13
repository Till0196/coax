// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbio

import "testing"

func TestSelected(t *testing.T) {
	tests := []struct {
		selector string
		location string
		index    int
		want     bool
	}{
		{"", "001:002", 0, true},
		{"", "001:002", 3, true},
		{"001:002", "001:002", 4, true},
		{"001:002", "001:003", 0, false},
		{" 001:002 ", "001:002", 0, true},
		{"1", "001:002", 1, true},
		{"1", "001:002", 0, false},
		{"nonsense", "001:002", 0, false},
	}
	for _, test := range tests {
		if got := selected(test.selector, test.location, test.index); got != test.want {
			t.Errorf("selected(%q, %q, %d) = %v, want %v", test.selector, test.location, test.index, got, test.want)
		}
	}
}

func TestIDIn(t *testing.T) {
	supported := []ID{{Vendor: 0x0511, Product: 0x0045}, {Vendor: 0x0572, Product: 0xc689}}
	if !(ID{Vendor: 0x0572, Product: 0xc689}).In(supported) {
		t.Error("a supported ID should match")
	}
	if (ID{Vendor: 0x0572, Product: 0x0045}).In(supported) {
		t.Error("a vendor and product from different entries should not match")
	}
	if (ID{Vendor: 0x1234, Product: 0x5678}).In(nil) {
		t.Error("nothing should match an empty supported list")
	}
}

func TestMeaningfulDropsSerialsOfZeros(t *testing.T) {
	for serial, want := range map[string]string{
		"":           "",
		"0":          "",
		"000000000":  "",
		"0123456789": "0123456789",
		"C844E86E4F": "C844E86E4F",
		"100":        "100",
	} {
		if got := Meaningful(serial); got != want {
			t.Errorf("Meaningful(%q) = %q, want %q", serial, got, want)
		}
	}
}
