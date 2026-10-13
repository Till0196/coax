// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestChannelPropertiesBuildsAChannel covers the ways a channel can be named on
// the command line. What comes out is a channel-file section, so the same words
// mean the same thing in a file and in an argument.
func TestChannelPropertiesBuildsAChannel(t *testing.T) {
	cases := []struct {
		name                              string
		system, channel, frequency, strID string
		extra                             map[string]string
		want                              map[string]string
	}{
		{
			name:    "a physical channel is enough, because only one system numbers them",
			channel: "27",
			want: map[string]string{
				"DELIVERY_SYSTEM": "ISDBT",
				"FREQUENCY":       "557142857",
			},
		},
		{
			name:    "the lowest channel",
			channel: "13",
			want:    map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473142857"},
		},
		{
			name:    "the highest",
			channel: "62",
			want:    map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "767142857"},
		},
		{
			name:      "satellite is named by frequency and stream",
			system:    "ISDBS",
			frequency: "1049480",
			strID:     "16625",
			want: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1049480",
				"STREAM_ID":       "16625",
			},
		},
		{
			name:      "anything else a channel needs goes through as written",
			system:    "DVBT2",
			frequency: "474000000",
			extra:     map[string]string{"bandwidth_hz": "6000000", "STREAM_ID": "0"},
			want: map[string]string{
				"DELIVERY_SYSTEM": "DVBT2",
				"FREQUENCY":       "474000000",
				"BANDWIDTH_HZ":    "6000000",
				"STREAM_ID":       "0",
			},
		},
		{
			// The channel field of a Mirakurun channel list is written both
			// ways, and this is the one recpt1 and the tools after it use.
			name:    "a satellite channel named as a transponder and a place",
			channel: "BS15_1",
			want: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1318000",
				// The place, which is all the name said.
				"STREAM_ID": "1",
			},
		},
		{
			// And this is the one the Japanese DVB channel files name their
			// sections by, and the one a scan of a satellite reports.
			name:    "the same channel named by its stream identifier",
			channel: "16625",
			want: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1318000",
				// The identifier, because that is what was written: it says
				// the place too (0x40F1 is transponder 15, place 1), but the
				// DVB API wants the identifier and the drivers that take a
				// place are the daemon's to know about.
				"STREAM_ID": "16625",
			},
		},
		{
			name:    "a TLV stream identifier lands on its transponder too",
			channel: "45168",
			want: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1164560",
				"STREAM_ID":       "45168",
			},
		},
		{
			name:    "a communications satellite, either way round",
			channel: "24608",
			want: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1613000",
				"STREAM_ID":       "24608",
			},
		},
		{
			name:    "an explicit system beats the one a channel number implies",
			system:  "ISDBT",
			channel: "27",
			want:    map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857"},
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, err := channelProperties(channelRequest{system: one.system, channel: one.channel, frequency: one.frequency, streamID: one.strID, extra: one.extra})
			if err != nil {
				t.Fatalf("build the channel: %v", err)
			}
			if !reflect.DeepEqual(got, one.want) {
				t.Errorf("built %v, want %v", got, one.want)
			}
		})
	}
}

