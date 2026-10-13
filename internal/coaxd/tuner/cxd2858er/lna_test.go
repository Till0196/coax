// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2858er

import "testing"

// TestSetLNAMovesBothInputs: the switch covers both amplifiers, not whichever
// one is live -- the setting arrives with a channel, and that channel may be the
// one that changes which input is live.
func TestSetLNAMovesBothInputs(t *testing.T) {
	tuner := New(nil, nil, 0xc0, Config{TerrestrialLNA: true, SatelliteLNA: true})
	tuner.SetLNA(false)
	if tuner.config.TerrestrialLNA || tuner.config.SatelliteLNA {
		t.Errorf("after SetLNA(false) the amplifiers read terrestrial=%v satellite=%v",
			tuner.config.TerrestrialLNA, tuner.config.SatelliteLNA)
	}
	tuner.SetLNA(true)
	if !tuner.config.TerrestrialLNA || !tuner.config.SatelliteLNA {
		t.Errorf("after SetLNA(true) the amplifiers read terrestrial=%v satellite=%v",
			tuner.config.TerrestrialLNA, tuner.config.SatelliteLNA)
	}
}
