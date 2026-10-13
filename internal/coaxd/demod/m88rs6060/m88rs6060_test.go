// Copyright (c) Montage; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package m88rs6060

const (
	testDemodAddress = 0x69
	testTunerAddress = 0x21
)

// fakeBus is a minimal i2c.Bus backed by per-address register maps: tests seed
// demod/tuner state and read back what was written, in order via writes. Read
// (address-only) always returns zeroes, since nothing here uses it.
type fakeBus struct {
	registers map[byte]map[byte]byte
	writes    []struct {
		address, register, value byte
	}
}

func newFakeBus() *fakeBus {
	return &fakeBus{registers: map[byte]map[byte]byte{
		testDemodAddress: {},
		testTunerAddress: {},
	}}
}

func (bus *fakeBus) Write(address byte, data []byte) error {
	if len(data) < 1 {
		return nil
	}
	register := data[0]
	if len(data) >= 2 {
		if bus.registers[address] == nil {
			bus.registers[address] = map[byte]byte{}
		}
		bus.registers[address][register] = data[1]
		bus.writes = append(bus.writes, struct{ address, register, value byte }{address, register, data[1]})
	}
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

// WriteRead answers a one-byte register write plus a read of that many
// consecutive registers; any other write length reads as zeroes.
func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	out := make([]byte, readLength)
	if len(write) == 1 {
		for i := range out {
			out[i] = bus.registers[address][write[0]+byte(i)]
		}
	}
	return out, nil
}

func newTestDemod(bus *fakeBus) *Demod {
	demod := New(bus, "", Config{DemodAddress: testDemodAddress, TunerAddress: testTunerAddress})
	demod.warm = true
	return demod
}
