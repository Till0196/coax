// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plex

import (
	"testing"

	"github.com/Till0196/coax/pkg/usbio"
)

func TestTwoDevicesBehindOneHubNameOneEnclosure(t *testing.T) {
	first := usbio.ID{Vendor: 0x0511, Product: 0x0252}
	second := usbio.ID{Vendor: 0x0511, Product: 0x0253}
	attached := []usbio.Attached{
		{ID: second, Location: "003:003", Port: "3-2"},
		{ID: first, Location: "003:002", Port: "3-1"},
		{ID: first, Location: "001:004", Port: "1-4.1"},
	}
	for _, own := range attached[:2] {
		partner := uint16(0x0253)
		if own.ID == second {
			partner = 0x0252
		}
		if got := PartnerEnclosure(attached, own, partner); got != "003:002" {
			t.Errorf("%s names %q, want 003:002", own.Location, got)
		}
	}
	if got := PartnerEnclosure(attached, attached[2], 0x0253); got != "" {
		t.Errorf("a device with no partner behind its hub names %q", got)
	}
	crowded := append(attached, usbio.Attached{ID: second, Location: "003:005", Port: "3-3"})
	if got := PartnerEnclosure(crowded, attached[1], 0x0253); got != "" {
		t.Errorf("a device with two possible partners names %q", got)
	}
}
