// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mxl136

import "testing"

func TestTuneData(t *testing.T) {
	t.Parallel()
	data, err := tuneData(557143)
	if err != nil {
		t.Fatal(err)
	}
	digital := uint64(557143<<3) / 125
	if data[5] != byte(digital) || data[7] != byte(digital>>8) {
		t.Fatalf("frequency bytes = %02x:%02x; want %02x:%02x", data[7], data[5], byte(digital>>8), byte(digital))
	}
	if data[17] != 0x41 {
		t.Fatalf("UHF band = %#02x; want 0x41", data[17])
	}

	data, err = tuneData(207857)
	if err != nil {
		t.Fatal(err)
	}
	if data[17] != 0x01 {
		t.Fatalf("VHF band = %#02x; want 0x01", data[17])
	}
}

func TestTuneDataRange(t *testing.T) {
	t.Parallel()
	for _, frequency := range []uint64{59999, 860000} {
		if _, err := tuneData(frequency); err == nil {
			t.Errorf("tuneData(%d) succeeded outside tuner range", frequency)
		}
	}
}
