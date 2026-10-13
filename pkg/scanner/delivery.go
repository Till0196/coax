// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"strconv"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/plan"
)

// Turning a network's own account of a multiplex into a channel to tune.
//
// A network table describes every multiplex of its network, each with a
// delivery system descriptor saying where it is and how it is modulated. That
// is the general way to find channels; the band plans this program walks are
// the special case, and work because Japan publishes its frequencies and nobody
// moves them.
//
// Nothing here tunes or reads anything: it is the arithmetic between a
// descriptor and a set of channel-file properties, kept apart so that both
// readings of the satellite descriptor can be tested against fixed bytes.

// Convention is which standard's rules a network table is written by.
//
// It has to be carried rather than worked out. ARIB STD-B10 and DVB EN 300 468
// divide one byte of the satellite delivery system descriptor differently, and
// no bit says which division was meant. What settles it is the multiplex the
// table was read out of, and that is known because this program tuned it.
type Convention int

const (
	// ARIB is how the Japanese networks write their tables.
	ARIB Convention = iota
	// DVB is how everybody else does.
	DVB
)

// ConventionFor says which rules a network table read out of a multiplex of
// this delivery system follows. A network table describes its own network, so
// the multiplex it came off and the ones it describes are of a piece.
//
// Asked of satellite delivery systems and nothing else, because the satellite
// descriptor is the only one with two readings: a cable descriptor gives its
// modulation a byte of its own.
func ConventionFor(deliverySystem string) Convention {
	switch channelconfig.NormalizeDeliverySystem(deliverySystem) {
	case "ISDBS", "ISDBS3":
		return ARIB
	}
	return DVB
}

// Dish is what stands between a downlink frequency and the tuner: the block on
// the dish, and the switch position the dish is on where there is one.
//
// A satellite descriptor gives the downlink frequency, which no tuner takes,
// and only the person who installed the dish knows which oscillator to
// subtract. The block's name is passed on to the daemon rather than acted on
// here. See tuning.ApplyLNBf.
type Dish struct {
	// Block is the low-noise block, as `--lnbf` names it. Empty means the
	// Japanese arrangement, whose two oscillators are picked by the hand of
	// the circular polarisation rather than by a name.
	Block string
	// Position is the DiSEqC port, written where the dish reaches the tuner
	// through a switch. Empty sends nothing, which is what a dish with one
	// block wants.
	Position string
}

// SatelliteChannel turns one multiplex of a satellite network into the
// properties a tune takes, and reports whether it could be reached at all.
//
// The two conventions part company over what the descriptor's modulation field
// holds, and over how the downlink frequency becomes something a tuner takes:
// Japan's two oscillators are converted here, while everywhere else a named
// block does it in the daemon.
func SatelliteChannel(described *isdb.SatelliteDelivery, deliverySystem string, dish Dish) (map[string]string, bool) {
	if described == nil {
		return nil, false
	}
	if ConventionFor(deliverySystem) == ARIB {
		return aribSatelliteChannel(described, deliverySystem)
	}
	return dvbSatelliteChannel(described, dish)
}

// aribSatelliteChannel is the Japanese arrangement: one dish, one block, and
// two oscillators picked by the hand of the circular polarisation. The
// frequency is converted here because there is no block to name -- every
// receiver in the country has the same one.
//
// The modulation field is left where it is: ARIB numbers it its own way and the
// Japanese demodulators are told the standard rather than the modulation.
func aribSatelliteChannel(described *isdb.SatelliteDelivery, deliverySystem string) (map[string]string, bool) {
	intermediate, reachable := plan.IntermediateFrequencyKHz(int(described.FrequencyKHz), described.Polarisation == "L")
	if !reachable {
		return nil, false
	}
	return map[string]string{
		"DELIVERY_SYSTEM": deliverySystem,
		"FREQUENCY":       strconv.Itoa(intermediate),
	}, true
}

// dvbSatelliteChannel is everybody else's: the downlink frequency as the
// descriptor gave it, with the block on the dish named beside it so that the
// daemon subtracts the right oscillator and puts the tone on the lead.
//
// The delivery system comes from the descriptor rather than from what was
// tuned. One network carries both generations on neighbouring transponders, so
// a DVB-S multiplex found from a DVB-S2 one has to be tuned as DVB-S.
func dvbSatelliteChannel(described *isdb.SatelliteDelivery, dish Dish) (map[string]string, bool) {
	if described.FrequencyKHz == 0 {
		return nil, false
	}
	system := "DVBS"
	if described.DVBSecondGeneration() {
		system = "DVBS2"
	}
	channel := map[string]string{
		"DELIVERY_SYSTEM": system,
		// The downlink, which is what the descriptor holds and what a channel
		// file naming a block writes. The daemon converts it.
		"FREQUENCY": strconv.FormatUint(uint64(described.FrequencyKHz), 10),
	}
	if dish.Block != "" {
		channel["LNB"] = dish.Block
	}
	if dish.Position != "" {
		channel["SAT_NUMBER"] = dish.Position
	}
	// H, V, L and R are what a channel file writes and what the daemon reads,
	// so the descriptor's own letter goes through as it stands.
	if described.Polarisation != "" {
		channel["POLARIZATION"] = described.Polarisation
	}
	if described.SymbolRate != 0 {
		channel["SYMBOL_RATE"] = strconv.FormatUint(uint64(described.SymbolRate), 10)
	}
	// Each of these is left out where the descriptor declined to say, rather
	// than written as a guess: a demodulator told nothing works it out, and
	// one told the wrong thing does not.
	if modulation := described.DVBModulation(); modulation != "" {
		channel["MODULATION"] = modulation
	}
	if rollOff := described.DVBRollOff(); rollOff != "" {
		channel["ROLLOFF"] = rollOff
	}
	if fec := described.InnerFEC(); fec != "" {
		channel["INNER_FEC"] = fec
	}
	return channel, true
}

// CableChannel turns one multiplex of a cable network into the properties a
// tune takes. There is one reading here rather than two: the cable delivery
// system descriptor gives its modulation a byte of its own. The delivery system
// is what was tuned, because the descriptor does not carry one.
func CableChannel(described *isdb.CableDelivery, deliverySystem string) (map[string]string, bool) {
	if described == nil || described.FrequencyHz == 0 {
		return nil, false
	}
	channel := map[string]string{
		"DELIVERY_SYSTEM": deliverySystem,
		// Hertz, which is what a cable channel file writes: there is no
		// oscillator between the carrier and the receiver.
		"FREQUENCY": strconv.FormatUint(uint64(described.FrequencyHz), 10),
	}
	if described.SymbolRate != 0 {
		channel["SYMBOL_RATE"] = strconv.FormatUint(uint64(described.SymbolRate), 10)
	}
	if modulation := described.CableModulation(); modulation != "" {
		channel["MODULATION"] = modulation
	}
	if fec := described.InnerFEC(); fec != "" {
		channel["INNER_FEC"] = fec
	}
	return channel, true
}
