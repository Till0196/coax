// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plan

import (
	"strconv"
	"testing"
)

// TestPlansAreTheBandsThemselves checks each plan's shape against the numbers
// the channel files write. Nothing is measured: a plan says where the carriers
// could be, and the scan finds out which of them are.
func TestPlansAreTheBandsThemselves(t *testing.T) {
	for _, test := range []struct {
		name  string
		plan  []Carrier
		count int
		first Carrier
		last  Carrier
	}{
		// Cable frequencies are in hertz and satellite ones in kilohertz,
		// which is the division the channel files and the DVB API both make.
		{"cable", Cable(), 113,
			Carrier{"1", 93000000}, Carrier{"62", 767000000}},
		// The same plan, a seventh of a megahertz up.
		{"terrestrial", Terrestrial(), 113,
			Carrier{"1", 93142857}, Carrier{"62", 767142857}},
		{"optical", Optical(), 108,
			Carrier{"73750", 73750000}, Carrier{"769250", 769250000}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if len(test.plan) != test.count {
				t.Fatalf("got %d carriers, want %d", len(test.plan), test.count)
			}
			if test.plan[0] != test.first {
				t.Errorf("first Carrier = %v, want %v", test.plan[0], test.first)
			}
			if got := test.plan[len(test.plan)-1]; got != test.last {
				t.Errorf("last carrier = %v, want %v", got, test.last)
			}
			seen := map[int]bool{}
			for _, position := range test.plan {
				if seen[position.Frequency] {
					t.Errorf("%d appears twice", position.Frequency)
				}
				seen[position.Frequency] = true
			}
		})
	}
}

// The 124/128 degree east plan, against the sources it was read from: the
// frequencies, widths and polarisations are the published frequency table's, and
// the symbol rates and modulations are what the transponder databases for these
// two orbital slots record. There is no dish here to measure against, so the
// whole plan is checked rather than a sample.
func TestSkyPlanMatchesThePublishedTable(t *testing.T) {
	channels := SkyChannels()
	// Sixty transponders, each tried both ways the grid is modulated.
	if len(channels) != 120 {
		t.Fatalf("the plan holds %d channels, want 120", len(channels))
	}
	for _, one := range []struct {
		name         string
		satellite    string
		downlinkKHz  int
		bandwidthMHz int
		polarization string
		modulation   string
		symbolRate   int
	}{
		// JCSAT-3A, the 27 MHz series: 12508 MHz and 15 MHz apiece.
		{"JCSAT3A/JD1", "JCSAT3A", 12_508_000, 27, "VERTICAL", "PSK/8", 23_303_000},
		{"JCSAT3A/JD2", "JCSAT3A", 12_523_000, 27, "HORIZONTAL", "PSK/8", 23_303_000},
		{"JCSAT3A/JD16", "JCSAT3A", 12_733_000, 27, "HORIZONTAL", "PSK/8", 23_303_000},
		// And the 36 MHz series: 12268 MHz and 20 MHz apiece. It is modulated
		// at the same rate as the rest -- the wider transponder is not filled.
		{"JCSAT3A/JD17", "JCSAT3A", 12_268_000, 36, "VERTICAL", "PSK/8", 23_303_000},
		{"JCSAT3A/JD28", "JCSAT3A", 12_488_000, 36, "HORIZONTAL", "PSK/8", 23_303_000},
		// JCSAT-4B, a quarter of a megahertz off its neighbour throughout.
		{"JCSAT4B/K1", "JCSAT4B", 12_268_250, 27, "VERTICAL", "PSK/8", 23_303_000},
		{"JCSAT4B/K16", "JCSAT4B", 12_493_250, 27, "HORIZONTAL", "PSK/8", 23_303_000},
		{"JCSAT4B/JD1", "JCSAT4B", 12_508_250, 27, "VERTICAL", "PSK/8", 23_303_000},
		{"JCSAT4B/JD16", "JCSAT4B", 12_733_250, 27, "HORIZONTAL", "PSK/8", 23_303_000},
		// The older carriers, on the same frequencies and modulated the older
		// way. JD9 and JD11 of JCSAT-3A are two that still are.
		{"JCSAT3A/JD9/QPSK", "JCSAT3A", 12_628_000, 27, "VERTICAL", "QPSK", 21_096_000},
		{"JCSAT3A/JD11/QPSK", "JCSAT3A", 12_658_000, 27, "VERTICAL", "QPSK", 21_096_000},
	} {
		got, found := SkyCarrier(one.name)
		if !found {
			t.Errorf("%s is not in the plan", one.name)
			continue
		}
		if got.Satellite != one.satellite || got.DownlinkKHz != one.downlinkKHz ||
			got.BandwidthMHz != one.bandwidthMHz || got.Polarization() != one.polarization ||
			got.Modulation != one.modulation || got.SymbolRate != one.symbolRate {
			t.Errorf("%s = %+v %s, want %s %d kHz %d MHz %s %s %d",
				one.name, got, got.Polarization(),
				one.satellite, one.downlinkKHz, one.bandwidthMHz, one.polarization,
				one.modulation, one.symbolRate)
		}
	}
	// Every downlink is inside what the block that looks at it receives,
	// 12.2 to 12.75 GHz, or the conversion would refuse it.
	seen := map[string]bool{}
	for _, one := range channels {
		if one.DownlinkKHz < 12_200_000 || one.DownlinkKHz > 12_750_000 {
			t.Errorf("%s at %d kHz is outside the block's band", one.Name, one.DownlinkKHz)
		}
		if seen[one.Name] {
			t.Errorf("%s appears twice", one.Name)
		}
		seen[one.Name] = true
	}
	if _, found := SkyCarrier("JCSAT3A/JD29"); found {
		t.Error("a transponder the table does not list was answered for")
	}
}

