// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

// TestWriteMirakurunIncludesOnlyFoundChannels: a channel list is a list of
// channels that are there, so a candidate that did not lock is left out.
func TestWriteMirakurunIncludesOnlyFoundChannels(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{
		{
			Entry:  channelconfig.Entry{Name: "45168", Route: "SAT"},
			Status: "found",
			Analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{OriginalNetworkID: 0x000b, Services: []isdb.ServiceInfo{{
				ServiceID: 181, Type: 0xa5, Name: "SAT 45168",
			}}}},
		},
		{Entry: channelconfig.Entry{Name: "45169", Route: "SAT"}, Status: "no_lock"},
	}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{Route: RouteAsScanned}); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{`name: "SAT 45168"`, "type: BS4K", "route: SAT", `channel: "45168"`} {
		if !strings.Contains(text, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "45169") {
		t.Errorf("output contains an unavailable channel:\n%s", text)
	}
}

// What is written by default is the least a channel list can say: the name, the
// kind, and where it is. Everything Mirakurun finds by looking is left for it
// to look for -- and to look for again later, so a change after this file was
// written is still followed.
func TestTheDefaultChannelListSaysOnlyWhereAChannelIs(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C13", Route: "CATV"},
		Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 2, OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 211, Type: 0x01, Name: "a service"}},
				}},
			},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	want := "- name: \"a service\"\n  type: BS\n  channel: \"C13\"\n"
	if output.String() != want {
		t.Errorf("the default list says more than where the channel is:\n%s\nwant:\n%s", output.String(), want)
	}
}

// A stream of a multiplexing frame is written out when it has to be said
// rather than found: one entry per stream, each carrying its number.
func TestRelativeStreamsAreWrittenWhenAskedFor(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C13"},
		Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				// Two streams of one kind, which is the case that needs the
				// number: nothing else in the entry tells them apart.
				{RelativeStream: 1, OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 211, Type: 0x01, Name: "first"}},
				}},
				{RelativeStream: 3, OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 311, Type: 0x01, Name: "second"}},
				}},
			},
		}},
	}}}

	// Left alone, the carrier is one channel: Mirakurun finds the frame.
	var carrier bytes.Buffer
	if _, err := WriteMirakurun(&carrier, reportValue, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(carrier.String(), "- name:"); got != 1 {
		t.Errorf("one carrier became %d entries:\n%s", got, carrier.String())
	}
	if strings.Contains(carrier.String(), "tsmfRelTs") {
		t.Errorf("a stream was numbered although none was asked for:\n%s", carrier.String())
	}

	var streams bytes.Buffer
	if _, err := WriteMirakurun(&streams, reportValue, MirakurunOptions{RelativeStreams: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(streams.String(), "- name:"); got != 2 {
		t.Errorf("two streams became %d entries:\n%s", got, streams.String())
	}
	for _, expected := range []string{"tsmfRelTs: 1", "tsmfRelTs: 3", `name: "first"`, `name: "second"`} {
		if !strings.Contains(streams.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, streams.String())
		}
	}
}

// Only a channel whose every service is under conditional access carries the
// key at all, and it carries it as false: the entry is marked for a second
// look, not turned off. The branch is on the SDT's free_CA_mode and on nothing
// else.
func TestConditionalAccessOnEveryServiceIsWhatDisablesAChannel(t *testing.T) {
	channel := func(name string, services ...isdb.ServiceInfo) report.Result {
		return report.Result{
			Entry:  channelconfig.Entry{Name: name},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				OriginalNetworkID: 6, Services: services,
			}},
		}
	}
	inTheClear := isdb.ServiceInfo{ServiceID: 1, Type: 0x01, Name: "in the clear"}
	scrambled := isdb.ServiceInfo{ServiceID: 2, Type: 0x01, Name: "scrambled", FreeCAMode: true}
	alsoScrambled := isdb.ServiceInfo{ServiceID: 3, Type: 0x01, Name: "scrambled too", FreeCAMode: true}

	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, report.Report{Results: []report.Result{
		channel("clear only", inTheClear),
		channel("under CA throughout", scrambled, alsoScrambled),
		// One service in the clear is a reason to receive the channel,
		// whatever it is carried alongside.
		channel("mixed", scrambled, inTheClear),
	}}, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "isDisabled: true") {
		t.Errorf("a channel was written as disabled:\n%s", output.String())
	}
	if got := strings.Count(output.String(), "isDisabled: false"); got != 1 {
		t.Errorf("%d channels carry the key, want 1:\n%s", got, output.String())
	}
	// The ones with something in the clear say nothing at all.
	if !strings.Contains(output.String(), "channel: \"under CA throughout\"\n  isDisabled: false") {
		t.Errorf("the channel under conditional access throughout is not the one carrying the key:\n%s", output.String())
	}
}

