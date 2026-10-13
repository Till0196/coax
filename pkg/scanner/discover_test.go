// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/plan"
)

// A downlink frequency is not what a tuner is given: the low-noise block has
// already taken its oscillator off, and which oscillator that was depends on
// the hand of the polarisation. Subtracting the wrong one gives a frequency
// 1173 MHz out, which is a transponder that is not there.
func TestIntermediateFrequencySubtractsTheRightOscillator(t *testing.T) {
	kHz := func(delivery *isdb.SatelliteDelivery) (string, bool) {
		channel, ok := SatelliteChannel(delivery, "ISDBS", Dish{})
		if !ok {
			return "", false
		}
		return channel["FREQUENCY"], true
	}
	// BS-1, right-hand circular, is where every Japanese installation starts.
	right := &isdb.SatelliteDelivery{FrequencyKHz: 11727480, Polarisation: "R"}
	if got, ok := kHz(right); !ok || got != "1049480" {
		t.Errorf("right-hand gave %q, %t; want 1049480", got, ok)
	}
	// BS-14, left-hand, where the channel file puts a TLV stream.
	left := &isdb.SatelliteDelivery{FrequencyKHz: 11976820, Polarisation: "L"}
	if got, ok := kHz(left); !ok || got != "2471820" {
		t.Errorf("left-hand gave %q, %t; want 2471820", got, ok)
	}
	// Nothing arrives here from outside the stretch that polarisation lands
	// on -- including a downlink that would give a plausible-looking number
	// against the other polarisation's oscillator.
	for _, outside := range []*isdb.SatelliteDelivery{
		{FrequencyKHz: 10700000, Polarisation: "R"},
		{FrequencyKHz: 13000000, Polarisation: "R"},
		{FrequencyKHz: 11727480, Polarisation: "L"},
	} {
		if got, ok := kHz(outside); ok {
			t.Errorf("%d kHz gave %s; want nothing", outside.FrequencyKHz, got)
		}
	}
}

// One channel per stream, in transponder order, and only for the transports the
// network said where to find.
func TestSatelliteEntriesAreOneChannelPerStream(t *testing.T) {
	on := func(kHz uint32) *isdb.SatelliteDelivery {
		return &isdb.SatelliteDelivery{FrequencyKHz: kHz, Polarisation: "R"}
	}
	networks := []isdb.NetworkInfo{{NetworkID: 4, Transports: []isdb.NetworkTransport{
		{TransportStreamID: 17488, Satellite: on(11804200)}, // BS-5
		{TransportStreamID: 16401, Satellite: on(11727480)}, // BS-1
		{TransportStreamID: 16400, Satellite: on(11727480)}, // BS-1
		// The network mentioned it but not where it is, so it cannot be tuned.
		{TransportStreamID: 9999},
		// And the same stream twice is still one channel.
		{TransportStreamID: 16400, Satellite: on(11727480)},
	}}}
	entries := satelliteEntries(networks, "ISDBS", Dish{})
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %#v", len(entries), entries)
	}
	want := []struct{ name, frequency string }{
		{"16400", "1049480"},
		{"16401", "1049480"},
		{"17488", "1126200"},
	}
	for index, expected := range want {
		got := entries[index]
		if got.Name != expected.name || got.Properties["FREQUENCY"] != expected.frequency {
			t.Errorf("entry %d = %s at %s, want %s at %s",
				index, got.Name, got.Properties["FREQUENCY"], expected.name, expected.frequency)
		}
		if got.Properties["STREAM_ID"] != expected.name {
			t.Errorf("entry %d asks for stream %q, want %q", index, got.Properties["STREAM_ID"], expected.name)
		}
	}
}

