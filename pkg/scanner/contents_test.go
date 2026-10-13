// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"testing"

	"github.com/Till0196/coax/pkg/isdb"
)

// What is being looked for is a slot carrying TLV, which nothing else on the
// line reveals: the carrier is a transport stream whether its slots hold
// transport streams or TLV, so the format beside the byte count says "ts" for
// both.
func TestSlotLineNamesWhatTheSlotDeclares(t *testing.T) {
	for _, test := range []struct {
		name     string
		relative isdb.RelativeStreamInfo
		want     string
	}{
		{
			name:     "a slot carrying TLV, which this exists for",
			relative: isdb.RelativeStreamInfo{RelativeStream: 10, Format: "tlv", StreamID: 45168, OriginalNetworkID: 11},
			want:     "TSMF [#10 tlv TSID=45168 ONID=11]",
		},
		{
			name:     "an ordinary slot",
			relative: isdb.RelativeStreamInfo{RelativeStream: 1, Format: "ts", StreamID: 17488, OriginalNetworkID: 4},
			want:     "TSMF [#1 ts TSID=17488 ONID=4]",
		},
		{
			name:     "a slot with no format read is still shown",
			relative: isdb.RelativeStreamInfo{RelativeStream: 3, StreamID: 100, OriginalNetworkID: 4},
			want:     "TSMF [#3 ? TSID=100 ONID=4]",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := slotLine(test.relative); got != test.want {
				t.Errorf("slotLine() = %q, want %q", got, test.want)
			}
		})
	}
}

// Every slot the frame header names gets a line of its own, in the order the
// header gives them, and a slot with no services still says it is there.
func TestEverySlotGetsItsOwnLine(t *testing.T) {
	lines := channelNotes(&isdb.Analysis{TSMF: &isdb.TSMFInfo{
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, Format: "ts", StreamID: 17488, OriginalNetworkID: 4},
			{RelativeStream: 2, Format: "tlv", StreamID: 45168, OriginalNetworkID: 11},
		},
	}})
	want := []string{
		frameIndent + "TSMF [#1 ts TSID=17488 ONID=4]",
		frameIndent + "TSMF [#2 tlv TSID=45168 ONID=11]",
	}
	if len(lines) != len(want) {
		t.Fatalf("channelNotes() = %#v, want %#v", lines, want)
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Errorf("line %d = %q, want %q", index, lines[index], want[index])
		}
	}
}

// A channel that is not multiplexed has no frame to describe.
func TestAChannelWithNoFrameSaysNothingAboutOne(t *testing.T) {
	if lines := channelNotes(&isdb.Analysis{}); lines != nil {
		t.Errorf("channelNotes() = %#v, want nothing", lines)
	}
	if lines := channelNotes(nil); lines != nil {
		t.Errorf("channelNotes(nil) = %#v, want nothing", lines)
	}
}

// A frame header that left a slot's identifiers at zero is not a slot without
// them: the stream in the slot names itself in its own tables, and a line
// reading "TSID=0" above services that carry a real one is a scan reporting
// what it cannot be tuned by.
func TestASlotTakesItsIdentifiersFromTheStreamWhenTheHeaderHasNone(t *testing.T) {
	line := slotLine(isdb.RelativeStreamInfo{
		RelativeStream: 2, Format: "ts",
		Transport: &isdb.TransportInfo{
			TransportStreamID: 18801, OriginalNetworkID: 4,
			Services: []isdb.ServiceInfo{{ServiceID: 265, Name: "サンプル放送"}},
		},
	})
	if line != "TSMF [#2 ts TSID=18801 ONID=4]" {
		t.Errorf("slotLine() = %q", line)
	}
}

// The header is believed where it said something.
func TestTheFrameHeaderNamesTheSlotWhenItCan(t *testing.T) {
	line := slotLine(isdb.RelativeStreamInfo{
		RelativeStream: 1, Format: "tlv", StreamID: 45168, OriginalNetworkID: 11,
		TLV: &isdb.TLVInfo{TLVStreamID: 45329, OriginalNetworkID: 4},
	})
	if line != "TSMF [#1 tlv TSID=45168 ONID=11]" {
		t.Errorf("slotLine() = %q", line)
	}
}

// A thin slot -- an engineering stream, say -- can go a whole sample without
// its own service table. The network table names it anyway, and saying so beats
// a slot line with nothing under it.
func TestASlotWithNoTableOfItsOwnIsNamedByTheNetworkTable(t *testing.T) {
	analysis := &isdb.Analysis{TSMF: &isdb.TSMFInfo{
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, Format: "ts", StreamID: 16400, OriginalNetworkID: 4,
				Transport: &isdb.TransportInfo{
					TransportStreamID: 16400, OriginalNetworkID: 4,
					Services: []isdb.ServiceInfo{{ServiceID: 151, Name: "サンプル衛星", Type: 0x01}},
					Networks: []isdb.NetworkInfo{{NetworkID: 4, Actual: true, Name: "サンプル新ネットワーク",
						Transports: []isdb.NetworkTransport{
							{TransportStreamID: 16400, OriginalNetworkID: 4},
							{TransportStreamID: 16663, OriginalNetworkID: 4,
								Services: []isdb.NetworkService{{ServiceID: 98, Type: 0xa4}}},
						}}},
				}},
			{RelativeStream: 9, Format: "ts", StreamID: 16663, OriginalNetworkID: 4,
				Transport: &isdb.TransportInfo{TransportStreamID: 16663}},
		},
	}}
	lines := channelNotes(analysis)
	want := []string{
		frameIndent + "TSMF [#1 ts TSID=16400 ONID=4]",
		streamIndent + "サンプル新ネットワーク  TSID=16400  ONID=4",
		serviceIndent + "151  サンプル衛星  (TV)",
		frameIndent + "TSMF [#9 ts TSID=16663 ONID=4]",
		streamIndent + "サンプル新ネットワーク  TSID=16663  ONID=4  (from the network table)",
		serviceIndent + "98  (unnamed)  (engineering)",
	}
	if len(lines) != len(want) {
		t.Fatalf("channelNotes() = %#v, want %#v", lines, want)
	}
	for index := range want {
		if lines[index] != want[index] {
			t.Errorf("line %d = %q, want %q", index, lines[index], want[index])
		}
	}
}

// A stream the network table does not describe either says nothing more than
// that it is there.
func TestASlotNothingDescribesKeepsItsLineAlone(t *testing.T) {
	analysis := &isdb.Analysis{TSMF: &isdb.TSMFInfo{
		RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 9, Format: "ts", StreamID: 16663, OriginalNetworkID: 4},
		},
	}}
	if lines := channelNotes(analysis); len(lines) != 1 {
		t.Errorf("channelNotes() = %#v, want the slot line alone", lines)
	}
}
