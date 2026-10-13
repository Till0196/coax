// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package idle decides when a program nobody is using should stop.
//
// It is shared because two programs need the same rule and the same subtlety:
// the tuner daemon, and the PC/SC socket `coax pcsc` serves. Both are started
// by whoever needs them and expected to go away again, and both would be wrong
// to count requests rather than clients.
package idle

import (
	"context"
	"time"
)

// Rule decides when a program nobody is using should stop. It counts connections
// rather than requests: a recording holds its stream open for hours and a PC/SC
// client waiting for a card may say nothing for days, yet both are in use.
// Several consecutive quiet checks are required so a reconnecting client -- a
// moment with no connections -- does not race the daemon to a shutdown.
type Rule struct {
	// Interval is how often to look.
	Interval time.Duration
	// Strikes is how many consecutive quiet looks end it.
	Strikes int
	// Count reports how many clients are attached.
	Count func() int
}

// Defaults for a rule a caller does not settle; the ones px4_drv's own host
// uses.
const (
	DefaultInterval = 5 * time.Second
	DefaultStrikes  = 3
)

// Wait blocks until nothing has used the daemon for long enough, or until the
// context is done. It reports whether it was the idleness that ended it.
func (rule Rule) Wait(ctx context.Context) bool {
	interval := rule.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	strikes := rule.Strikes
	if strikes <= 0 {
		strikes = DefaultStrikes
	}
	if rule.Count == nil {
		<-ctx.Done()
		return false
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	quiet := 0
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
		if rule.Count() == 0 {
			quiet++
			if quiet >= strikes {
				return true
			}
			continue
		}
		// Consecutive, or a daemon in steady use would accumulate scattered quiet
		// moments into a shutdown.
		quiet = 0
	}
}
