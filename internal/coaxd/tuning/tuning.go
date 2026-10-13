// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package tuning parses the channel-file properties that reach a board driver
// and normalises delivery-system names, so every backend spells DVB-C/ANNEX_A,
// ISDB-T and friends the same way.
package tuning

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Normalize folds a delivery system name to upper case with separators removed,
// so "DVB-C/ANNEX_A" becomes "DVBC/ANNEXA".
func Normalize(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '-' || character == '_' || character == ' ' {
			return -1
		}
		return character
	}, strings.ToUpper(value))
}

// NormalizeModulation folds a MODULATION value to its comparable form, dropping
// the slash a delivery system name keeps: a channel file writes "QAM/256" while
// drivers and datasheets write "QAM256".
func NormalizeModulation(value string) string {
	return strings.ReplaceAll(Normalize(value), "/", "")
}

// Number reads a numeric property. Missing optional keys yield zero; missing
// required keys are an error.
func Number(properties map[string]string, key string, required bool) (uint64, error) {
	value := strings.Trim(strings.TrimSpace(properties[key]), "\"'")
	if value == "" {
		if required {
			return 0, fmt.Errorf("%s is required", key)
		}
		return 0, nil
	}
	parsed, err := ParseNumber(value, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, value, err)
	}
	return parsed, nil
}

// ParseNumber accepts decimal and 0x-prefixed hexadecimal property values.
func ParseNumber(value string, bits int) (uint64, error) {
	base := 10
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		base = 0
	}
	return strconv.ParseUint(value, base, bits)
}

// StreamID reads the STREAM_ID an ISDB-S channel picks its stream out of the
// carrier with, and checks it fits the 16-bit identifier. Absent means relative
// slot zero, not "let the demodulator choose" as PLP has it: a TC90522 keeps
// whichever stream it was last set to and a CXD2856 has no choice of its own,
// so leaving it unset would hand back whatever the previous tune picked. Slot
// zero is also what px4_drv takes when the channel does not say.
func StreamID(properties map[string]string) (uint16, error) {
	value, err := Number(properties, "STREAM_ID", false)
	if err != nil {
		return 0, err
	}
	if value > 0xffff {
		return 0, fmt.Errorf("STREAM_ID %d is outside 0..65535", value)
	}
	return uint16(value), nil
}

// FrequencyKHz folds a channel file's FREQUENCY down to kHz. Files carry Hz for
// terrestrial and kHz for satellite, and tuners want kHz; ten million or more
// can only be Hz, and anything smaller is taken as kHz already.
func FrequencyKHz(frequency uint64) uint64 {
	if frequency >= 10_000_000 {
		frequency /= 1000
	}
	return frequency
}

// PLP reads the STREAM_ID property naming the DVB-T2 physical layer pipe, or
// the DVB-S2 input stream, to filter on. Absence means -1: let the demodulator
// choose, which is not the same as pipe zero.
//
// The identifier is eight bits and a wider value is masked down rather than
// refused: files in the wild pack the scrambling sequence into the bits above,
// as do the drivers this has to agree with. PackedScrambling reads it back out.
func PLP(properties map[string]string) (int, error) {
	value := strings.Trim(strings.TrimSpace(properties["STREAM_ID"]), "\"'")
	if value == "" {
		return -1, nil
	}
	parsed, err := ParseNumber(value, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid STREAM_ID %q: %w", value, err)
	}
	return int(parsed & 0xff), nil
}

// ScramblingProperty is the DVB-S2 physical layer scrambling sequence index,
// which is eighteen bits and separate from the stream identifier.
const ScramblingProperty = "SCRAMBLING_SEQUENCE_INDEX"

// ErrNoScrambling reports that a board cannot be told a scrambling sequence. A
// channel that named one is refused rather than tuned at the default sequence,
// which would lock to nothing without saying why.
var ErrNoScrambling = errors.New("this tuner cannot be given a scrambling sequence index")

