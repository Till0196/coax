// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plan

import (
	"fmt"
	"strings"
)

// televisionBands are the Japanese television channel plan, one entry per stretch of
// spectrum it is laid out in.
//
// It is one plan, not a cable one and a terrestrial one: VHF and UHF go through the air,
// the mid band and the super high band between them only over a cable, and a cable carries
// all of it on the same numbers -- channel 13 over the air and channel 13 on the cable are
// the same channel, a seventh of a megahertz apart.
//
// Every channel is 6 MHz wide and every stretch is packed from its low edge, so a band is
// a first channel, a first frequency and a count. What looks irregular in one flat list is
// only the joins between bands:
//
//   - The mid band runs 108 to 170 MHz, two megahertz more than its ten channels need.
//     C13 to C21 are packed from the bottom and C22 put against the top edge, so the spare
//     falls between them.
//   - VHF is two stretches, 170 to 194 and 192 to 222 MHz, overlapping by two megahertz.
//     That is the terrestrial allocation's own doing, and why 7 and 8 are four megahertz
//     apart rather than six.
//   - The super high band ends at 468 and UHF begins at 470, so the spare falls above C63.
//
// The order is the plan's own, low to high, and it is load-bearing: the physical channel
// numbers the drivers use are positions in this list. See PTXChannel.
var televisionBands = []televisionBand{
	{"VHF low", "", 1, 3, 93_000_000},
	{"mid band", "C", 13, 21, 111_000_000},
	{"mid band, against the top edge", "C", 22, 22, 167_000_000},
	{"VHF high, lower half", "", 4, 7, 173_000_000},
	{"VHF high, upper half", "", 8, 12, 195_000_000},
	{"super high band", "C", 23, 63, 225_000_000},
	{"UHF", "", 13, 62, 473_000_000},
}

// televisionBand is a run of channels evenly spaced from a first frequency,
// named with that band's prefix: C for the stretches only a cable has, nothing
// for the ones carrying the terrestrial numbering.
type televisionBand struct {
	name    string
	prefix  string
	first   int
	last    int
	firstHz int
}

// channelSpacingHz is how wide a channel is here, in every band of the plan.
const channelSpacingHz = 6_000_000

// The offsets the two systems put their carriers at inside the same channel. A
// terrestrial carrier sits a seventh of a megahertz above a cable one --
// 473142857 against 473000000 -- for every channel of the plan. Mixing the two
// up is a lock that never comes.
const (
	cableOffsetHz       = 0
	terrestrialOffsetHz = 142857
)

var (
	cableCarriers       = buildTelevisionPlan(cableOffsetHz)
	terrestrialCarriers = buildTelevisionPlan(terrestrialOffsetHz)
	// ptxChannels is where each channel sits in the plan, which is the
	// physical channel number the drivers know it by. See PTXChannel.
	ptxChannels = buildPTXChannels()
)

func Terrestrial() []Carrier { return terrestrialCarriers }

// TerrestrialWalk is the same carriers in the order worth trying them: UHF
// first, then the bands only a cable has.
//
// Terrestrial television in Japan is all in UHF. The rest of the plan is here
// because a cable may carry the same broadcasts moved down into it -- but
// walking it first means sixty carriers of nothing before the first one that
// could answer.
//
// The plan's own order is left alone: the drivers' physical channel numbers are
// positions in it. See PTXChannel.
func TerrestrialWalk() []Carrier {
	walk := make([]Carrier, 0, len(terrestrialCarriers))
	for _, one := range terrestrialCarriers {
		if one.Frequency >= uhfStartsHz {
			walk = append(walk, one)
		}
	}
	for _, one := range terrestrialCarriers {
		if one.Frequency < uhfStartsHz {
			walk = append(walk, one)
		}
	}
	return walk
}

// uhfStartsHz is the bottom of UHF in this plan, which is where the terrestrial
// broadcasts are.
const uhfStartsHz = 470_000_000

func Cable() []Carrier { return cableCarriers }

func buildTelevisionPlan(offsetHz int) []Carrier {
	var list []Carrier
	for _, band := range televisionBands {
		list = append(list, band.carriers(offsetHz)...)
	}
	return list
}

func (b televisionBand) carriers(offsetHz int) []Carrier {
	list := make([]Carrier, 0, b.last-b.first+1)
	for number := b.first; number <= b.last; number++ {
		list = append(list, Carrier{
			Name:      fmt.Sprintf("%s%d", b.prefix, number),
			Frequency: b.firstHz + (number-b.first)*channelSpacingHz + offsetHz,
		})
	}
	return list
}

// TerrestrialHz is the frequency an ISDB-T carrier sits at on a named channel
// of the plan: "27" over the air, "C23" on a cable.
func TerrestrialHz(name string) (int, bool) {
	return frequencyOf(terrestrialCarriers, name)
}

func frequencyOf(carriers []Carrier, name string) (int, bool) {
	wanted := strings.ToUpper(strings.TrimSpace(name))
	for _, one := range carriers {
		if one.Name == wanted {
			return one.Frequency, true
		}
	}
	return 0, false
}

func TelevisionChannelNames() string {
	return "1 to 12 and 13 to 62 over the air, and C13 to C63 on a cable"
}

// The physical channel numbers are the ones BonDriver_PX4 turns back into frequencies, so a
// file written here can be read by a driver already installed:
//
//	freq kHz = 93143 + ptx * 6000, and 2000 more for ptx 12..16 and 63..112
//
//	ptx        channel          first kHz
//	  0 -   2  VHF 1-3ch          93143
//	  3 -  11  CATV C13-C21ch    111143
//	 12 -  16  CATV C22ch,       167143
//	           VHF 4-7ch
//	 17 -  21  VHF 8-12ch        195143
//	 22 -  62  CATV C23-C63ch    225143
//	 63 - 112  UHF 13-62ch       473143
//
// The number is where the channel falls in the plan, counted from the bottom, and the two
// megahertz appearing twice in that arithmetic is the plan's own. The numbering runs
// through without a gap, so the position is the number. The same arithmetic holds for the
// cable band 143 kHz below each of those, which is why one table serves both.
//
// Older drivers refuse the VHF numbers -- 0 to 2 and 13 to 21 -- for want of that
// correction rather than of a tuner that can reach the band. A file naming them is written
// anyway: it is the numbering the Earthsoft drivers and recpt1 have always used.

// PTXChannel is a named channel's position in the plan, counted from the
// bottom.
func PTXChannel(name string) (int, bool) {
	channel, ok := ptxChannels[strings.ToUpper(strings.TrimSpace(name))]
	return channel, ok
}

func buildPTXChannels() map[string]int {
	// Either plan does: the two carry the same channels at the same positions
	// and differ only in where inside a channel the carrier sits.
	numbers := make(map[string]int, len(terrestrialCarriers))
	for index, carrier := range terrestrialCarriers {
		numbers[carrier.Name] = index
	}
	return numbers
}
