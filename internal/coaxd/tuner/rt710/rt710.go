// Copyright (c) 2018-2021 nns779; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from px4_drv (rt710.c) by nns779.

// Package rt710 drives the RafaelMicro RT710 and RT720 satellite tuners over
// any i2c.Bus, typically the gated tuner bus behind a TC90522. The two chips
// share a register map and are told apart at initialisation.
package rt710

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

const registerCount = 0x10

// ChipType is which of the two RafaelMicro satellite tuners is fitted. They
// share a register map but differ in PLL setup and gain reporting; Initialize
// tells them apart from the status register.
type ChipType int

const (
	ChipUnknown ChipType = iota
	ChipRT710
	ChipRT720
)

// SignalOutputMode selects single-ended or differential IF output, fixed by the
// board's IF wiring.
type SignalOutputMode int

const (
	SignalOutputSingle SignalOutputMode = iota
	SignalOutputDifferential
)

// AGCMode is the polarity of the demodulator's gain control line.
type AGCMode int

const (
	AGCNegative AGCMode = iota
	AGCPositive
)

// VGAAttenuateMode switches the variable gain amplifier's attenuator; On pads
// the IF output down for boards that overdrive their demodulator.
type VGAAttenuateMode int

const (
	VGAAttenuateOff VGAAttenuateMode = iota
	VGAAttenuateOn
)

// FineGain trims the IF gain, from most to least added gain. The RT720 only
// distinguishes the 3/2 dB pair from the 1/0 dB pair.
type FineGain int

const (
	FineGain3dB FineGain = iota
	FineGain2dB
	FineGain1dB
	FineGain0dB
)

// ScanMode selects the RT720's bandwidth search behaviour; the RT710 ignores it.
// Auto widens the channel filter so a blind scan does not miss off-centre
// transponders.
type ScanMode int

const (
	ScanManual ScanMode = iota
	ScanAuto
)

// Config describes how the tuner is wired on the board.
type Config struct {
	// XtalKHz is the crystal frequency in kHz; every known board uses 24000.
	XtalKHz          uint32
	LoopThrough      bool
	ClockOut         bool
	SignalOutputMode SignalOutputMode
	AGCMode          AGCMode
	VGAAttenuateMode VGAAttenuateMode
	FineGain         FineGain
	ScanMode         ScanMode
}

// ISDBSBoardConfig is how every board in this tree wires the RT710 for ISDB-S:
// a 24 MHz crystal, differential IF output, positive AGC, the attenuator off,
// the full 3 dB fine gain and manual scanning.
var ISDBSBoardConfig = Config{
	XtalKHz:          24000,
	SignalOutputMode: SignalOutputDifferential,
	AGCMode:          AGCPositive,
	VGAAttenuateMode: VGAAttenuateOff,
	FineGain:         FineGain3dB,
	ScanMode:         ScanManual,
}

type bandwidthParams struct {
	coarse byte
	fine   byte
}

var rt710InitRegisters = [registerCount]byte{
	0x40, 0x1d, 0x20, 0x10, 0x41, 0x50, 0xed, 0x25,
	0x07, 0x58, 0x39, 0x64, 0x38, 0xe7, 0x90, 0x35,
}

var rt720InitRegisters = [registerCount]byte{
	0x00, 0x1c, 0x00, 0x10, 0x41, 0x48, 0xda, 0x4b,
	0x07, 0x58, 0x38, 0x40, 0x37, 0xe7, 0x4c, 0x59,
}

var sleepRegisters = [registerCount]byte{
	0xff, 0x5c, 0x88, 0x30, 0x41, 0xc8, 0xed, 0x25,
	0x47, 0xfc, 0x48, 0xa2, 0x08, 0x0f, 0xf3, 0x59,
}