// Written per service, each reads its own free_CA_mode: the scrambled one
// carries the key, the one in the clear does not.
func TestAServiceUnderConditionalAccessIsDisabledOnItsOwn(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "ND02"},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			OriginalNetworkID: 6,
			Services: []isdb.ServiceInfo{
				{ServiceID: 1, Type: 0x01, Name: "in the clear"},
				{ServiceID: 2, Type: 0x01, Name: "scrambled", FreeCAMode: true},
			},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{ServiceIDs: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "isDisabled: false"); got != 1 {
		t.Errorf("%d services carry the key, want 1:\n%s", got, output.String())
	}
	if strings.Contains(output.String(), "isDisabled: true") {
		t.Errorf("a service was written as disabled:\n%s", output.String())
	}
}

// A frame carrying one stream is the carrier, so its number is not written
// even when the numbers were asked for. There would be nothing to tell apart.
func TestASingleRelativeStreamIsNotNumbered(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C13"},
		Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 2, OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 211, Type: 0x01, Name: "the only one"}},
				}},
			},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{RelativeStreams: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "tsmfRelTs") {
		t.Errorf("the one stream on this carrier was numbered:\n%s", output.String())
	}
}

// A 124/128 degree east channel says which satellite it is on, because nothing
// downstream can work it out: the two slots share a block, a lead and an
// oscillator, and only the 22 kHz tone separates them. It is also the field
// szap-s2j reads as -l <satellite>.
func TestASkyChannelNamesItsSatellite(t *testing.T) {
	channel := func(name string, networkID uint16, properties map[string]string) report.Result {
		return report.Result{
			Entry:  channelconfig.Entry{Name: name, Properties: properties},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				OriginalNetworkID: networkID,
				Services:          []isdb.ServiceInfo{{ServiceID: 1, Type: 0x01, Name: "a service"}},
			}},
		}
	}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, report.Report{Results: []report.Result{
		// Named by the block it was tuned through, which is what a channel
		// file carries.
		channel("JD5", 0x000a, map[string]string{"DELIVERY_SYSTEM": "DVBS2", "LNB": "JCSAT4B"}),
		// And named by the plan, which puts the satellite in the name.
		channel("JCSAT3A/JD1", 0x000a, map[string]string{"DELIVERY_SYSTEM": "DVBS2"}),
		// A satellite channel from anywhere else has one slot and one way to
		// it, so naming it would be decoration.
		channel("BS15_1", 4, map[string]string{"DELIVERY_SYSTEM": "ISDBS"}),
	}}, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"  commandVars:\n    satellite: JCSAT4B\n",
		"  commandVars:\n    satellite: JCSAT3A\n",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
	if got := strings.Count(output.String(), "commandVars:"); got != 2 {
		t.Errorf("%d channels named a satellite, want 2:\n%s", got, output.String())
	}
	// Every one of them is the 124/128 degree east service, whichever way it
	// arrived.
	if got := strings.Count(output.String(), "type: SKY"); got != 2 {
		t.Errorf("the 124/128 degree east channels are not both SKY:\n%s", output.String())
	}
}

