// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"io"
	"testing"
	"time"
)

// TestDiscoveryAsksForTheSupply: --lnb-power has to reach every satellite
// transponder a discovery tunes, or a dish whose block needs feeding answers
// none of them.
func TestDiscoveryAsksForTheSupply(t *testing.T) {
	device := &recordingDevice{}
	options := Options{Device: device, SatelliteVoltage: "18", LockTimeout: 0, PollInterval: time.Millisecond}
	_, _ = Discover(context.Background(), options, io.Discard, []string{"ISDBS"})
	if len(device.tuned) == 0 {
		t.Fatal("nothing was tuned")
	}
	for _, properties := range device.tuned {
		if properties["VOLTAGE"] != "18" {
			t.Fatalf("a transponder was tuned with VOLTAGE %q, want 18: %v", properties["VOLTAGE"], properties)
		}
	}
}
