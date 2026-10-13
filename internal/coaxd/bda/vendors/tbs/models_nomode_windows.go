// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

// The vendor's satellite-only cards, whose drivers carry no demodulator-mode
// property.
//
// The multi-standard cards answer identifier 0x3c on the vendor's GUID with the
// standard they are wired for; these do not -- the 6902 carries fifteen
// identifiers with no 0x3c among them, and the 6903, 6904, 6905, 6908, 6909 and
// 6910 are the same. A read of an absent property fails and would look like a
// card that cannot be opened, so these declare it and the read is skipped. They
// all carry the raw I2C property (0x19).
//
// These entries also cover the siblings sharing a model number, because that is
// what the table is keyed by. Those are different cards with different chips
// and some do carry 0x3c, but treating the family alike is still right: they
// receive satellite and nothing else. The whole cost is not reporting a mode.
var (
	Model6902 = Model{
		Name: "TBS 6902", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
	Model6903 = Model{
		Name: "TBS 6903", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
	Model6904 = Model{
		Name: "TBS 6904", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
	Model6905 = Model{
		Name: "TBS 6905", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
	Model6908 = Model{
		Name: "TBS 6908", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
	Model6909 = Model{
		Name: "TBS 6909", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
	Model6910 = Model{
		Name: "TBS 6910", Width: 1, Reply: 2, NoMode: true,
		ModeFor: noModeFor,
		NameFor: satelliteOnlyName,
		SlotFor: cardWide,
	}
)

// noModeFor is the ModeFor of a card with no mode property: nothing maps.
func noModeFor(string) (DemodMode, bool) { return ModeUnknown, false }

// satelliteOnlyName says what such a card is, rather than rendering a number it
// never answers with.
func satelliteOnlyName(DemodMode) string { return "DVB-S/S2" }