// The list is written in the order Mirakurun lists the kinds, whatever order
// the feed was scanned in: a scan walks one band at a time and one cable
// carrier can carry several kinds at once.
func TestTheChannelListIsOrderedByKind(t *testing.T) {
	// Scanned back to front: a TLV satellite carrier, then the 124/128
	// degree east platform off a cable, then BS, then a terrestrial multiplex.
	reportValue := report.Report{Results: []report.Result{
		{
			Entry:  channelconfig.Entry{Name: "45168"},
			Status: "found",
			Analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{OriginalNetworkID: 0x000b,
				Services: []isdb.ServiceInfo{{ServiceID: 181, Type: 0xa5, Name: "a TLV one"}}}},
		},
		{
			Entry:  channelconfig.Entry{Name: "C13"},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{OriginalNetworkID: 0x000a,
				Services: []isdb.ServiceInfo{{ServiceID: 300, Type: 1, Name: "a 124/128 degree east one"}}}},
		},
		{
			Entry:  channelconfig.Entry{Name: "BS15_0"},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{OriginalNetworkID: 0x0004,
				Services: []isdb.ServiceInfo{{ServiceID: 211, Type: 1, Name: "a BS one"}}}},
		},
		{
			Entry:  channelconfig.Entry{Name: "17"},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{OriginalNetworkID: 0x7fc0,
				Services: []isdb.ServiceInfo{{ServiceID: 1024, Type: 1, Name: "a terrestrial one"}}}},
		},
	}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}

	var written []string
	for _, line := range strings.Split(output.String(), "\n") {
		if kind, found := strings.CutPrefix(strings.TrimSpace(line), "type: "); found {
			written = append(written, kind)
		}
	}
	want := []string{"GR", "BS", "BS4K", "SKY"}
	if strings.Join(written, ",") != strings.Join(want, ",") {
		t.Errorf("the kinds are written %v; want %v:\n%s", written, want, output.String())
	}
}

// The lead given at export time overrides what the scan recorded, so a scan run
// without saying which lead it was of is corrected without being run again. The
// answer is not in the signal, so no amount of rescanning finds it.
func TestTheExportedLeadCanBeSaidAfterTheScan(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C13"},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			OriginalNetworkID: 4,
			Services:          []isdb.ServiceInfo{{ServiceID: 161, Type: 0x01, Name: "BS"}},
		}},
	}}}
	var said bytes.Buffer
	if _, err := WriteMirakurun(&said, reportValue, MirakurunOptions{Route: "CATV"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said.String(), "route: CATV") {
		t.Errorf("the lead given at export time was not written:\n%s", said.String())
	}
	// And a channel nobody has said a lead for is written without one, rather
	// than with a guess. Mirakurun takes the lead its channel type implies.
	var unsaid bytes.Buffer
	if _, err := WriteMirakurun(&unsaid, reportValue, MirakurunOptions{Route: RouteAsScanned}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unsaid.String(), "route:") {
		t.Errorf("a lead was invented:\n%s", unsaid.String())
	}
}

// The channel field is written both ways in the wild, so it can be written
// either way here. A satellite channel becomes the transponder and the stream's
// place on it, which is what recpt1 and the drivers that only take a place
// understand.
func TestTransponderNamesAreOnlyForSatelliteChannels(t *testing.T) {
	satellite := func(name, frequency, stream string, networkID uint16) report.Result {
		return report.Result{
			Entry: channelconfig.Entry{Name: name, Properties: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": frequency, "STREAM_ID": stream,
			}},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				OriginalNetworkID: networkID,
				Services:          []isdb.ServiceInfo{{ServiceID: 211, Type: 0x01, Name: "a service"}},
			}},
		}
	}
	reportValue := report.Report{Results: []report.Result{
		// Named by its identifier, which decodes: 0x40F1 is transponder 15,
		// place 1.
		satellite("16625", "1318000", "16625", 4),
		// Named whatever its author called it, so the frequency and the
		// stream are what place it.
		satellite("mine", "1049480", "1", 4),
		// Terrestrial, which has no transponder and keeps its own numbering.
		{
			Entry: channelconfig.Entry{Name: "27", Properties: map[string]string{
				"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857",
			}},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				OriginalNetworkID: 32391,
				Services:          []isdb.ServiceInfo{{ServiceID: 1024, Type: 0x01, Name: "terrestrial"}},
			}},
		},
	}}
	var output bytes.Buffer
	unnamed, err := WriteMirakurun(&output, reportValue, MirakurunOptions{TransponderNames: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(unnamed) != 0 {
		t.Errorf("nothing here should have been left alone: %v", unnamed)
	}
	for _, expected := range []string{`channel: "BS15_1"`, `channel: "BS01_1"`, `channel: "27"`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}

// A TLV stream keeps its identifier and says why. Those streams are selected
// by identifier on every driver that receives them, so "the first stream on
// this transponder" would name the older service sharing it.
//
// Both spellings are tried: a file may write these as ISDB-S with a TLV
// identifier, which is what Linux takes, or name ISDB-S3 outright.
func TestAnAdvancedStreamKeepsItsIdentifier(t *testing.T) {
	for _, system := range []string{"ISDBS", "ISDBS3"} {
		t.Run(system, func(t *testing.T) {
			reportValue := report.Report{Results: []report.Result{{
				Entry: channelconfig.Entry{Name: "45168", Properties: map[string]string{
					"DELIVERY_SYSTEM": system, "FREQUENCY": "1164560", "STREAM_ID": "45168",
				}},
				Status: "found",
				Analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{
					OriginalNetworkID: 0x000b,
					Services:          []isdb.ServiceInfo{{ServiceID: 401, Type: 0x01, Name: "TLV"}},
				}},
			}}}
			var output bytes.Buffer
			unnamed, err := WriteMirakurun(&output, reportValue, MirakurunOptions{TransponderNames: true})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), `channel: "45168"`) {
				t.Errorf("the identifier was not kept:\n%s", output.String())
			}
			if len(unnamed) != 1 || !strings.Contains(unnamed[0], "45168") {
				t.Errorf("the reason it was left alone was not reported: %v", unnamed)
			}
		})
	}
}

