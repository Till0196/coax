// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/tuner/fc0012"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// laddered stands in for the FC0012: a tuner whose gain can be moved.
type laddered struct{ step int }

func (tuner *laddered) Tune(uint32) error { return nil }

func (tuner *laddered) SetLNAGain(step int) error {
	tuner.step = step
	return nil
}

// plainTuner is a tuner with no gain setting this program can drive.
type plainTuner struct{}

func (plainTuner) Tune(uint32) error { return nil }

func terrestrialFront(tuner terrestrialTuner) *frontend {
	return &frontend{hardware: &hardware{tunerT: tuner}, index: terrestrial}
}

func TestTheLadderIsTheFC0012s(t *testing.T) {
	ladder := terrestrialFront(&laddered{}).GainLadder("ISDBT")
	if ladder.Steps != fc0012.LNASteps {
		t.Errorf("the ladder has %d steps, want the chip's %d", ladder.Steps, fc0012.LNASteps)
	}
	// The ends are what min and max reach; the chip is left at the top.
	if got := ladder.Describe(0); got != "-9.9dB" {
		t.Errorf("the bottom of the ladder is %q, want -9.9dB", got)
	}
	if got := ladder.Describe(ladder.Steps - 1); got != "+19.7dB" {
		t.Errorf("the top of the ladder is %q, want +19.7dB", got)
	}
}

// TestOnlyADriveableTunerOffersALadder. Without board-level amplifiers, the
// ladder is the tuner's own, and only the terrestrial one has it.
func TestOnlyADriveableTunerOffersALadder(t *testing.T) {
	if !terrestrialFront(&laddered{}).GainLadder("ISDBT").Usable() {
		t.Error("the FC0012's ladder was not offered")
	}
	if terrestrialFront(plainTuner{}).GainLadder("ISDBT").Usable() {
		t.Error("a tuner with no gain setting claimed one")
	}
	satelliteFront := &frontend{hardware: &hardware{tunerT: &laddered{}}, index: satellite}
	if satelliteFront.GainLadder("ISDBS").Usable() {
		t.Error("the satellite tuner claimed the terrestrial tuner's ladder")
	}
}

func TestSetGainReachesTheChip(t *testing.T) {
	tuner := &laddered{step: -1}
	if err := terrestrialFront(tuner).SetGain(5); err != nil {
		t.Fatalf("SetGain: %v", err)
	}
	if tuner.step != 5 {
		t.Errorf("SetGain(5) moved the chip to step %d", tuner.step)
	}
	if err := terrestrialFront(plainTuner{}).SetGain(5); err == nil {
		t.Error("a tuner with no gain setting accepted a gain step")
	}
}

// TestTheLadderNeverGoesBackwards is what lets a level be matched to a step:
// tuning.GainLadder.Choose picks the nearest, and a ladder that dipped in the
// middle would make two levels choose the same step.
func TestTheLadderNeverGoesBackwards(t *testing.T) {
	steps := fc0012.GainStepsDB()
	for step := 1; step < len(steps); step++ {
		if steps[step] < steps[step-1] {
			t.Fatalf("step %d is %.1f dB, below step %d's %.1f dB", step, steps[step], step-1, steps[step-1])
		}
	}
	var _ tuning.GainLadder = tuning.NewGainLadder(steps...)
}

// TestTheV2SwitchesAreWrittenTheWayTheBoardIsWired. The two lines do not run
// the same way round: the terrestrial one is active low on the main GPIO port,
// the satellite one two bits wide on the extended one.
func TestTheV2SwitchesAreWrittenTheWayTheBoardIsWired(t *testing.T) {
	for _, one := range []struct {
		name        string
		index       int
		on          bool
		extended    bool
		value, mask byte
	}{
		{"terrestrial on writes zero", terrestrial, true, false, 0x00, 0x01},
		{"terrestrial off writes the bit", terrestrial, false, false, 0x01, 0x01},
		{"satellite on writes both bits", satellite, true, true, 0x03, 0x03},
		{"satellite off clears them", satellite, false, true, 0x00, 0x03},
	} {
		t.Run(one.name, func(t *testing.T) {
			extended, value, mask := v2Amplifiers.request(one.index, one.on)
			if extended != one.extended || value != one.value || mask != one.mask {
				t.Errorf("got extended=%v value=%#02x mask=%#02x, want extended=%v value=%#02x mask=%#02x",
					extended, value, mask, one.extended, one.value, one.mask)
			}
		})
	}
}

// TestTheV2OffersASwitchAndTheOlderBoardsALadder: the V2 offers a switch on
// both tuners, the older boards a ladder on one.
func TestTheV2OffersASwitchAndTheOlderBoardsALadder(t *testing.T) {
	for _, index := range []int{terrestrial, satellite} {
		front := &frontend{hardware: &hardware{wired: v2Wiring}, index: index}
		ladder := front.GainLadder("ISDBT")
		if ladder.Steps != 2 || len(ladder.Decibels) != 0 {
			t.Errorf("the V2's tuner %d offers %+v, want a two-step switch with no figures", index, ladder)
		}
	}
	older := &frontend{hardware: &hardware{wired: olderWiring, tunerT: &laddered{}}, index: terrestrial}
	if got := older.GainLadder("ISDBT"); len(got.Decibels) == 0 {
		t.Error("the older boards' terrestrial tuner lost its ladder")
	}
}

// recordingBus notes every write, so a sequence can be checked.
type recordingBus struct {
	address byte
	writes  [][]byte
}

func (bus *recordingBus) Write(address byte, data []byte) error {
	bus.address = address
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *recordingBus) Read(byte, int) ([]byte, error) {
	return nil, fmt.Errorf("this tuner is never read from")
}
