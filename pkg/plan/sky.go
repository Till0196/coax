// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package plan

import (
	"fmt"
	"strings"
)

// The 124/128 degree east service, on its two neighbouring slots: JCSAT-3A at 128 degrees
// east and JCSAT-4B at 124.
//
// The numbers are the published frequency table's, checked against every one of the sixty
// transponders it lists:
//
//	JCSAT-3A  JD1..JD16    12508 MHz + 15 MHz apiece,    27 MHz wide
//	JCSAT-3A  JD17..JD28   12268 MHz + 20 MHz apiece,    36 MHz wide
//	JCSAT-4B  K1..K16      12268.25 MHz + 15 MHz apiece, 27 MHz wide
//	JCSAT-4B  JD1..JD16    12508.25 MHz + 15 MHz apiece, 27 MHz wide
//
// The quarter megahertz is JCSAT-4B's own offset from its neighbour, which is what keeps
// the two apart in a plan that numbers them alike. They share one lead, dish and
// oscillator and are told apart only by the 22 kHz tone, so a carrier here names its
// satellite: the name selects the block and the block puts the tone on.
//
// The frequencies are downlinks, not intermediate frequencies: the published table gives
// two IFs for every carrier, 522 MHz apart. Naming the block and letting it do the
// arithmetic is the only way this table serves both.
var skyBands = []SkyBand{
	{"JCSAT3A", "JD", 1, 16, 12_508_000, 15_000, 27, true},
	{"JCSAT3A", "JD", 17, 12, 12_268_000, 20_000, 36, true},
	{"JCSAT4B", "K", 1, 16, 12_268_250, 15_000, 27, false},
	{"JCSAT4B", "JD", 1, 16, 12_508_250, 15_000, 27, true},
}

type SkyBand struct {
	// Satellite is the block on the dish that looks at it: the name a tuning
	// knows it by, and what a Mirakurun channel list carries in
	// commandVars.satellite.
	Satellite string
	// Series is the letter the transponders of this run are numbered under. The
	// two satellites number theirs alike, hence the satellite in every name.
	Series      string
	FirstNumber int
	Count       int
	// DownlinkKHz is the centre of the first transponder at the dish.
	DownlinkKHz int
	SpacingKHz  int
	// BandwidthMHz is not one number for the service: JCSAT-3A carries twelve
	// 36 MHz transponders beside its sixteen 27 MHz ones.
	BandwidthMHz int
	// Numbered says whether a channel definition file has a physical channel
	// number for this run. The K series has none: the numbering continues past
	// where BonDriver_PX4's stops, and adding the K carriers would shift every
	// number after them. They are still scanned.
	Numbered bool
}

type SkyTransponder struct {
	// Name is what the published table calls it, with the satellite in front
	// because the two number theirs the same way: JCSAT3A/JD5, JCSAT4B/K12.
	Name         string
	Satellite    string
	DownlinkKHz  int
	BandwidthMHz int
	// Vertical is the polarisation, which the table gives by parity: odd
	// vertical, even horizontal, on both satellites and in both series.
	Vertical bool
	// PTXChannel is the physical channel number a channel definition file writes
	// for it, and whether it has one. See SkyBand.Numbered.
	PTXChannel    int
	HasPTXChannel bool
}

func (transponder SkyTransponder) Polarization() string {
	if transponder.Vertical {
		return "VERTICAL"
	}
	return "HORIZONTAL"
}

// skyProfiles are the ways a carrier of this plan is modulated: the grid
// carries two generations. The service moved to DVB-S2 with 8PSK at 23.3037
// Msym/s while a handful still run DVB-S with QPSK at 21.096 Msym/s on the same
// frequencies, so a scan assuming one would report the other's carriers as
// empty.
//
// The symbol rate is the one thing a demodulator cannot work out for itself;
// modulation and inner code are announced in a DVB-S2 frame header, so none is
// written.
var skyProfiles = []skyProfile{
	{"", "PSK/8", 23_303_000},
	{"QPSK", "QPSK", 21_096_000},
}

