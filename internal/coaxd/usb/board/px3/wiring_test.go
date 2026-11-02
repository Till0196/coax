// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

var errFrontendTransfer = errors.New("injected transfer failure")

// Record the real FC0012/NM120 initialization through newFrontEnds, including
// the V2's two demodulator addresses. RF reads return a supported NM131 ID.
type frontendTrace struct {
	writes       [][]byte
	selected     uint16
	failRegister byte
}

func (*frontendTrace) SetGPIO(byte, byte) error   { return errors.New("unexpected GPIO access") }
func (*frontendTrace) SetGPIOEx(byte, byte) error { return errors.New("unexpected GPIO access") }
func (bus *frontendTrace) I2C(byte) i2c.Bus       { return bus }
func (bus *frontendTrace) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	if len(data) == 2 && data[0] == bus.failRegister {
		return errFrontendTransfer
	}
	if len(data) >= 4 && bytes.Equal(data[:2], []byte{0xfe, 0xce}) {
		bus.selected = uint16(data[2])<<8 | uint16(data[3])
	}
	return nil
}

func (*frontendTrace) Read(byte, int) ([]byte, error) { return nil, errors.New("unexpected read") }
func (bus *frontendTrace) WriteRead(_ byte, write []byte, length int) ([]byte, error) {
	if !bytes.Equal(write, []byte{0xfe, 0xcf}) {
		return nil, errors.New("unexpected register read")
	}
	out := make([]byte, length)
	if bus.selected == 0x3fc && length == 4 {
		copy(out, []byte{0, 0x31, 1, 0})
	}
	return out, nil
}

func TestDemodulatorFailureStopsBeforeRFInitialization(t *testing.T) {
	for _, wired := range []wiring{fc0012Wiring, v2WiringAt(0), v2WiringAt(1)} {
		registers := []byte{0xb8}
		if len(wired.beforeRF) != 0 {
			registers = append(registers, 0x03, 0x75)
		}
		for _, register := range registers {
			t.Run(fmt.Sprintf("%02x/%02x", wired.demodTerrestrial, register), func(t *testing.T) {
				bus := &frontendTrace{failRegister: register}
				if _, err := newFrontEnds(bus, wired); !errors.Is(err, errFrontendTransfer) {
					t.Fatalf("preparation failure: %v", err)
				}
				for _, write := range bus.writes {
					if write[1] == 0xfe {
						t.Fatalf("accessed RF tuner after demodulator failure: % x", write)
					}
				}
			})
		}
	}
}

