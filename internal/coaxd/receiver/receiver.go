// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package receiver

import (
	"context"
	"errors"
)

var ErrWouldBlock = errors.New("receiver would block")

// ErrOutOfRange reports that a channel asks for a frequency the tuner cannot
// reach. It is separated from an ordinary tuning failure because the two mean
// different things: the hardware is not broken and nothing is wrong with the
// channel, the two simply do not go together. A scan of a whole band against a
// tuner that covers part of it would otherwise fill its report with errors.
var ErrOutOfRange = errors.New("frequency is outside the tuner's range")

// Measurement is a signal reading a backend may not be able to make. Zero is
// a legitimate reading, so whether one was taken is carried separately.
type Measurement struct {
	Value float64
	Known bool
}

// Measured wraps a reading a backend did make.
func Measured(value float64) Measurement { return Measurement{Value: value, Known: true} }

type Status struct {
	Locked bool
	Names  []string
	// CNR is the carrier-to-noise ratio in dB.
	CNR Measurement
	// Strength is the input level in dBm.
	Strength Measurement
	// Quality is a driver-defined 0..100 figure. Only the BDA backend reports
	// one; it is kept apart from CNR because it is not a decibel measurement
	// and must not be presented as one.
	Quality Measurement
}

type Stream interface {
	Read(context.Context, []byte) (int, error)
	Close() error
}

type Device interface {
	Backend() string
	Name() string
	Supports(string) (bool, error)
	Tune(map[string]string) error
	Status() (Status, error)
	OpenStream() (Stream, error)
	Close() error
}

type Options struct {
	Backend string
	Device  string
	// Tuner selects which tuner of a multi-tuner USB board to use.
	Tuner        int
	Adapter      int
	Frontend     int
	Demux        int
	DVR          int
	LinuxDVBRoot string
	FirmwareDir  string
}
