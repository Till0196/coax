// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import "testing"

func TestBuildTuneProfile(t *testing.T) {
	tests := []struct {
		name       string
		properties map[string]string
		want       TuneProfile
	}{
		{
			name: "terrestrial frequency in Hz",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "ISDB-T", "FREQUENCY": "557142857", "BANDWIDTH_HZ": "6000000",
			},
			want: TuneProfile{Delivery: DeliveryTerrestrial, System: "ISDBT", FrequencyKHz: 557142, BandwidthMHz: 6, SymbolRate: -1, StreamID: -1},
		},
		{
			name: "satellite IF in kHz",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1164560", "STREAM_ID": "45168",
			},
			want: TuneProfile{Delivery: DeliverySatellite, System: "ISDBS", FrequencyKHz: 1164560, BandwidthMHz: -1, SymbolRate: -1, StreamID: 45168},
		},
		{
			name: "J.83 Annex B",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVB-C/ANNEX-B", "FREQUENCY": "111000000", "SYMBOL_RATE": "5360537", "MODULATION": "QAM/64",
			},
			want: TuneProfile{Delivery: DeliveryCable, System: "DVBCANNEXB", FrequencyKHz: 111000, BandwidthMHz: -1, SymbolRate: 5360537, StreamID: -1, Modulation: "QAM64"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := BuildTuneProfile(test.properties)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("BuildTuneProfile() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSupports(t *testing.T) {
	for _, system := range []string{"ISDB-T", "ISDB-S", "ISDB-S3", "DVB-C/ANNEX-B", "DVB-S2"} {
		if !Supports(system) {
			t.Errorf("Supports(%q) = false", system)
		}
	}
	if Supports("UNDEFINED") {
		t.Error("Supports(UNDEFINED) = true")
	}
}

// TestEverySpellingOfACableChannelIsAccepted checks that every spelling of a
// cable delivery system normalises to DeliveryCable, that the non-cable systems
// this backend tunes are still supported, and that ATSC is refused.
//
// dvbv5 writes "DVBC/ANNEX_A", and that spelling was once refused as unsupported
// -- on a TBS 6205SE that receives exactly those channels -- because the
// underscore survived normalisation and matched nothing.
func TestEverySpellingOfACableChannelIsAccepted(t *testing.T) {
	cable := []string{
		"DVBC/ANNEX_A", "DVB-C/ANNEX_A", "DVBC/ANNEXA", "dvbc_annex_a",
		"DVBC/ANNEX_B", "DVB-C/ANNEX_C", "ISDB-C", "ISDBC", "DVB-C", "J.83B",
	}
	for _, system := range cable {
		if !Supports(system) {
			t.Errorf("%q was refused; it names a cable channel this backend tunes", system)
			continue
		}
		profile, err := BuildTuneProfile(map[string]string{
			"DELIVERY_SYSTEM": system,
			"FREQUENCY":       "111000000",
			"SYMBOL_RATE":     "5274000",
		})
		if err != nil {
			t.Errorf("%q: %v", system, err)
			continue
		}
		if profile.Delivery != DeliveryCable {
			t.Errorf("%q was read as delivery %v, want cable", system, profile.Delivery)
		}
	}

	for _, system := range []string{"ISDB-T", "DVB-T2", "ISDB-S3", "DVB-S2"} {
		if !Supports(system) {
			t.Errorf("%q was refused", system)
		}
	}
	if Supports("ATSC") {
		t.Error("ATSC was accepted; this backend builds no tune request for it")
	}
}
