// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// The error counters. The CXD2856ER, CXD2857ER and CXD2878 keep them at the
// same banks and offsets; what differs is what the count is divided by, which
// ErrorBasis passes on rather than hides.

package sony

import (
	"errors"
	"fmt"
)

// ErrorBasis says what an error count was measured against.
type ErrorBasis int

const (
	// ErrorsPerBit means Total is a number of bits, so Errors divided by
	// Total is a bit error rate.
	ErrorsPerBit ErrorBasis = iota
	// ErrorsPerCounterPeriod means Total is the counter's configured period, in
	// a unit the chip does not publish. The ratio compares readings of one
	// channel against each other, but is not a bit error rate.
	ErrorsPerCounterPeriod
)

func (basis ErrorBasis) String() string {
	if basis == ErrorsPerCounterPeriod {
		return "counter periods"
	}
	return "bits"
}

// ErrorCount is what the demodulator's error counter reported. The two halves
// are kept apart rather than divided because for two systems the denominator's
// unit is unknown, which a single floating-point "BER" would hide.
type ErrorCount struct {
	Errors uint64
	Total  uint64
	Basis  ErrorBasis
}

// Rate is Errors over Total. It is a bit error rate only when Basis is
// ErrorsPerBit; otherwise it is a relative figure whose scale is the chip's
// own.
func (count ErrorCount) Rate() float64 {
	if count.Total == 0 {
		return 0
	}
	return float64(count.Errors) / float64(count.Total)
}

// ErrNoErrorCount reports that the counter has not finished a measurement.
var ErrNoErrorCount = errors.New("the demodulator has no error count yet")

// Banked is the banked register access the counters need.
type Banked interface {
	Bank(n byte) error
	ReadBanked(register byte, length int) ([]byte, error)
}

// ISDBTErrorCount reads the ISDB-T counter, the one terrestrial case with a
// real denominator: whole 204-byte transport packets. The read runs under the
// chip's freeze, passed in by the caller, so packet count and error count
// describe the same measurement.
func ISDBTErrorCount(bus Banked, freeze func(hold bool) error) (ErrorCount, error) {
	if err := freeze(true); err != nil {
		return ErrorCount{}, err
	}
	defer freeze(false)
	if err := bus.Bank(0x60); err != nil {
		return ErrorCount{}, err
	}
	packets, err := bus.ReadBanked(0x5b, 2)
	if err != nil {
		return ErrorCount{}, err
	}
	counted := uint64(packets[0])<<8 | uint64(packets[1])
	if counted == 0 {
		return ErrorCount{}, ErrNoErrorCount
	}
	data, err := bus.ReadBanked(0x16, 3)
	if err != nil {
		return ErrorCount{}, err
	}
	return ErrorCount{
		Errors: uint64(data[0]&0x7f)<<16 | uint64(data[1])<<8 | uint64(data[2]),
		Total:  counted * 204 * 8,
		Basis:  ErrorsPerBit,
	}, nil
}

// PeriodicErrorCount reads the DVB-T and J.83 counters, which share a register
// layout and differ only in the bank: 0x10 for DVB-T, 0x40 for cable. The period
// register's unit is undocumented, so the pair is reported as it stands.
func PeriodicErrorCount(bus Banked, bank byte) (ErrorCount, error) {
	if err := bus.Bank(bank); err != nil {
		return ErrorCount{}, err
	}
	valid, err := bus.ReadBanked(0x39, 1)
	if err != nil {
		return ErrorCount{}, err
	}
	if valid[0]&0x01 == 0 {
		return ErrorCount{}, ErrNoErrorCount
	}
	data, err := bus.ReadBanked(0x22, 2)
	if err != nil {
		return ErrorCount{}, err
	}
	selector, err := bus.ReadBanked(0x6f, 1)
	if err != nil {
		return ErrorCount{}, err
	}
	period := uint64(256)
	if exponent := selector[0] & 0x07; exponent != 0 {
		period = 4096 << exponent
	}
	return ErrorCount{
		Errors: uint64(data[0])<<8 | uint64(data[1]),
		Total:  period,
		Basis:  ErrorsPerCounterPeriod,
	}, nil
}

