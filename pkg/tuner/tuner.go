// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package tuner is what the scanner drives: an interface and nothing else. The
// hardware lives in another program -- one board can only be driven by one
// process, so a daemon owns them all and hands out shares over HTTP. Keeping
// the shape here rather than importing it leaves no compiled dependency on that
// program.
package tuner

import (
	"context"
	"errors"
	"time"
)

// ErrWouldBlock reports that no data was there yet. A read that returns it has
// not failed and should be tried again.
var ErrWouldBlock = errors.New("tuner would block")

// ErrOutOfRange reports a channel the tuner cannot reach, kept apart from an
// ordinary tuning failure: nothing is broken, and a scan of a whole band against
// a tuner covering part of it would otherwise fill its report with errors.
var ErrOutOfRange = errors.New("frequency is outside the tuner's range")

// ErrUnsupported reports a delivery system no tuner carries.
var ErrUnsupported = errors.New("no tuner carries that delivery system")

// Measurement is a reading a tuner may not have been able to take. Zero is a
// real reading, so whether one was taken is carried separately.
type Measurement struct {
	Value float64
	Known bool
}

func Measured(value float64) Measurement { return Measurement{Value: value, Known: true} }

// Status is what a tuner can say about the channel it is on.
type Status struct {
	Locked bool
	Names  []string
	// CNR is the carrier-to-noise ratio in dB.
	CNR Measurement
	// Strength is the input level in dBm.
	Strength Measurement
	// Quality is a driver-defined 0..100 figure, which only some backends
	// report. It is kept apart from CNR because it is not a decibel measurement
	// and must not be presented as one.
	Quality Measurement
}

// Stream is a transport stream being received.
type Stream interface {
	Read(context.Context, []byte) (int, error)
	Close() error
}

// Bounds are how much of a stream is wanted, for a caller that wants a sample
// rather than a recording: it ends by itself after Bytes bytes, or For after
// the first of them, whichever comes first. A zero field is no bound.
//
// A stream with no end can only be stopped by hanging up on it, and the daemon
// gives a tuner back when the connection that took it goes -- so a scan
// stopping a stream that way loses its tuner between channels. A bounded stream
// is read to the end.
type Bounds struct {
	Bytes int
	For   time.Duration
}

// Device is one tuner.
type Device interface {
	Backend() string
	// RelativeStreams is how many places a satellite transponder's table holds,
	// for a tuner whose driver names a stream by its place rather than its
	// identifier. Zero for one that takes an identifier, which is nearly all of
	// them, and zero before anything has been tuned: no tuner is chosen until
	// then.
	RelativeStreams() int
	Name() string
	Supports(string) (bool, error)
	Tune(map[string]string) error
	Status() (Status, error)
	OpenStream(Bounds) (Stream, error)
	Close() error
}
