// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package config

import (
	"strconv"
	"testing"
)

// TestParseSatelliteChannel covers the names recpt1 writes and the frequencies
// they land on, which are the channel files' and the ones measured on a PT3.
// A name written this way says a place and no identifier.
func TestParseSatelliteChannel(t *testing.T) {
	for name, want := range map[string]SatelliteChannel{
		"BS01":   {Transponder: "BS01", FrequencyKHz: 1_049_480},
		"bs01_0": {Transponder: "BS01", FrequencyKHz: 1_049_480, HasStream: true},
		"BS01_1": {Transponder: "BS01", FrequencyKHz: 1_049_480, Stream: 1, HasStream: true},
		"BS03_2": {Transponder: "BS03", FrequencyKHz: 1_087_840, Stream: 2, HasStream: true},
		"BS23":   {Transponder: "BS23", FrequencyKHz: 1_471_440},
		"ND02":   {Transponder: "ND02", FrequencyKHz: 1_613_000},
		"CS4":    {Transponder: "CS04", FrequencyKHz: 1_653_000},
		"ND24_0": {Transponder: "ND24", FrequencyKHz: 2_053_000, HasStream: true},
		// The other hand of each satellite: even BS is left-hand, odd ND was.
		"BS14": {Transponder: "BS14", FrequencyKHz: 2_471_820},
		"ND01": {Transponder: "ND01", FrequencyKHz: 2_766_000},
	} {
		got, err := ParseSatelliteChannel(name)
		if err != nil {
			t.Errorf("ParseSatelliteChannel(%q): %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSatelliteChannel(%q) = %+v, want %+v", name, got, want)
		}
	}
}

// Every section of the ISDB-S channel file distributed for Japan, read as a bare
// transport stream identifier: the other spelling of the channel field, and the
// one a scan produces. The table is the whole file rather than a sample, because
// what is asserted is that the identifier is decoded rather than looked up, and
// a rule holding for most of the file would be worse than no rule.
//
// Regenerate from config/dvbconf-for-isdb/conf/dvbv5_channels_sat.conf.
func TestASatelliteStreamIdentifierSaysWhereItIs(t *testing.T) {
	for _, one := range []struct {
		identifier   int
		transponder  string
		frequencyKHz int
		place        int
	}{
		{16400, "BS01", 1_049_480, 0},
		{16401, "BS01", 1_049_480, 1},
		{16402, "BS01", 1_049_480, 2},
		{16432, "BS03", 1_087_840, 0},
		{17969, "BS03", 1_087_840, 1},
		{17970, "BS03", 1_087_840, 2},
		{17488, "BS05", 1_126_200, 0},
		{17489, "BS05", 1_126_200, 1},
		{45168, "BS07", 1_164_560, 0},
		{45169, "BS07", 1_164_560, 1},
		{45170, "BS07", 1_164_560, 2},
		{16528, "BS09", 1_202_920, 0},
		{16530, "BS09", 1_202_920, 2},
		{45232, "BS11", 1_241_280, 0},
		{45233, "BS11", 1_241_280, 1},
		{45234, "BS11", 1_241_280, 2},
		{16592, "BS13", 1_279_640, 0},
		{16593, "BS13", 1_279_640, 1},
		{18130, "BS13", 1_279_640, 2},
		{16625, "BS15", 1_318_000, 1},
		{16626, "BS15", 1_318_000, 2},
		{18675, "BS15", 1_318_000, 3},
		{45328, "BS17", 1_356_360, 0},
		{45329, "BS17", 1_356_360, 1},
		{45330, "BS17", 1_356_360, 2},
		{18224, "BS19", 1_394_720, 0},
		{18225, "BS19", 1_394_720, 1},
		{18226, "BS19", 1_394_720, 2},
		{18227, "BS19", 1_394_720, 3},
		{18256, "BS21", 1_433_080, 0},
		{18257, "BS21", 1_433_080, 1},
		{18258, "BS21", 1_433_080, 2},
		{18288, "BS23", 1_471_440, 0},
		{18801, "BS23", 1_471_440, 1},
		{18803, "BS23", 1_471_440, 3},
		{24608, "ND02", 1_613_000, 0},
		{28736, "ND04", 1_653_000, 0},
		{28768, "ND06", 1_693_000, 0},
		{24704, "ND08", 1_733_000, 0},
		{24736, "ND10", 1_773_000, 0},
		{28864, "ND12", 1_813_000, 0},
		{28896, "ND14", 1_853_000, 0},
		{28928, "ND16", 1_893_000, 0},
		{28960, "ND18", 1_933_000, 0},
		{28992, "ND20", 1_973_000, 0},
		{29024, "ND22", 2_013_000, 0},
		{29056, "ND24", 2_053_000, 0},
		{45280, "BS14", 2_471_820, 0},
	} {
		got, err := ParseSatelliteChannel(strconv.Itoa(one.identifier))
		if err != nil {
			t.Errorf("stream %d: %v", one.identifier, err)
			continue
		}
		want := SatelliteChannel{
			Transponder:  one.transponder,
			FrequencyKHz: one.frequencyKHz,
			Stream:       one.place,
			HasStream:    true,
			StreamID:     uint16(one.identifier),
			HasStreamID:  true,
		}
		if got != want {
			t.Errorf("stream %d = %+v, want %+v", one.identifier, got, want)
		}
	}
}

// TestParseSatelliteChannelRefusesWhatItCannotPlace: a name that is nearly right
// is the dangerous one, since answering BS25 with a frequency would be a tune
// that never locks for no stated reason. A bare number is refused unless it is
// an identifier of one of these networks, which keeps the two spellings apart:
// 13 is channel 13 and never a stream.
func TestParseSatelliteChannelRefusesWhatItCannotPlace(t *testing.T) {
	for _, name := range []string{
		"", "BS", "BS00", "BS25", "ND26", "CS0", "BS01_", "BS01_x", "13", "GR13",
		"0", "16383", "-1", "65536", "20480", "36864",
	} {
		if got, err := ParseSatelliteChannel(name); err == nil {
			t.Errorf("ParseSatelliteChannel(%q) = %+v, want a refusal", name, got)
		}
	}
}