type skyProfile struct {
	suffix     string
	modulation string
	symbolRate int
}

// SkyChannel is a transponder of the plan in one of the plan's modulations. It
// is what a scan tunes and what a channel names.
type SkyChannel struct {
	SkyTransponder
	// Modulation and SymbolRate are the profile's. See skyProfiles.
	Modulation string
	SymbolRate int
	// DeliverySystem: the older carriers of this grid are DVB-S rather than
	// DVB-S2, and asking for S2 on one of them is a lock that does not come.
	DeliverySystem string
}

var (
	skyTransponders = buildSkyPlan()
	skyChannels     = buildSkyChannels()
)

// SkyChannels is every transponder in every modulation, for a scan to walk.
func SkyChannels() []SkyChannel { return skyChannels }

func buildSkyPlan() []SkyTransponder {
	var list []SkyTransponder
	ptx := 0
	for _, band := range skyBands {
		for index := range band.Count {
			number := band.FirstNumber + index
			transponder := SkyTransponder{
				Name:          fmt.Sprintf("%s/%s%d", band.Satellite, band.Series, number),
				Satellite:     band.Satellite,
				DownlinkKHz:   band.DownlinkKHz + index*band.SpacingKHz,
				BandwidthMHz:  band.BandwidthMHz,
				Vertical:      number%2 == 1,
				HasPTXChannel: band.Numbered,
			}
			if band.Numbered {
				transponder.PTXChannel = ptx
				ptx++
			}
			list = append(list, transponder)
		}
	}
	return list
}

func buildSkyChannels() []SkyChannel {
	channels := make([]SkyChannel, 0, len(skyTransponders)*len(skyProfiles))
	for _, transponder := range skyTransponders {
		for _, profile := range skyProfiles {
			named := transponder
			if profile.suffix != "" {
				named.Name += "/" + profile.suffix
			}
			system := "DVBS2"
			if profile.modulation == "QPSK" {
				system = "DVBS"
			}
			channels = append(channels, SkyChannel{
				SkyTransponder: named,
				Modulation:     profile.modulation,
				SymbolRate:     profile.symbolRate,
				DeliverySystem: system,
			})
		}
	}
	return channels
}

// SkyCarrier reads a name of the plan -- JCSAT3A/JD5, JCSAT4B/K12,
// JCSAT3A/JD9/QPSK. A name with no modulation after it is the service as
// broadcast today; the older one is reached by saying so, which is also the
// name a scan of it reports.
func SkyCarrier(name string) (SkyChannel, bool) {
	wanted := strings.ToUpper(strings.TrimSpace(name))
	for _, one := range skyChannels {
		if strings.ToUpper(one.Name) == wanted {
			return one, true
		}
	}
	return SkyChannel{}, false
}

// IsSkyName reports whether a name is plainly meant for this plan, whether or
// not the plan holds it, so a mistyped transponder is answered against this
// table rather than a satellite one it was never meant for.
func IsSkyName(name string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(name)), "JCSAT")
}

func SkyCarrierNames() string {
	return "JCSAT3A/JD1 to JD28, JCSAT4B/K1 to K16 and JCSAT4B/JD1 to JD16, each with /QPSK after it for the older carriers"
}

// SkyOscillatorKHz is the oscillator of the converter this service is received
// with -- not the 10678 MHz one BS and CS-110 use, which is the number that
// comes to mind first in a Japanese installation and a carrier that will not
// lock. It is the same for both satellites; only the tone differs. Nothing here
// subtracts it.
const SkyOscillatorKHz = 11_200_000

// SkyTransponderAt names the transponder at a downlink frequency in kilohertz --
// a downlink, because that is what a channel of this plan carries; see skyBands.
// Transponders the numbering does not cover are not answered for.
func SkyTransponderAt(downlinkKHz int) (SkyTransponder, bool) {
	for _, transponder := range skyTransponders {
		if !transponder.HasPTXChannel {
			continue
		}
		if transponder.DownlinkKHz == downlinkKHz {
			return transponder, true
		}
	}
	return SkyTransponder{}, false
}
