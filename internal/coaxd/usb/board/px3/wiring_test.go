// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

var errFrontendTransfer = errors.New("injected transfer failure")

type terrestrialTuneFunc func(uint32) error

func (tune terrestrialTuneFunc) Tune(frequency uint32) error { return tune(frequency) }

func TestTerrestrialAcquisitionSequence(t *testing.T) {
	for _, product := range []uint16{1, 3, 4, 5, 6} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/fail=%t", product, fail), func(t *testing.T) {
				bus := &frontendTrace{}
				wired := wiringFor(product)
				rfAddress := byte(0xc6)
				if product == v2Product {
					rfAddress = 0xce
				}
				ends := frontEnds{
					wired:  wired,
					demodT: tc90522.New(bus, wired.demodTerrestrial, false),
					tunerT: terrestrialTuneFunc(func(uint32) error {
						_ = bus.Write(wired.demodTerrestrial, []byte{0xfe, rfAddress})
						if fail {
							return errFrontendTransfer
						}
						return nil
					}),
				}
				front := frontend{hardware: &hardware{frontEnds: ends, controller: &controller{}}}
				err := front.tuneTerrestrial(557143000)
				if fail && !errors.Is(err, errFrontendTransfer) || !fail && err != nil {
					t.Fatalf("tune result: %v", err)
				}
				want := [][]byte{{0x30, 0x25, 0}, {0x30, 0x23, 0x4d}, {0x30, 0xfe, 0xc6}}
				if product == v2Product {
					want = [][]byte{{0x20, 1, 0x50}, {0x20, 0x47, 0x30}, {0x20, 0x25, 0}, {0x20, 0x20, 0}, {0x20, 0x23, 0x4d}, {0x20, 0xfe, 0xce}}
				}
				if !fail {
					value := byte(0x34)
					if product == 1 {
						value = 0x14
					}
					if product == v2Product {
						want = append(want, []byte{0x20, 0x23, 0x4c}, []byte{0x20, 1, 0x50}, []byte{0x20, 0x71, 1}, []byte{0x20, 0x72, 0x24}, []byte{0x20, 0x75, 2})
					} else {
						want = append(want, []byte{0x30, 0x0f, value}, []byte{0x30, 1, 0x40}, []byte{0x30, 0x23, 0x4c})
					}
				}
				if len(bus.writes) != len(want) {
					t.Fatalf("acquisition writes = % x; want % x", bus.writes, want)
				}
				for at := range want {
					if !bytes.Equal(bus.writes[at], want[at]) {
						t.Fatalf("acquisition writes = % x; want % x", bus.writes, want)
					}
				}
			})
		}
	}
}

func TestAcquisitionStopsAtFailedDemodulatorWrite(t *testing.T) {
	for _, register := range []byte{0x25, 0x0f, 0x01} {
		t.Run(fmt.Sprintf("%02x", register), func(t *testing.T) {
			bus := &frontendTrace{failRegister: register}
			called := false
			ends := frontEnds{
				wired:  wiringFor(0x0005),
				demodT: tc90522.New(bus, 0x30, false),
				tunerT: terrestrialTuneFunc(func(uint32) error { called = true; return nil }),
			}
			front := frontend{hardware: &hardware{frontEnds: ends, controller: &controller{}}}
			if err := front.tuneTerrestrial(557143000); !errors.Is(err, errFrontendTransfer) {
				t.Fatalf("missing transfer failure: %v", err)
			}
			if called != (register != 0x25) {
				t.Fatalf("RF tuner called=%t after failure at %02x", called, register)
			}
			if last := bus.writes[len(bus.writes)-1]; last[1] != register {
				t.Fatalf("continued after failed write: % x", bus.writes)
			}
		})
	}
}

func TestS3U2PulseFailureStopsInitialization(t *testing.T) {
	bus := &frontendTrace{failWrite: []byte{0xfe, 0xc6, 0x10, 0x10}}
	if _, err := newFrontEnds(bus, wiringFor(0x0003)); !errors.Is(err, errFrontendTransfer) {
		t.Fatalf("missing pulse failure: %v", err)
	}
	for _, write := range bus.writes {
		if write[0] == 0x32 {
			t.Fatalf("satellite initialized after RF pulse failure: % x", write)
		}
	}
}

func TestMalformedTunerSetupFailsBeforeWriting(t *testing.T) {
	bus := &frontendTrace{}
	ends := frontEnds{demodT: tc90522.New(bus, 0x30, false)}
	err := ends.configureDemods(bus, []demodSetup{{index: terrestrial, tuner: 0xc6, pairs: []byte{0x10}}})
	if err == nil || len(bus.writes) != 0 {
		t.Fatalf("malformed setup: err=%v, writes=% x", err, bus.writes)
	}
}

