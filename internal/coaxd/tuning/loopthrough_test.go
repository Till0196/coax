// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import "testing"

func TestParseLoopThrough(t *testing.T) {
	for value, want := range map[string]LoopThrough{
		"": LoopThroughMax, "max": LoopThroughMax, "High": LoopThroughHigh, "MID": LoopThroughMid, "low": LoopThroughLow,
	} {
		got, set, err := ParseLoopThrough(map[string]string{LoopThroughProperty: value})
		if err != nil || got != want || set != (value != "") {
			t.Errorf("%q: %v, %v, %v; want %v", value, got, set, err, want)
		}
	}
	if _, _, err := ParseLoopThrough(map[string]string{LoopThroughProperty: "+3dB"}); err == nil {
		t.Error("a level in decibels was taken")
	}
}
