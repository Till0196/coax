// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package chardev

import (
	"errors"
	"testing"
)

// The expected channel numbers here are px4_drv's own.
func TestTerrestrialChannelMatchesTheDriverTable(t *testing.T) {
	for _, one := range []struct {
		name      string
		frequency int64
		channel   int
	}{
		{"cable C13", 111_143, 3},
		{"cable C22 sits 2 MHz above its run", 167_143, 12},
		{"cable C23", 225_143, 22},
		{"cable C63", 465_143, 62},
		{"terrestrial 13", 473_143, 63},
		{"terrestrial 27", 557_143, 77},
		{"terrestrial 62", 767_143, 112},
	} {
		t.Run(one.name, func(t *testing.T) {
			channel, offset, err := TerrestrialChannel(one.frequency)
			if err != nil {
				t.Fatalf("TerrestrialChannel(%d): %v", one.frequency, err)
			}
			if channel != one.channel {
				t.Errorf("channel = %d, want %d", channel, one.channel)
			}
			if offset != 0 {
				t.Errorf("offset = %d, want 0", offset)
			}
		})
	}
}

// A frequency between two channels is carried through as an offset rather than
// rounded away, because the driver adds it back to the frequency it looked up.
func TestTerrestrialChannelKeepsTheOffset(t *testing.T) {
	channel, offset, err := TerrestrialChannel(473_143 + 142)
	if err != nil {
		t.Fatalf("TerrestrialChannel: %v", err)
	}
	if channel != 63 || offset != 142 {
		t.Errorf("got channel %d offset %d, want 63 and 142", channel, offset)
	}
}

// A frequency that names no channel of the plan is refused. The plan is
// contiguous at six megahertz from VHF 1 to channel 62, so the only frequencies
// that name no channel are the ones outside it altogether.
func TestTerrestrialChannelRejectsWhatIsNotAChannel(t *testing.T) {
	for _, frequency := range []int64{80_000, 90_142, 770_144, 800_000} {
		if _, _, err := TerrestrialChannel(frequency); !errors.Is(err, ErrNoChannel) {
			t.Errorf("TerrestrialChannel(%d) = %v, want ErrNoChannel", frequency, err)
		}
	}
}

// The two VHF stretches have no number in px4_drv's table and are reached from
// the nearest number it does have, with the difference in the offset. The
// driver adds the two back together, so what matters is that they sum to the
// frequency asked for and that the offset stays small.
func TestTerrestrialChannelReachesVHFThroughTheOffset(t *testing.T) {
	for _, one := range []struct {
		name      string
		frequency int64
	}{
		{"VHF 1", 93_143},
		{"VHF 2", 99_143},
		{"VHF 3", 105_143},
		{"VHF 4", 173_143},
		{"VHF 8", 195_143},
		{"VHF 12", 219_143},
	} {
		channel, offset, err := TerrestrialChannel(one.frequency)
		if err != nil {
			t.Errorf("%s at %d kHz: %v", one.name, one.frequency, err)
			continue
		}
		centre, ok := terrestrialCentreKHz(channel)
		if !ok {
			t.Errorf("%s: channel %d is not one the driver has", one.name, channel)
			continue
		}
		if centre+int64(offset) != one.frequency {
			t.Errorf("%s: channel %d (%d kHz) plus %d kHz is %d, want %d",
				one.name, channel, centre, offset, centre+int64(offset), one.frequency)
		}
		// The widest the plan ever needs: VHF 8 sits 28 MHz above C22, the
		// middle of the stretch the numbering skips.
		if offset > 28_000 || offset < -28_000 {
			t.Errorf("%s: offset %d kHz is further than the plan ever needs", one.name, offset)
		}
	}
}

