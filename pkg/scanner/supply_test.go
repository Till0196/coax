// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"testing"

	"github.com/Till0196/coax/pkg/tuner"
)

// tuneRecorder notes what it was tuned with.
type tuneRecorder struct {
	tuner.Device
	tuned []map[string]string
}

func (device *tuneRecorder) Tune(properties map[string]string) error {
	device.tuned = append(device.tuned, properties)
	return nil
}

func TestTheSupplyGoesOnSatelliteChannelsThatNameNone(t *testing.T) {
	recorder := &tuneRecorder{}
	options := withSatelliteSupply(Options{Device: recorder, SatelliteVoltage: "18"})
	for _, channel := range []map[string]string{
		{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000"},
		{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "VOLTAGE": "OFF"},
		{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473142857"},
	} {
		if err := options.Device.Tune(channel); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"18", "OFF", ""}
	for index, tuned := range recorder.tuned {
		if tuned["VOLTAGE"] != want[index] {
			t.Errorf("channel %d was tuned with VOLTAGE %q, want %q", index, tuned["VOLTAGE"], want[index])
		}
	}
	if twice := withSatelliteSupply(options); twice.Device != options.Device {
		t.Error("wrapping twice put a second layer on")
	}
}
