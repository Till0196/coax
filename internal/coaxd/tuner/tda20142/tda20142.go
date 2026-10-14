// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// Package tda20142 drives the NXP TDA20142 ISDB-S tuner over any i2c.Bus,
// typically the satellite tuner bus behind a TC90522.
package tda20142

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// address is where a TDA20142 answers, on both boards that carry one: the
// PX-W3U3 V2 and the US-3POUT, each behind its TC90522's tuner bus.
const address byte = 0xa8

// Tuner is a TDA20142 reached over an I2C bus.
type Tuner struct {
	bus i2c.Bus
}

// New addresses a TDA20142 on the given bus, brings its registers to the state
// the part needs, and runs the two calibrations that have to finish before the
// first tune.
func New(bus i2c.Bus) (*Tuner, error) {
	tuner := &Tuner{bus: bus}
	if err := tuner.configure(); err != nil {
		return nil, err
	}
	if err := tuner.calibrateOscillator(); err != nil {
		return nil, err
	}
	if err := tuner.calibrateFilter(); err != nil {
		return nil, err
	}
	return tuner, nil
}

// setup applies a run of register writes in order and keeps the first failure,
// so an initialisation that is one long sequence reads as that sequence instead
// of as forty error checks. Nothing after a failure reaches the bus.
type setup struct {
	tuner *Tuner
	err   error
}

// write replaces a whole register.
func (s *setup) write(register, value byte) {
	if s.err == nil {
		s.err = s.tuner.masked(register, value, 0)
	}
}

// set turns the given bits on and leaves the rest of the register alone.
func (s *setup) set(register, bits byte) {
	if s.err == nil {
		s.err = s.tuner.masked(register, bits, bits)
	}
}

// clear turns the given bits off.
func (s *setup) clear(register, bits byte) {
	if s.err == nil {
		s.err = s.tuner.masked(register, 0, bits)
	}
}

// replace rewrites the bits under mask and leaves the others.
func (s *setup) replace(register, mask, value byte) {
	if s.err == nil {
		s.err = s.tuner.masked(register, value, mask)
	}
}

// configure brings the part up. The TDA20142 has no public register
// documentation, so the bit patterns stay as the part requires them; what the
// form of each operation says is which bits an entry actually disturbs, which
// is the part that matters when reading the sequence back. The order is the
// part's own: several of these latch on the write that follows.
func (tuner *Tuner) configure() error {
	s := setup{tuner: tuner}
	s.set(0x02, 0x81)
	s.replace(0x06, 0xb9, 0x39)
	s.set(0x07, 0xae)
	s.set(0x0f, 0x80)
	s.clear(0x18, 0x80)
	s.set(0x1a, 0xc0)
	s.write(0x22, 0xff)
	s.clear(0x23, 0x01)
	s.set(0x25, 0x08)
	s.replace(0x27, 0xe0, 0xc0)
	s.replace(0x24, 0x34, 0x04)
	s.clear(0x0d, 0x20)
	s.replace(0x09, 0xfe, 0xb0)
	s.replace(0x0a, 0xef, 0x6f)
	s.replace(0x0b, 0xfe, 0x7a)
	s.write(0x0c, 0x00)
	s.write(0x19, 0xfa)
	s.clear(0x1b, 0x80)
	s.set(0x21, 0x40)
	s.replace(0x10, 0xd0, 0x90)
	s.replace(0x14, 0x30, 0x20)
	return s.err
}

// calibrateOscillator pulses the local oscillator calibration. Register 0x18's
// bit 0 is the trigger: it is raised, the two enables around it are set, and
// then it is dropped again, which is what starts the run.
func (tuner *Tuner) calibrateOscillator() error {
	s := setup{tuner: tuner}
	s.set(0x1a, 0x40)
	s.set(0x18, 0x01)
	s.set(0x18, 0x80)
	s.set(0x1b, 0x80)
	s.clear(0x18, 0x01)
	return s.err
}