// Every channel of the plan is reachable, and each comes back as a number the
// driver accepts: 3..12, 22..62 and 63..112 and nothing else.
func TestEveryChannelOfThePlanIsReachable(t *testing.T) {
	for number := earthsoftFirstChannel; number <= pt1LastChannel; number++ {
		frequency, ok := PT1TerrestrialCentreKHz(number)
		if !ok {
			t.Fatalf("the plan has no channel %d", number)
		}
		channel, offset, err := TerrestrialChannel(frequency)
		if err != nil {
			t.Errorf("plan channel %d at %d kHz: %v", number, frequency, err)
			continue
		}
		accepted := (channel >= cableLowFirst && channel <= cableLowLast) ||
			(channel >= cableHighFirst && channel <= cableHighLast) ||
			(channel >= uhfFirst && channel <= uhfLast)
		if !accepted {
			t.Errorf("plan channel %d came back as %d, which the driver refuses", number, channel)
		}
		centre, _ := terrestrialCentreKHz(channel)
		if centre+int64(offset) != frequency {
			t.Errorf("plan channel %d: %d + %d is not %d", number, centre, offset, frequency)
		}
	}
}

func TestSatelliteChannelMatchesTheDriverTable(t *testing.T) {
	for _, one := range []struct {
		name      string
		frequency int64
		channel   int
	}{
		{"BS-1", 1_049_480, 0},
		{"BS-23", 1_049_480 + 11*38_360, 11},
		{"ND02", 1_613_000, 12},
		{"ND24", 1_613_000 + 11*40_000, 23},
	} {
		t.Run(one.name, func(t *testing.T) {
			channel, err := SatelliteChannel(one.frequency)
			if err != nil {
				t.Fatalf("SatelliteChannel(%d): %v", one.frequency, err)
			}
			if channel != one.channel {
				t.Errorf("channel = %d, want %d", channel, one.channel)
			}
		})
	}
}

// A downlink frequency rather than a first intermediate one is a plausible
// thing to find in a channel file, and is nowhere near any transponder.
func TestSatelliteChannelRejectsADownlinkFrequency(t *testing.T) {
	if _, err := SatelliteChannel(11_727_480); !errors.Is(err, ErrNoChannel) {
		t.Errorf("SatelliteChannel = %v, want ErrNoChannel", err)
	}
}

func TestKilohertzOfRoundsRatherThanTruncates(t *testing.T) {
	// Terrestrial channel 13 in hertz, whose 1/7 MHz offset a truncation
	// would turn into a kilohertz of detuning.
	if got := KilohertzOf(473_142_857); got != 473_143 {
		t.Errorf("KilohertzOf(473142857) = %d, want 473143", got)
	}
	// A satellite intermediate frequency is already in kilohertz.
	if got := KilohertzOf(1_049_480); got != 1_049_480 {
		t.Errorf("KilohertzOf(1049480) = %d, want 1049480", got)
	}
}

