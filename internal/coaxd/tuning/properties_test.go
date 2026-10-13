// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"maps"
	"slices"
	"testing"
)

// A key the Linux DVB backend learns and this set does not would be filtered
// out before it ever reached the backend, looking like the property being
// ignored rather than unimplemented. The backend's list is restated rather than
// imported, so this package stays off that one platform.
func TestTuningPropertiesMatchTheLinuxBackend(t *testing.T) {
	// dvb.BuildTuneProperties seeds DELIVERY_SYSTEM and then walks orderedKeys.
	backend := []string{
		"DELIVERY_SYSTEM",
		"FREQUENCY", "MODULATION", "BANDWIDTH_HZ", "SYMBOL_RATE", "INVERSION",
		"INNER_FEC", "CODE_RATE_HP", "CODE_RATE_LP", "TRANSMISSION_MODE",
		"GUARD_INTERVAL", "HIERARCHY", "VOLTAGE", "POLARIZATION", "TONE",
		"PILOT", "ROLLOFF", "STREAM_ID", GainProperty, LNAProperty,
		// Not a frontend property: the backend takes it and passes it over,
		// because a channel that named its low-noise block has to carry that
		// name as far as the backends whose drivers do the downconversion.
		LNBProperty,
		// Nor this one: it is a message put on the lead before the tune
		// rather than anything the demodulator is told. See diseqc.go.
		SatNumberProperty,
	}
	slices.Sort(backend)
	ours := slices.Sorted(maps.Keys(TuningProperties))
	if !slices.Equal(ours, backend) {
		t.Errorf("the tuning property set has drifted from the Linux backend's\n ours: %v\n dvb:  %v", ours, backend)
	}
}

func TestSelectTuningLeavesServiceKeysBehind(t *testing.T) {
	// What a dvbv5-zap file for one service looks like: tuning parameters and
	// the identifiers of a programme inside the multiplex, in one section.
	section := map[string]string{
		"DELIVERY_SYSTEM": "ISDBT",
		"FREQUENCY":       "557142857",
		"BANDWIDTH_HZ":    "6000000",
		"SERVICE_ID":      "1024",
		"VIDEO_PID":       "273",
		"AUDIO_PID":       "274",
	}
	selected, ignored := SelectTuning(section)

	for _, key := range []string{"DELIVERY_SYSTEM", "FREQUENCY", "BANDWIDTH_HZ"} {
		if selected[key] != section[key] {
			t.Errorf("%s did not survive selection", key)
		}
	}
	if len(selected) != 3 {
		t.Errorf("selected %d properties, want 3: %v", len(selected), selected)
	}
	if !slices.Equal(ignored, []string{"AUDIO_PID", "SERVICE_ID", "VIDEO_PID"}) {
		t.Errorf("reported %v as left behind", ignored)
	}
}
