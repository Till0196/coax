// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/Till0196/coax/pkg/usbio"
)

func TestOriginalEnclosureUsesTheSamePairFromEitherBridge(t *testing.T) {
	id := usbio.ID{Vendor: 0x0b06, Product: 0x0005}
	attached := []usbio.Attached{
		{ID: id, Location: "001:010", Port: "1-2.1"},
		{ID: id, Location: "001:011", Port: "1-2.2"},
		{ID: id, Location: "001:012", Port: "1-3.1"},
		{ID: id, Location: "001:013", Port: "1-3.2"},
		{ID: usbio.ID{Vendor: 0x0b06, Product: v2Product}, Location: "001:014", Port: "1-2.3"},
	}
	for _, own := range attached[:2] {
		sibling, err := enclosureSibling(attached, own.Location, id.Product)
		if err != nil {
			t.Fatal(err)
		}
		if got := enclosureLocations(own.Location, sibling); !reflect.DeepEqual(got, []string{"001:010", "001:011"}) {
			t.Fatalf("opening %s selected enclosure %v", own.Location, got)
		}
	}
	if _, err := enclosureSibling(attached[:1], attached[0].Location, id.Product); err == nil {
		t.Fatal("accepted an enclosure missing its second bridge")
	}
	for _, ports := range [][2]string{{"1-2", "1-2.1"}, {"1-2.1", "1-2"}} {
		candidates := append([]usbio.Attached(nil), attached[:2]...)
		candidates[0].Port, candidates[1].Port = ports[0], ports[1]
		if _, err := enclosureSibling(candidates, candidates[0].Location, id.Product); err == nil {
			t.Fatal("paired a USB parent with its child")
		}
	}
	attached = append(attached, usbio.Attached{ID: id, Location: "001:015", Port: "1-2.4"})
	if _, err := enclosureSibling(attached, attached[0].Location, id.Product); err == nil {
		t.Fatal("selected an ambiguous sibling")
	}
}

func TestEnclosureRetainsBridgesUntilItsLastUserCloses(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d control bridges", count), func(t *testing.T) {
			var holds []*sharedBridge
			for at := 0; at < count; at++ {
				// One independent USB owner plus the enclosure's reference.
				holds = append(holds, &sharedBridge{location: fmt.Sprintf("test:%d", at), refs: 2})
			}
			built := &enclosure{key: holds[0].location, holds: holds, refs: 2}
			enclosures[built.key] = built
			t.Cleanup(func() { delete(enclosures, built.key) })
			if err := built.release(); err != nil {
				t.Fatal(err)
			}
			for _, held := range holds {
				if held.refs != 2 {
					t.Fatal("released a bridge while its enclosure was still in use")
				}
			}
			if enclosures[built.key] != built {
				t.Fatal("removed an enclosure still in use")
			}
			if err := built.release(); err != nil {
				t.Fatal(err)
			}
			for _, held := range holds {
				if held.refs != 1 {
					t.Fatal("did not release an enclosure bridge reference")
				}
			}
			if enclosures[built.key] != nil {
				t.Fatal("retained an unused enclosure")
			}
		})
	}
}

// TestTheV2sSecondPairIsUnitsTwoAndThree: the bridge at position one streams
// units two and three, whose demodulators sit at 0x24 and 0x26 behind the
// first bridge.
func TestTheV2sSecondPairIsUnitsTwoAndThree(t *testing.T) {
	for at, want := range [][2]byte{{0x20, 0x22}, {0x24, 0x26}} {
		got := v2WiringAt(at)
		if got.demodTerrestrial != want[0] || got.demodSatellite != want[1] {
			t.Errorf("position %d is wired to %#02x/%#02x, want %#02x/%#02x",
				at, got.demodTerrestrial, got.demodSatellite, want[0], want[1])
		}
		if got.terrestrialName != "NM120" || got.satelliteName != "TDA20142" {
			t.Errorf("position %d names its tuners %q/%q", at, got.terrestrialName, got.satelliteName)
		}
	}
}

func TestParentPort(t *testing.T) {
	for port, want := range map[string]string{"1-2.3": "1-2", "1-2.3.4": "1-2.3", "3-1": "3-1", "": ""} {
		if got := parentPort(port); got != want {
			t.Errorf("parentPort(%q) = %q, want %q", port, got, want)
		}
	}
}

// Both bridges of an enclosure name it the same, from the list alone; a
// one-bridge product and a bridge whose sibling is missing name none.
func TestBothBridgesOfAnEnclosureNameItTheSame(t *testing.T) {
	v2 := usbio.ID{Vendor: 0x0b06, Product: v2Product}
	s3u := usbio.ID{Vendor: 0x0b06, Product: 0x0003}
	attached := []usbio.Attached{
		{ID: v2, Location: "001:021", Port: "1-14.2"},
		{ID: v2, Location: "001:020", Port: "1-14.1"},
		{ID: s3u, Location: "001:030", Port: "1-15.1"},
		{ID: s3u, Location: "001:031", Port: "1-15.2"},
	}
	for _, own := range attached[:2] {
		if got := enclosureName(attached, own); got != "001:020" {
			t.Errorf("%s names its enclosure %q, want 001:020", own.Location, got)
		}
	}
	for _, own := range attached[2:] {
		if got := enclosureName(attached, own); got != "" {
			t.Errorf("a one-bridge %s names an enclosure %q", own.Location, got)
		}
	}
	if got := enclosureName(attached[:1], attached[0]); got != "" {
		t.Errorf("a bridge without its sibling names an enclosure %q", got)
	}
}

// One enclosure supply switches the line on both bridges, and goes off only
// when the last holder lets go.
func TestTheEnclosureSupplyIsSwitchedOnBothBridges(t *testing.T) {
	first, second := &frontendTrace{}, &frontendTrace{}
	supply := enclosureSupply([]boardControl{first, second}, 0x00)
	if err := supply.Acquire(); err != nil {
		t.Fatal(err)
	}
	if err := supply.Acquire(); err != nil {
		t.Fatal(err)
	}
	if err := supply.Release(); err != nil {
		t.Fatal(err)
	}
	for _, bridge := range []*frontendTrace{first, second} {
		if !reflect.DeepEqual(bridge.gpio, [][2]byte{{0x00, lnbSupplyMask}}) {
			t.Fatalf("one holder left, a bridge saw %v", bridge.gpio)
		}
	}
	if err := supply.Release(); err != nil {
		t.Fatal(err)
	}
	for _, bridge := range []*frontendTrace{first, second} {
		if !reflect.DeepEqual(bridge.gpio, [][2]byte{{0x00, lnbSupplyMask}, {lnbSupplyMask, lnbSupplyMask}}) {
			t.Fatalf("the last holder gone, a bridge saw %v", bridge.gpio)
		}
	}
}
