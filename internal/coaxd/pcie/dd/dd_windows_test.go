// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"testing"
)

func TestSearchCommands(t *testing.T) {
	for _, test := range []struct {
		name     string
		channel  map[string]string
		command  byte
		wantFreq uint32
	}{
		{"ISDB-S in kilohertz",
			map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "16400"},
			cmdSearchISDBS, 1049480000},
		{"ISDB-T in hertz",
			map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "521142857", "BANDWIDTH_HZ": "6000000"},
			cmdSearchISDBT, 521142000},
		{"DVB-S2 with a symbol rate",
			map[string]string{"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1318000", "SYMBOL_RATE": "28860"},
			cmdSearchDVBS, 1318000000},
	} {
		command, err := searchFor(test.channel)
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if command.command != test.command {
			t.Errorf("%s: command = %#02x, want %#02x", test.name, command.command, test.command)
		}
		if got := decodeUint32(command.params[searchFrequency:]); got != test.wantFreq {
			t.Errorf("%s: frequency = %d Hz, want %d", test.name, got, test.wantFreq)
		}
	}
}

// TestFamilyOf checks the network family read out of a filter's name.
func TestFamilyOf(t *testing.T) {
	for name, want := range map[string]family{
		"Digital Devices DVB-T Tuner 1":   familyTerrestrial,
		"Digital Devices DVB-T2 Tuner 2":  familyTerrestrial,
		"Digital Devices ISDB-T Tuner 3":  familyTerrestrial,
		"Digital Devices DVB-C2 Tuner 1":  familyTerrestrial,
		"Digital Devices DVB-C Tuner 4":   familyCable,
		"Digital Devices ISDB-C Tuner 1":  familyCable,
		"Digital Devices J83-B Tuner 2":   familyCable,
		"Digital Devices DVB-S2 Tuner 1":  familySatellite,
		"Digital Devices DVB-S2X Tuner 1": familySatellite,
		"Digital Devices ISDB-S Tuner 4":  familySatellite,
		"Some Other Tuner 1":              familyUnknown,
	} {
		if got := familyOf(name); got != want {
			t.Errorf("familyOf(%q) = %d, want %d", name, got, want)
		}
	}

}

// TestDemodOf pins the off-by-one between the name and the command interface.
func TestDemodOf(t *testing.T) {
	for name, want := range map[string]byte{
		"Digital Devices DVB-T2 Tuner 1": 0,
		"Digital Devices DVB-S2 Tuner 4": 3,
		"Digital Devices J83-B Tuner 2":  1,
	} {
		if got := demodOf(name); got != want {
			t.Errorf("demodOf(%q) = %d, want %d", name, got, want)
		}
	}
}

// TestFaceDeliveries checks which delivery systems each filter is offered for,
// given what the card itself lists.
func TestFaceDeliveries(t *testing.T) {
	card := []string{
		"ISDB-T", "ISDB-S", "ISDB-C",
		"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
		"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C", "DVB-C2",
	}
	for name, want := range map[string][]string{
		"Digital Devices ISDB-T Tuner 1": {"ISDB-T"},
		"Digital Devices ISDB-S Tuner 2": {"ISDB-S"},
		"Digital Devices ISDB-C Tuner 3": {"ISDB-C", "DVB-C", "DVB-C/ANNEX_C"},
		"Digital Devices J83-B Tuner 4":  {"DVB-C/ANNEX_B"},
		"Digital Devices DVB-T Tuner 1":  {"ISDB-T", "DVB-T"},
		"Digital Devices DVB-T2 Tuner 1": {"ISDB-T", "DVB-T", "DVB-T2"},
		"Digital Devices DVB-S2 Tuner 1": {"DVB-S", "DVB-S2"},
		"Digital Devices DVB-C Tuner 1":  {"ISDB-C", "DVB-C", "DVB-C/ANNEX_C"},
		"Digital Devices DVB-C2 Tuner 1": {"DVB-C2"},
		"Some Other Tuner 1":             nil,
	} {
		got := faceDeliveries(name, card)
		if len(got) != len(want) {
			t.Errorf("faceDeliveries(%q) = %v, want %v", name, got, want)
			continue
		}
		for index := range want {
			if got[index] != want[index] {
				t.Errorf("faceDeliveries(%q) = %v, want %v", name, got, want)
				break
			}
		}
	}
	// A cable filter answers to the standard's long name as well as the short
	// one the card's own list uses.
	if !faceOffers("DVB-C", "DVB-C/ANNEX_A") {
		t.Error("the DVB-C filter should offer Annex A")
	}
	if faceOffers("DVB-C", "DVB-C/ANNEX_B") {
		t.Error("the DVB-C filter should not offer Annex B; that is the J83-B filter's")
	}
}

func TestM4StandardsDoNotDependOnPublishedFilters(t *testing.T) {
	card, ok := modelFor(0x0050dd01)
	if !ok {
		t.Fatal("MAX M4 is missing")
	}
	device := &Device{model: card, label: "DVB-T2", faces: map[string]string{
		"DVB-T2": "terrestrial", "DVB-C": "cable", "DVB-S2": "satellite",
	}}
	for _, system := range []string{"ISDBT", "ISDBC", "ISDBS", "DVBC/ANNEX_A", "DVBC/ANNEX_C", "DVBC/ANNEX_B", "DVBC2"} {
		if !device.Supports(system) {
			t.Errorf("MAX M4 lost %s because its filter is not published", system)
		}
	}
	for _, system := range []string{"ATSC", "ISDBS3", "unknown"} {
		if device.Supports(system) {
			t.Errorf("MAX M4 advertises unsupported %s", system)
		}
	}
}