// The transponders are a fixed grid: only their frequencies are needed and
// those do not move. The TLV probe identifiers are guesses to get in with,
// checked here against the ones measured on a real satellite -- BS 7 carries
// 0xB070.
func TestTranspondersAreTheFixedGrid(t *testing.T) {
	for name, satellite := range map[string][]transponder{
		"BS": broadcastTransponders,
		"ND": communicationsTransponders,
	} {
		if len(satellite) != 12 {
			t.Errorf("%s has %d transponders, want 12", name, len(satellite))
		}
	}
	for _, want := range []struct {
		on       []transponder
		index    int
		name     string
		kHz      int
		tlvProbe uint16
	}{
		{broadcastTransponders, 0, "BS01", 1049480, 0xb010},
		{broadcastTransponders, 3, "BS07", 1164560, 0xb070},
		{broadcastTransponders, 11, "BS23", 1471440, 0xb170},
		{communicationsTransponders, 0, "ND02", 1613000, 0xc020},
		{communicationsTransponders, 11, "ND24", 2053000, 0xc180},
	} {
		got := want.on[want.index]
		if got.name != want.name || got.kHz != want.kHz || got.tlvProbe != want.tlvProbe {
			t.Errorf("transponder %d = %s at %d probe %#04x, want %s at %d probe %#04x",
				want.index, got.name, got.kHz, got.tlvProbe, want.name, want.kHz, want.tlvProbe)
		}
	}
}

// A plan entry carries what the band needs, and its own settings do not
// overwrite the frequency.
func TestPlanEntriesCarryWhatTheBandNeeds(t *testing.T) {
	entries := planEntries([]plan.Carrier{{Name: "C13", Frequency: 111000000}}, map[string]string{
		"DELIVERY_SYSTEM": "DVBC/ANNEX_A",
		"SYMBOL_RATE":     "5274000",
	})
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got.Name != "C13" {
		t.Errorf("entry = %s, want C13", got.Name)
	}
	// A plan does not claim a lead. Which one this is arriving on is the
	// scan's to declare, once, for every channel it finds.
	if got.Route != "" {
		t.Errorf("the band plan claimed the lead %q", got.Route)
	}
	for key, want := range map[string]string{
		"FREQUENCY":       "111000000",
		"DELIVERY_SYSTEM": "DVBC/ANNEX_A",
		"SYMBOL_RATE":     "5274000",
	} {
		if got.Properties[key] != want {
			t.Errorf("%s = %q, want %q", key, got.Properties[key], want)
		}
	}
}