// calibrateFilter runs the baseband filter calibration and applies its result.
// The part measures the filter itself and leaves the correction in register
// 0x10; that value goes into the low nibble of 0x0f, and the enables raised for
// the measurement are taken back down.
func (tuner *Tuner) calibrateFilter() error {
	s := setup{tuner: tuner}
	s.replace(0x0f, 0xe0, 0x80)
	s.replace(0x13, 0x30, 0x20)
	s.set(0x12, 0xc0)
	// The start bit is written twice. The part does not act on the first one.
	s.set(0x10, 0x20)
	s.set(0x10, 0x20)
	s.set(0x0f, 0x20)
	if s.err != nil {
		return s.err
	}

	// Register 0x11 stays zero until the measurement lands.
	var calibration byte
	for attempt := 0; attempt < 3; attempt++ {
		value, err := tuner.read(0x11)
		if err != nil {
			return err
		}
		calibration = value
		if value != 0 {
			break
		}
	}
	if calibration == 0 {
		return fmt.Errorf("TDA20142 calibration timed out")
	}
	correction, err := tuner.read(0x10)
	if err != nil {
		return err
	}

	s.replace(0x0f, 0x0f, correction)
	s.set(0x0f, 0x40)
	s.clear(0x0f, 0x20)
	s.clear(0x12, 0x80)
	s.replace(0x0d, 0x60, 0x20)
	return s.err
}

