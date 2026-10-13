// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import "github.com/Till0196/coax/internal/coaxd/bda/vendors"

// Statistics is what a Digital Devices demodulator says about its channel.
type Statistics struct {
	// CNR is the carrier-to-noise ratio in decibels, and Level the input in
	// dBm. Zero and false is a demodulator with nothing to measure.
	CNR     float64
	Level   float64
	Quality float64
	// SymbolRate is symbols per second, or zero for a standard without one.
	SymbolRate uint32

	KnownCNR     bool
	KnownLevel   bool
	KnownQuality bool
}

// ReadStatistics asks the driver what the demodulator is measuring. A driver
// that does not answer this set reports nothing rather than an error.
func ReadStatistics(properties vendors.Properties) (Statistics, bool) {
	var statistics Statistics
	answered := false
	if raw, err := readWord(properties, StatisticsSet, ddStatCNR); err == nil {
		statistics.CNR = float64(int32(raw)) / 10
		statistics.KnownCNR = true
		answered = true
	}
	if raw, err := readWord(properties, StatisticsSet, ddStatLevel); err == nil {
		statistics.Level = float64(int32(raw))/10 - dBuVToDBm
		statistics.KnownLevel = true
		answered = true
	}
	if raw, err := readWord(properties, StatisticsSet, ddStatQuality); err == nil {
		statistics.Quality = float64(int32(raw))
		statistics.KnownQuality = true
		answered = true
	}
	if raw, err := readWord(properties, StatisticsSet, ddStatSymbolRate); err == nil && int32(raw) > 0 {
		statistics.SymbolRate = raw
	}
	return statistics, answered
}
