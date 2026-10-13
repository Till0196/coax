// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package tbs is what this host knows about TBS's private interface: the property set their
// driver puts on a tuner's input pin, and what the values in it mean on each of their cards.
//
// The GUID and identifier are the same on every card that has the property; its width, how
// much belongs to which tuner, and whether the card carries it at all differ by model.
// Cards live in the models_* files, grouped by behaviour:
//
//	models_cardwide_windows.go   one mode for the whole card
//	models_pertuner_windows.go   a mode per tuner, written without disturbing the others
//	models_nomode_windows.go     no mode property at all, so none is read
//	models_isdb_windows.go       the 6812, whose values are its own vocabulary
//
// The 6205SE and the 6812 are the two checked against the hardware here.
package tbs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// DemodMode is the standard a card is wired for, as named next door.
type DemodMode = vendors.DemodMode

// Tuner is a card's tuner, as named next door.
type Tuner = vendors.Tuner

// The mode values every card of this vendor takes: the same seven, in the same
// order, from the 6205SE to the 6508. The 6812 is the exception and has its own.
const (
	ModeUnknown = vendors.ModeUnknown
	ModeDVBT    = vendors.ModeDVBT
	ModeDVBS    = vendors.ModeDVBS
	ModeATSC    = vendors.ModeATSC
	ModeDVBC    = vendors.ModeDVBC
	ModeISDBT   = vendors.ModeISDBT
	ModeQAMB    = vendors.ModeQAMB
	ModeDVBC2   = vendors.ModeDVBC2
)

// Model is what one card model's demodulator-mode property means. A card is
// recognised before the property is touched: a wrong guess writes a wrong
// standard.
type Model struct {
	// Name is the card, for messages.
	Name string
	// Width is how many mode bytes a write carries, not always how many the card
	// has: a 6209 takes two and only the first means anything.
	Width int
	// Reply is how large a buffer a read must offer, and is not Width: a 6508
	// answers 72 bytes while filling only the 8 that mean anything.
	Reply int
	// ModeFor maps a delivery system to the value this card wants.
	ModeFor func(system string) (DemodMode, bool)
	// NameFor says what a value means on this card.
	NameFor func(DemodMode) string
	// SlotFor picks the mode byte a tuner owns out of the property. A card
	// with one mode for the whole of it answers zero for every tuner.
	SlotFor func(tuner Tuner) int
	// NoMode says this card's driver does not carry the mode property at all, so
	// it is never asked: the failed read would look like a card that cannot be
	// opened.
	NoMode bool
}

// models is every card known to carry this property, keyed by the model its PCI
// subsystem names it with. The model is the subsystem vendor (a 6281SE is
// subsystem vendor 6281), and a Windows moniker spells it SUBSYS_ssssvvvv --
// subsystem device first -- so a 6812 appears as subsys_00016812. The key is
// text, not a number: the field is hexadecimal but the models are written in it
// as if decimal.
var models = map[string]*Model{
	"6205": &Model6205SE,
	"6209": &Model6209,
	"6281": &Model6281SE,
	"6504": &Model6504H,
	"6508": &Model6508,
	"6522": &Model6522H,
	"6590": &Model6590SE,
	"6216": &Model6216,
	"7230": &Model7230,
	"6902": &Model6902,
	"6903": &Model6903,
	"6904": &Model6904,
	"6905": &Model6905,
	"6908": &Model6908,
	"6909": &Model6909,
	"6910": &Model6910,
	"6812": &Model6812,
}

// ModelDefault is what an unlisted card of this vendor gets: the 6205SE's
// layout, the narrowest one (a two-byte read, a one-byte write). A card with
// more modes keeps its others untouched, which is the safe way to be wrong.
var ModelDefault = Model{
	Name: "TBS", Width: 1, Reply: 2,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: cardWide,
}

// Vendor is TBS Technologies' PCI vendor identifier, as a moniker spells it.
const Vendor = "ven_544d"

// ModelFor recognises the card a tuner is on, and says whether it has this
// property at all.
//
// Recognising rather than assuming is load-bearing: a Digital Devices card
// answers a read of this identifier on this GUID with a number of its own,
// which then compares unequal to the wanted mode and gets written back. An
// unlisted card of this vendor gets the narrowest layout rather than a refusal:
// one card-wide mode, written one byte at a time.
func ModelFor(tuner Tuner) (Model, bool) {
	path := strings.ToLower(tuner.Path)
	name := strings.ToLower(tuner.Name)
	if model, known := models[modelOf(path, name)]; known {
		return *model, true
	}
	// The vendor identifier is the reliable half; the name covers a USB card,
	// which has no PCI vendor in its moniker but still says whose it is.
	if strings.Contains(path, Vendor) || strings.Contains(name, "tbs ") {
		return ModelDefault, true
	}
	return Model{}, false
}

// modelOf reads the model out of a tuner's moniker, or an empty string. The
// subsystem is tried first because a driver update cannot reword it; the
// friendly name is the fallback for a moniker carrying no subsystem.
func modelOf(path, name string) string {
	if at := strings.Index(path, "subsys_"); at >= 0 {
		field := path[at+len("subsys_"):]
		if len(field) >= 8 {
			if _, known := models[field[4:8]]; known {
				return field[4:8]
			}
		}
	}
	for candidate := range models {
		if strings.Contains(name, candidate) {
			return candidate
		}
	}
	return ""
}

// tunerNumberOf is which tuner of its card a filter is, as the driver numbers
// them in its name: "TBS 6281SE Tuner 1".
func tunerNumberOf(tuner Tuner) int {
	name := strings.ToLower(tuner.Name)
	at := strings.LastIndex(name, "tuner ")
	if at < 0 {
		return 0
	}
	number, err := strconv.Atoi(strings.TrimSpace(name[at+len("tuner "):]))
	if err != nil || number < 0 {
		return 0
	}
	return number
}

// cardWide is the SlotFor of a card that keeps one mode for all its tuners.
func cardWide(Tuner) int { return 0 }

// perTuner is the SlotFor of a card that gives each tuner a byte, bounded by
// how many bytes there are.
func perTuner(count int) func(Tuner) int {
	return func(tuner Tuner) int {
		number := tunerNumberOf(tuner)
		if number >= count {
			return count - 1
		}
		return number
	}
}

// dvbModeFor is the mode a delivery system needs on every card but the 6812.
// ITU J.83 Annex C is demodulated in DVB-C mode -- Annex A and C differ in
// roll-off and interleaving, not in anything the demodulator is told
// separately. Annex B is its own mode.
func dvbModeFor(system string) (DemodMode, bool) {
	switch system {
	case "DVBT", "DVBT2":
		return ModeDVBT, true
	case "DVBS", "DVBS2":
		return ModeDVBS, true
	case "DVBC", "DVBCANNEXA", "J83A", "ISDBC", "DVBCANNEXC", "J83C":
		return ModeDVBC, true
	case "ISDBT":
		return ModeISDBT, true
	case "DVBCANNEXB", "J83B":
		return ModeQAMB, true
	}
	return ModeUnknown, false
}

func standardNameFor(mode DemodMode) string { return mode.String() }

func unknownName(mode DemodMode) string { return fmt.Sprintf("mode %d", byte(mode)) }
