// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbio

import "testing"

// descriptors carries two interfaces, so the walk has to skip one.
var descriptors = []byte{
	// configuration: length 9, type 2, total 46, 2 interfaces
	9, descriptorTypeConfiguration, 46, 0, 2, 1, 0, 0x80, 50,
	// interface 0, alternate 0, 1 endpoint
	9, descriptorTypeInterface, 0, 0, 1, 0xff, 0, 0, 0,
	// endpoint 0x83, interrupt, 64 bytes
	7, descriptorTypeEndpoint, 0x83, 0x03, 64, 0, 1,
	// interface 1, alternate 0, 2 endpoints
	9, descriptorTypeInterface, 1, 0, 2, 0xff, 0, 0, 0,
	// endpoint 0x81, bulk, 512 bytes
	7, descriptorTypeEndpoint, 0x81, 0x02, 0, 2, 0,
	// endpoint 0x02, bulk, 512 bytes
	7, descriptorTypeEndpoint, 0x02, 0x02, 0, 2, 0,
}

func TestParseEndpointsSelectsInterface(t *testing.T) {
	got, err := parseEndpoints(descriptors, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []Endpoint{
		{Address: 0x81, Type: TransferBulk, MaxPacketSize: 512},
		{Address: 0x02, Type: TransferBulk, MaxPacketSize: 512},
	}
	if len(got) != len(want) {
		t.Fatalf("parseEndpoints = %#v; want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("endpoint %d = %#v; want %#v", index, got[index], want[index])
		}
	}
	if !got[0].In() || got[1].In() {
		t.Errorf("direction: %v, %v", got[0].In(), got[1].In())
	}
}

func TestParseEndpointsOtherInterface(t *testing.T) {
	got, err := parseEndpoints(descriptors, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Address != 0x83 || got[0].Type != TransferInterrupt {
		t.Fatalf("parseEndpoints = %#v", got)
	}
}

func TestParseEndpointsMissingInterface(t *testing.T) {
	if _, err := parseEndpoints(descriptors, 7); err == nil {
		t.Fatal("expected an error for an interface that is not present")
	}
}