// TestChannelBandsJoinWhereThePlanIsIrregular checks the three joins that make
// the plan look irregular written out as one list -- eight megahertz at two
// places and four at another. They are the whole of it: everywhere else is six.
func TestChannelBandsJoinWhereThePlanIsIrregular(t *testing.T) {
	at := map[string]int{}
	for _, position := range Cable() {
		at[position.Name] = position.Frequency
	}
	for _, join := range []struct {
		from, to string
		step     int
		why      string
	}{
		{"C21", "C22", 8_000_000, "the mid band's spare two megahertz, with C22 against the top edge"},
		{"7", "8", 4_000_000, "the two halves of VHF high, which overlap by two megahertz"},
		{"C63", "13", 8_000_000, "the super high band ending at 468 where UHF begins at 470"},
	} {
		if got := at[join.to] - at[join.from]; got != join.step {
			t.Errorf("%s to %s is %d, want %d: %s", join.from, join.to, got, join.step, join.why)
		}
	}
	// And inside a band, six every time.
	for _, band := range televisionBands {
		carriers := band.carriers(cableOffsetHz)
		for index := 1; index < len(carriers); index++ {
			if got := carriers[index].Frequency - carriers[index-1].Frequency; got != channelSpacingHz {
				t.Errorf("%s: %s to %s is %d, want %d",
					band.name, carriers[index-1].Name, carriers[index].Name, got, channelSpacingHz)
			}
		}
	}
	// The bands account for the whole plan and for nothing twice.
	if len(Cable()) != 113 {
		t.Errorf("the bands give %d channels, want the 113 the channel files list", len(Cable()))
	}
	for _, want := range []Carrier{
		{"1", 93_000_000}, {"C13", 111_000_000}, {"C22", 167_000_000},
		{"4", 173_000_000}, {"8", 195_000_000}, {"C23", 225_000_000},
		{"C63", 465_000_000}, {"13", 473_000_000}, {"62", 767_000_000},
	} {
		if at[want.Name] != want.Frequency {
			t.Errorf("%s is at %d, want %d", want.Name, at[want.Name], want.Frequency)
		}
	}
}