// Record the real FC0012/NM120 initialization through newFrontEnds, including
// the V2's two demodulator addresses. RF reads return a supported NM131 ID.
type frontendTrace struct {
	writes       [][]byte
	selected     uint16
	failRegister byte
	failWrite    []byte
	gpio         [][2]byte
	fcSelected   byte
	fcUnready    bool
}

func (bus *frontendTrace) SetGPIO(value, mask byte) error {
	bus.gpio = append(bus.gpio, [2]byte{value, mask})
	return nil
}
func (*frontendTrace) SetGPIOEx(byte, byte) error { return errors.New("unexpected GPIO access") }
func (bus *frontendTrace) I2C(byte) i2c.Bus       { return bus }
func (bus *frontendTrace) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte{address}, data...))
	if len(data) == 2 && data[0] == bus.failRegister || bytes.Equal(data, bus.failWrite) {
		return errFrontendTransfer
	}
	if len(data) >= 4 && bytes.Equal(data[:2], []byte{0xfe, 0xce}) {
		bus.selected = uint16(data[2])<<8 | uint16(data[3])
	}
	if len(data) == 3 && bytes.Equal(data[:2], []byte{0xfe, 0xc6}) {
		bus.fcSelected = data[2]
	}
	return nil
}

func (*frontendTrace) Read(byte, int) ([]byte, error) { return nil, errors.New("unexpected read") }
func (bus *frontendTrace) WriteRead(_ byte, write []byte, length int) ([]byte, error) {
	if bytes.Equal(write, []byte{0xfe, 0xc7}) {
		out := make([]byte, length)
		if bus.fcSelected == 0x0e {
			out[0] = 0x60
			if bus.fcUnready {
				out[0] = 0x20
			}
		}
		return out, nil
	}
	if len(write) == 1 {
		return make([]byte, length), nil
	}
	if !bytes.Equal(write, []byte{0xfe, 0xcf}) {
		return nil, errors.New("unexpected register read")
	}
	out := make([]byte, length)
	if bus.selected == 0x3fc && length == 4 {
		copy(out, []byte{0, 0x31, 1, 0})
	}
	return out, nil
}

func TestW3RFCalibrationRetriesAreBounded(t *testing.T) {
	for _, failReset := range []bool{false, true} {
		bus := &frontendTrace{fcUnready: true}
		if failReset {
			bus.failRegister = 0x1c
		}
		rf, err := newW3FC0012(bus, 0x30)
		if err != nil {
			t.Fatal(err)
		}
		err = rf.(*fc0012Tuner).acquire(tc90522.New(bus, 0x30, false), 557_142_857)
		if failReset && !errors.Is(err, errFrontendTransfer) || !failReset && err != nil {
			t.Fatalf("reset failure=%t: %v", failReset, err)
		}
		attempts, initializations := 0, 0
		for _, write := range bus.writes {
			if bytes.Equal(write, []byte{0x30, 0xfe, 0xc6, 1, 2}) {
				attempts++
			}
			if bytes.Equal(write, []byte{0x30, 0xfe, 0xc6, 0x15, 4}) {
				initializations++
			}
		}
		want := 4
		if failReset {
			want = 1
		}
		if attempts != want || initializations != want {
			t.Fatalf("RF attempts=%d initializations=%d; want %d", attempts, initializations, want)
		}
	}
}

func TestW3ManualGainSurvivesRFReinitialization(t *testing.T) {
	bus := &frontendTrace{fcUnready: true}
	rf, err := newW3FC0012(bus, 0x30)
	if err != nil {
		t.Fatal(err)
	}
	tuner := rf.(*fc0012Tuner)
	if err := tuner.SetLNAGain(23); err != nil {
		t.Fatal(err)
	}
	if err := tuner.acquire(tc90522.New(bus, 0x30, false), 557_142_857); err != nil {
		t.Fatal(err)
	}
	for _, write := range bus.writes {
		if len(write) == 5 && bytes.Equal(write[:4], []byte{0x30, 0xfe, 0xc6, 0x13}) && write[4] != 0x10 {
			t.Fatalf("manual gain replaced: % x", write)
		}
	}
}