// bandwidthTable maps a required bandwidth in kHz to the RT710's filter codes.
var bandwidthTable = []struct {
	bandwidth uint32
	params    bandwidthParams
}{
	{50000, bandwidthParams{0, 0}}, {73000, bandwidthParams{0, 1}},
	{96000, bandwidthParams{1, 0}}, {104000, bandwidthParams{1, 1}},
	{116000, bandwidthParams{2, 0}}, {126000, bandwidthParams{2, 1}},
	{134000, bandwidthParams{3, 0}}, {146000, bandwidthParams{3, 1}},
	{158000, bandwidthParams{4, 0}}, {170000, bandwidthParams{4, 1}},
	{178000, bandwidthParams{5, 0}}, {190000, bandwidthParams{5, 1}},
	{202000, bandwidthParams{6, 0}}, {212000, bandwidthParams{6, 1}},
	{218000, bandwidthParams{7, 0}}, {234000, bandwidthParams{7, 1}},
	{244000, bandwidthParams{9, 1}}, {246000, bandwidthParams{10, 0}},
	{262000, bandwidthParams{10, 1}}, {266000, bandwidthParams{11, 0}},
	{282000, bandwidthParams{11, 1}}, {298000, bandwidthParams{12, 1}},
	{318000, bandwidthParams{13, 1}}, {340000, bandwidthParams{14, 1}},
	{358000, bandwidthParams{15, 1}}, {379999, bandwidthParams{16, 1}},
}

// Accumulated LNA gain in tenths of a dB, indexed by the reported gain step.
var rt710LNAGain = [19]uint16{
	0, 26, 42, 74, 103, 129, 158, 181,
	188, 200, 220, 248, 280, 312, 341, 352,
	366, 389, 409,
}

var rt720LNAGain = [32]uint16{
	0, 27, 53, 81, 109, 134, 156, 176,
	194, 202, 211, 221, 232, 245, 258, 271,
	285, 307, 326, 341, 357, 374, 393, 410,
	428, 439, 445, 470, 476, 479, 495, 507,
}

// Tuner is an RT710 or RT720 reached over an I2C bus.
type Tuner struct {
	bus     i2c.Bus
	address byte
	config  Config

	mu          sync.Mutex
	initialized bool
	chip        ChipType
	frequency   uint32
}

// New addresses the tuner on the given bus. address is the 8-bit I2C address.
func New(bus i2c.Bus, address byte, config Config) *Tuner {
	if config.XtalKHz == 0 {
		config.XtalKHz = 24000
	}
	return &Tuner{bus: bus, address: address, config: config}
}

func (tuner *Tuner) Chip() ChipType { return tuner.chip }

// FineGainStepsDB is what the fine gain trim can be set to, in decibels, least
// first. The RT720 folds 3/2 dB into one setting and 1/0 dB into the other, so
// it offers two steps rather than four it could not reach.
// RT710FineGainStepsDB is the RT710's trim, for describing a board before it is
// opened and the chip known: an RT720 has two of these steps, and a level asked
// for is matched to the nearest step either way.
func RT710FineGainStepsDB() []float64 { return []float64{0, 1, 2, 3} }

func (tuner *Tuner) FineGainStepsDB() []float64 {
	if tuner.chip == ChipRT720 {
		return []float64{0, 3}
	}
	return []float64{0, 1, 2, 3}
}

// SetFineGain sets the trim from a step of FineGainStepsDB. It takes effect at
// the next tune, which rebuilds the whole register set from scratch.
func (tuner *Tuner) SetFineGain(step int) error {
	steps := tuner.FineGainStepsDB()
	if step < 0 || step >= len(steps) {
		return fmt.Errorf("the RT710's fine gain has %d steps, 0 to %d, not %d",
			len(steps), len(steps)-1, step)
	}
	// The enumeration counts the other way: FineGain3dB is 0, FineGain0dB is 3.
	fine := FineGain3dB
	if tuner.chip == ChipRT720 {
		if step == 0 {
			fine = FineGain0dB
		}
	} else {
		fine = FineGain(3 - step)
	}
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	tuner.config.FineGain = fine
	return nil
}

// reverseBits mirrors a byte; the chip returns its registers bit-reversed.
func reverseBits(value byte) byte {
	value = value&0x55<<1 | value&0xaa>>1
	value = value&0x33<<2 | value&0xcc>>2
	return value&0x0f<<4 | value&0xf0>>4
}

