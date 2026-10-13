// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// TestWiringIsPerGeneration. Pointing one generation's wiring at the other
// reaches bus addresses where nothing answers, so which entry a product ID
// lands on is worth pinning.
func TestWiringIsPerGeneration(t *testing.T) {
	for product, older := range map[uint16]bool{
		0x0001: true,  // PX-S3U
		0x0003: true,  // PX-S3U2
		0x0004: true,  // PX-W3U2
		0x0005: true,  // PX-W3U3
		0x0006: false, // PX-W3U3 V2
	} {
		got := wiringFor(product)
		want := v2Wiring
		if older {
			want = olderWiring
		}
		if got.demodTerrestrial != want.demodTerrestrial || got.demodSatellite != want.demodSatellite {
			t.Errorf("product %#04x is wired to demodulators %#02x/%#02x, want %#02x/%#02x",
				product, got.demodTerrestrial, got.demodSatellite,
				want.demodTerrestrial, want.demodSatellite)
		}
		if got.satelliteName != want.satelliteName {
			t.Errorf("product %#04x calls its satellite tuner %q, want %q",
				product, got.satelliteName, want.satelliteName)
		}
	}
}

// TestTheTwoGenerationsAgreeOnNothing is the other half of the same guard: if
// the two entries were made equal, the test above would pass saying nothing.
func TestTheTwoGenerationsAgreeOnNothing(t *testing.T) {
	if olderWiring.demodTerrestrial == v2Wiring.demodTerrestrial {
		t.Error("the two generations should not share a terrestrial demodulator address")
	}
	if olderWiring.demodSatellite == v2Wiring.demodSatellite {
		t.Error("the two generations should not share a satellite demodulator address")
	}
	// Both generations put the terrestrial demodulator at the lower address. The
	// V2 once had them the other way round, which sent the NM120's writes to the
	// satellite demodulator, where nothing at 0xce answers.
	if v2Wiring.demodTerrestrial != 0x20 || v2Wiring.demodSatellite != 0x22 {
		t.Errorf("the V2's demodulators are at %#02x/%#02x, want 0x20/0x22",
			v2Wiring.demodTerrestrial, v2Wiring.demodSatellite)
	}
	if olderWiring.demodSatellite <= olderWiring.demodTerrestrial {
		t.Error("the older boards put their satellite demodulator at the higher address")
	}
	if olderWiring.terrestrialName == v2Wiring.terrestrialName ||
		olderWiring.satelliteName == v2Wiring.satelliteName {
		t.Error("the two generations should not name the same tuners")
	}
}

// TestListingNamesTheTunerThatIsFitted checks that a listing does not tell
// somebody their PX-W3U3 has a chip it does not have.
func TestListingNamesTheTunerThatIsFitted(t *testing.T) {
	for product, want := range map[uint16]string{
		0x0005: "FC0012",
		0x0006: "NM120",
	} {
		tuners := Layout(usbio.ID{Vendor: 0x0b06, Product: product})
		if len(tuners) != 2 {
			t.Fatalf("product %#04x lists %d tuners, want 2", product, len(tuners))
		}
		if tuners[terrestrial].Name != want {
			t.Errorf("product %#04x names its terrestrial tuner %q, want %q",
				product, tuners[terrestrial].Name, want)
		}
	}
}

// TestTheLNBSupplyRunsOppositeWaysOnTheTwoGenerations guards a line that looks
// identical from the outside and is not: the same mask reaches both, and the
// older boards feed the aerial lead when zero is written where the V2 feeds it
// when the bit is. One polarity for both starves one of them.
func TestTheLNBSupplyRunsOppositeWaysOnTheTwoGenerations(t *testing.T) {
	if olderWiring.lnbSupplyOn != 0x00 {
		t.Errorf("the older boards feed the lead by writing %#02x, want 0x00", olderWiring.lnbSupplyOn)
	}
	if v2Wiring.lnbSupplyOn != lnbSupplyMask {
		t.Errorf("the V2 feeds the lead by writing %#02x, want %#02x", v2Wiring.lnbSupplyOn, lnbSupplyMask)
	}
	// The off value is derived from the on value and the mask, so every
	// product has to land on one state of the line or the other.
	for product := range products {
		if on := wiringFor(product).lnbSupplyOn; on != 0x00 && on != lnbSupplyMask {
			t.Errorf("product %#04x feeds the lead by writing %#02x, which is neither state of the line",
				product, on)
		}
	}
}
