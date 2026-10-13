// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"testing"
	"time"
)

// Throughput measures the capture, and says nothing when there was not enough
// of one to divide by: a sample that arrived in one read has a moment rather
// than a duration, and dividing by it reports a channel running at gigabits.
func TestThroughputIsAboutTheCapture(t *testing.T) {
	if got := throughput(1_000_000, time.Second); got == nil || *got != 8 {
		t.Errorf("a megabyte in a second is 8 Mbps, got %v", got)
	}
	if got := throughput(0, time.Second); got != nil {
		t.Errorf("no bytes is no measurement, got %v", *got)
	}
	if got := throughput(1_000_000, time.Millisecond); got != nil {
		t.Errorf("a capture shorter than %v is no measurement, got %v", minimumThroughputSpan, *got)
	}
}
