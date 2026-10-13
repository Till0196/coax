// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
)

// tableOn is a network table for a fake walk: the multiplexes it names, as
// frequency and identifier.
func tableOn(places ...[2]int) []channelconfig.Entry {
	var entries []channelconfig.Entry
	for _, place := range places {
		entries = append(entries, channelconfig.Entry{
			Name: strconv.Itoa(place[1]),
			Properties: map[string]string{
				"FREQUENCY": strconv.Itoa(place[0]),
				"STREAM_ID": strconv.Itoa(place[1]),
			},
		})
	}
	return entries
}

// fakePass walks three transponders whose tables are given, noting which were
// read.
func fakePass(tables map[string][]channelconfig.Entry, read *[]string) satellitePass {
	return satellitePass{
		system: "ISDBS",
		what:   "BS",
		on: []transponder{
			{name: "BS01", kHz: 1049480},
			{name: "BS03", kHz: 1087840},
			{name: "BS15", kHz: 1318000},
		},
		networks: 1,
		read: func(_ context.Context, _ Options, on transponder, _ string) ([]channelconfig.Entry, uint16, error) {
			*read = append(*read, on.name)
			return tables[on.name], 4, nil
		},
	}
}

// Two transponders that agree settle the network: the rest are not read.
func TestTwoAgreeingTablesSettleTheNetwork(t *testing.T) {
	same := tableOn([2]int{1049480, 0x4010}, [2]int{1087840, 0x4030}, [2]int{1318000, 0x40f1})
	var read []string
	pass := fakePass(map[string][]channelconfig.Entry{"BS01": same, "BS03": same, "BS15": same}, &read)
	found := discover(context.Background(), Options{}, newDiscoveryDisplay(nil), pass)
	if strings.Join(read, ",") != "BS01,BS03" {
		t.Errorf("read %v, want the first table and one more", read)
	}
	if len(found) != 3 {
		t.Errorf("found %d entries, want the three both tables named", len(found))
	}
}

// A table that disagrees, when one broadcaster's uplink sent
// stale channel information -- makes the walk read every transponder, scan
// everything any table named, and say which table said what.
func TestADisagreeingTableMakesTheWalkReadEverything(t *testing.T) {
	stale := tableOn([2]int{1049480, 0x4010}, [2]int{1087840, 0x4030}, [2]int{1318000, 0x40f2})
	right := tableOn([2]int{1049480, 0x4010}, [2]int{1087840, 0x4030}, [2]int{1318000, 0x48f3})
	var read []string
	pass := fakePass(map[string][]channelconfig.Entry{"BS01": stale, "BS03": right, "BS15": right}, &read)
	var said bytes.Buffer
	found := discover(context.Background(), Options{}, newDiscoveryDisplay(&said), pass)
	if strings.Join(read, ",") != "BS01,BS03,BS15" {
		t.Errorf("read %v, want every transponder", read)
	}
	named := map[string]bool{}
	for _, entry := range found {
		named[entry.Properties["STREAM_ID"]] = true
	}
	for _, want := range []int{0x40f2, 0x48f3} {
		if !named[strconv.Itoa(want)] {
			t.Errorf("%#x is not among what is scanned", want)
		}
	}
	if !strings.Contains(said.String(), "disagrees") {
		t.Errorf("the disagreement was not written out: %q", said.String())
	}
}