// TestSkyTransponderCarriesItsSatellite checks that a 124/128 degree east
// transponder names the satellite it is on, since that name selects the block:
// two slots reach one lead through one block, and the block puts the 22 kHz tone
// on that tells them apart.
//
// The frequency written is the downlink, not an intermediate one: the published
// table gives two IFs, 522 MHz apart, so naming the block and letting it convert
// is what makes one plan serve both installations.
func TestSkyTransponderCarriesItsSatellite(t *testing.T) {
	for _, one := range []struct {
		channel      string
		downlink     string
		satellite    string
		polarization string
		symbolRate   string
	}{
		{"JCSAT3A/JD1", "12508000", "JCSAT3A", "VERTICAL", "23303000"},
		{"JCSAT3A/JD16", "12733000", "JCSAT3A", "HORIZONTAL", "23303000"},
		{"JCSAT3A/JD17", "12268000", "JCSAT3A", "VERTICAL", "23303000"},
		{"jcsat4b/jd1", "12508250", "JCSAT4B", "VERTICAL", "23303000"},
		{"JCSAT4B/K16", "12493250", "JCSAT4B", "HORIZONTAL", "23303000"},
	} {
		got, err := channelProperties(channelRequest{channel: one.channel})
		if err != nil {
			t.Errorf("--ch %s: %v", one.channel, err)
			continue
		}
		for key, value := range map[string]string{
			"DELIVERY_SYSTEM": "DVBS2",
			"FREQUENCY":       one.downlink,
			"LNB":             one.satellite,
			"POLARIZATION":    one.polarization,
			"SYMBOL_RATE":     one.symbolRate,
		} {
			if got[key] != value {
				t.Errorf("--ch %s: %s = %q, want %q", one.channel, key, got[key], value)
			}
		}
	}
	// A block named on the command line is what is used, so a dish with
	// something else on it is not overruled by the plan.
	got, err := channelProperties(channelRequest{channel: "JCSAT3A/JD1", lnbf: "110BS"})
	if err != nil {
		t.Fatal(err)
	}
	if got["LNB"] != "110BS" {
		t.Errorf("--lnbf was overruled by the plan: %q", got["LNB"])
	}
	// The older carriers of the same grid are asked for by saying so, which
	// is also the name a scan of one reports.
	old, err := channelProperties(channelRequest{channel: "JCSAT3A/JD9/QPSK"})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"DELIVERY_SYSTEM": "DVBS",
		"FREQUENCY":       "12628000",
		"MODULATION":      "QPSK",
		"SYMBOL_RATE":     "21096000",
	} {
		if old[key] != want {
			t.Errorf("the older carrier: %s = %q, want %q", key, old[key], want)
		}
	}
	// And a symbol rate or a polarisation written by hand stands: a measured
	// value beats a table.
	got, err = channelProperties(channelRequest{
		channel: "JCSAT3A/JD1",
		extra:   map[string]string{"SYMBOL_RATE": "23303000", "POLARIZATION": "HORIZONTAL"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["SYMBOL_RATE"] != "23303000" || got["POLARIZATION"] != "HORIZONTAL" {
		t.Errorf("what was written by hand was overruled: %v", got)
	}
}

// A channel needing more than a name can carry is asked for by name out of a
// file, which is the shape the 124/128 degree east service is already received
// under: szap-s2j with a configuration file and a channel name out of it.
func TestAChannelFileSectionIsTunedByName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sky.conf")
	file := `[JCSAT3A/JD5]
	DELIVERY_SYSTEM = DVBS2
	FREQUENCY = 12268000
	LNB = JCSAT3A
	SYMBOL_RATE = 23303000
	MODULATION = PSK/8
	POLARIZATION = VERTICAL

[JCSAT4B/JD2]
	DELIVERY_SYSTEM = DVBS2
	FREQUENCY = 12328000
	LNB = JCSAT4B
	SYMBOL_RATE = 21096000
	MODULATION = PSK/8
	POLARIZATION = HORIZONTAL
`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := channelProperties(channelRequest{channels: path, channel: "JCSAT3A/JD5"})
	if err != nil {
		t.Fatalf("tune a section: %v", err)
	}
	for key, want := range map[string]string{
		"DELIVERY_SYSTEM": "DVBS2",
		"FREQUENCY":       "12268000",
		"LNB":             "JCSAT3A",
		"SYMBOL_RATE":     "23303000",
		"MODULATION":      "PSK/8",
		"POLARIZATION":    "VERTICAL",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}

	// The command line covers what the file said, which is what lets one file
	// of transponders be used with the block named beside it.
	got, err = channelProperties(channelRequest{channels: path, channel: "JCSAT4B/JD2", lnbf: "JCSAT4A"})
	if err != nil {
		t.Fatal(err)
	}
	if got["LNB"] != "JCSAT4A" {
		t.Errorf("--lnbf did not cover the file: %q", got["LNB"])
	}

	// A section that is not there is said so, rather than falling through to
	// a plan and tuning something else under the right name.
	if _, err := channelProperties(channelRequest{channels: path, channel: "27"}); err == nil {
		t.Error("a missing section was accepted")
	} else if !strings.Contains(err.Error(), "JCSAT3A/JD5") {
		t.Errorf("the refusal does not say what the file holds: %v", err)
	}
	// And saying where twice is refused, as it is everywhere else here.
	if _, err := channelProperties(channelRequest{channels: path, channel: "JCSAT3A/JD5", frequency: "12268000"}); err == nil {
		t.Error("--channels and --freq were both accepted")
	}
	if _, err := channelProperties(channelRequest{channels: path}); err == nil {
		t.Error("--channels with no --ch was accepted")
	}
}

// TestChannelPropertiesRefusesAnAmbiguousChannel: every one of these would
// otherwise be answered by tuning something the caller did not ask for.
func TestChannelPropertiesRefusesAnAmbiguousChannel(t *testing.T) {
	cases := []struct {
		name                              string
		system, channel, frequency, strID string
		want                              string
	}{
		{name: "nothing at all", want: "delivery system"},
		{name: "a system with no channel", system: "ISDBT", want: "channel is required"},
		{name: "two ways of saying where", system: "ISDBT", channel: "27", frequency: "557142857", want: "one of them"},
		// Neither vocabulary claims it, and the refusal names both so that
		// somebody who meant one of them can see which they missed.
		{name: "a channel that is neither", channel: "UHF27", want: "none of a television channel"},
		{name: "a satellite transponder that does not exist", channel: "BS25", want: "odd number to 23"},
		// 12 is a channel now -- VHF 12 -- so the ends of the plan are where
		// the plan ends, not where the UHF band does.
		{name: "below the plan", channel: "0", want: "none of a television channel"},
		{name: "above it", channel: "63", want: "none of a television channel"},
		// The cable numbering has its own ends, and a bare number is never
		// read as one of them.
		{name: "a cable channel that does not exist", channel: "C64", want: "none of a television channel"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			_, err := channelProperties(channelRequest{system: one.system, channel: one.channel, frequency: one.frequency, streamID: one.strID})
			if err == nil {
				t.Fatal("accepted a channel that says nothing usable")
			}
			if !strings.Contains(err.Error(), one.want) {
				t.Errorf("said %q, which does not mention %q", err, one.want)
			}
		})
	}
}