// Scrambling reads the sequence a channel asks for, from its own property or
// from the bits above the stream identifier; zero is the default. The packed
// form the DVB-S2 multistream files carry is the identifier in the low eight
// bits, the sequence in the eighteen above it, and a mode in the two above
// that.
func Scrambling(properties map[string]string) (uint32, error) {
	if index, packed, err := PackedScrambling(properties); err != nil {
		return 0, err
	} else if packed {
		return index, nil
	}
	value := strings.Trim(strings.TrimSpace(properties[ScramblingProperty]), "\"'")
	if value == "" {
		return 0, nil
	}
	parsed, err := ParseNumber(value, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", ScramblingProperty, value, err)
	}
	if parsed > 0x3ffff {
		return 0, fmt.Errorf("%s %d is outside 0..262143", ScramblingProperty, parsed)
	}
	return uint32(parsed), nil
}

// rollOffPercent is how much wider than its symbol rate a channel of each
// delivery system occupies. Cable and satellite files carry a symbol rate
// rather than a bandwidth; these are the Linux DVB core's own factors.
var rollOffPercent = map[string]uint64{
	"DVBC":        115, // a bare DVB-C spelling means Annex A
	"DVBC/ANNEXA": 115,
	"DVBC/ANNEXC": 113,
	"DSS":         120,
	"DVBS":        135,
	"TURBO":       135,
	"ISDBS":       135,
}

// j83AnnexACDeliverySystems are the spellings that name ITU-T J.83 Annex A or
// Annex C. The two differ only in roll-off (15% against 13%), and ISDB-C is
// Annex C, so one demodulator mode serves all of them. Annex B is deliberately
// absent: its trellis coding differs.
var j83AnnexACDeliverySystems = map[string]bool{
	"DVBC":        true, // a bare DVB-C spelling means Annex A
	"DVBC/ANNEXA": true,
	"DVBC/ANNEXC": true,
	"ANNEXC":      true,
	"J.83C":       true,
	"J83C":        true,
	"ISDBC":       true,
}

// IsJ83AnnexAC reports whether a delivery system names J.83 Annex A or Annex C:
// the ones whose channel files carry a symbol rate rather than a bandwidth, and
// the ones a single DVB-C demodulator mode can lock.
func IsJ83AnnexAC(deliverySystem string) bool {
	return j83AnnexACDeliverySystems[Normalize(deliverySystem)]
}

// DemodDeliverySystem is the spelling to switch a demodulator's own table on:
// Normalize, with every name for J.83 Annex A or Annex C folded to the one
// spelling those tables are written under.
//
// The annexes differ only in roll-off, which is the tuner's business, so one
// mode locks all of them -- but a channel asked for as ISDB-C would otherwise
// reach a table listing DVB-C and be refused on hardware that receives it. A
// backend whose driver really does tell the annexes apart switches on Normalize
// and is not affected.
func DemodDeliverySystem(deliverySystem string) string {
	if IsJ83AnnexAC(deliverySystem) {
		return "DVBC/ANNEXA"
	}
	return Normalize(deliverySystem)
}

// IsJ83 reports whether a delivery system names any annex of ITU-T J.83. The
// annexes differ in coding but are one cable band to a tuner's channel filter.
func IsJ83(deliverySystem string) bool {
	return IsJ83AnnexAC(deliverySystem) || Normalize(deliverySystem) == "DVBC/ANNEXB"
}

// fixedBandwidthHz are the systems whose channels are always the same width.
var fixedBandwidthHz = map[string]uint64{
	"ATSC":        6_000_000,
	"DVBC/ANNEXB": 6_000_000,
	"ISDBC":       6_000_000,
}

// dvbS2RollOffPercent maps the ROLLOFF property of a DVB-S2 channel.
var dvbS2RollOffPercent = map[string]uint64{"20": 120, "25": 125, "35": 135}

