// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"math"
	"testing"
)

func rate(value float64) *float64 { return &value }

// Scaling the slots up to the frame is what makes one channel comparable with
// the next: two multiplexes filling different numbers of slots still run at
// whatever their modulation gives them.
//
// The numbers are from a cable feed measured on a Digital Devices MAX M4:
// channel 13 fills 48 slots at 26.41 Mbps and runs at 64QAM, while C14 fills 52
// at 38.04 and runs at 256QAM.
func TestCarrierRateScalesTheSlotsUpToTheFrame(t *testing.T) {
	for _, test := range []struct {
		name      string
		relatives []RelativeStreamInfo
		want      float64
	}{
		{
			name:      "64QAM, 48 of 52 slots used",
			relatives: []RelativeStreamInfo{{SlotCount: 48, BitrateMbps: rate(26.41)}},
			want:      29.16,
		},
		{
			name:      "256QAM, 52 of 52 slots used",
			relatives: []RelativeStreamInfo{{SlotCount: 52, BitrateMbps: rate(38.04)}},
			want:      38.77,
		},
		{
			name: "several slots add up before they are scaled",
			relatives: []RelativeStreamInfo{
				{SlotCount: 24, BitrateMbps: rate(13.2)},
				{SlotCount: 24, BitrateMbps: rate(13.2)},
			},
			want: 29.15,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := carrierRate(test.relatives)
			if got == nil {
				t.Fatal("a measured slot must give a carrier rate")
			}
			if math.Abs(*got-test.want) > 0.01 {
				t.Errorf("carrierRate() = %v, want %v", *got, test.want)
			}
		})
	}
}

// A multiplex whose slots all carry something this program cannot time has no
// rate to report and must not be given one: a channel shown at 0.01 Mbps reads
// as a channel in trouble.
func TestCarrierRateSaysNothingWithoutAMeasuredSlot(t *testing.T) {
	if got := carrierRate(nil); got != nil {
		t.Errorf("no slots gave %v", *got)
	}
	if got := carrierRate([]RelativeStreamInfo{{SlotCount: 52}}); got != nil {
		t.Errorf("an unmeasured slot gave %v", *got)
	}
	if got := carrierRate([]RelativeStreamInfo{{BitrateMbps: rate(30)}}); got != nil {
		t.Errorf("a measurement with no slots to scale by gave %v", *got)
	}
}

// A TLV rate counts the bytes lifted out of the packets; a transport stream's
// counts the packets. Added together unscaled, they understate the carrier by
// whatever the packet headers cost.
func TestInSlotTermsPutsTLVOnTheSameFooting(t *testing.T) {
	// 1000 packets carried 180000 bytes of TLV, so each 188-byte packet spent
	// 180 bytes on payload: the rate in packet terms is 188/180 of it.
	got := inSlotTerms(rate(180), 1000, 180000)
	if got == nil {
		t.Fatal("a measured TLV rate must scale")
	}
	if want := 180.0 * 188 / 180; math.Abs(*got-want) > 0.01 {
		t.Errorf("inSlotTerms() = %v, want %v", *got, want)
	}
	if got := inSlotTerms(nil, 1000, 180000); got != nil {
		t.Errorf("an unmeasured rate scaled to %v", *got)
	}
	if got := inSlotTerms(rate(180), 0, 180000); got != nil {
		t.Errorf("no carrying packets scaled to %v", *got)
	}
}
