// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package cxd2878

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/demod/sony"
)

// TestCableBandwidthTableCoversEveryChannelWidth: a width the wake-up will
// accept has to have a band entry, or the tune fails after the chip has
// already been put into the system.
func TestCableBandwidthTableCoversEveryChannelWidth(t *testing.T) {
	demod := &Demod{Core: sony.NewCore(&scriptedBus{}, sony.Config{DemodAddress: 0x64})}
	for _, bandwidth := range []uint32{6, 7, 8} {
		if err := demod.SetBandwidthDVBC(bandwidth); err != nil {
			t.Errorf("SetBandwidthDVBC(%d): %v", bandwidth, err)
		}
	}
	if err := demod.SetBandwidthDVBC(5); err == nil {
		t.Error("a 5 MHz cable channel was accepted")
	}
}

// scriptedBus discards writes and answers every read with zeroes, which is
// enough for a band setting.
type scriptedBus struct{}

func (scriptedBus) Write(byte, []byte) error { return nil }

func (scriptedBus) Read(_ byte, length int) ([]byte, error) { return make([]byte, length), nil }

// TestJ83BBandSettingTakesEitherSymbolRate: the two J.83B configurations are
// two different signals, chosen by the rate the channel says it carries.
func TestJ83BBandSettingTakesEitherSymbolRate(t *testing.T) {
	demod := &Demod{Core: sony.NewCore(&scriptedBus{}, sony.Config{DemodAddress: 0x64})}
	for _, rate := range []uint32{5_056_941, 5_360_537, 5_600_000} {
		if err := demod.SetBandwidthJ83B(rate); err != nil {
			t.Errorf("SetBandwidthJ83B(%d): %v", rate, err)
		}
	}
}