// BandwidthHz reports the channel bandwidth to program. It prefers an explicit
// BANDWIDTH_HZ, which terrestrial files carry; cable and satellite files do not,
// so it is derived from the symbol rate the way the DVB core does.
func BandwidthHz(properties map[string]string) (uint64, error) {
	explicit, err := Number(properties, "BANDWIDTH_HZ", false)
	if err != nil {
		return 0, err
	}
	if explicit != 0 {
		return explicit, nil
	}
	delivery := Normalize(properties["DELIVERY_SYSTEM"])
	if fixed, ok := fixedBandwidthHz[delivery]; ok {
		return fixed, nil
	}
	rollOff, ok := rollOffPercent[delivery]
	if delivery == "DVBS2" {
		rollOff, ok = dvbS2RollOffPercent[strings.Trim(strings.TrimSpace(properties["ROLLOFF"]), "\"'")]
		if !ok {
			// The default the DVB core assumes when a file says nothing.
			rollOff, ok = 135, true
		}
	}
	if !ok {
		return 0, fmt.Errorf("BANDWIDTH_HZ is required for %s", properties["DELIVERY_SYSTEM"])
	}
	symbolRate, err := Number(properties, "SYMBOL_RATE", false)
	if err != nil {
		return 0, err
	}
	if symbolRate == 0 {
		return 0, fmt.Errorf("either BANDWIDTH_HZ or SYMBOL_RATE is required for %s", properties["DELIVERY_SYSTEM"])
	}
	return symbolRate * rollOff / 100, nil
}

// equivalentDeliveries are the spellings that name one and the same system: a
// MyGica T230C2 says "DVB-C", meaning Annex A as a bare DVB-C always does, while
// the channel file says "DVBC/ANNEX_A". Both are right.
var equivalentDeliveries = [][]string{
	{"DVB-C", "DVB-C/ANNEX_A"},
	{"ISDB-C", "DVB-C/ANNEX_C"},
}

// ExpandDeliveries adds every other spelling of the systems in a list, so "does
// this tuner receive X" answers the same whichever spelling the asker holds.
// The original order is kept.
func ExpandDeliveries(names []string) []string {
	expanded := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	add := func(name string) {
		if key := Normalize(name); !seen[key] {
			seen[key] = true
			expanded = append(expanded, name)
		}
	}
	for _, name := range names {
		add(name)
	}
	for _, name := range names {
		for _, group := range equivalentDeliveries {
			if !containsDelivery(group, name) {
				continue
			}
			for _, other := range group {
				add(other)
			}
		}
	}
	return expanded
}

// SameDelivery reports whether two names mean the same delivery system.
func SameDelivery(one, other string) bool {
	if Normalize(one) == Normalize(other) {
		return true
	}
	for _, group := range equivalentDeliveries {
		if containsDelivery(group, one) && containsDelivery(group, other) {
			return true
		}
	}
	return false
}

func containsDelivery(group []string, name string) bool {
	for _, member := range group {
		if Normalize(member) == Normalize(name) {
			return true
		}
	}
	return false
}

// The two multiplex formats anything here can come out as.
const (
	FormatTS  = "ts"
	FormatTLV = "tlv"
)

// StreamFormat says which multiplex a delivery system's tuner is likely to
// produce. Everything here is an MPEG-2 transport stream except ISDB-S3, whose
// multiplex is MMT over TLV: variable length packets beginning with 0x7f rather
// than fixed ones with 0x47.
//
// Only a hint, because the Linux DVB API has no delivery system number for
// ISDB-S3: an ISDB-S3 channel is written as ISDBS with a TLV stream identifier in
// STREAM_ID.
func StreamFormat(deliverySystem string) string {
	if Normalize(deliverySystem) == "ISDBS3" {
		return FormatTLV
	}
	return FormatTS
}

// PackedScrambling reads a scrambling sequence out of the bits above a
// STREAM_ID and says whether there was one. Bits 26 and 27 are the mode: zero
// means the eighteen bits below are the sequence itself, anything else that
// they index the gold codes.
func PackedScrambling(properties map[string]string) (uint32, bool, error) {
	value := strings.Trim(strings.TrimSpace(properties["STREAM_ID"]), "\"'")
	if value == "" {
		return 0, false, nil
	}
	parsed, err := ParseNumber(value, 32)
	if err != nil {
		return 0, false, fmt.Errorf("invalid STREAM_ID %q: %w", value, err)
	}
	sequence := uint32(parsed>>8) & 0x3ffff
	if sequence == 0 {
		return 0, false, nil
	}
	return sequence, true, nil
}