// DVBT2ErrorCount reads the DVB-T2 counter. Here the denominator is real: the
// counter runs for a power-of-two number of LDPC frames, and the frame length
// is one of two the chip reports.
func DVBT2ErrorCount(bus Banked) (ErrorCount, error) {
	if err := bus.Bank(0x20); err != nil {
		return ErrorCount{}, err
	}
	data, err := bus.ReadBanked(0x39, 4)
	if err != nil {
		return ErrorCount{}, err
	}
	if data[0]&0x10 == 0 {
		return ErrorCount{}, ErrNoErrorCount
	}
	count := uint64(data[0]&0x0f)<<24 | uint64(data[1])<<16 | uint64(data[2])<<8 | uint64(data[3])
	selector, err := bus.ReadBanked(0x6f, 1)
	if err != nil {
		return ErrorCount{}, err
	}
	exponent := selector[0] & 0x0f
	if err := bus.Bank(0x22); err != nil {
		return ErrorCount{}, err
	}
	length, err := bus.ReadBanked(0x5e, 1)
	if err != nil {
		return ErrorCount{}, err
	}
	frameBits := uint64(64800)
	if length[0]&0x03 == 0 {
		frameBits = 16200
	}
	total := uint64(1) << exponent * frameBits
	if count > total {
		return ErrorCount{}, ErrNoErrorCount
	}
	return ErrorCount{Errors: count, Total: total, Basis: ErrorsPerBit}, nil
}

// ViterbiErrorCount reads the satellite Viterbi counter, which reports both
// halves itself. It serves ISDB-S and DVB-S.
func ViterbiErrorCount(bus Banked) (ErrorCount, error) {
	if err := bus.Bank(0xa0); err != nil {
		return ErrorCount{}, err
	}
	data, err := bus.ReadBanked(0x35, 11)
	if err != nil {
		return ErrorCount{}, err
	}
	if data[0]&0x01 == 0 {
		return ErrorCount{}, ErrNoErrorCount
	}
	count := uint64(data[1]&0x3f)<<16 | uint64(data[2])<<8 | uint64(data[3])
	total := uint64(data[8]&0x3f)<<16 | uint64(data[9])<<8 | uint64(data[10])
	if total == 0 || count > total {
		return ErrorCount{}, ErrNoErrorCount
	}
	return ErrorCount{Errors: count, Total: total, Basis: ErrorsPerBit}, nil
}

// LDPCErrorCount reads the DVB-S2 counter, whose period is a power-of-two
// number of 64800-bit frames.
func LDPCErrorCount(bus Banked) (ErrorCount, error) {
	if err := bus.Bank(0xb2); err != nil {
		return ErrorCount{}, err
	}
	data, err := bus.ReadBanked(0x30, 5)
	if err != nil {
		return ErrorCount{}, err
	}
	if data[0]&0x01 == 0 {
		return ErrorCount{}, ErrNoErrorCount
	}
	count := uint64(data[1]&0x0f)<<24 | uint64(data[2])<<16 | uint64(data[3])<<8 | uint64(data[4])
	if err := bus.Bank(0xa0); err != nil {
		return ErrorCount{}, err
	}
	selector, err := bus.ReadBanked(0x7a, 1)
	if err != nil {
		return ErrorCount{}, err
	}
	total := uint64(1) << (selector[0] & 0x0f) * 64800
	if count > total {
		return ErrorCount{}, ErrNoErrorCount
	}
	return ErrorCount{Errors: count, Total: total, Basis: ErrorsPerBit}, nil
}

// SystemErrorCount picks the counter a system is measured by and reads it. A
// system with no entry errors rather than answering a zero that would read as a
// clean channel. Two omissions are deliberate: ISDB-S3 carries its own LDPC
// arrangement with nothing to read here, and ATSC's counters are not worked out
// in this tree.
func SystemErrorCount(bus Banked, freeze func(hold bool) error, system System) (ErrorCount, error) {
	switch system {
	case SystemISDBT:
		return ISDBTErrorCount(bus, freeze)
	case SystemDVBT:
		return PeriodicErrorCount(bus, 0x10)
	case SystemDVBC, SystemJ83B:
		return PeriodicErrorCount(bus, 0x40)
	case SystemDVBT2:
		return DVBT2ErrorCount(bus)
	case SystemISDBS, SystemDVBS:
		return ViterbiErrorCount(bus)
	case SystemDVBS2:
		return LDPCErrorCount(bus)
	case SystemUnspecified:
		return ErrorCount{}, ErrNoSystem
	default:
		return ErrorCount{}, fmt.Errorf("this port has no error counter for %s", system)
	}
}
