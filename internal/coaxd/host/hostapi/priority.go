// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package hostapi

import (
	"fmt"
	"strconv"
	"strings"
)

// PriorityHeader is how a request says how much the work behind it matters.
//
// A header rather than a query parameter, because it is not part of the
// channel: the same channel asked for by a recorder and by a viewer is the same
// request with different weight behind it. It is also carried by every endpoint
// at once.
const PriorityHeader = "X-Coax-Priority"

// DefaultPriority is what a request that says nothing is worth. Zero rather
// than the middle of a range: the daemon has been answering requests without
// this header since before it existed, and every one of them has to keep
// meaning exactly what it meant.
const DefaultPriority = 0

// ParsePriority reads the priority header. An absent or empty one is
// DefaultPriority; anything that is not a whole number is refused rather than
// taken as zero, since a client that meant to say something and was
// misunderstood would find its recording quietly stopped. Negative numbers mean
// what they say: work anything else may take a tuner from.
func ParsePriority(header string) (int, error) {
	written := strings.TrimSpace(header)
	if written == "" {
		return DefaultPriority, nil
	}
	priority, err := strconv.Atoi(written)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a whole number", PriorityHeader, header)
	}
	return priority, nil
}
