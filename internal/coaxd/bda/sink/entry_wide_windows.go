// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows && !386

package sink

// The entry points whose arguments are wider than a machine word on 32-bit
// Windows (see entry_windows_386.go). Here every argument is one word: a
// REFERENCE_TIME fits a register, and the double NewSegment is given travels in
// a floating-point register the callback never reads.

// sinkRunEntry is IBaseFilter::Run(REFERENCE_TIME tStart).
func sinkRunEntry(this, tStart uintptr) uintptr { return sinkRun(this) }

// pinNewSegmentEntry is IPin::NewSegment(REFERENCE_TIME tStart,
// REFERENCE_TIME tStop, double dRate).
func pinNewSegmentEntry(this, tStart, tStop, dRate uintptr) uintptr { return pinNewSegment(this) }