// The arithmetic here stands in for the PT3 driver's table of 112 frequency
// constants and is checked against every one. The values are in hertz.
func TestPT3TerrestrialCentreMatchesTheDriverTable(t *testing.T) {
	table := []int64{
		0x58d3f49, 0x5e8ccc9, 0x6445a49, 0x69fe7c9, 0x6fb7549,
		0x75702c9, 0x7b29049, 0x80e1dc9, 0x869ab49, 0x8c538c9,
		0x920c649, 0x97c53c9, 0x9f665c9, 0xa51f349, 0xaad80c9,
		0xb090e49, 0xb649bc9, 0xba1a4c9, 0xbfd3249, 0xc58bfc9,
		0xcb44d49, 0xd0fdac9, 0xd6b6849, 0xdc6f5c9, 0xe228349,
		0xe7e10c9, 0xed99e49, 0xf352bc9, 0xf90b949, 0xfec46c9,
		0x1047d449, 0x10a361c9, 0x10feef49, 0x115a7cc9, 0x11b60a49,
		0x121197c9, 0x126d2549, 0x12c8b2c9, 0x13244049, 0x137fcdc9,
		0x13db5b49, 0x1436e8c9, 0x14927649, 0x14ee03c9, 0x15499149,
		0x15a51ec9, 0x1600ac49, 0x165c39c9, 0x16b7c749, 0x171354c9,
		0x176ee249, 0x17ca6fc9, 0x1825fd49, 0x18818ac9, 0x18dd1849,
		0x1938a5c9, 0x19943349, 0x19efc0c9, 0x1a4b4e49, 0x1aa6dbc9,
		0x1b026949, 0x1b5df6c9, 0x1bb98449, 0x1c339649, 0x1c8f23c9,
		0x1ceab149, 0x1d463ec9, 0x1da1cc49, 0x1dfd59c9, 0x1e58e749,
		0x1eb474c9, 0x1f100249, 0x1f6b8fc9, 0x1fc71d49, 0x2022aac9,
		0x207e3849, 0x20d9c5c9, 0x21355349, 0x2190e0c9, 0x21ec6e49,
		0x2247fbc9, 0x22a38949, 0x22ff16c9, 0x235aa449, 0x23b631c9,
		0x2411bf49, 0x246d4cc9, 0x24c8da49, 0x252467c9, 0x257ff549,
		0x25db82c9, 0x26371049, 0x26929dc9, 0x26ee2b49, 0x2749b8c9,
		0x27a54649, 0x2800d3c9, 0x285c6149, 0x28b7eec9, 0x29137c49,
		0x296f09c9, 0x29ca9749, 0x2a2624c9, 0x2a81b249, 0x2add3fc9,
		0x2b38cd49, 0x2b945ac9, 0x2befe849, 0x2c4b75c9, 0x2ca70349,
		0x2d0290c9, 0x2d5e1e49,
	}
	if len(table) != pt3LastChannel-earthsoftFirstChannel+1 {
		t.Fatalf("the table has %d entries and the range covers %d", len(table), pt3LastChannel+1)
	}
	for channel, hertz := range table {
		centre, ok := PT3TerrestrialCentreKHz(channel)
		if !ok {
			t.Fatalf("channel %d is not in range", channel)
		}
		if want := KilohertzOf(uint64(hertz)); centre != want {
			t.Errorf("channel %d: %d kHz, want %d", channel, centre, want)
		}
	}
	// Beyond the table the driver reads off the end of it, so the range is the
	// table's and not the band's: terrestrial channel 62 would be number 112.
	if _, ok := PT3TerrestrialCentreKHz(pt3LastChannel + 1); ok {
		t.Error("channel 112 is past the end of the driver's table")
	}
}

// The two drivers number the channels they both cover the same way, which is
// why one set of channel files works against either.
func TestBothDriversNumberSharedChannelsAlike(t *testing.T) {
	for _, frequency := range []int64{111_143, 167_143, 225_143, 465_143, 473_143, 557_143} {
		px4, offset, err := TerrestrialChannel(frequency)
		if err != nil {
			t.Fatalf("TerrestrialChannel(%d): %v", frequency, err)
		}
		pt3, err := PT3TerrestrialChannel(frequency)
		if err != nil {
			t.Fatalf("PT3TerrestrialChannel(%d): %v", frequency, err)
		}
		if px4 != pt3 || offset != 0 {
			t.Errorf("%d kHz: px4_drv says %d (offset %d), PT3 says %d", frequency, px4, offset, pt3)
		}
	}
}

// The VHF channels px4_drv has no number for are in the PT3 driver's table.
func TestPT3HasItsOwnNumbersForVHF(t *testing.T) {
	for _, one := range []struct {
		frequency int64
		channel   int
	}{
		{93_143, 0},   // VHF 1
		{173_143, 13}, // VHF 4
		{195_143, 17}, // VHF 8
	} {
		channel, err := PT3TerrestrialChannel(one.frequency)
		if err != nil || channel != one.channel {
			t.Errorf("PT3TerrestrialChannel(%d) = %d, %v; want %d", one.frequency, channel, err, one.channel)
		}
	}
}

