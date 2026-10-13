// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

func TestChannelTypeForNetwork(t *testing.T) {
	for networkID, want := range map[uint16]string{
		0x0004: "BS",
		0x0006: "CS",
		0x0007: "CS",
		0x000a: "SKY",
		0x000b: "BS4K",
		0x000c: "CS4K",
		0x7880: "GR",
		// A cable operator's own carriage names itself at the top of the
		// range, from 0xfff7 up.
		0xfff7: "CATV",
		0xfff8: "CATV",
		0xfffb: "CATV",
		0xfffc: "CATV",
		0xffff: "CATV",
		// Just below the block is not in it, and is nothing else this knows,
		// so it is OTHER. (mirakurunChannelType is what later folds OTHER
		// onto SKY, Mirakurun's catch-all.)
		0xfff6: "OTHER",
		0x0000: "",
	} {
		if got := channelTypeForNetwork(networkID); got != want {
			t.Errorf("network %#04x type = %q, want %q", networkID, got, want)
		}
	}
}

// TestMirakurunChannelTypeFoldsWhatItHasNoNameFor: this program tells apart more
// types than Mirakurun accepts, and the folding happens where the two meet
// rather than by giving up the distinction earlier.
func TestMirakurunChannelTypeFoldsWhatItHasNoNameFor(t *testing.T) {
	for internal, want := range map[string]string{
		"CS4K":  "BS4K",
		"CATV":  "SKY",
		"OTHER": "SKY",
		"BS":    "BS",
		"CS":    "CS",
		"BS4K":  "BS4K",
		"GR":    "GR",
		"SKY":   "SKY",
		"":      "",
	} {
		if got := mirakurunChannelType(internal); got != want {
			t.Errorf("mirakurunChannelType(%q) = %q, want %q", internal, got, want)
		}
	}
}

func TestNetworkIDDeterminesTypeIndependentlyOfTransport(t *testing.T) {
	tlvResult := report.Result{
		Entry: channelconfig.Entry{Name: "tlv", Route: "SAT"}, Status: "found",
		Analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{OriginalNetworkID: 0x0004, Services: []isdb.ServiceInfo{{ServiceID: 1, Type: 0x01}}}},
	}
	tsResult := report.Result{
		Entry: channelconfig.Entry{Name: "ts", Route: "SAT"}, Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{OriginalNetworkID: 0x000a, Services: []isdb.ServiceInfo{{ServiceID: 2, Type: 0xa5}}}},
	}
	if got := candidates(tlvResult); len(got) != 1 || got[0].Type != "BS" {
		t.Fatalf("TLV with network 0x0004 = %#v", got)
	}
	if got := candidates(tsResult); len(got) != 1 || got[0].Type != "SKY" {
		t.Fatalf("TS with network 0x000a = %#v", got)
	}
	if got := candidates(report.Result{Entry: channelconfig.Entry{Name: "unknown"}, Status: "found"}); len(got) != 0 {
		t.Fatalf("result without a network ID was exported: %#v", got)
	}
}