// readRegisters reads len(into) registers starting at register. The chip always
// answers from register 0, so the transfer covers everything up to the range.
func (tuner *Tuner) readRegisters(register byte, into []byte) error {
	length := int(register) + len(into)
	if len(into) == 0 || length > registerCount {
		return fmt.Errorf("RT710 register read %#02x+%d is out of range", register, len(into))
	}
	if err := tuner.bus.Write(tuner.address, []byte{0x00}); err != nil {
		return err
	}
	data, err := tuner.bus.Read(tuner.address, length)
	if err != nil {
		return err
	}
	if len(data) < length {
		return fmt.Errorf("RT710 short register read: %d of %d", len(data), length)
	}
	for index := range into {
		into[index] = reverseBits(data[int(register)+index])
	}
	return nil
}

func (tuner *Tuner) writeRegisters(registers *[registerCount]byte, register byte, length int) error {
	if length == 0 || int(register)+length > registerCount {
		return fmt.Errorf("RT710 register write %#02x+%d is out of range", register, length)
	}
	command := make([]byte, 1+length)
	command[0] = register
	copy(command[1:], registers[register:int(register)+length])
	return tuner.bus.Write(tuner.address, command)
}

func (tuner *Tuner) writeRegister(registers *[registerCount]byte, register byte) error {
	return tuner.writeRegisters(registers, register, 1)
}

// Initialize identifies the fitted chip.
func (tuner *Tuner) Initialize() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	tuner.initialized = false
	tuner.frequency = 0
	var status [1]byte
	if err := tuner.readRegisters(0x03, status[:]); err != nil {
		return fmt.Errorf("RT710 did not respond: %w", err)
	}
	if status[0]&0xf0 == 0x70 {
		tuner.chip = ChipRT710
	} else {
		tuner.chip = ChipRT720
	}
	tuner.initialized = true
	return nil
}

// setPLL programs the synthesiser for a frequency in kHz.
func (tuner *Tuner) setPLL(registers *[registerCount]byte, frequency uint32) error {
	xtal := tuner.config.XtalKHz
	vcoMin := uint32(2350000)
	vcoMax := vcoMin * 2
	mixDiv := uint32(2)
	vcoFrequency := frequency * mixDiv

	tuner.frequency = 0
	for mixDiv <= 16 {
		if vcoFrequency >= vcoMin && vcoFrequency <= vcoMax {
			break
		}
		mixDiv *= 2
		vcoFrequency = frequency * mixDiv
	}

	var divNumber byte
	switch mixDiv {
	case 2:
		divNumber = 1
	case 4:
		divNumber = 0
	case 8:
		divNumber = 2
	case 16:
		divNumber = 3
	default:
		divNumber = 0
	}

	registers[0x04] = registers[0x04]&0xfe | divNumber&0x01
	if err := tuner.writeRegister(registers, 0x04); err != nil {
		return err
	}

	if tuner.chip == ChipRT720 {
		registers[0x08] = registers[0x08]&0xef | divNumber<<3&0x10
		if err := tuner.writeRegister(registers, 0x08); err != nil {
			return err
		}
		registers[0x04] &= 0x3f
		if divNumber <= 1 {
			registers[0x04] |= 0x40
			registers[0x0c] |= 0x10
		} else {
			registers[0x04] |= 0x80
			registers[0x0c] &= 0xef
		}
		if err := tuner.writeRegister(registers, 0x04); err != nil {
			return err
		}
		if err := tuner.writeRegister(registers, 0x0c); err != nil {
			return err
		}
	}

	integer := uint32(vcoFrequency / 2 / xtal)
	fraction := vcoFrequency - xtal*2*integer
	switch {
	case fraction < xtal/64:
		fraction = 0
	case fraction > xtal*127/64:
		fraction = 0
		integer++
	case fraction > xtal*127/128 && fraction < xtal:
		fraction = xtal * 127 / 128
	case fraction > xtal && fraction < xtal*129/128:
		fraction = xtal * 129 / 128
	}

	ni := byte((integer - 13) / 4)
	si := byte(integer - uint32(ni)*4 - 13)
	registers[0x05] = ni&0x3f | si<<6&0xc0
	if err := tuner.writeRegister(registers, 0x05); err != nil {
		return err
	}

	if fraction == 0 {
		registers[0x04] |= 0x02
	}
	if err := tuner.writeRegister(registers, 0x04); err != nil {
		return err
	}

	// Accumulate the sigma-delta fraction one halving at a time.
	nsdm := uint32(2)
	sdm := uint16(0)
	for fraction > 1 {
		step := xtal * 2 / nsdm
		if fraction > step {
			sdm += uint16(0x8000 / (nsdm / 2))
			fraction -= step
			if nsdm >= 0x8000 {
				break
			}
		}
		nsdm *= 2
	}
	registers[0x07] = byte(sdm >> 8)
	registers[0x06] = byte(sdm)
	if err := tuner.writeRegister(registers, 0x07); err != nil {
		return err
	}
	if err := tuner.writeRegister(registers, 0x06); err != nil {
		return err
	}

	tuner.frequency = frequency
	return nil
}

