// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2858er

import (
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"strings"
	"testing"
)

// TestSatelliteCutoffFollowsTheSymbolRate pins the filter widths each curve
// produces.
func TestSatelliteCutoffFollowsTheSymbolRate(t *testing.T) {
	for _, test := range []struct {
		system       sony.System
		symbolRateKS uint32
		want         byte
	}{
		// ISDB-S is fixed, and its symbol rate is ignored.
		{sony.SystemISDBS, 0, 22},
		{sony.SystemISDBS, 28860, 22},
		// Both DVB curves floor at 5 MHz below 4000 kSym/s.
		{sony.SystemDVBS, 1000, 5},
		{sony.SystemDVBS2, 4000, 5},
		// DVB-S: 47/40000 up to 10000, then 27/40000 plus five.
		{sony.SystemDVBS, 10000, 12}, // ceil(10000*47/40000) = 12
		{sony.SystemDVBS, 27500, 24}, // ceil(27500*27/40000) + 5 = 19 + 5
		// DVB-S2 widens faster: 11/10000, then 3/5000 plus five.
		{sony.SystemDVBS2, 10000, 11}, // ceil(10000*11/10000)
		{sony.SystemDVBS2, 27500, 22}, // ceil(27500*3/5000) + 5 = 17 + 5
		// Both clamp at 36.
		{sony.SystemDVBS, 60000, 36},
		{sony.SystemDVBS2, 60000, 36},
	} {
		got, err := satelliteCutoffMHz(test.system, test.symbolRateKS)
		if err != nil {
			t.Errorf("%s at %d kSym/s: %v", test.system, test.symbolRateKS, err)
			continue
		}
		if got != test.want {
			t.Errorf("%s at %d kSym/s: cutoff = %d MHz, want %d",
				test.system, test.symbolRateKS, got, test.want)
		}
	}
}

// TestISDBSCutoffIsDecimalMegahertz: the register's 5..36 range rules out 0x22
// (= 34), so a "correction" from 22 to 0x22 fails here rather than on air.
func TestISDBSCutoffIsDecimalMegahertz(t *testing.T) {
	got, err := satelliteCutoffMHz(sony.SystemISDBS, 28860)
	if err != nil {
		t.Fatal(err)
	}
	if got != 22 {
		t.Fatalf("cutoff = %d, want 22", got)
	}
	if got == 0x22 {
		t.Fatal("the cutoff is 0x22, which is 34 MHz and outside the chip's range")
	}
}

// TestTerrestrialParamsCoverTheChannelWidths guards against a system added to
// the enum but not the table, which would tune the previous channel's filter.
func TestTerrestrialParamsCoverTheChannelWidths(t *testing.T) {
	for _, test := range []struct {
		system sony.System
		widths []uint32
	}{
		{sony.SystemDVBT, []uint32{5, 6, 7, 8}},
		{sony.SystemDVBT2, []uint32{5, 6, 7, 8}},
		{sony.SystemDVBC, []uint32{6, 8}},
	} {
		widths, ok := dvbTerrestrialParams[test.system]
		if !ok {
			t.Errorf("%s has no settings at all", test.system)
			continue
		}
		if len(widths) != len(test.widths) {
			t.Errorf("%s has %d channel widths, want %d", test.system, len(widths), len(test.widths))
		}
		for _, width := range test.widths {
			if _, ok := widths[width]; !ok {
				t.Errorf("%s has no %d MHz settings", test.system, width)
			}
		}
	}
	// The satellite systems have no terrestrial settings, and asking for them
	// must fail rather than fall back.
	for _, system := range []sony.System{sony.SystemISDBS, sony.SystemDVBS, sony.SystemDVBS2, sony.SystemISDBT} {
		if _, ok := dvbTerrestrialParams[system]; ok {
			t.Errorf("%s should not have terrestrial settings in this table", system)
		}
	}
}

func TestSatelliteGrouping(t *testing.T) {
	for system, want := range map[sony.System]bool{
		sony.SystemISDBS: true, sony.SystemDVBS: true, sony.SystemDVBS2: true,
		sony.SystemISDBT: false, sony.SystemDVBT: false, sony.SystemDVBT2: false,
		sony.SystemDVBC: false, sony.SystemUnspecified: false,
	} {
		if got := system.IsSatellite(); got != want {
			t.Errorf("satellite(%s) = %v, want %v", system, got, want)
		}
	}
}

// recordingBus records every write, including the leading register byte. Reads
// answer zeroes, which is all a masked write needs here.
type recordingBus struct{ writes [][]byte }

