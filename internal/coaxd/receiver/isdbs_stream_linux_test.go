// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import "testing"

func TestISDBSStreamsAreReadByDriver(t *testing.T) {
	cases := []struct {
		driver, frontend string
		want             isdbsStreams
	}{
		{"ddbridge", "M4", isdbsMarkedIdentifiers},
		{"ddbridge", "M_S", isdbsMarkedIdentifiers},
		{"ddbridge", "M", isdbsMarkedIdentifiers},
		{"earth_pt3", "Toshiba TC90522 ISDB-S module", isdbsIdentifiersOnly},
		{"earth-pt1", "Toshiba TC90522 ISDB-S module", isdbsIdentifiersOnly},
		{"em28xx", "Toshiba TC90522 ISDB-S module", isdbsIdentifiersOnly},
		{"", "Socionext MN88443x", isdbsIdentifiersOnly},
		{"TBSECP3 driver", "Sony CXD2857 ISDB-S/S3/T/C demodulator", isdbsStreamsAsGiven},
		{"", "", isdbsStreamsAsGiven},
	}
	for _, c := range cases {
		if got := isdbsStreamsFor(c.driver, c.frontend, false); got != c.want {
			t.Errorf("isdbsStreamsFor(%q, %q) = %v, want %v", c.driver, c.frontend, got, c.want)
		}
	}
}

// --dvb-relative-streams hands a place to the TC90522 and MN88443x drivers as
// it is, and changes nothing for the others.
func TestRelativeStreamsPassThePlaceToTheIdentifierOnlyDrivers(t *testing.T) {
	cases := []struct {
		driver, frontend string
		want             isdbsStreams
	}{
		{"earth_pt3", "Toshiba TC90522 ISDB-S module", isdbsStreamsAsGiven},
		{"", "Socionext MN88443x", isdbsStreamsAsGiven},
		{"ddbridge", "M4", isdbsMarkedIdentifiers},
		{"", "", isdbsStreamsAsGiven},
	}
	for _, c := range cases {
		if got := isdbsStreamsFor(c.driver, c.frontend, true); got != c.want {
			t.Errorf("isdbsStreamsFor(%q, %q, true) = %v, want %v", c.driver, c.frontend, got, c.want)
		}
	}
}

func TestISDBSStreamIDIsWrittenTheWayTheDriverReadsIt(t *testing.T) {
	const tc90522 = "Toshiba TC90522 ISDB-S module"
	cases := []struct {
		kind     isdbsStreams
		system   string
		streamID string
		want     string
		refused  bool
	}{
		// ddbridge takes a place as it is and an identifier only when marked.
		{isdbsMarkedIdentifiers, "ISDBS", "0", "0", false},
		{isdbsMarkedIdentifiers, "ISDB-S", "2", "2", false},
		{isdbsMarkedIdentifiers, "ISDBS", "16625", "82161", false},
		{isdbsMarkedIdentifiers, "ISDBS", "0x4973", "84339", false},
		{isdbsMarkedIdentifiers, "ISDBS", "82161", "82161", false},
		{isdbsMarkedIdentifiers, "ISDBS", "8", "", true},
		{isdbsMarkedIdentifiers, "ISDBS", "11", "", true},
		{isdbsMarkedIdentifiers, "ISDBS3", "45328", "45328", false},
		// The TC90522 has no way to choose by place, but falls back to the
		// first stream, which is place zero.
		{isdbsIdentifiersOnly, "ISDBS", "1", "", true},
		{isdbsIdentifiersOnly, "ISDBS", "0", "0", false},
		{isdbsIdentifiersOnly, "ISDBS", "16401", "16401", false},
		{isdbsIdentifiersOnly, "ISDBT", "1", "1", false},
		{isdbsStreamsAsGiven, "ISDBS", "1", "1", false},
	}
	for _, c := range cases {
		properties := map[string]string{"DELIVERY_SYSTEM": c.system, "FREQUENCY": "1049480", "STREAM_ID": c.streamID}
		got, err := isdbsStreamProperties(c.kind, tc90522, properties)
		if c.refused {
			if err == nil {
				t.Errorf("%v %s STREAM_ID %s should have been refused, got %q", c.kind, c.system, c.streamID, got["STREAM_ID"])
			}
			continue
		}
		if err != nil {
			t.Errorf("%v %s STREAM_ID %s: %v", c.kind, c.system, c.streamID, err)
			continue
		}
		if got["STREAM_ID"] != c.want {
			t.Errorf("%v %s STREAM_ID %s = %q, want %q", c.kind, c.system, c.streamID, got["STREAM_ID"], c.want)
		}
		if properties["STREAM_ID"] != c.streamID {
			t.Errorf("%v %s STREAM_ID %s: the caller's map was changed to %q", c.kind, c.system, c.streamID, properties["STREAM_ID"])
		}
	}
	// A channel naming no stream is left for the driver to choose.
	properties := map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480"}
	if got, err := isdbsStreamProperties(isdbsIdentifiersOnly, tc90522, properties); err != nil || len(got) != 2 {
		t.Errorf("no STREAM_ID: %v, %v", got, err)
	}
}