func TestDemodulatorFailureStopsBeforeRFInitialization(t *testing.T) {
	for _, wired := range []wiring{fc0012WiringFor(0x0005), v2WiringAt(0), v2WiringAt(1)} {
		registers := []byte{0xb8, 0x75}
		if wired.demodTerrestrial != 0x30 {
			registers = append(registers, 0x03)
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
	fcSetup := []byte{0x47, 0, 0x75, 2, 0xb0, 0xa0, 0xb2, 0x3d, 0xb3, 0x25, 0xb4, 0x8b, 0xb5, 0x4b, 0xb6, 0x3f, 0xb7, 0xff, 0xb8, 0xff, 0x22, 0x8f, 0x5f, 0x80, 0xef, 1}
	w3Setup := append([]byte{4, 0, 0x11, 0x1a, 0x12, 4, 0x13, 0x33, 0x14, 0x20, 0x31, 0, 0x32, 0, 0x38, 0, 0x39, 0xaa}, fcSetup...)
	v2Setup := append(bytes.Clone(common), 3, 0, 0x1d, 0, 0x1f, 0, 0x0e, 0x77, 0x0f, 0x13, 0x75, 2)
	for _, one := range []struct {
		name    string
		wired   wiring
		setup   []byte
		firstRF []byte
		finish  []byte
		last    []byte
	}{
		{"S3U", wiringFor(0x0001),
			fcSetup,
			[]byte{0xfe, 0xc6, 1, 5}, nil, []byte{0x30, 0xfe, 0xc6, 0x15, 4}},
		{"S3U2", wiringFor(0x0003),
			fcSetup,
			[]byte{0xfe, 0xc6, 1, 5}, []byte{0x0f, 0x34}, []byte{0x30, 0x0f, 0x34}},
		{"W3U3", wiringFor(0x0005),
			w3Setup,
			[]byte{0xfe, 0xc6, 1, 5}, []byte{0x0f, 0x34, 0x0f, 0x34}, []byte{0x30, 0x23, 0x4c}},
		{"V2/0", v2WiringAt(0),
			v2Setup,
			[]byte{0xfe, 0xce, 0, 6, 0x48}, []byte{0x0e, 0x77, 0x0f, 0x13}, []byte{0x22, 0x04, 0x02}},
		{"V2/1", v2WiringAt(1),
			v2Setup,
			[]byte{0xfe, 0xce, 0, 6, 0x48}, []byte{0x0e, 0x77, 0x0f, 0x13}, []byte{0x26, 0x04, 0x02}},
	} {
		t.Run(one.name, func(t *testing.T) {
			bus := &frontendTrace{}
			// Satellite RF setup is independent of the terrestrial sequence.
			one.wired.newSatellite = newTableSatellite
			if _, err := newFrontEnds(bus, one.wired); err != nil {
				t.Fatal(err)
			}
			var initial []byte
			firstRF := -1
			for at, write := range bus.writes {
				if write[1] == 0xfe {
					firstRF = at
					break
				}
				if write[0] == one.wired.demodTerrestrial {
					initial = append(initial, write[1:]...)
				}
			}
			if !bytes.Equal(initial, one.setup) {
				t.Fatalf("terrestrial preparation = % x; want % x", initial, one.setup)
			}
			if firstRF < 0 || !bytes.Equal(bus.writes[firstRF][1:], one.firstRF) {
				t.Fatalf("first RF transfer missing or incorrect: % x", bus.writes)
			}
			var finish []byte
			for _, write := range bus.writes[firstRF+1:] {
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
			if one.name == "W3U3" {
				if len(bus.gpio) != 1 || bus.gpio[0] != [2]byte{0, 1} {
					t.Fatalf("initial RF LNA state: %v", bus.gpio)
				}
				var clock byte
				for _, write := range bus.writes {
					if len(write) == 5 && bytes.Equal(write[:4], []byte{0x30, 0xfe, 0xc6, 6}) {
						clock = write[4]
					}
				}
				if clock != 0x8a {
					t.Fatalf("initial 773143kHz clock setting = %02x", clock)
				}
			} else if len(bus.gpio) != 0 {
				t.Fatalf("unexpected RF GPIO operations: %v", bus.gpio)
			}
			if one.name == "S3U2" {
				var pulses []byte
				var positions []int
				firstSatellite, lastSatellite := -1, -1
				for at, write := range bus.writes {
					if write[0] == 0x32 {
						if firstSatellite < 0 {
							firstSatellite = at
						}
						lastSatellite = at
					}
					if len(write) == 5 && bytes.Equal(write[:4], []byte{0x30, 0xfe, 0xc6, 0x10}) {
						pulses = append(pulses, write[4])
						positions = append(positions, at)
					}
				}
				if !bytes.Equal(pulses, []byte{0, 0, 0x10, 0, 0, 0x10, 0}) {
					t.Fatalf("RF10 initialization and pulses = % x", pulses)
				}
				if positions[1] != firstRF+21 || positions[3]+1 != firstSatellite || lastSatellite+1 != positions[4] || positions[6]+1 != len(bus.writes)-1 {
					t.Fatalf("RF pulses at %v, satellite initialization at %d..%d", positions, firstSatellite, lastSatellite)
				}
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
			want = fc0012WiringFor(0x0005)
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
	if fc0012WiringFor(0x0005).demodTerrestrial == v2Wiring.demodTerrestrial {
		t.Error("the two generations should not share a terrestrial demodulator address")
	}
	if fc0012WiringFor(0x0005).demodSatellite == v2Wiring.demodSatellite {
		t.Error("the two generations should not share a satellite demodulator address")
	}
	// Both generations put the terrestrial demodulator at the lower address. The
	// V2 once had them the other way round, which sent the NM120's writes to the
	// satellite demodulator, where nothing at 0xce answers.
	if v2Wiring.demodTerrestrial != 0x20 || v2Wiring.demodSatellite != 0x22 {
		t.Errorf("the V2's demodulators are at %#02x/%#02x, want 0x20/0x22",
			v2Wiring.demodTerrestrial, v2Wiring.demodSatellite)
	}
	if fc0012WiringFor(0x0005).demodSatellite <= fc0012WiringFor(0x0005).demodTerrestrial {
		t.Error("the FC0012 boards put their satellite demodulator at the higher address")
	}
	if fc0012WiringFor(0x0005).terrestrialName == v2Wiring.terrestrialName ||
		fc0012WiringFor(0x0005).satelliteName == v2Wiring.satelliteName {
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
	if fc0012WiringFor(0x0005).lnbSupplyOn != 0x00 {
		t.Errorf("the FC0012 boards feed the lead by writing %#02x, want 0x00", fc0012WiringFor(0x0005).lnbSupplyOn)
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

// The V2's two tuners both have four levels, the FC0012 is on or off, and the
// QM1D1B0004 has no loop-through output.
func TestTheLoopThroughIsListedWhereTheTunerHasOne(t *testing.T) {
	v2 := Layout(usbio.ID{Vendor: 0x0b06, Product: 0x0006})
	for _, index := range []int{terrestrial, satellite} {
		if loop := v2[index].Controls.LoopThrough; loop == nil || loop.Switch {
			t.Errorf("the V2's tuner %d lists %+v, want four levels", index, loop)
		}
	}
	w3 := Layout(usbio.ID{Vendor: 0x0b06, Product: 0x0005})
	if loop := w3[terrestrial].Controls.LoopThrough; loop == nil || !loop.Switch {
		t.Errorf("the FC0012 lists %+v, want on or off", loop)
	}
	if loop := w3[satellite].Controls.LoopThrough; loop != nil {
		t.Errorf("the QM1D1B0004 lists %+v, want none", loop)
	}
}

// An enclosure of two bridges feeds one lead from its supply.
func TestTheSupplyIsSharedByAnEnclosureOfTwoBridges(t *testing.T) {
	for product, want := range map[uint16]string{0x0004: "card", 0x0005: "card", v2Product: "card"} {
		lnb := Layout(usbio.ID{Vendor: 0x0b06, Product: product})[satellite].Controls.LNB
		if lnb == nil || lnb.Shared != want {
			t.Errorf("product %#04x lists the supply %+v, want shared %q", product, lnb, want)
		}
	}
}

// The PX-S3U and PX-S3U2 offer no supply, a channel naming one is refused, and
// tuning one leaves GPIO 0x20 alone.
func TestTheS3UBoardsOfferNoSupply(t *testing.T) {
	for _, product := range []uint16{0x0001, 0x0003} {
		if lnb := Layout(usbio.ID{Vendor: 0x0b06, Product: product})[satellite].Controls.LNB; lnb != nil {
			t.Errorf("product %#04x offers the supply %+v", product, lnb)
		}
		if !wiringFor(product).noLNBControl {
			t.Errorf("product %#04x would switch GPIO 0x20 for the supply", product)
		}
	}
	for _, product := range []uint16{0x0004, 0x0005, v2Product} {
		if wiringFor(product).noLNBControl {
			t.Errorf("product %#04x has lost its supply", product)
		}
	}
	front := &frontend{hardware: &hardware{frontEnds: frontEnds{wired: wiringFor(0x0003)}}, index: satellite}
	for _, voltage := range []string{"off", "high"} {
		err := front.tuneSatellite(1318000, map[string]string{"DELIVERY_SYSTEM": "ISDBS", "VOLTAGE": voltage})
		if !errors.Is(err, tuning.ErrNoLNBSwitch) {
			t.Errorf("VOLTAGE=%s gave %v, want the supply refused", voltage, err)
		}
	}
}