func TestDemodulatorPreparationPrecedesRFInitialization(t *testing.T) {
	common := []byte{0xb0, 0xa0, 0xb2, 0x3d, 0xb3, 0x25, 0xb4, 0x8b, 0xb5, 0x4b, 0xb6, 0x3f, 0xb7, 0xff, 0xb8, 0xc0}
	v2Setup := []byte{3, 0, 0x1d, 0, 0x1f, 0, 0x0e, 0x77, 0x0f, 0x13, 0x75, 2}
	for _, one := range []struct {
		name    string
		wired   wiring
		setup   []byte
		firstRF []byte
		b8      byte
		finish  []byte
		last    []byte
	}{
		{"S3U", wiringFor(0x0001),
			nil,
			[]byte{0xfe, 0xc6, 1, 5}, 0xff, nil, []byte{0x32, 0x08, 0x77}},
		{"S3U2", wiringFor(0x0003),
			nil,
			[]byte{0xfe, 0xc6, 1, 5}, 0xff, []byte{0x0f, 0x34}, []byte{0x30, 0x0f, 0x34}},
		{"W3U3", wiringFor(0x0005),
			nil,
			[]byte{0xfe, 0xc6, 1, 5}, 0xff, []byte{0x0f, 0x34}, []byte{0x30, 0x0f, 0x34}},
		{"V2/0", v2WiringAt(0),
			v2Setup,
			[]byte{0xfe, 0xce, 0, 6, 0x48}, 0xc0, []byte{0x0e, 0x77, 0x0f, 0x13}, []byte{0x22, 0x04, 0x02}},
		{"V2/1", v2WiringAt(1),
			v2Setup,
			[]byte{0xfe, 0xce, 0, 6, 0x48}, 0xc0, []byte{0x0e, 0x77, 0x0f, 0x13}, []byte{0x26, 0x04, 0x02}},
	} {
		t.Run(one.name, func(t *testing.T) {
			bus := &frontendTrace{}
			// Satellite RF setup is independent of the terrestrial sequence.
			one.wired.newSatellite = newTableSatellite
			if _, err := newFrontEnds(bus, one.wired); err != nil {
				t.Fatal(err)
			}
			var want [][]byte
			defaults := bytes.Clone(common)
			defaults[len(defaults)-1] = one.b8
			for _, pairs := range [][]byte{defaults, one.setup} {
				for at := 0; at < len(pairs); at += 2 {
					want = append(want, []byte{one.wired.demodTerrestrial, pairs[at], pairs[at+1]})
				}
			}
			want = append(want, append([]byte{one.wired.demodTerrestrial}, one.firstRF...))
			if len(bus.writes) < len(want) || !reflect.DeepEqual(bus.writes[:len(want)], want) {
				t.Fatalf("initial writes = % x; want prefix % x", bus.writes, want)
			}
			var finish []byte
			for _, write := range bus.writes[len(want):] {
				if len(write) == 3 && write[0] == one.wired.demodTerrestrial && write[1] >= 0xb0 && write[1] <= 0xb8 {
					t.Fatalf("duplicate demodulator initialization: % x", write)
				}
				if len(write) == 3 && write[0] == one.wired.demodTerrestrial && (write[1] == 0x0e || write[1] == 0x0f) {
					finish = append(finish, write[1:]...)
				}
			}
			if !bytes.Equal(finish, one.finish) {
				t.Fatalf("final terrestrial settings = % x, want % x", finish, one.finish)
			}
			if last := bus.writes[len(bus.writes)-1]; !bytes.Equal(last, one.last) {
				t.Fatalf("last initialization write = % x, want % x", last, one.last)
			}
			if !bytes.Equal(tc90522.InitTerrestrial, common) {
				t.Fatal("common defaults changed")
			}
		})
	}
}

// TestWiringIsPerGeneration. Pointing one generation's wiring at the other
// reaches bus addresses where nothing answers, so which entry a product ID
// lands on is worth pinning.
func TestWiringIsPerGeneration(t *testing.T) {
	for product, fc0012Board := range map[uint16]bool{
		0x0001: true,  // PX-S3U
		0x0003: true,  // PX-S3U2
		0x0004: true,  // PX-W3U2
		0x0005: true,  // PX-W3U3
		0x0006: false, // PX-W3U3 V2
	} {
		got := wiringFor(product)
		want := v2Wiring
		if fc0012Board {
			want = fc0012Wiring
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
	if fc0012Wiring.demodTerrestrial == v2Wiring.demodTerrestrial {
		t.Error("the two generations should not share a terrestrial demodulator address")
	}
	if fc0012Wiring.demodSatellite == v2Wiring.demodSatellite {
		t.Error("the two generations should not share a satellite demodulator address")
	}
	// Both generations put the terrestrial demodulator at the lower address. The
	// V2 once had them the other way round, which sent the NM120's writes to the
	// satellite demodulator, where nothing at 0xce answers.
	if v2Wiring.demodTerrestrial != 0x20 || v2Wiring.demodSatellite != 0x22 {
		t.Errorf("the V2's demodulators are at %#02x/%#02x, want 0x20/0x22",
			v2Wiring.demodTerrestrial, v2Wiring.demodSatellite)
	}
	if fc0012Wiring.demodSatellite <= fc0012Wiring.demodTerrestrial {
		t.Error("the FC0012 boards put their satellite demodulator at the higher address")
	}
	if fc0012Wiring.terrestrialName == v2Wiring.terrestrialName ||
		fc0012Wiring.satelliteName == v2Wiring.satelliteName {
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
// FC0012 boards feed the aerial lead when zero is written where the V2 feeds it
// when the bit is. One polarity for both starves one of them.
func TestTheLNBSupplyRunsOppositeWaysOnTheTwoGenerations(t *testing.T) {
	if fc0012Wiring.lnbSupplyOn != 0x00 {
		t.Errorf("the FC0012 boards feed the lead by writing %#02x, want 0x00", fc0012Wiring.lnbSupplyOn)
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