// A channel is written once by default, with no service on it: Mirakurun finds
// the services itself, so a list that does not name them stays right when a
// broadcaster adds or drops one.
func TestAChannelIsWrittenOnceWithNoServiceByDefault(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "BS15_1", Route: "SAT"},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			OriginalNetworkID: 4,
			Services: []isdb.ServiceInfo{
				{ServiceID: 211, Type: 0x01, Name: "first"},
				{ServiceID: 212, Type: 0x01, Name: "second"},
				{ServiceID: 213, Type: 0x01, Name: "third"},
			},
		}},
	}}}

	var collapsed bytes.Buffer
	if _, err := WriteMirakurun(&collapsed, reportValue, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(collapsed.String(), "- name:"); got != 1 {
		t.Errorf("three services on one channel became %d entries:\n%s", got, collapsed.String())
	}
	if strings.Contains(collapsed.String(), "serviceId") {
		t.Errorf("a service was named although none was asked for:\n%s", collapsed.String())
	}
	// Named after the service at the front of the multiplex, which is what a
	// multiplex with no name of its own has always been called here.
	if !strings.Contains(collapsed.String(), `name: "first"`) {
		t.Errorf("the channel is not named after its first service:\n%s", collapsed.String())
	}

	// Asked for, every service is written with its own identifier.
	var each bytes.Buffer
	if _, err := WriteMirakurun(&each, reportValue, MirakurunOptions{ServiceIDs: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(each.String(), "- name:"); got != 3 {
		t.Errorf("three services became %d entries:\n%s", got, each.String())
	}
	for _, expected := range []string{"serviceId: 211", "serviceId: 212", "serviceId: 213"} {
		if !strings.Contains(each.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, each.String())
		}
	}
}

// One carrier can hold more than one channel of the list. A cable carrier with
// a TSMF frame on it multiplexes several broadcast streams, and collapsing to
// one entry per channel must not collapse those together.
func TestOneCarrierCarryingTwoKindsStaysTwoChannels(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C13", Route: "CATV"},
		Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				{OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 211, Type: 0x01, Name: "a satellite service"}},
				}},
				{OriginalNetworkID: 32391, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 32391,
					Services:          []isdb.ServiceInfo{{ServiceID: 1024, Type: 0x01, Name: "a terrestrial one"}},
				}},
			},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"type: BS", "type: GR"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
	if got := strings.Count(output.String(), "- name:"); got != 2 {
		t.Errorf("two kinds on one carrier became %d entries:\n%s", got, output.String())
	}
}

// TestWriteMirakurunUsesAnalyzedServiceNameAndNetworkType: the name comes from
// the SDT and the type from the original network identifier, not from what the
// carrier was called or the lead it arrived on.
func TestWriteMirakurunUsesAnalyzedServiceNameAndNetworkType(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C13", Route: "CATV"},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			OriginalNetworkID: 4,
			Services:          []isdb.ServiceInfo{{ServiceID: 161, Type: 0x01, Name: "ＢＳ放送"}},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{Route: RouteAsScanned, ServiceIDs: true}); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{`name: "ＢＳ放送"`, "type: BS", "route: CATV", `channel: "C13"`, "serviceId: 161"} {
		if !strings.Contains(text, expected) {
			t.Errorf("output does not contain %q:\n%s", expected, text)
		}
	}
}

