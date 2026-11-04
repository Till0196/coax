// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px4

import (
	"testing"

	"github.com/Till0196/coax/pkg/usbio"
)

// The two devices of a Q3 model name one enclosure, by the serial number they
// share where both list it and by their hub where not; a four-tuner model
// names none.
func TestTheTwoDevicesOfAQ3ModelNameOneEnclosure(t *testing.T) {
	q3 := usbio.ID{Vendor: 0x0511, Product: 0x084a}
	attached := []usbio.Attached{
		{ID: q3, Location: "001:010", Port: "1-3.1", Serial: "123456789012341"},
		{ID: q3, Location: "001:011", Port: "1-3.2", Serial: "123456789012342"},
	}
	for _, own := range attached {
		if got := enclosureName(attached, own); got != "12345678901234" {
			t.Errorf("%s names %q, want the shared serial", own.Location, got)
		}
	}
	attached[1].Serial = ""
	for _, own := range attached {
		if got := enclosureName(attached, own); got != "001:010" {
			t.Errorf("without both serials, %s names %q, want 001:010", own.Location, got)
		}
	}
	w3 := usbio.ID{Vendor: 0x0511, Product: 0x083f}
	if got := enclosureName(attached, usbio.Attached{ID: w3, Location: "001:012"}); got != "" {
		t.Errorf("a four-tuner model names %q", got)
	}
	for product, want := range map[uint16]string{0x083f: "board", 0x084a: "card"} {
		lnb := Layout(usbio.ID{Vendor: 0x0511, Product: product})[0].Controls.LNB
		if lnb == nil || lnb.Shared != want {
			t.Errorf("product %#04x lists the supply %+v, want shared %q", product, lnb, want)
		}
	}
}