// A transponder can fail to answer for reasons that say nothing about the
// satellite: a dish pointed a little off, a band the distribution stops short
// of, a carrier that happens to be down. Stopping at the first refusal would
// report a satellite as empty on the strength of one frequency.
func TestDiscoverWalksPastATransponderThatDoesNotAnswer(t *testing.T) {
	// An answer names the transponders of its own network, which is what
	// spares the walk from tuning them again.
	answerFor := func(frequencies ...int) []channelconfig.Entry {
		var entries []channelconfig.Entry
		for _, kHz := range frequencies {
			entries = append(entries, channelconfig.Entry{
				Name:       "16400",
				Properties: map[string]string{"FREQUENCY": strconv.Itoa(kHz)},
			})
		}
		return entries
	}
	wholeBand := func() []int {
		var frequencies []int
		for _, on := range broadcastTransponders {
			frequencies = append(frequencies, on.kHz)
		}
		return frequencies
	}()

	for _, test := range []struct {
		name string
		fail func(transponder) ([]channelconfig.Entry, error)
		// networkOf is which network the transponder's table belongs to, since
		// the walk ends once every network of the band has spoken.
		networkOf func(transponder) uint16
		networks  int
		tried     int
		answers   bool
	}{
		{
			name: "the first three refuse and the fourth speaks for the band",
			fail: func(on transponder) ([]channelconfig.Entry, error) {
				if on.name == "BS07" {
					return answerFor(wholeBand...), nil
				}
				return nil, errors.New("no lock")
			},
			// Four to find it, then every other transponder the table named for a
			// second opinion: here none answers, so none is heard.
			networkOf: func(transponder) uint16 { return 4 },
			tried:     len(broadcastTransponders), answers: true,
		},
		{
			name: "a transponder that locks and carries nothing is walked past too",
			fail: func(on transponder) ([]channelconfig.Entry, error) {
				if on.name == "BS05" {
					return answerFor(wholeBand...), nil
				}
				return nil, nil // locked, nothing this can reach
			},
			networkOf: func(transponder) uint16 { return 4 },
			tried:     len(broadcastTransponders), answers: true,
		},
		{
			// 110 degree CS is two networks on one band, and the table of the
			// first speaks only for its own. Stopping there gave three
			// transponders of twelve.
			name: "a table that speaks for part of the band is followed by the rest",
			fail: func(on transponder) ([]channelconfig.Entry, error) {
				if on.name == "BS01" {
					return answerFor(wholeBand[0], wholeBand[1], wholeBand[2]), nil
				}
				return answerFor(wholeBand[3:]...), nil
			},
			// Two networks on one band, which is 110 degree CS.
			networkOf: func(on transponder) uint16 {
				if on.name == "BS01" {
					return 6
				}
				return 7
			},
			// Two to find both networks. The first is then never heard from again
			// (every other transponder here answers for the second), so the walk
			// asks all of them for its second opinion.
			networks: 2,
			tried:    len(broadcastTransponders), answers: true,
		},
		{
			name: "and a satellite that answers nowhere is reported as nothing",
			fail: func(transponder) ([]channelconfig.Entry, error) {
				return nil, errors.New("no lock")
			},
			networkOf: func(transponder) uint16 { return 0 },
			tried:     len(broadcastTransponders), answers: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tried := 0
			networks := test.networks
			if networks == 0 {
				networks = 1
			}
			got := discover(context.Background(), Options{}, newDiscoveryDisplay(io.Discard),
				satellitePass{
					what: "BS", on: broadcastTransponders, networks: networks,
					read: func(_ context.Context, _ Options, on transponder, _ string) ([]channelconfig.Entry, uint16, error) {
						tried++
						entries, err := test.fail(on)
						return entries, test.networkOf(on), err
					},
				})
			if tried != test.tried {
				t.Errorf("tried %d transponders, want %d", tried, test.tried)
			}
			if (len(got) != 0) != test.answers {
				t.Errorf("got %v, want an answer=%t", got, test.answers)
			}
		})
	}
}

func TestDiscoverStopsWhenAskedTo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tried := 0
	got := discover(ctx, Options{}, newDiscoveryDisplay(io.Discard), satellitePass{
		what: "BS", on: broadcastTransponders, networks: 1,
		read: func(context.Context, Options, transponder, string) ([]channelconfig.Entry, uint16, error) {
			tried++
			cancel()
			return nil, 0, errors.New("no lock")
		},
	})
	if tried != 1 || len(got) != 0 {
		t.Errorf("tried %d transponders and got %v; want it to stop after the first", tried, got)
	}
}

// TestTheWalkEndsWhenEveryNetworkHasSpoken.
//
// A network's table names every transponder of that network, so once it has
// spoken, and a second transponder's table agrees with it, there is nothing
// left up there to ask.
func TestTheWalkEndsWhenEveryNetworkHasSpoken(t *testing.T) {
	// The first transponder answers for its own network and names three of the
	// twelve positions, leaving nine no table has spoken for.
	tried := 0
	got := discover(context.Background(), Options{}, newDiscoveryDisplay(io.Discard),
		satellitePass{
			what: "BS", on: broadcastTransponders, networks: 1,
			read: func(_ context.Context, _ Options, on transponder, _ string) ([]channelconfig.Entry, uint16, error) {
				tried++
				var entries []channelconfig.Entry
				for _, named := range broadcastTransponders[:3] {
					entries = append(entries, channelconfig.Entry{
						Name:       named.name,
						Properties: map[string]string{"FREQUENCY": strconv.Itoa(named.kHz)},
					})
				}
				return entries, 4, nil
			},
		})
	if tried != 2 {
		t.Errorf("tried %d transponders, want the one that spoke for the network and one to agree", tried)
	}
	if len(got) != 3 {
		t.Errorf("got %d channels, want the three the table named", len(got))
	}
}
