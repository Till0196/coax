// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

// The cards that give each tuner a mode of its own: the property is several
// settings side by side, one byte per tuner. A tuner writing its own byte must
// write the others back exactly as read, or it takes another tuner off a
// standard somebody is using. That is what SlotFor is for.

// The TBS 6281SE V2: two terrestrial and cable tuners, a mode each. Two bytes
// read and written, a separate setting for Tuner 0 and Tuner 1, so one can be on
// DVB-T while the other is on ISDB-T.
var Model6281SE = Model{
	Name: "TBS 6281SE", Width: 2, Reply: 2,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: perTuner(2),
}

// The TBS 6508: eight tuners, a mode each. A read answers 72 bytes of which the
// first eight are one mode per tuner, and those eight are what a write carries
// back. Terrestrial and cable cannot be mixed across tuners because the card has
// one aerial input -- a fact about the antenna, not the property, so not
// enforced here.
var Model6508 = Model{
	Name: "TBS 6508", Width: 8, Reply: 72,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: perTuner(8),
}

// The TBS 6522H: two switchable tuners and two that are always satellite. Two
// bytes read and written, one each for Tuner 0 and Tuner 1; the fixed DVB-S/S2
// pair has no byte, so a tuner numbered past the second is held to the last byte
// rather than reading past the end.
var Model6522H = Model{
	Name: "TBS 6522H", Width: 2, Reply: 2,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: perTuner(2),
}

// The TBS 6590SE: two tuners, each satellite or terrestrial. Unlike the 6522H
// both switch -- each carries two demodulators and the mode says which is wired
// to the transport stream. Two bytes, one per tuner, the 6522H's shape.
var Model6590SE = Model{
	Name: "TBS 6590SE", Width: 2, Reply: 2,
	ModeFor: dvbModeFor,
	NameFor: standardNameFor,
	SlotFor: perTuner(2),
}
