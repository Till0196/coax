// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// stubBoard stands in for hardware: startLinger only asks whether a board is
// open and, if it decides to close one, closes it.
type stubBoard struct{}

func (stubBoard) Name() string                        { return "stub" }
func (stubBoard) Tuners() []device.TunerInfo          { return nil }
func (stubBoard) OpenTuner(int) (device.Tuner, error) { return nil, nil }
func (stubBoard) Close() error                        { return nil }

// TestAnExclusiveBoardIsNotLingeredOn: on a device whose driver allows one open,
// lingering is a lock held against every other program on the machine rather
// than a head start for the next caller.
func TestAnExclusiveBoardIsNotLingeredOn(t *testing.T) {
	for _, test := range []struct {
		name      string
		exclusive bool
		linger    time.Duration
		wantOpen  bool
	}{
		{"an ordinary board is kept open for a while", false, time.Minute, true},
		{"an exclusive one is let go at once", true, time.Minute, false},
		{"and a zero linger still closes either", false, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			board := newBoard(&Supervisor{options: Options{Linger: test.linger}}, entry{
				detected: Detected{Exclusive: test.exclusive},
			})
			board.opened = stubBoard{}
			board.mutex.Lock()
			board.startLinger()
			open := board.opened != nil
			pending := board.linger != nil
			board.stopLinger()
			board.mutex.Unlock()
			if open != test.wantOpen {
				t.Errorf("board open = %t, want %t", open, test.wantOpen)
			}
			if test.exclusive && pending {
				t.Error("an exclusive board was given a linger timer")
			}
		})
	}
}
