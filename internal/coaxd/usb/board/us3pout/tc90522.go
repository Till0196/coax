// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
package us3pout

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/pkg/i2c"
)

// The board fits one dual-core TC90522: the terrestrial core answers at 0x20
// and the satellite one at 0x22.
const (
	terrestrialAddress = 0x20
	satelliteAddress   = 0x22
)

// demodulator is the board's pair of TC90522 cores plus the power-up and
// acquisition sequences the US-3POUT needs, which differ from the ones the
// PX-series boards use.
type demodulator struct {
	terrestrial *tc90522.Demod
	satellite   *tc90522.Demod
	// active is the core currently powered up.
	active *tc90522.Demod
	// satelliteActive records which core that is, since the status registers
	// differ between them.
	satelliteActive bool
}

// The TC90522 is not ready the instant the bridge lets go of its reset. The
// bridge's own register writes after it are twenty or thirty milliseconds of USB
// traffic, which is not a wait: on runs where that was not long enough the
// demodulator did not acknowledge its address.
const (
	demodWakeTimeout = 500 * time.Millisecond
	demodWakePoll    = 5 * time.Millisecond
)

// waitForDemodulator waits until the demodulator answers its address. It reads
// a byte rather than writing one, because a read is the one thing that can be
// asked of an I2C slave without changing it; the answer wanted is not the byte
// but whether there was one.
func waitForDemodulator(bus i2c.Bus) error {
	deadline := time.Now().Add(demodWakeTimeout)
	for attempt := 1; ; attempt++ {
		_, err := bus.Read(terrestrialAddress, 1)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the TC90522 did not answer within %v, after %d attempts: %w",
				demodWakeTimeout, attempt, err)
		}
		time.Sleep(demodWakePoll)
	}
}

// newDemodulator brings both cores up on the bridge's I2C bus.
func newDemodulator(bus i2c.Bus) (*demodulator, error) {
	if err := waitForDemodulator(bus); err != nil {
		return nil, err
	}
	demod := &demodulator{
		terrestrial: tc90522.New(bus, terrestrialAddress, false),
		satellite:   tc90522.New(bus, satelliteAddress, true),
	}
	if err := demod.terrestrial.WriteRegister(0x01, 0x80); err != nil {
		return nil, err
	}
	if err := demod.satellite.WriteRegister(0x01, 0x90); err != nil {
		return nil, err
	}
	time.Sleep(10 * time.Millisecond)
	if err := demod.satellite.WritePairs([]byte{0x0a, 0, 0x10, 0, 0x11, 0, 0x03, 1}); err != nil {
		return nil, err
	}
	if err := demod.terrestrial.WritePairs([]byte{0xb0, 0xa0, 0xb2, 0x3d}); err != nil {
		return nil, err
	}
	return demod, nil
}

// selectSatellite powers up one core and parks the other.
func (demod *demodulator) selectSatellite(satellite bool) error {
	if satellite {
		if err := demod.satellite.WritePairs([]byte{0x01, 0x90, 0x0a, 0, 0x10, 0, 0x11, 0, 0x03, 1}); err != nil {
			return err
		}
		if err := demod.terrestrial.WriteRegister(0x1f, 0x20); err != nil {
			return err
		}
		time.Sleep(15 * time.Millisecond)
		if err := demod.terrestrial.WriteRegister(0x1f, 0x30); err != nil {
			return err
		}
		time.Sleep(15 * time.Millisecond)
		if err := demod.terrestrial.WritePairs([]byte{0x1f, 0x20, 0x0e, 0x77, 0x0f, 0x10, 0x03, 0xf0}); err != nil {
			return err
		}
		if err := demod.satellite.WritePairs([]byte{0x07, 1, 0x08, 0x10, 0x8e, 0x24, 0x13, 0, 0x17, 0}); err != nil {
			return err
		}
		demod.active, demod.satelliteActive = demod.satellite, true
		return nil
	}
	if err := demod.terrestrial.WritePairs([]byte{0x01, 0x50, 0x47, 0x30, 0x25, 0, 0x20, 0, 0x23, 0x4d, 0x1f, 0x30}); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	if err := demod.terrestrial.WriteRegister(0x1f, 0x20); err != nil {
		return err
	}
	time.Sleep(15 * time.Millisecond)
	if err := demod.terrestrial.WriteRegister(0x1f, 0x30); err != nil {
		return err
	}
	if err := demod.satellite.WritePairs([]byte{0x13, 0x80, 0x17, 0xff}); err != nil {
		return err
	}
	if err := demod.terrestrial.WritePairs([]byte{0x0e, 1, 0x0f, 0x77, 0x71, 0x20, 0x03, 0}); err != nil {
		return err
	}
	demod.active, demod.satelliteActive = demod.terrestrial, false
	return nil
}

// reset restarts acquisition on the selected core once the tuner has settled.
func (demod *demodulator) reset() error {
	if demod.satelliteActive {
		return demod.satellite.WritePairs([]byte{0x0a, 0xff, 0x10, 0xb2, 0x11, 0, 0x03, 1, 0x8f, 0, 0x90, 0})
	}
	if err := demod.terrestrial.WritePairs([]byte{0x23, 0x4c, 0x01, 0x40}); err != nil {
		return err
	}
	if err := demod.terrestrial.WriteRegisterMask(0x71, 0, 0x07); err != nil {
		return err
	}
	return demod.terrestrial.WritePairs([]byte{0x72, 0x24, 0x75, 2})
}

// selectStream picks the ISDB-S stream to demultiplex. SelectStreamS resolves a
// relative TMCC slot -- the 0-based numbering a channel named BS15_0 comes from
// -- into the TSID it stands for, then waits for the core to settle on it.
// Writing the register and walking away meant a stream ID that is not on the
// transponder was not an error: the core stayed on whatever it had and the
// caller got another channel's stream under the name it asked for.
func (demod *demodulator) selectStream(streamID uint16) error {
	return demod.satellite.SelectStreamS(streamID)
}

// cnr reports the active core's carrier-to-noise ratio in dB, and whether the
// chip has a measurement to report. The two cores keep the measurement in
// different registers, so the active one decides.
func (demod *demodulator) cnr() (float64, bool, error) {
	if demod.satelliteActive {
		return demod.active.CNRS()
	}
	return demod.active.CNRT()
}

// status reports the selected core's lock state and the frontend status names
// it implies.
func (demod *demodulator) status() (bool, []string, error) {
	register := byte(0x80)
	if demod.satelliteActive {
		register = 0xc3
	}
	value, err := demod.active.ReadRegister(register)
	if err != nil {
		return false, nil, err
	}
	names := []string{}
	locked := false
	if demod.satelliteActive {
		if value&0x80 == 0 {
			names = append(names, "SIGNAL")
		}
		if value&0x60 == 0 {
			names = append(names, "CARRIER", "VITERBI", "SYNC")
		}
		locked = value&0x10 == 0
	} else if value&0xf0 == 0 {
		names = append(names, "SIGNAL", "CARRIER")
		if value&0x04 == 0 {
			names = append(names, "VITERBI")
		}
		if value&0x08 == 0 {
			names = append(names, "SYNC")
		}
		locked = value&0x02 == 0
	}
	if locked {
		names = append(names, "LOCK")
	}
	return locked, names, nil
}
