// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"testing"

	"github.com/Till0196/coax/pkg/isdb"
)

// A TLV carrier's service table lists every service channel its network
// defines, not the ones on the carrier. Writing them all made each carrier of
// one network export as the same first service.
func TestOnlyTheServicesTheTLVStreamHoldsAreCarried(t *testing.T) {
	var stream carriedStream
	stream.takeTLV(&isdb.TLVInfo{
		TLVStreamID: 45329, OriginalNetworkID: 11,
		CarriedServiceIDs: []uint16{161},
		Services: []isdb.ServiceInfo{
			{ServiceID: 101, Type: 0x01},
			{ServiceID: 161, Type: 0x01},
			{ServiceID: 181, Type: 0x01},
		},
	})
	if len(stream.services) != 1 || stream.services[0].ServiceID != 161 {
		t.Fatalf("services = %#v, want only the one the stream holds", stream.services)
	}
}
