// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows && 386

package sink

// The entry points whose arguments are wider than a machine word.
//
// On 32-bit Windows a COM method is stdcall: the method itself takes its
// arguments off the stack on the way out, and a callback made with
// syscall.NewCallback takes one word for each parameter it declares. A
// REFERENCE_TIME or a double is two words there, so each is declared as two;
// declared as one, the callback returns with the caller's stack pointer wrong by
// the difference, and DirectShow fails in whatever it does next.

// sinkRunEntry is IBaseFilter::Run(REFERENCE_TIME tStart).
func sinkRunEntry(this, tStartLow, tStartHigh uintptr) uintptr { return sinkRun(this) }

// pinNewSegmentEntry is IPin::NewSegment(REFERENCE_TIME tStart,
// REFERENCE_TIME tStop, double dRate).
func pinNewSegmentEntry(this, tStartLow, tStartHigh, tStopLow, tStopHigh, dRateLow, dRateHigh uintptr) uintptr {
	return pinNewSegment(this)
}
