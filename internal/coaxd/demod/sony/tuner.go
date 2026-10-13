// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "github.com/Till0196/coax/internal/coaxd/i2c"

// TunerWriter reaches the tuner, a real I2C device at its own address rather
// than one of the demodulator's four bank-selected ones -- whether it is a
// chip of its own on the board or a die in the demodulator's package.
type TunerWriter struct {
	bus     i2c.Bus
	address byte
}

// Tuner addresses the tuner core, which sits behind the demodulator's I2C
// repeater at its own address on the same bus.
func (core *Core) Tuner() TunerWriter {
	return NewTunerWriter(core.bus, core.config.TunerAddress)
}

// NewTunerWriter addresses a tuner directly, for a driver that reaches one
// without holding a Core: the CXD2858ER is gated rather than repeated, so it
// is handed the bus its board has already arranged.
func NewTunerWriter(bus i2c.Bus, address byte) TunerWriter {
	return TunerWriter{bus: bus, address: address}
}

func (t TunerWriter) Write(register, value byte) error {
	return t.bus.Write(t.address, []byte{register, value})
}

func (t TunerWriter) WriteMulti(register byte, data []byte) error {
	return t.bus.Write(t.address, append([]byte{register}, data...))
}

func (t TunerWriter) Read(register byte, length int) ([]byte, error) {
	return i2c.WriteRead(t.bus, t.address, []byte{register}, length)
}

// WriteMasked does a read-modify-write on a tuner register.
func (t TunerWriter) WriteMasked(register, value, mask byte) error {
	if mask == 0 {
		return nil
	}
	if mask != 0xff {
		current, err := t.Read(register, 1)
		if err != nil {
			return err
		}
		value = i2c.Mask(current[0], value, mask)
	}
	return t.Write(register, value)
}

// TunerRFGainByte composes the gain byte both tuner cores write at the head
// of their tuning packet: 0x80 selects automatic RF gain when the parameter
// table says 0xff, otherwise the table's own value goes into bits 6:4, and
// the IF band-pass filter gain fills the low nibble either way.
func TunerRFGainByte(rfGain, ifBPFGain byte) byte {
	value := byte(0x80)
	if rfGain != 0xff {
		value = rfGain << 4 & 0x70
	}
	return value | ifBPFGain&0x0f
}

// TunerOverloadIndex picks the overload-table column for a frequency: the
// parameter tables carry one entry each for at most 172 MHz, at most 464 MHz
// and everything above.
func TunerOverloadIndex(frequencyKHz uint32) int {
	switch {
	case frequencyKHz <= 172000:
		return 0
	case frequencyKHz <= 464000:
		return 1
	default:
		return 2
	}
}
