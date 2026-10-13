// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"errors"
	"testing"

	"github.com/Till0196/coax/pkg/tuner"
)

// TestAProblemBecomesTheErrorTheScannerExpects: a frequency outside a tuner's
// range and a delivery system it does not carry are the same code from the
// daemon but different answers to a scan -- one channel against every channel of
// that system -- and reporting either as the other fills a report with the wrong
// reason.
func TestAProblemBecomesTheErrorTheScannerExpects(t *testing.T) {
	for _, test := range []struct {
		reason string
		want   error
	}{
		{ReasonOutOfRange, tuner.ErrOutOfRange},
		{"", tuner.ErrUnsupported},
	} {
		got := translate(&FailureError{Problem: Problem{
			Code: CodeUnsupported, Reason: test.reason, Message: "no",
		}})
		if !errors.Is(got, test.want) {
			t.Errorf("reason %q became %v, want %v", test.reason, got, test.want)
		}
	}
	// Anything else is passed through as it came.
	own := errors.New("something else")
	if got := translate(own); !errors.Is(got, own) {
		t.Errorf("an error that is not a problem became %v", got)
	}
}