// read fetches one register.
func (tuner *Tuner) read(register byte) (byte, error) {
	if err := tuner.bus.Write(address, []byte{register}); err != nil {
		return 0, err
	}
	data, err := tuner.bus.Read(address, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// masked writes a register, merging value under mask when only some bits change.
func (tuner *Tuner) masked(register, value, mask byte) error {
	if mask != 0 && mask != 0xff {
		current, err := tuner.read(register)
		if err != nil {
			return err
		}
		value = i2c.Mask(current, value, mask)
	}
	return tuner.bus.Write(address, []byte{register, value})
}

// multiplierB picks the second PLL multiplier/divisor pair that lands the given
// comparison frequency inside the band the chip reports in registers 0x21 and
// 0x1b, and programs it. The result packs extra | multiplier<<8 | divisor, where
// extra (bit 16) is register 0x21's 0x40 flag shifted up; setFrequency unpacks
// it the same way.
func (tuner *Tuner) multiplierB(frequency uint64) (uint64, error) {
	value21, err := tuner.read(0x21)
	if err != nil {
		return 0, err
	}
	index := uint64((value21 >> 4) & 4)
	extra := uint64(value21&0x40) << 10
	value1b, err := tuner.read(0x1b)
	if err != nil {
		return 0, err
	}
	index |= uint64((value1b << 1) & 2)
	bounds := []uint64{1345, 2485, 1305, 2525, 1310, 2510, 1290, 2530}
	low, high := bounds[index], bounds[index|1]
	half, multiplier, divisor := frequency>>1, uint64(1), uint64(4)
	if high >= half {
		divisor = 2
		if low > half {
			if high >= frequency {
				if low <= frequency {
					multiplier = 2
				} else {
					multiplier = 3
				}
			} else {
				multiplier, divisor = 3, 4
			}
		}
	}
	result := frequency * multiplier / divisor
	if result < low || result > high {
		return 0, fmt.Errorf("TDA20142 multiplier is outside %d..%d", low, high)
	}
	if err := tuner.masked(0x03, byte((multiplier-1)<<6), 0xc0); err != nil {
		return 0, err
	}
	if err := tuner.masked(0x1a, byte(divisor<<3), 0x20); err != nil {
		return 0, err
	}
	return extra | multiplier<<8 | divisor, nil
}

// setFrequency programs the LO divider chain for a frequency in kHz: the first
// multiplier from the frequency band, the second stage from multiplierB, and the
// product long-divided against the 27 MHz reference into the whole (0x1e) and
// 16-bit fractional (0x1f/0x20) divider registers.
func (tuner *Tuner) setFrequency(frequency uint64) error {
	bounds := []uint64{1720000, 1433000, 1228000, 1075000}
	multiplierIndex := 0
	for multiplierIndex < len(bounds) && frequency < bounds[multiplierIndex] {
		multiplierIndex++
	}
	values := []byte{0x08, 0x88, 0x28, 0xa8, 0x04}
	register22 := byte(0xff)
	if multiplierIndex == 4 {
		register22 = 0x1e
	}
	if err := tuner.masked(0x22, register22, 0); err != nil {
		return err
	}
	if err := tuner.masked(0x23, values[multiplierIndex]|0x03, 0xaf); err != nil {
		return err
	}
	multiplierA := uint64(multiplierIndex + 4)
	remainder := frequency * multiplierA * 10
	integer := remainder / 27000
	remainder %= 27000
	multiplierB, err := tuner.multiplierB(integer)
	if err != nil {
		return err
	}
	numerator := (multiplierB >> 8) & 0xff
	integer *= numerator
	remainder *= numerator
	denominator := (multiplierB & 0xff) * 10
	whole := integer / denominator
	integer %= denominator
	remainder += integer * 27000
	denominator *= 27000
	flag := (multiplierB >> 16) & 0xff
	remainder <<= 8
	fractionHigh := remainder / denominator
	remainder %= denominator
	whole += fractionHigh >> 8
	fractionHigh &= 0xff
	remainder <<= 8
	fraction := fractionHigh<<8 | remainder/denominator
	if flag&1 == 0 {
		fraction = (fraction + 1) >> 1
		if fraction < 0x4000 {
			fraction += 0x8000
			whole--
		}
	}
	if whole < 128 {
		return fmt.Errorf("TDA20142 divider underflow")
	}
	for _, register := range [][3]byte{{0x1e, byte(whole - 128), 0}, {0x1f, byte(fraction >> 8), 0}, {0x20, byte(fraction), 0}} {
		if err := tuner.masked(register[0], register[1], register[2]); err != nil {
			return err
		}
	}
	return nil
}

// setClock runs the chip's clock calibration, waits for it to report ready, and
// programs the data-rate and filter registers for the fixed ISDB-S symbol rate.
func (tuner *Tuner) setClock() error {
	s := setup{tuner: tuner}
	s.replace(0x12, 0xc0, 0x40)
	s.replace(0x13, 0x03, 0x02)
	s.clear(0x13, 0x80)
	// As with the filter, the start bit is written twice.
	s.set(0x13, 0x20)
	s.set(0x13, 0x20)
	s.clear(0x13, 0x80)
	s.set(0x13, 0x10)
	if s.err != nil {
		return s.err
	}
	ready := false
	for attempt := 0; attempt < 3; attempt++ {
		value, err := tuner.read(0x15)
		if err != nil {
			return err
		}
		if value&0x10 != 0 {
			ready = true
			break
		}
	}
	if !ready {
		return fmt.Errorf("TDA20142 clock calibration timed out")
	}
	s.clear(0x13, 0x10)
	s.clear(0x12, 0x80)

	// The baseband filter is set from the bandwidth the signal occupies, which
	// for the fixed ISDB-S symbol rate and roll-off works out at one value.
	bandwidth := occupiedBandwidth(symbolRate, rollOff)
	s.replace(0x0a, 0x0f, dataRateSetting(bandwidth))
	s.replace(0x0b, 0xfe, filterSetting(bandwidth)<<1)
	return s.err
}

// The ISDB-S carrier this part sees: 28.86 Mbaud at a roll-off of 0.35, both
// fixed by the standard.
const (
	symbolRate = 28860
	rollOff    = 120
)

// occupiedBandwidth is the width in kHz the signal takes up, which is what both
// filter settings are chosen from. The 2000 kHz is the margin the part wants
// either side; the divisor scales the roll-off factor back out.
func occupiedBandwidth(rate, roll uint64) uint64 {
	return (rate*roll/2+3920*100)/72 + 2000
}

// dataRateSetting is register 0x0a's low nibble: four brackets, widest signal
// to narrowest.
func dataRateSetting(bandwidth uint64) byte {
	switch {
	case bandwidth >= 45000:
		return 0x08
	case bandwidth >= 35000:
		return 0x0a
	case bandwidth >= 25000:
		return 0x0c
	default:
		return 0x0f
	}
}

// filterCutoff pairs the upper edge of a bandwidth bracket, in kHz without the
// margin, with the filter setting the part takes for it.
type filterCutoff struct {
	upTo    uint64
	setting byte
}

// filterSettings covers 2.85 MHz to 100 MHz. The part has no evenly spaced
// codes, so the brackets are uneven and the settings are not monotonic.
var filterSettings = []filterCutoff{
	{2850, 0x13}, {3900, 0x18}, {4850, 0x22}, {5800, 0x25}, {6650, 0x31},
	{7700, 0x33}, {8900, 0x41}, {9900, 0x43}, {10700, 0x51}, {11400, 0x52},
	{12500, 0x60}, {13500, 0x62}, {14550, 0x70}, {15800, 0x72}, {16800, 0x73},
	{17750, 0x74}, {18350, 0x3d}, {19200, 0x75}, {20200, 0x76}, {21000, 0x5a},
	{22450, 0x77}, {23900, 0x5b}, {25000, 0x6a}, {26500, 0x79}, {27900, 0x6b},
	{28750, 0x7a}, {29700, 0x4e}, {30700, 0x5d}, {31450, 0x6c}, {33950, 0x7b},
	{39000, 0x7c}, {42500, 0x7d}, {43950, 0x6e}, {47350, 0x5f}, {51700, 0x7e},
	{57750, 0x6f}, {100000, 0x7f},
}

// filterSetting picks register 0x0b's setting for a bandwidth. Anything wider
// than the table covers takes the last entry, which is the part's widest.
func filterSetting(bandwidth uint64) byte {
	width := bandwidth - 2000
	for _, cutoff := range filterSettings {
		if width < cutoff.upTo {
			return cutoff.setting
		}
	}
	return filterSettings[len(filterSettings)-1].setting
}

// setAGC takes one AGC measurement and sets the gain-control registers from
// the level bracket it falls into.
func (tuner *Tuner) setAGC() error {
	value, err := tuner.read(0x0d)
	if err != nil {
		return err
	}
	if value&0x80 != 0 {
		return fmt.Errorf("TDA20142 AGC is busy")
	}
	if err := tuner.masked(0x0d, value|0x80, 0); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	high, err := tuner.read(0x0d)
	if err != nil {
		return err
	}
	low, err := tuner.read(0x0e)
	if err != nil {
		return err
	}
	if err := tuner.masked(0x0d, 0, 0x80); err != nil {
		return err
	}
	agc := uint16(high&1)<<8 | uint16(low)
	bounds := []uint16{0, 7, 63}
	mode := 0
	for mode < len(bounds) && agc > bounds[mode] {
		mode++
	}
	a2 := []byte{3, 2, 0, 4}[mode]
	a3 := []byte{3, 7, 15, 6}[mode]
	value, err = tuner.read(0x07)
	if err != nil {
		return err
	}
	value = value&0xac | 0x02
	if err := tuner.masked(0x06, 0, 0x40); err != nil {
		return err
	}
	if err := tuner.masked(0x07, value, 0); err != nil {
		return err
	}
	value, err = tuner.read(0x06)
	if err != nil {
		return err
	}
	value &= 0x48
	if a2 != 4 {
		value |= 0x80
	}
	if a2 == 3 {
		value |= 1
	}
	if a2&0xfc == 0 {
		value |= (a2 & 3) << 4
	}
	for _, register := range [][3]byte{{0x06, value, 0}, {0x09, 0xb0, 0xfc}, {0x0a, 0x60, 0xe0}, {0x0c, a3 << 4, 0xf0}} {
		if err := tuner.masked(register[0], register[1], register[2]); err != nil {
			return err
		}
	}
	return nil
}

// Tune moves the tuner to a satellite frequency given in kHz.
func (tuner *Tuner) Tune(frequencyKHz uint64) error {
	if frequencyKHz < 900000 || frequencyKHz > 2450000 {
		return fmt.Errorf("TDA20142 satellite frequency %d kHz is out of range", frequencyKHz)
	}
	if err := tuner.setFrequency(frequencyKHz); err != nil {
		return err
	}
	if err := tuner.setClock(); err != nil {
		return err
	}
	return tuner.setAGC()
}