// TestPropertiesCollectRepeatedOptions checks the escape hatch for a channel
// this command has no flag for.
func TestPropertiesCollectRepeatedOptions(t *testing.T) {
	collected := &properties{values: map[string]string{}}
	if err := collected.Set("modulation=QAM/64"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Names are spelled as a channel file spells them whatever the caller
	// typed, so -P system=ISDBT and --system agree.
	if err := collected.Set(" symbol_rate = 28860 "); err != nil {
		t.Fatalf("Set: %v", err)
	}
	want := map[string]string{"MODULATION": "QAM/64", "SYMBOL_RATE": " 28860 "}
	if !reflect.DeepEqual(collected.values, want) {
		t.Errorf("collected %v, want %v", collected.values, want)
	}
	if err := collected.Set("nonsense"); err == nil {
		t.Error("accepted a property with no value")
	}
	if err := collected.Set("=1"); err == nil {
		t.Error("accepted a property with no name")
	}
}

// The supply setting is named in the same words on the command line, in a
// channel file and in the daemon's vocabulary, and the numbers people reach for
// are accepted as names for the two levels.
func TestLNBPowerFlagBecomesTheVoltageProperty(t *testing.T) {
	for _, one := range []struct{ written, want string }{
		{"off", "off"}, {"OFF", "off"}, {"0", "off"}, {"none", "off"},
		{"low", "low"}, {"11", "low"}, {"13", "low"},
		{"high", "high"}, {"on", "high"}, {"15", "high"}, {"18", "high"},
		{" 15 ", "high"},
	} {
		properties, err := channelProperties(channelRequest{system: "ISDBS", frequency: "1049480", lnbPower: one.written})
		if err != nil {
			t.Fatalf("--lnb-power %q: %v", one.written, err)
		}
		if properties["VOLTAGE"] != one.want {
			t.Errorf("--lnb-power %q became %q, want %q", one.written, properties["VOLTAGE"], one.want)
		}
	}
}

// A spelling that means nothing is refused here rather than carried to a
// daemon that would have to be started first to refuse it.
func TestLNBPowerFlagRefusesASettingThatMeansNothing(t *testing.T) {
	for _, written := range []string{"12", "24", "yes", "-1"} {
		if _, err := channelProperties(channelRequest{system: "ISDBS", frequency: "1049480", lnbPower: written}); err == nil {
			t.Errorf("--lnb-power %q should have been refused", written)
		}
	}
}

// Saying it twice, once each way, is a mistake worth naming rather than one to
// resolve by picking a winner.
func TestLNBPowerFlagAndTheRawPropertyAreNotBothAccepted(t *testing.T) {
	_, err := channelProperties(channelRequest{system: "ISDBS", frequency: "1049480", lnbPower: "high", extra: map[string]string{"VOLTAGE": "low"}})
	if err == nil {
		t.Error("--lnb-power together with -P VOLTAGE should be refused")
	}
}

// The block on the dish is named through to the daemon, which holds the table.
func TestLNBfFlagBecomesTheLNBProperty(t *testing.T) {
	properties, err := channelProperties(channelRequest{system: "DVBS2", frequency: "12268000", lnbf: "JCSAT3A"})
	if err != nil {
		t.Fatalf("--lnbf: %v", err)
	}
	if properties["LNB"] != "JCSAT3A" {
		t.Errorf("--lnbf JCSAT3A became LNB=%q", properties["LNB"])
	}
}

func TestLNBfFlagAndTheRawPropertyAreNotBothAccepted(t *testing.T) {
	_, err := channelProperties(channelRequest{system: "DVBS2", frequency: "12268000", lnbf: "JCSAT3A", extra: map[string]string{"LNB": "UNIVERSAL"}})
	if err == nil {
		t.Error("--lnbf together with -P LNB should be refused")
	}
}

// TestChannelPropertiesTakesSatelliteNames checks the recpt1 spelling: the place
// after the underscore is the stream's position on the transponder, not a
// transport stream identifier, and the two must not be given at once.
func TestChannelPropertiesTakesSatelliteNames(t *testing.T) {
	for _, test := range []struct {
		channel string
		want    map[string]string
	}{
		{"BS01", map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480"}},
		{"BS01_1", map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "1"}},
		{"nd02", map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1613000"}},
	} {
		got, err := channelProperties(channelRequest{channel: test.channel})
		if err != nil {
			t.Errorf("channelProperties(%q): %v", test.channel, err)
			continue
		}
		if len(got) != len(test.want) {
			t.Errorf("%s gave %v, want %v", test.channel, got, test.want)
			continue
		}
		for key, want := range test.want {
			if got[key] != want {
				t.Errorf("%s: %s = %q, want %q", test.channel, key, got[key], want)
			}
		}
	}
	if _, err := channelProperties(channelRequest{channel: "BS01_1", streamID: "16401"}); err == nil {
		t.Error("naming the stream twice, once by place and once by identifier, must be refused")
	}
}

func TestTheGainFlagBecomesTheGainProperty(t *testing.T) {
	for _, one := range []struct{ written, want string }{
		{"Off", "off"},
		{"MAX", "max"},
		{"auto", "auto"},
		{"+13dB", "+13dB"},
		{"-9.9dB", "-9.9dB"},
	} {
		properties, err := channelProperties(channelRequest{system: "ISDBT", frequency: "473142857", gain: one.written})
		if err != nil {
			t.Errorf("--gain %s: %v", one.written, err)
			continue
		}
		if properties["GAIN"] != one.want {
			t.Errorf("--gain %s became GAIN=%q, want %q", one.written, properties["GAIN"], one.want)
		}
	}
	// A bare number is refused here as well as at the daemon: a step number is
	// the mistake worth catching before a tuner is claimed.
	for _, written := range []string{"23", "13.5", "loud"} {
		if _, err := channelProperties(channelRequest{system: "ISDBT", frequency: "473142857", gain: written}); err == nil {
			t.Errorf("--gain %s was accepted", written)
		}
	}
	for _, key := range []string{"GAIN", "LNA"} {
		_, err := channelProperties(channelRequest{system: "ISDBT", frequency: "473142857", gain: "max", extra: map[string]string{key: "min"}})
		if err == nil {
			t.Errorf("--gain together with -P %s should be refused", key)
		}
	}
}

// The whole television plan is nameable, and each name lands on the frequency
// the repository's own channel list carries for it. The VHF and cable stretches
// are the point: a cable pass-through carries the same multiplexes on the mid
// and super high bands, so a channel file naming C23 has to reach the tuner as
// 225142857 rather than as an error.
func TestChannelPropertiesNamesTheWholePlan(t *testing.T) {
	for _, one := range []struct {
		channel string
		want    string
	}{
		{"1", "93142857"},
		{"12", "219142857"},
		{"C13", "111142857"},
		{"C22", "167142857"},
		{"C23", "225142857"},
		{"C63", "465142857"},
		{"13", "473142857"},
		{"62", "767142857"},
		// Lower case is taken, and the prefix still decides: c27 is the cable
		// channel, not UHF 27. The two are 308 MHz apart.
		{"c27", "249142857"},
		{" 27 ", "557142857"},
	} {
		properties, err := channelProperties(channelRequest{channel: one.channel})
		if err != nil {
			t.Errorf("--ch %s: %v", one.channel, err)
			continue
		}
		if properties["FREQUENCY"] != one.want {
			t.Errorf("--ch %s is %s Hz, want %s", one.channel, properties["FREQUENCY"], one.want)
		}
		if properties["DELIVERY_SYSTEM"] != "ISDBT" {
			t.Errorf("--ch %s came out as %s", one.channel, properties["DELIVERY_SYSTEM"])
		}
	}
}