// TestTerrestrialAndCableAreNotTheSameFrequency checks the 142857 Hz between a
// channel of the same number on the two plans. Tuning one as the other is a lock
// that never comes, which reads as a broken tuner.
func TestTerrestrialAndCableAreNotTheSameFrequency(t *testing.T) {
	cable := map[string]int{}
	for _, position := range Cable() {
		cable[position.Name] = position.Frequency
	}
	for _, position := range Terrestrial() {
		want, known := cable[position.Name]
		if !known {
			t.Errorf("%s is on the terrestrial plan and not the cable one", position.Name)
			continue
		}
		if position.Frequency-want != terrestrialOffsetHz {
			t.Errorf("%s is %d apart, want %d", position.Name, position.Frequency-want, terrestrialOffsetHz)
		}
	}
	// The two the channel files are usually read for.
	for _, want := range []Carrier{{"13", 473142857}, {"C13", 111142857}} {
		found := false
		for _, position := range Terrestrial() {
			if position == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s at %d is not on the terrestrial plan", want.Name, want.Frequency)
		}
	}
}

// TestTerrestrialPTXChannelMatchesTheDriver: these numbers are not ours to
// choose. A driver already installed turns them back into frequencies, and a
// file written with different ones tunes the wrong channel rather than failing.
func TestTerrestrialPTXChannelMatchesTheDriver(t *testing.T) {
	for name, want := range map[string]int{
		"C13": 3, "C22": 12, // the cable run the driver knows
		"C23": 22, "C63": 62,
		"13": 63, "62": 112, // UHF
		"1": 0, "3": 2, // the three lowest cable channels, in numbers it leaves free
		"4": 13, "12": 21, // the nine VHF ones, in the nine it leaves free
	} {
		got, ok := PTXChannel(name)
		if !ok || got != want {
			t.Errorf("PTXChannel(%q) = %d, %t; want %d", name, got, ok, want)
		}
	}
	for _, name := range []string{"", "C12", "C64", "63", "0", "BS01", "C"} {
		if got, ok := PTXChannel(name); ok {
			t.Errorf("PTXChannel(%q) = %d; want no answer", name, got)
		}
	}
}

// TestTerrestrialPTXChannelTunesTheRightFrequency puts every channel of the plan
// through the driver's own arithmetic, written out here once, and compares the
// result with where that channel actually broadcasts: the number a file carries
// is worth nothing except through that conversion.
//
// The frequencies below are stated rather than derived, because a plan derived
// the way the code derives it would agree with a mistake.
func TestTerrestrialPTXChannelTunesTheRightFrequency(t *testing.T) {
	// What BonDriver_PX4 and px4_drv compute from a physical channel number.
	driverKHz := func(ptx int) int {
		frequency := 93143 + ptx*6000
		if (ptx >= 12 && ptx <= 16) || ptx >= 63 {
			frequency += 2000
		}
		return frequency
	}

	// Each run of the plan: the first channel, its centre, and how many
	// channels are packed from it at six megahertz apart.
	for _, band := range []struct {
		name  func(int) string
		first int
		kHz   int
		count int
	}{
		{func(n int) string { return strconv.Itoa(n) }, 1, 93143, 3},          // VHF 1-3
		{func(n int) string { return "C" + strconv.Itoa(n) }, 13, 111143, 9},  // C13-C21
		{func(n int) string { return "C" + strconv.Itoa(n) }, 22, 167143, 1},  // C22, at the top edge
		{func(n int) string { return strconv.Itoa(n) }, 4, 173143, 4},         // VHF 4-7
		{func(n int) string { return strconv.Itoa(n) }, 8, 195143, 5},         // VHF 8-12
		{func(n int) string { return "C" + strconv.Itoa(n) }, 23, 225143, 41}, // C23-C63
		{func(n int) string { return strconv.Itoa(n) }, 13, 473143, 50},       // UHF 13-62
	} {
		for step := 0; step < band.count; step++ {
			name := band.name(band.first + step)
			want := band.kHz + step*6000
			ptx, ok := PTXChannel(name)
			if !ok {
				t.Errorf("PTXChannel(%q) has no answer; want the channel at %d kHz", name, want)
				continue
			}
			if got := driverKHz(ptx); got != want {
				t.Errorf("%s is physical channel %d, which the driver tunes to %d kHz; want %d kHz",
					name, ptx, got, want)
			}
		}
	}
}
