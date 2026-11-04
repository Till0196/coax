// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// stepped stands in for the NM120 and the TDA20142.
type stepped struct{ step int }

func (tuner *stepped) Tune(uint32) error { return nil }

func (tuner *stepped) SetLoopThrough(step int) error {
	tuner.step = step
	return nil
}

// switched stands in for the FC0012.
type switched struct{ on bool }

func (tuner *switched) Tune(uint32) error { return nil }

func (tuner *switched) SetLoopThrough(on bool) error {
	tuner.on = on
	return nil
}

// The level goes to the tuner being tuned, not to another pair's.
func TestTheLoopThroughIsTheTunersOwn(t *testing.T) {
	for value, want := range map[string]int{"": 0, "max": 0, "high": 1, "mid": 2, "low": 3} {
		tuner := &stepped{step: -1}
		if err := terrestrialFront(tuner).setLoopThrough(map[string]string{tuning.LoopThroughProperty: value}); err != nil {
			t.Fatal(err)
		}
		if tuner.step != want {
			t.Errorf("%q set step %d, want %d", value, tuner.step, want)
		}
	}
}

// The FC0012 is left off unless a channel asks for it.
func TestTheFC0012LoopThroughIsOnlyOnWhenAsked(t *testing.T) {
	for value, want := range map[string]bool{"": false, "max": true, "high": true, "mid": true, "low": false} {
		tuner := &switched{on: !want}
		if err := terrestrialFront(tuner).setLoopThrough(map[string]string{tuning.LoopThroughProperty: value}); err != nil {
			t.Fatal(err)
		}
		if tuner.on != want {
			t.Errorf("%q left the loop-through on=%v", value, tuner.on)
		}
	}
	if err := terrestrialFront(plainTuner{}).setLoopThrough(map[string]string{tuning.LoopThroughProperty: "max"}); err != nil {
		t.Errorf("a tuner without a loop-through refused the channel: %v", err)
	}
}
