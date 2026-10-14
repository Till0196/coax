// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/tuner/qm1d1b0004"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/i2c"
)

// The satellite tuner on the older boards is a QM1D1B0004, the same chip the
// PT1 and PT2 carry but driven differently: the divider is programmed with the
// charge pump hard and backed off once the loop has pulled in, which is what
// lets the wait be ten milliseconds rather than twenty. It is never read from
// and gets no initialisation beyond a tune.
//
// No board of this generation has been seen here.

// satelliteAddress is where the tuner answers on the demodulator's tuner bus,
// which is what puts the transfer through the repeater.
const satelliteAddress = 0xc0

// The charge pump either side of the loop settling: hard while it pulls in,
// backed off once it has.
const (
	tuningChargePump  = 1
	settledChargePump = 2
)

// satelliteBlockAlias is the low-noise block these boards are used with. Every
// backend in this tree is handed the intermediate frequency -- for BS-1,
// 1,049,480 kHz rather than 11,727,480 -- and the difference is this block's
// oscillator, looked up rather than written down so that there is one copy of
// it.
const satelliteBlockAlias = "110BS"

// newSatelliteTuner builds the older boards' front end on the demodulator's
// tuner bus, so a write comes out on the far side of the repeater.
func newSatelliteTuner(bus i2c.Bus) *qm1d1b0004.Tuner {
	return qm1d1b0004.New(bus, satelliteAddress, qm1d1b0004.Config{
		Pump:        tuningChargePump,
		SettledPump: settledChargePump,
	})
}

// tableSatelliteTuner is the older boards' satellite front end. It answers in
// intermediate frequencies and so does the chip, so there is nothing to
// convert.
type tableSatelliteTuner struct{ tuner *qm1d1b0004.Tuner }

// Tune points the tuner at an intermediate frequency. Anything the chip
// reaches is fair game: the boards were only ever pointed at Japan's
// twenty-four BS and CS-110 transponders, but the bytes for those are the
// chip's own arithmetic rather than a calibration curve, so computing serves
// every frequency in between as well.
func (tuner tableSatelliteTuner) Tune(intermediateKHz uint64) error {
	if intermediateKHz > uint64(qm1d1b0004.FrequencyMax) {
		return fmt.Errorf("this board's satellite tuner cannot reach an intermediate frequency of %d kHz", intermediateKHz)
	}
	if err := tuner.tuner.Tune(uint32(intermediateKHz)); err != nil {
		return fmt.Errorf("satellite tuner: %w", err)
	}
	return nil
}

// satelliteOscillatorKHz is the block's local oscillator.
func satelliteOscillatorKHz() (uint64, bool) {
	block, ok := tuning.LookupLNBf(satelliteBlockAlias)
	if !ok {
		return 0, false
	}
	return uint64(block.LowLO) * 1000, true
}