func (bus *recordingBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *recordingBus) Read(_ byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

type openGate struct{}

func (openGate) OpenTunerGate(bool) error { return nil }

// blockWritten returns the payload written to a register, less the register
// byte.
func blockWritten(t *testing.T, bus *recordingBus, register byte) []byte {
	t.Helper()
	for _, write := range bus.writes {
		if len(write) > 1 && write[0] == register {
			return write[1:]
		}
	}
	t.Fatalf("nothing was written to register %#02x", register)
	return nil
}

func tuneRecording(t *testing.T, xtal sony.TunerXtal, system sony.System) *recordingBus {
	t.Helper()
	bus := &recordingBus{}
	tuner := New(bus, openGate{}, 0xc0, Config{Xtal: xtal})
	if err := tuner.Tune(system, TuneParams{FrequencyKHz: 111143, BandwidthMHz: 6}); err != nil {
		t.Fatalf("tune %s on a %s crystal: %v", system, xtal, err)
	}
	return bus
}

// TestTheJ83DividerFollowsTheCrystal checks the reference divider, the fifth
// byte of the 0x5e block. The cable systems run the PLL off a 1 MHz phase
// reference, so it is the crystal in whole megahertz -- the broadcast systems
// divide by eight.
func TestTheJ83DividerFollowsTheCrystal(t *testing.T) {
	for _, test := range []struct {
		xtal sony.TunerXtal
		want byte
	}{
		{sony.TunerXtal16000KHz, 0x10}, // 16
		{sony.TunerXtal20500KHz, 0x14}, // 20, rounded down
		{sony.TunerXtal24000KHz, 0x18}, // 24
		{sony.TunerXtal41000KHz, 0x28}, // 40, rounded down
	} {
		block := blockWritten(t, tuneRecording(t, test.xtal, sony.SystemDVBC), 0x5e)
		if len(block) < 5 {
			t.Fatalf("the 0x5e block is %d bytes", len(block))
		}
		if block[4] != test.want {
			t.Errorf("a %s crystal writes reference divider %#02x, want %#02x (%d MHz)",
				test.xtal, block[4], test.want, test.want)
		}
	}
}

// TestAnUnknownCrystalIsRefusedForJ83: a wrong divider does not fail, it locks
// badly or not at all with nothing to say why.
func TestAnUnknownCrystalIsRefusedForJ83(t *testing.T) {
	bus := &recordingBus{}
	// A crystal outside sony.TunerXtal's four has no divider either way.
	tuner := New(bus, openGate{}, 0xc0, Config{Xtal: sony.TunerXtal(9)})
	err := tuner.Tune(sony.SystemDVBC, TuneParams{FrequencyKHz: 111143, BandwidthMHz: 6})
	if err == nil {
		t.Fatal("an unknown crystal was accepted for J.83")
	}
	if !strings.Contains(err.Error(), "crystal") {
		t.Errorf("the error does not name the crystal: %v", err)
	}
}

// TestTheRestOfTheJ83BlockIsCrystalIndependent: the bytes checked here differ
// from the broadcast path, and none of them depends on the crystal.
func TestTheRestOfTheJ83BlockIsCrystalIndependent(t *testing.T) {
	for _, xtal := range []sony.TunerXtal{sony.TunerXtal16000KHz, sony.TunerXtal24000KHz} {
		bus := tuneRecording(t, xtal, sony.SystemDVBC)
		for _, want := range []struct {
			register byte
			data     []byte
		}{
			{0x91, []byte{0x16, 0x26}},             // analogue block
			{0x9c, []byte{0x00, 0x00}},             // no internal loop filter
			{0x5e, []byte{0xee, 0x02, 0x1e, 0x67}}, // the block's head, before the divider
		} {
			block := blockWritten(t, bus, want.register)
			if len(block) < len(want.data) {
				t.Errorf("%s: register %#02x got %d bytes", xtal, want.register, len(block))
				continue
			}
			for index, value := range want.data {
				if block[index] != value {
					t.Errorf("%s: register %#02x byte %d is %#02x, want %#02x",
						xtal, want.register, index, block[index], value)
				}
			}
		}
		// The loop filter constants sit after the divider in the same block.
		block := blockWritten(t, bus, 0x5e)
		if got := block[5:9]; string(got) != string([]byte{0x1c, 0x78, 0x08, 0x1c}) {
			t.Errorf("%s: J.83 loop constants %#v", xtal, got)
		}
	}
}

// TestTheBroadcastDividerIsUnchanged: the terrestrial systems' divider stays the
// crystal over eight.
func TestTheBroadcastDividerIsUnchanged(t *testing.T) {
	for _, test := range []struct {
		xtal sony.TunerXtal
		want byte
	}{{sony.TunerXtal16000KHz, 0x02}, {sony.TunerXtal24000KHz, 0x03}} {
		block := blockWritten(t, tuneRecording(t, test.xtal, sony.SystemDVBT), 0x5e)
		if block[4] != test.want {
			t.Errorf("DVB-T on a %s crystal writes %#02x, want %#02x",
				test.xtal, block[4], test.want)
		}
		if got := block[5:9]; string(got) != string([]byte{0xb4, 0x78, 0x08, 0x30}) {
			t.Errorf("DVB-T loop constants %#v", got)
		}
	}
}