// A name written with an ideographic space is written with one. Go's own
// string quoting escapes it, which parses back correctly and reads terribly.
func TestYAMLKeepsTheCharactersItWasGiven(t *testing.T) {
	if got := yamlString("テスト　放送"); got != `"テスト　放送"` {
		t.Errorf("yamlString() = %s", got)
	}
	if got := yamlString(`say "hi"\`); got != `"say \"hi\"\\"` {
		t.Errorf("yamlString() = %s", got)
	}
	if got := yamlString("a\nb"); got != `"a\nb"` {
		t.Errorf("yamlString() = %s", got)
	}
}

// A multiplex too wide for one carrier is sent over several, and none of them
// carries a whole service table. Dropping such a carrier for want of a service
// name loses the multiplex, since no carrier of it is any better off.
func TestASplitMultiplexIsWrittenCarrierByCarrier(t *testing.T) {
	split := func(channel string, sequence byte) report.Result {
		return report.Result{
			Entry: channelconfig.Entry{Name: channel,
				Properties: map[string]string{"DELIVERY_SYSTEM": "DVBC/ANNEX_A"}},
			Status: "found",
			Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
				GroupID: 8, NumberOfCarriers: 3, CarrierSequence: sequence,
				RelativeStreams: []isdb.RelativeStreamInfo{
					{RelativeStream: 1, Format: "tlv", StreamID: 45280, OriginalNetworkID: 11,
						TLV: &isdb.TLVInfo{}},
				},
			}},
		}
	}
	var written strings.Builder
	if _, err := WriteMirakurun(&written, report.Report{Results: []report.Result{
		split("1", 1), split("2", 2), split("3", 3),
	}}, MirakurunOptions{GroupIDs: true}); err != nil {
		t.Fatal(err)
	}
	got := written.String()
	for _, channel := range []string{`channel: "1"`, `channel: "2"`, `channel: "3"`} {
		if !strings.Contains(got, channel) {
			t.Errorf("%s is missing:\n%s", channel, got)
		}
	}
	if count := strings.Count(got, "tsmfGroupId: 8"); count != 3 {
		t.Errorf("tsmfGroupId written %d times, want one per carrier:\n%s", count, got)
	}
	// Named by the stream it carries: every carrier of the group holds the
	// same one, and none of them named a service.
	if count := strings.Count(got, `name: "45280"`); count != 3 {
		t.Errorf("the carriers are named %q:\n%s", "45280", got)
	}
}

// The group is written only when it was asked for. Mirakurun reads it out of
// the signal itself, and a value in the file is one it stops re-reading.
func TestTheGroupIsNotWrittenUnasked(t *testing.T) {
	var written strings.Builder
	if _, err := WriteMirakurun(&written, report.Report{Results: []report.Result{{
		Entry: channelconfig.Entry{Name: "1"}, Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			GroupID: 8, NumberOfCarriers: 3, CarrierSequence: 1,
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 1, Format: "tlv", StreamID: 45280, OriginalNetworkID: 11,
					TLV: &isdb.TLVInfo{}},
			},
		}},
	}}}, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(written.String(), "tsmfGroupId") {
		t.Errorf("the group was written unasked:\n%s", written.String())
	}
	// The carrier itself is still written, which is the point of naming it by
	// its stream.
	if !strings.Contains(written.String(), `name: "45280"`) {
		t.Errorf("the carrier was dropped:\n%s", written.String())
	}
}

// A carrier that is not one of several says nothing about a group.
func TestAnUnsplitCarrierWritesNoGroup(t *testing.T) {
	var written strings.Builder
	if _, err := WriteMirakurun(&written, report.Report{Results: []report.Result{{
		Entry: channelconfig.Entry{Name: "C13"}, Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			NumberOfCarriers: 1,
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 1, Format: "tlv", StreamID: 45168, OriginalNetworkID: 11,
					TLV: &isdb.TLVInfo{
						TLVStreamID: 45168, OriginalNetworkID: 11,
						Services: []isdb.ServiceInfo{{ServiceID: 101, Name: "サンプル４Ｋ", Type: 0x01}},
					}},
			},
		}},
	}}}, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(written.String(), "tsmfGroupId") {
		t.Errorf("a single carrier was given a group:\n%s", written.String())
	}
}

// A slot of a multiplexing frame that this list does not write -- a data
// carousel, a download stream -- still occupies the frame, so the stream beside
// it is numbered all the same. Counting the entries rather than the slots asks
// for the whole frame.
func TestAStreamBesideANonTelevisionSlotIsNumbered(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C45"},
		Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 1, OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 101, Type: 0x01, Name: "a service"}},
				}},
				// A download stream: no television service, so no entry of
				// its own, but the frame holds it and the slots are two.
				{RelativeStream: 9, OriginalNetworkID: 4, Transport: &isdb.TransportInfo{
					OriginalNetworkID: 4,
					Services:          []isdb.ServiceInfo{{ServiceID: 98, Type: 0xa4}},
				}},
			},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{RelativeStreams: true}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "- name:"); got != 1 {
		t.Errorf("one television stream became %d entries:\n%s", got, output.String())
	}
	if !strings.Contains(output.String(), "tsmfRelTs: 1") {
		t.Errorf("the stream was written without its place in the frame:\n%s", output.String())
	}
}

// A cable operator's own carriage is written as SKY, the only type left for it,
// but it is not a broadcaster's multiplex: an entry standing for every service
// on the carrier is named after the carrier. Asked for one entry per service,
// the services keep their names.
func TestOperatorCarriageIsNamedAfterItsCarrier(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C32"},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			// 65534, a cable operator's own network.
			OriginalNetworkID: 0xfffe,
			Services: []isdb.ServiceInfo{
				{ServiceID: 1, Type: 0x01, Name: "the first one read"},
				{ServiceID: 2, Type: 0x01, Name: "another on the same carrier"},
			},
		}},
	}}}

	var channel bytes.Buffer
	if _, err := WriteMirakurun(&channel, reportValue, MirakurunOptions{}); err != nil {
		t.Fatal(err)
	}
	want := "- name: \"C32\"\n  type: SKY\n  channel: \"C32\"\n"
	if channel.String() != want {
		t.Errorf("the carrier is not named after itself:\n%s\nwant:\n%s", channel.String(), want)
	}

	var services bytes.Buffer
	if _, err := WriteMirakurun(&services, reportValue, MirakurunOptions{ServiceIDs: true}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`name: "the first one read"`, `name: "another on the same carrier"`} {
		if !strings.Contains(services.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, services.String())
		}
	}
}

// Several streams of a multiplexing frame on an operator's carriage are
// several entries, so the name says which stream as well as which carrier.
func TestOperatorCarriageNamesEachStreamOfAFrame(t *testing.T) {
	service := func(id uint16, name string) *isdb.TransportInfo {
		return &isdb.TransportInfo{
			OriginalNetworkID: 0xfffe,
			Services:          []isdb.ServiceInfo{{ServiceID: id, Type: 0x01, Name: name}},
		}
	}
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "C32"},
		Status: "found",
		Analysis: &isdb.Analysis{TSMF: &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				{RelativeStream: 1, OriginalNetworkID: 0xfffe, Transport: service(1, "first")},
				{RelativeStream: 2, OriginalNetworkID: 0xfffe, Transport: service(2, "second")},
			},
		}},
	}}}
	var output bytes.Buffer
	if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{RelativeStreams: true}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`name: "C32_1"`, `name: "C32_2"`} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}

// The same rule the channel definition files are written under: a carrier of a
// platform that sells its slots one at a time is named after itself, and a
// broadcaster's own multiplex keeps the service name.
func TestOnlyABroadcastMultiplexIsNamedAfterItsService(t *testing.T) {
	for _, carrier := range []struct {
		name      string
		channel   string
		networkID uint16
		want      string
	}{
		{"110 degrees east CS", "ND02", 6, "ND02"},
		{"124/128 degrees east CS", "CH301", 0x000a, "CH301"},
		{"a cable operator's own carriage", "C32", 0xfffe, "C32"},
		{"BS", "BS15_0", 4, "a service"},
		{"terrestrial", "27", 0x7c10, "a service"},
	} {
		t.Run(carrier.name, func(t *testing.T) {
			reportValue := report.Report{Results: []report.Result{{
				Entry:  channelconfig.Entry{Name: carrier.channel},
				Status: "found",
				Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
					OriginalNetworkID: carrier.networkID,
					Services:          []isdb.ServiceInfo{{ServiceID: 1, Type: 0x01, Name: "a service"}},
				}},
			}}}
			var output bytes.Buffer
			if _, err := WriteMirakurun(&output, reportValue, MirakurunOptions{}); err != nil {
				t.Fatal(err)
			}
			if want := fmt.Sprintf("- name: %q\n", carrier.want); !strings.Contains(output.String(), want) {
				t.Errorf("output does not contain %q:\n%s", want, output.String())
			}
		})
	}
}
