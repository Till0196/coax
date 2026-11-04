// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"fmt"
	"strings"
)

// LoopThroughProperty is the channel-file key setting the level of the tuner's
// own loop-through output, the one handed on to whatever is fed from it.
const LoopThroughProperty = "LOOP_THROUGH"

// LoopThrough is a loop-through level, LoopThroughMax the strongest and
// LoopThroughLow the weakest. A chip with four levels takes them in this order.
type LoopThrough int

const (
	LoopThroughMax LoopThrough = iota
	LoopThroughHigh
	LoopThroughMid
	LoopThroughLow
)

// DefaultLoopThrough is the level a chip with four levels is left at when a
// channel does not say.
const DefaultLoopThrough = LoopThroughMax

// ParseLoopThrough reads the loop-through level out of a channel's properties,
// and whether the channel named one. A channel that does not gets
// DefaultLoopThrough.
func ParseLoopThrough(properties map[string]string) (LoopThrough, bool, error) {
	value := strings.Trim(strings.TrimSpace(properties[LoopThroughProperty]), "\"'")
	switch Normalize(value) {
	case "":
		return DefaultLoopThrough, false, nil
	case "MAX":
		return LoopThroughMax, true, nil
	case "HIGH":
		return LoopThroughHigh, true, nil
	case "MID":
		return LoopThroughMid, true, nil
	case "LOW":
		return LoopThroughLow, true, nil
	}
	return 0, false, fmt.Errorf("%s %q is not a level; write max, high, mid or low", LoopThroughProperty, value)
}
