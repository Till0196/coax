// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// Package mb86a20s drives the Fujitsu MB86A20S and MB86A21S ISDB-T
// demodulators over any i2c.Bus. The two parts share their register interface,
// their tuner gate and the way they report acquisition progress and CNR; only
// the power-up register sets differ, so the caller says which silicon it is.
package mb86a20s

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// address is the demodulator's I2C slave address, already shifted.
const address byte = 0x20

// Registers the driver reaches for by name.
const (
	registerSequenceState = 0x0a
	registerTunerGate     = 0xfe
	registerCNControl     = 0x45
	registerCNHigh        = 0x46
	registerCNLow         = 0x47
)

// lockedSequenceState is the point in the acquisition sequence at which the
// transport stream is usable. Anything above 3 is far enough along for the CNR
// reading to mean something.
const (
	lockedSequenceState  = 9
	carrierSequenceState = 4
)

// Variant selects which of the two demodulators is fitted.
type Variant int

const (
	// A20S is the MB86A20S, paired with the TDA18211HD tuner.
	A20S Variant = iota
	// A21S is the MB86A21S, paired with the MxL135RF tuner.
	A21S
)

// String names the variant for status output.
func (variant Variant) String() string {
	if variant == A21S {
		return "MB86A21S"
	}
	return "MB86A20S"
}

// Demod is an MB86A20S or MB86A21S reached over an I2C bus. Not safe for
// concurrent use: a caller sharing one between goroutines must serialise.
type Demod struct {
	bus     i2c.Bus
	variant Variant
}

// New addresses the demodulator on the given bus.
func New(bus i2c.Bus, variant Variant) *Demod {
	return &Demod{bus: bus, variant: variant}
}

// Variant reports which silicon this is.
func (demod *Demod) Variant() Variant { return demod.variant }

// WriteRegister sets one of the demodulator's registers.
func (demod *Demod) WriteRegister(register, value byte) error {
	return demod.bus.Write(address, []byte{register, value})
}

// ReadRegister reads one register. The address and the data have to stay in one
// transaction, so it goes through WriteRead.
func (demod *Demod) ReadRegister(register byte) (byte, error) {
	data, err := i2c.WriteRead(demod.bus, address, []byte{register}, 1)
	if err != nil {
		return 0, err
	}
	if len(data) != 1 {
		return 0, fmt.Errorf("MB86A2xS short read from register %#02x", register)
	}
	return data[0], nil
}

func (demod *Demod) writePairs(pairs [][2]byte) error {
	for _, pair := range pairs {
		if err := demod.WriteRegister(pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}

// OpenTunerGate opens or closes the I2C pass-through to the tuner (i2c.Gate).
// The register is active low: zero lets the tuner through.
func (demod *Demod) OpenTunerGate(open bool) error {
	value := byte(1)
	if open {
		value = 0
	}
	return demod.WriteRegister(registerTunerGate, value)
}

// Initialize runs the demodulator's power-up register set.
func (demod *Demod) Initialize() error {
	if demod.variant == A21S {
		return demod.writePairs(initA21S)
	}
	return demod.writePairs(initA20S)
}

// Reset restarts acquisition, done once the tuner has settled on a channel.
func (demod *Demod) Reset() error {
	if demod.variant == A21S {
		return demod.writePairs(resetA21S)
	}
	return demod.writePairs(resetA20S)
}

// SequenceState reports how far the demodulator has got through acquisition.
func (demod *Demod) SequenceState() (byte, error) {
	return demod.ReadRegister(registerSequenceState)
}

// Locked reports whether acquisition has reached the point where the transport
// stream is usable.
func (demod *Demod) Locked() (bool, error) {
	state, err := demod.SequenceState()
	if err != nil {
		return false, err
	}
	return state >= lockedSequenceState, nil
}

// WaitForLock polls the acquisition sequence until it locks or the timeout
// expires.
func (demod *Demod) WaitForLock(timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		locked, err := demod.Locked()
		if err != nil || locked {
			return locked, err
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// CNR reports the carrier-to-noise ratio in thousandths of a dB, or zero when
// there is no reading yet. Register 0x46 holds the counter's low byte and 0x47
// its high byte; only 0x47 is read here.
func (demod *Demod) CNR() (uint32, error) {
	state, err := demod.SequenceState()
	if err != nil {
		return 0, err
	}
	if state < carrierSequenceState {
		return 0, nil
	}
	control, err := demod.ReadRegister(registerCNControl)
	if err != nil {
		return 0, err
	}
	if control&0x40 == 0 {
		return 0, nil
	}
	high, err := demod.ReadRegister(registerCNHigh)
	if err != nil {
		return 0, err
	}
	low, err := demod.ReadRegister(registerCNLow)
	if err != nil {
		return 0, err
	}
	// Restart the counter so the next reading is fresh.
	if err := demod.WriteRegister(registerCNControl, control|0x10); err != nil {
		return 0, err
	}
	if err := demod.WriteRegister(registerCNControl, control&0x6f); err != nil {
		return 0, err
	}
	return interpolate(uint32(high)<<8|uint32(low), cnrToDB), nil
}

// linearSegment is one point of a piecewise-linear conversion, an x on the
// demodulator's own scale against the y it stands for.
type linearSegment struct {
	x, y uint32
}

// cnrToDB converts the carrier-to-noise counter to thousandths of a dB: Linux's
// cnr_to_db_table, x descending as the signal improves. Both ends are entries
// of their own -- at or above the first there is nothing to report, below the
// last it is the best the demodulator can distinguish.
var cnrToDB = []linearSegment{
	{19648, 0}, {18187, 1000}, {16534, 2000}, {14823, 3000}, {13161, 4000},
	{11622, 5000}, {10279, 6000}, {9089, 7000}, {8042, 8000}, {7137, 9000},
	{6342, 10000}, {5641, 11000}, {5030, 12000}, {4474, 13000}, {3988, 14000},
	{3556, 15000}, {3180, 16000}, {2841, 17000}, {2541, 18000}, {2276, 19000},
	{2038, 20000}, {1800, 21000}, {1625, 22000}, {1462, 23000}, {1324, 24000},
	{1175, 25000}, {1063, 26000}, {980, 27000}, {907, 28000}, {840, 29000},
	{788, 30000},
}

// interpolate converts a reading through a descending table, linearly between
// the two entries it falls among (Linux's interpolate_value).
func interpolate(value uint32, segments []linearSegment) uint32 {
	if value >= segments[0].x {
		return segments[0].y
	}
	last := len(segments) - 1
	if value < segments[last].x {
		return segments[last].y
	}
	index := last
	for i := 1; i < last; i++ {
		if value == segments[i].x {
			return segments[i].y
		}
		if value > segments[i].x {
			index = i
			break
		}
	}
	dy := segments[index].y - segments[index-1].y
	dx := segments[index-1].x - segments[index].x
	return segments[index].y - (value-segments[index].x)*dy/dx
}