// The PT1 and PT2 driver names its satellite transponders by opaque tuner
// words rather than by frequency, so the mapping is checked against those
// words. Each is the intermediate frequency in megahertz plus a fixed base,
// which holds for every entry of the driver's table but the first.
func TestPT1SatelliteMatchesTheDriverTable(t *testing.T) {
	// The driver's satellite tuner words, in table order: BS-1..BS-23,
	// ND02..ND24, ND01..ND23. Each value is the 16-bit word of an entry's
	// step 1.
	words := []int{
		0x4829, 0x4440, 0x4466, 0x448d, 0x44b3, 0x44d9,
		0x4500, 0x4526, 0x454c, 0x4573, 0x4599, 0x45bf,
		0x464d, 0x4675, 0x469d, 0x46c5, 0x46ed, 0x4715,
		0x473d, 0x4765, 0x478d, 0x47b5, 0x47dd, 0x4805,
		0x4639, 0x4661, 0x4689, 0x46b1, 0x46d9, 0x4701,
		0x4729, 0x4751, 0x4779, 0x47a1, 0x47c9, 0x47f1,
	}
	const base = 16384
	if len(words) != satelliteBSCount+satelliteCSCount+satelliteOddCSCount {
		t.Fatalf("the table has %d entries and the range covers %d",
			len(words), satelliteBSCount+satelliteCSCount+satelliteOddCSCount)
	}
	for channel, word := range words {
		centre, ok := satelliteCentreKHz(channel)
		if !ok {
			t.Fatalf("channel %d is not in range", channel)
		}
		if channel == 0 {
			// BS-1's word is not in sequence with the rest and its band byte
			// differs from its neighbours' too, so this arithmetic does not
			// apply. What channel number BS-1 is stays settled by recpt1's
			// own table, which the tests below use.
			continue
		}
		// The word can only carry whole megahertz, so a BS transponder whose
		// intermediate frequency is not one -- they are spaced 38.36 MHz
		// apart -- is rounded in it. Agreement to within that rounding is all
		// there is to check.
		megahertz := int64(word - base)
		if away := centre - megahertz*1000; away > 500 || away < -500 {
			t.Errorf("channel %d: %d kHz, but the driver's word says %d MHz",
				channel, centre, megahertz)
		}
	}
}

// The odd-numbered CS transponders are in the PT1 driver's table and in
// neither of the others'.
func TestPT1ReachesTheOddCSTransponders(t *testing.T) {
	// ND01, the first of them.
	channel, err := PT1SatelliteChannel(1_593_000)
	if err != nil || channel != 24 {
		t.Errorf("PT1SatelliteChannel(1593000) = %d, %v; want 24", channel, err)
	}
	// ND23, the last.
	if channel, err := PT1SatelliteChannel(1_593_000 + 11*40_000); err != nil || channel != 35 {
		t.Errorf("PT1SatelliteChannel(ND23) = %d, %v; want 35", channel, err)
	}
	// The other two drivers have no number for it: px4_drv has none at all,
	// and the PT3 driver's tuner table stops before it.
	if _, err := SatelliteChannel(1_593_000); err == nil {
		t.Error("the shared table should have no number for ND01")
	}
	// The transponders they do share are numbered the same.
	for _, frequency := range []int64{1_049_480, 1_613_000, 2_053_000} {
		shared, err := SatelliteChannel(frequency)
		if err != nil {
			t.Fatalf("SatelliteChannel(%d): %v", frequency, err)
		}
		pt1, err := PT1SatelliteChannel(frequency)
		if err != nil || pt1 != shared {
			t.Errorf("%d kHz: shared says %d, PT1 says %d (%v)", frequency, shared, pt1, err)
		}
	}
}

// The PT1 driver computes one channel further than the PT3 driver's table
// reaches, which is what puts terrestrial channel 62 within its range.
func TestPT1ReachesOneTerrestrialChannelFurtherThanPT3(t *testing.T) {
	const channel62 = 767_143
	channel, err := PT1TerrestrialChannel(channel62)
	if err != nil || channel != 112 {
		t.Errorf("PT1TerrestrialChannel(%d) = %d, %v; want 112", channel62, channel, err)
	}
	if _, err := PT3TerrestrialChannel(channel62); err == nil {
		t.Error("the PT3 driver's table stops before terrestrial channel 62")
	}
	// Everything below that is numbered alike.
	for _, frequency := range []int64{93_143, 111_143, 173_143, 225_143, 473_143, 761_143} {
		pt3, err := PT3TerrestrialChannel(frequency)
		if err != nil {
			t.Fatalf("PT3TerrestrialChannel(%d): %v", frequency, err)
		}
		pt1, err := PT1TerrestrialChannel(frequency)
		if err != nil || pt1 != pt3 {
			t.Errorf("%d kHz: PT3 says %d, PT1 says %d (%v)", frequency, pt3, pt1, err)
		}
	}
}
