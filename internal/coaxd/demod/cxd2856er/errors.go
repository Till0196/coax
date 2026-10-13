// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cxd2856er

import "github.com/Till0196/coax/internal/coaxd/demod/sony"

// The error counters live in the sony package: the registers are the family's,
// and the CXD2857ER and CXD2878 read the same ones. The embedded core
// satisfies sony.Banked directly, so there is no adapter.

// ErrorBasis and ErrorCount are the family's, re-exported so that a caller
// holding a *Demod does not have to know which package they came from.
type (
	ErrorBasis = sony.ErrorBasis
	ErrorCount = sony.ErrorCount
)

const (
	ErrorsPerBit           = sony.ErrorsPerBit
	ErrorsPerCounterPeriod = sony.ErrorsPerCounterPeriod
)

// ErrorCount reads the demodulator's error counter for the system it is in.
func (demod *Demod) ErrorCount() (ErrorCount, error) {
	demod.mu.Lock()
	defer demod.mu.Unlock()
	return sony.SystemErrorCount(demod, demod.Freeze, demod.System)
}