// Tune moves the tuner to a frequency in kHz and sets the channel filter from
// the symbol rate in kBaud and the roll-off index (0 to 5).
func (tuner *Tuner) Tune(frequency, symbolRate, rolloff uint32) error {
	if rolloff > 5 {
		return fmt.Errorf("RT710 roll-off index %d is outside 0..5", rolloff)
	}
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return errors.New("RT710 is not initialised")
	}

	registers := rt720InitRegisters
	if tuner.chip == ChipRT710 {
		registers = rt710InitRegisters
	}

	if tuner.config.LoopThrough {
		registers[0x01] &= 0xfb
	} else {
		registers[0x01] |= 0x04
	}
	if tuner.config.ClockOut {
		registers[0x03] &= 0xef
	} else {
		registers[0x03] |= 0x10
	}
	if tuner.config.SignalOutputMode == SignalOutputDifferential {
		registers[0x0b] &= 0xef
	} else {
		registers[0x0b] |= 0x10
	}
	if tuner.config.AGCMode == AGCPositive {
		registers[0x0d] |= 0x10
	} else {
		registers[0x0d] &= 0xef
	}
	if tuner.config.VGAAttenuateMode == VGAAttenuateOn {
		registers[0x0b] |= 0x08
	} else {
		registers[0x0b] &= 0xf7
	}
	if tuner.chip == ChipRT710 {
		if tuner.config.FineGain >= FineGain3dB && tuner.config.FineGain <= FineGain0dB {
			registers[0x0e] = registers[0x0e]&0xfc | byte(tuner.config.FineGain)&0x03
		}
	} else {
		if tuner.config.FineGain == FineGain3dB || tuner.config.FineGain == FineGain2dB {
			registers[0x0e] &= 0xfe
		} else {
			registers[0x0e] |= 0x01
		}
		registers[0x03] &= 0xf0
	}

	if err := tuner.writeRegisters(&registers, 0x00, registerCount); err != nil {
		return fmt.Errorf("RT710 register load: %w", err)
	}
	if err := tuner.setPLL(&registers, frequency); err != nil {
		return fmt.Errorf("RT710 synthesiser: %w", err)
	}
	time.Sleep(10 * time.Millisecond)

	if tuner.chip == ChipRT710 {
		if frequency-1600000 >= 350000 {
			registers[0x02] &= 0xbf
			registers[0x08] &= 0x7f
			if frequency >= 1950000 {
				registers[0x0a] = 0x38
			}
		} else {
			registers[0x02] |= 0x40
			registers[0x08] |= 0x80
		}
		for _, register := range []byte{0x0a, 0x02, 0x08} {
			if err := tuner.writeRegister(&registers, register); err != nil {
				return err
			}
		}
		registers[0x0e] &= 0xf3
		if frequency >= 2000000 {
			registers[0x0e] |= 0x08
		}
		if err := tuner.writeRegister(&registers, 0x0e); err != nil {
			return err
		}
	} else {
		if tuner.config.ScanMode == ScanAuto {
			registers[0x0b] |= 0x02
			symbolRate += 10000
		} else {
			registers[0x0b] &= 0xfc
			if symbolRate >= 15000 {
				symbolRate += 6000
			}
		}
		if err := tuner.writeRegister(&registers, 0x0b); err != nil {
			return err
		}
	}

	bandwidth := symbolRate * (115 + rolloff*5) / 10
	if bandwidth == 0 {
		return errors.New("RT710: symbol rate gives a zero channel bandwidth")
	}
	params := tuner.bandwidthCodes(bandwidth, symbolRate, rolloff)
	registers[0x0f] = params.coarse<<2&0xfc | params.fine&0x03
	return tuner.writeRegister(&registers, 0x0f)
}

