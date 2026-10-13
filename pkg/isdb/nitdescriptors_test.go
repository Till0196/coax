// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "testing"

// The rest of what ARIB STD-B10 Table 6-1 allows in a network information
// table, each against bytes laid out as the standard writes it, and each
// against a body that ran out part way -- a descriptor that did not arrive
// whole must not become half a value.

func TestLinkagePointsAtAService(t *testing.T) {
	got := ParseLinkage([]byte{0x40, 0x11, 0x00, 0x04, 0x04, 0x00, 0x01, 0xaa})
	if got == nil {
		t.Fatal("refused")
	}
	if got.TransportStreamID != 0x4011 || got.OriginalNetworkID != 4 ||
		got.ServiceID != 0x0400 || got.LinkageType != 1 {
		t.Errorf("read %+v", *got)
	}
	if len(got.PrivateData) != 1 || got.PrivateData[0] != 0xaa {
		t.Errorf("private data %v", got.PrivateData)
	}
	if ParseLinkage([]byte{0x40, 0x11, 0x00, 0x04, 0x04, 0x00}) != nil {
		t.Error("a truncated descriptor was read")
	}
}

// Its presence is the signal: the descriptor is sent only while an emergency
// alarm broadcast is being made.
func TestEmergencyInformationNamesTheAreas(t *testing.T) {
	// One service, alarm started, level 0, two area codes.
	got := ParseEmergencyInformation([]byte{
		0x04, 0x00, 0x80, 0x04, 0x01, 0x20, 0x03, 0x40,
	})
	if got == nil || len(got.Services) != 1 {
		t.Fatalf("read %+v", got)
	}
	one := got.Services[0]
	if one.ServiceID != 0x0400 || !one.Started || one.SignalLevel != 0 {
		t.Errorf("read %+v", one)
	}
	if len(one.AreaCodes) != 2 || one.AreaCodes[0] != 0x012 || one.AreaCodes[1] != 0x034 {
		t.Errorf("area codes %v", one.AreaCodes)
	}
	// The end of the alarm is the same descriptor with the flag cleared.
	ended := ParseEmergencyInformation([]byte{0x04, 0x00, 0x00, 0x00})
	if ended == nil || ended.Services[0].Started {
		t.Errorf("the end of an alarm read as its start: %+v", ended)
	}
	// An area list longer than what arrived is a descriptor cut short.
	if got := ParseEmergencyInformation([]byte{0x04, 0x00, 0x80, 0x08, 0x01}); got != nil {
		t.Errorf("a truncated area list was read: %+v", got)
	}
	if ParseEmergencyInformation(nil) != nil {
		t.Error("an empty descriptor was read")
	}
}

// Two multiplexes sharing a group are halves of one thing, which is what a
// scan cannot work out from either of them alone.
func TestConnectedTransmissionNamesItsGroup(t *testing.T) {
	got := ParseConnectedTransmission([]byte{0x12, 0x34, 0x6c, 0xff})
	if got == nil {
		t.Fatal("refused")
	}
	// 0x6c is 01 10 11 00: segment type 1, and the three layers 2, 3, 0.
	if got.GroupID != 0x1234 || got.SegmentType != 1 ||
		got.ModulationA != 2 || got.ModulationB != 3 || got.ModulationC != 0 {
		t.Errorf("read %+v", *got)
	}
	if len(got.Extra) != 1 {
		t.Errorf("the trailing byte was lost: %v", got.Extra)
	}
	if ParseConnectedTransmission([]byte{0x12, 0x34}) != nil {
		t.Error("a truncated descriptor was read")
	}
}

func TestServiceGroupPairsServices(t *testing.T) {
	got := ParseServiceGroup([]byte{0x10, 0x04, 0x00, 0x04, 0x01})
	if got == nil || got.GroupType != 1 || len(got.Pairs) != 1 {
		t.Fatalf("read %+v", got)
	}
	if got.Pairs[0].PrimaryServiceID != 0x0400 || got.Pairs[0].SecondaryServiceID != 0x0401 {
		t.Errorf("pair %+v", got.Pairs[0])
	}
	// Any other type carries private data, which is kept rather than read as
	// pairs it is not.
	other := ParseServiceGroup([]byte{0x20, 0xde, 0xad})
	if other == nil || other.GroupType != 2 || len(other.Pairs) != 0 ||
		len(other.PrivateData) != 2 {
		t.Errorf("read %+v", other)
	}
	if ParseServiceGroup(nil) != nil {
		t.Error("an empty descriptor was read")
	}
}

func TestAreaBroadcastingNamesItsStations(t *testing.T) {
	got := ParseAreaBroadcastingInformation([]byte{
		0x01, 0x00, 0x12, 0x34, 0x56, 0x78, 0x02, 0x01, 0x99,
	})
	if got == nil || len(got.Stations) != 1 {
		t.Fatalf("read %+v", got)
	}
	one := got.Stations[0]
	if one.StationID != 0x001234 || one.LocationCode != 0x5678 || one.SignalFormat != 2 {
		t.Errorf("read %+v", one)
	}
	if len(one.Extra) != 1 || one.Extra[0] != 0x99 {
		t.Errorf("the station's own bytes were lost: %v", one.Extra)
	}
	// A count larger than what arrived is a descriptor cut short: what did
	// arrive stands, and nothing is invented for the rest.
	if got := ParseAreaBroadcastingInformation([]byte{0x02, 0x00, 0x12}); got != nil {
		t.Errorf("a truncated station list was read: %+v", got)
	}
	if ParseAreaBroadcastingInformation(nil) != nil {
		t.Error("an empty descriptor was read")
	}
}
