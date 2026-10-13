// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

// The cards that keep one demodulator mode for the whole card: every tuner reads
// and writes the same byte, and moving it restarts all of them. Only the read
// and write widths differ, which is a fact about the driver generation.

// The TBS 6205SE: four terrestrial and cable tuners, one mode between them. The
// property reads two bytes and writes one, and switching it restarts the whole
// card. One of the two models here checked against real hardware.
var Model6205SE = Model{
	Name: "TBS 6205SE", Width: 1, Reply: 2,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: cardWide,
}

// The TBS 6209 and 6209SE: eight terrestrial and cable tuners, one mode. The
// property reads and writes two bytes and only the first names a standard --
// hence Width 2 with every tuner on slot zero -- and the second is written back
// as it was read.
var Model6209 = Model{
	Name: "TBS 6209", Width: 2, Reply: 2,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: cardWide,
}

// The TBS 6216 and TBS 7230: the plain one-byte card-wide form. Neither has been
// seen here; they are named rather than left to the default so a listing says
// which card it is.
var (
	Model6216 = Model{
		Name: "TBS 6216", Width: 1, Reply: 2,
		ModeFor: dvbModeFor,
		NameFor: standardNameFor,
		SlotFor: cardWide,
	}
	Model7230 = Model{
		Name: "TBS 7230", Width: 1, Reply: 2,
		ModeFor: dvbModeFor,
		NameFor: standardNameFor,
		SlotFor: cardWide,
	}
)

// The TBS 6504H: four satellite tuners beside four terrestrial and cable ones.
// The satellite half does not switch; the other half shares one mode, one byte
// written. The read is the wide 72-byte one, like the 6508's -- this driver
// generation answers with a whole structure rather than the bytes that matter.
var Model6504H = Model{
	Name: "TBS 6504H", Width: 1, Reply: 72,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: cardWide,
}