func (tuner *Tuner) bandwidthCodes(bandwidth, symbolRate, rolloff uint32) bandwidthParams {
	var params bandwidthParams
	if tuner.chip == ChipRT710 {
		if bandwidth >= 380000 {
			bandwidth -= 380000
			if bandwidth%17400 != 0 {
				params.coarse++
			}
			params.coarse += byte(bandwidth/17400) + 0x10
			params.fine = 1
			return params
		}
		for _, entry := range bandwidthTable {
			if bandwidth <= entry.bandwidth {
				return entry.params
			}
		}
		return params
	}

	// The RT720 derives its filter from the symbol rate directly.
	if rolloff > 1 {
		params.fine = 1
	}
	span := uint32(params.fine) * 20000
	scaled := symbolRate * 12
	switch {
	case symbolRate <= 15000:
		symbolRate += 3000
	case symbolRate <= 20000:
		symbolRate += 2000
	case symbolRate <= 30000:
		symbolRate += 1000
	}
	switch {
	case scaled <= 88000+span:
		params.coarse = 0
	case scaled <= 368000+span:
		params.coarse = byte((scaled - 88000 - span) / 20000)
		if (scaled-88000-span)%20000 != 0 {
			params.coarse++
		}
		if params.coarse > 6 {
			params.coarse++
		}
	case scaled <= 764000+span:
		params.coarse = byte((scaled-368000-span)/20000) + 15
		if (scaled+25216-span)%20000 != 0 {
			params.coarse++
		}
		switch {
		case params.coarse >= 33:
			params.coarse += 3
		case params.coarse >= 29:
			params.coarse += 2
		case params.coarse >= 27:
			params.coarse += 3
		case params.coarse >= 24:
			params.coarse += 2
		case params.coarse >= 19:
			params.coarse++
		}
	default:
		params.coarse = 42
	}
	return params
}

// PLLLocked reports whether the synthesiser reached lock.
func (tuner *Tuner) PLLLocked() (bool, error) {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return false, errors.New("RT710 is not initialised")
	}
	var status [1]byte
	if err := tuner.readRegisters(0x02, status[:]); err != nil {
		return false, err
	}
	return status[0]&0x80 != 0, nil
}

// Gain reports the tuner's current RF gain step.
func (tuner *Tuner) Gain() (byte, error) {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return 0, errors.New("RT710 is not initialised")
	}
	var status [1]byte
	if err := tuner.readRegisters(0x01, status[:]); err != nil {
		return 0, err
	}
	return tuner.gainStep(status[0]), nil
}

// gainStep folds the raw gain field into the step the gain tables are indexed by.
func (tuner *Tuner) gainStep(raw byte) byte {
	step := raw&0xf0>>4 | raw&0x01<<4
	if tuner.chip != ChipRT710 {
		return step
	}
	switch {
	case step <= 2:
		return 0
	case step <= 9:
		return step - 2
	case step <= 12:
		return 7
	case step <= 22:
		return step - 5
	default:
		return 18
	}
}

// SignalStrength estimates the input level in thousandths of a dBm.
func (tuner *Tuner) SignalStrength() (int32, error) {
	gain, err := tuner.Gain()
	if err != nil {
		return 0, err
	}
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	var value int32
	if tuner.chip == ChipRT710 {
		switch {
		case tuner.frequency < 1200000:
			value = 190
		case tuner.frequency < 1800000:
			value = 170
		default:
			value = 140
		}
		if int(gain) < len(rt710LNAGain) {
			value += int32(rt710LNAGain[gain])
		}
	} else {
		value = 70
		if int(gain) < len(rt720LNAGain) {
			value += int32(rt720LNAGain[gain])
		}
	}
	return value * -100, nil
}

// Sleep powers the tuner down.
func (tuner *Tuner) Sleep() error {
	tuner.mu.Lock()
	defer tuner.mu.Unlock()
	if !tuner.initialized {
		return errors.New("RT710 is not initialised")
	}
	registers := sleepRegisters
	if tuner.chip == ChipRT720 {
		registers[0x01] = 0x5e
		registers[0x03] |= 0x20
	} else if tuner.config.ClockOut {
		registers[0x03] = 0x20
	}
	return tuner.writeRegisters(&registers, 0x00, registerCount)
}
