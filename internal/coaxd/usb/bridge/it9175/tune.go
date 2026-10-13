// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package it9175

import (
	"fmt"
	"time"
)

// boundaryCount is how many tuning bands the chip divides its range into.
const boundaryCount = 8

// defaultBoundaries are the nominal upper edges of those bands in kHz. They are
// replaced during bring-up by figures derived from the chip's measured clock.
var defaultBoundaries = [boundaryCount]uint32{78200, 117300, 156400, 234600, 312800, 469200, 625600, 950000}

// ndivTable is the synthesiser divider each band uses. It has one entry more
// than there are bands, for a frequency above the highest edge.
var ndivTable = [boundaryCount + 1]uint8{48, 32, 24, 16, 12, 8, 6, 4, 2}

// lnaBands are the upper edges in kHz of the tuner's low-noise amplifier
// ranges, which are independent of the synthesiser bands above.
var lnaBands = [8]uint32{444000, 484000, 533000, 587000, 645000, 710000, 782000, 860000}

// bandwidthCodes translate the chip's bandwidth mode into the tuner's filter
// setting. ISDB-T is always mode 0.
var bandwidthCodes = [4]byte{2, 4, 6, 0}

// tuneParams1 and tuneParams2 are coefficient sets rewritten before every
// acquisition. They do not depend on the channel.
var tuneParams1 = []byte{
	0x5e, 0x45, 0x5b, 0x7f, 0x56, 0x6a, 0x4d, 0xc5, 0x2c, 0xf9, 0x2b, 0xa7,
	0x29, 0x3a, 0x25, 0x1b, 0x17, 0x01, 0x16, 0x53, 0x15, 0x16, 0x12, 0xfa,
}

var tuneParams2 = []byte{
	0x03, 0x35, 0xed, 0xd7, 0x01, 0x9a, 0xf6, 0xeb, 0x00, 0xcd, 0x81, 0xe2,
	0x00, 0xcd, 0x7b, 0x76, 0x00, 0xcd, 0x75, 0x0a, 0x00, 0x66, 0xbd, 0xbb,
	0x01, 0x9a, 0xf6, 0xeb, 0x00, 0xcd, 0x7b, 0x76, 0x7e, 0x02, 0x9b, 0x01,
}

// freqIndex reports which synthesiser band a frequency falls in and the divider
// that band uses. A frequency above the highest edge takes the extra entry.
func (device *Device) freqIndex(frequencyKHz uint32) (int, uint8) {
	for index, upper := range device.boundaries {
		if upper >= frequencyKHz {
			return index, ndivTable[index]
		}
	}
	return boundaryCount, ndivTable[boundaryCount]
}

// calibrateBoundaries recomputes the band edges from the chip's own measured
// reference, so the tuning arithmetic tracks the fitted crystal rather than a
// nominal one. count comes from the chip and is clamped: the table cannot grow.
func (device *Device) calibrateBoundaries(reference uint32, count int) {
	if count > boundaryCount {
		count = boundaryCount
	}
	for index := 0; index < count; index++ {
		device.boundaries[index] = (reference / uint32(ndivTable[index+1])) >> 2
	}
}

// tunerWord builds the synthesiser's frequency word, rounded to nearest, and
// picks the amplifier band.
func tunerWord(frequencyKHz uint32, multiplier, divisor uint32) (uint32, byte) {
	band := len(lnaBands) - 1
	for index, upper := range lnaBands {
		if upper >= frequencyKHz {
			band = index
			break
		}
	}
	if divisor == 0 {
		return 0, byte(band)
	}
	value := (uint64(frequencyKHz) << 1) * uint64(multiplier) / uint64(divisor)
	return uint32((value + 1) >> 1), byte(band)
}

// synthesis is everything a tune computes rather than reads from a table: the
// amplifier band and the three forms the synthesiser word is written in.
type synthesis struct {
	lnaBand byte
	// word is the frequency word itself, with the band number in its top bits.
	word uint32
	// corrected is the same word with the tuner's calibration applied, which
	// is what the tuner's own registers take.
	corrected uint32
	// shifted repeats the word with the band number offset by two, which is
	// what the demodulator's copy takes.
	shifted uint32
}

// synthesiser works out the frequency words for a channel. It reads no registers
// -- the calibration byte is its one value from the chip -- so the tuning
// arithmetic can be checked without hardware.
func (device *Device) synthesiser(frequencyKHz uint32, calibration byte) synthesis {
	index, ndiv := device.freqIndex(frequencyKHz)
	word, lna := tunerWord(frequencyKHz, uint32(ndiv)*uint32(device.fdiv), uint32(device.xtal))

	// The calibration register holds a sign-magnitude six-bit correction.
	correction := (int(calibration)&0x1f - int(calibration)&0x20) * int(ndiv)
	if device.clockMode == 0 {
		correction = correction * 9 >> 5
	} else {
		correction >>= 1
	}

	word |= uint32(index) << 13
	return synthesis{
		lnaBand:   lna,
		word:      word,
		corrected: uint32(int32(word) + int32(correction)),
		// The band number wraps in a byte before it is shifted in.
		shifted: uint32(byte(index-2))<<13 | word&0x1fff,
	}
}

// scaled is the chip's fixed-point divide: a shifted left by sh, over b.
func scaled(a, b uint64, sh uint) uint32 {
	return uint32((a << sh) / b)
}

// initialize brings the demodulator and tuner up once the firmware is running.
func (device *Device) initialize() error {
	if err := device.writeValues([]regValue{
		// I2C bus clock 100 kHz, now that the download is done.
		{0xf103, 0x1a},
		// No second demodulator shares the bus on this chip.
		{0x4bfb, 0},
		{0xcfff, 0},
		{0xf641, device.tunerID},
	}); err != nil {
		return err
	}
	if err := device.writeMasks([]regMask{
		{0x80ec4c, 0xa8, 0},
		{0xd8c8, 0x01, 0x01},
		{0xd8c9, 0x01, 0x01},
		{0xd8bc, 0x01, 0x01},
		{0xd8bd, 0x01, 0x01},
		{0xe00c, 0, 0},
		{0xd864, 0, 0},
		{0x80f5ca, 0x01, 0x01},
		{0x80f715, 0x01, 0x01},
	}); err != nil {
		return err
	}

	// Program the core clock at 12 MHz and the ADC at 20.25 MHz. The ADC value
	// is written three bytes wide even though it is computed as four.
	clock := scaled(12_000_000, 1_000_000, 19)
	if err := device.WriteRegisters(0x800025, []byte{byte(clock), byte(clock >> 8), byte(clock >> 16), byte(clock >> 24)}); err != nil {
		return err
	}
	adc := scaled(20_250_000, 1_000_000, 19)
	if err := device.WriteRegisters(0x80f1cd, []byte{byte(adc), byte(adc >> 8), byte(adc >> 16)}); err != nil {
		return err
	}

	if err := device.writeBlocks(demodInit1); err != nil {
		return fmt.Errorf("initialize the IT9175 demodulator: %w", err)
	}
	if err := device.writeBlocks(demodInit2); err != nil {
		return fmt.Errorf("initialize the IT9175 demodulator: %w", err)
	}
	if err := device.writeValues([]regValue{{0x80004e, 0}, {0x800000, 0x01}}); err != nil {
		return err
	}
	time.Sleep(30 * time.Millisecond)
	if err := device.writeValues([]regValue{{0xd827, 0}, {0xd829, 0}}); err != nil {
		return err
	}

	if err := device.readClockMode(); err != nil {
		return err
	}
	if err := device.calibrate(); err != nil {
		return err
	}
	if err := device.checkConfigured(); err != nil {
		return err
	}
	return device.configureStream()
}

// readClockMode asks the chip which reference it is running on and picks the
// crystal figures the tuning arithmetic uses.
func (device *Device) readClockMode() error {
	mode, err := device.ReadRegister(0x80ec86)
	if err != nil {
		return err
	}
	device.clockMode = mode
	switch mode {
	case 0: // 12.000 MHz
		device.xtal, device.fdiv, device.iqikCal = 2000, 3, 16
	case 1: // 20.480 MHz
		device.xtal, device.fdiv, device.iqikCal = 20480, 18, 6
	default:
		// A chip reporting something new still tunes, if badly.
		device.xtal, device.fdiv, device.iqikCal = 640, 1, 6
	}
	return nil
}

// calibrate waits for the chip's frequency measurement to settle, rebuilds the
// band table from it and seeds the I/Q imbalance register.
func (device *Device) calibrate() error {
	count, err := device.ReadRegister(0x80ed03)
	if err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	var measured uint32
	for attempt := 0; attempt < 15; attempt++ {
		data, err := device.ReadRegisters(0x80ed23, 2)
		if err != nil {
			return err
		}
		measured = uint32(data[1])<<8 | uint32(data[0])
		if measured != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if device.fdiv != 0 {
		reference := uint32(uint64(measured<<2) * uint64(device.xtal) / uint64(device.fdiv))
		device.calibrateBoundaries(reference, int(count))
	}

	time.Sleep(20 * time.Millisecond)
	for attempt := 0; attempt < 10; attempt++ {
		state, err := device.ReadRegister(0x80ec82)
		if err != nil {
			return err
		}
		if state != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return device.WriteRegister(0x80ed81, device.iqikCal)
}

// configureStream sets the transport interface up: one bulk stream on endpoint
// 0x84, packets gathered into transfers of streamPackets, and a filter dropping
// the null packets the chip pads the stream with.
func (device *Device) configureStream() error {
	if err := device.writeMasks([]regMask{
		{0xd8d0, 0x01, 0x01},
		{0xd8d1, 0x01, 0x01},
		{0xf41f, 0x04, 0x04},
		{0x80f990, 0x00, 0x01}, // full-speed streaming off
		{0xf41a, 0x01, 0x01},
		{0x80f985, 0x00, 0x01}, // serial TS off
		{0x80f986, 0x00, 0x01}, // parallel TS off
		{0xd91c, 0x00, 0x01},
		{0xd91b, 0x00, 0x01},
	}); err != nil {
		return err
	}
	device.bandwidthMode = 0
	if err := device.WriteRegisterMask(0x80cfff, device.bandwidthMode, 0x03); err != nil {
		return err
	}
	if err := device.WriteRegisterMask(0xcfff, device.bandwidthMode, 0x03); err != nil {
		return err
	}
	if err := device.writeMasks([]regMask{
		{0x80f99d, 0x01, 0x01}, // hold both stream engines in reset
		{0x80f9a4, 0x01, 0x01},
		{0xdd11, 0x00, 0x60}, // endpoints 4 and 5 off
		{0xdd13, 0x00, 0x60},
		{0xdd11, 0x20, 0x60}, // endpoint 4 back on, 5 left off
	}); err != nil {
		return err
	}

	// The chip counts the transfer threshold in 32-bit words.
	frame := streamPackets * 188 / 4
	if err := device.WriteRegisters(0xdd88, []byte{byte(frame), byte(frame >> 8)}); err != nil {
		return err
	}
	// 128 is the endpoint's 512-byte maximum packet, also in words.
	if err := device.WriteRegister(0xdd0c, 128); err != nil {
		return err
	}

	if err := device.writeMasks([]regMask{
		{0x80f985, 0x00, 0x01},
		{0x80f986, 0x00, 0x01},
		{0x80f9a3, 0x00, 0x01}, // second stream engine stays off
		{0x80f9cd, 0x00, 0x01},
		{0x80f99d, 0x00, 0x01}, // release both engines from reset
		{0x80f9a4, 0x00, 0x01},
	}); err != nil {
		return err
	}
	if err := device.writeValues([]regValue{
		{0xd8fd, 0x01},
		{0xd833, 0x01},
		{0xd830, 0x00},
		{0x80ec57, 0},
		{0x80ec58, 0},
		{0x80ec40, 0x01},
		{0xd8b8, 0x01},
		{0xd8b9, 0x01},
		{0xd831, 0x00},
		{0xd832, 0x00},
	}); err != nil {
		return err
	}
	if err := device.writeMasks([]regMask{
		{0x80f992, 0x01, 0x01}, // clear the PID table
		{0x80f991, 0x01, 0x01}, // treat the table as a reject list
		{0x80f993, 0x01, 0x01}, // and enable it
	}); err != nil {
		return err
	}
	// Entry zero rejects PID 0x1fff, the null packets the chip pads with.
	if err := device.WriteRegisters(0x80f996, []byte{0xff, 0x1f}); err != nil {
		return err
	}
	if err := device.WriteRegister(0x80f995, 0); err != nil {
		return err
	}
	return device.WriteRegisterMask(0x80f994, 0x01, 0x01)
}

func (device *Device) writeBlocks(table []regBlock) error {
	for _, block := range table {
		if err := device.WriteRegisters(block.register, block.data); err != nil {
			return err
		}
	}
	return nil
}

// Tune acquires the ISDB-T channel at the given centre frequency in kHz.
func (device *Device) Tune(frequencyKHz uint32) error {
	if frequencyKHz < 53000 || frequencyKHz >= 860000 {
		return fmt.Errorf("IT9175: %d kHz is outside the 53000..859999 kHz tuning range", frequencyKHz)
	}
	if err := device.WriteRegisterMask(0x80cfff, device.bandwidthMode, 0x03); err != nil {
		return err
	}
	if err := device.WriteRegisters(0x80011b, tuneParams1); err != nil {
		return err
	}
	if err := device.WriteRegisters(0x800001, tuneParams2); err != nil {
		return err
	}
	if err := device.writeValues([]regValue{{0x800040, 0}, {0x800047, 0}}); err != nil {
		return err
	}
	if err := device.WriteRegisterMask(0x80f999, 0x00, 0x01); err != nil {
		return err
	}
	// The UHF path needs a different input stage from VHF.
	highBand := byte(0)
	if frequencyKHz > 300000 {
		highBand = 1
	}
	if err := device.WriteRegister(0x80004b, highBand); err != nil {
		return err
	}

	// Boards fitted with SDRAM retime it per channel, to keep its switching
	// harmonics away from the one being received.
	fitted, err := device.ReadRegister(0x8001c6)
	if err != nil {
		return err
	}
	if fitted != 0 {
		clock := uint32(sdramClock(frequencyKHz)) << 3
		if err := device.writeValues([]regValue{
			{0x80fb25, 0x65},
			{0x80fbb5, byte(clock>>8) | 0x30},
			{0x80fbb6, byte(clock)},
			{0x80fbb7, 0x04},
			{0x80fbb9, 0x4a},
			{0x80fbb9, 0x48},
			{0x80fbba, 0x40},
		}); err != nil {
			return err
		}
	}
	if err := device.checkConfigured(); err != nil {
		return err
	}

	lband, err := device.ReadRegister(0x80ec4c)
	if err != nil {
		return err
	}
	calibration, err := device.ReadRegister(0x80ed81)
	if err != nil {
		return err
	}
	synth := device.synthesiser(frequencyKHz, calibration)

	if err := device.WriteRegister(0x800160, synth.lnaBand); err != nil {
		return err
	}
	if err := device.WriteRegister(0x80ec56, bandwidthCodes[device.bandwidthMode&0x03]); err != nil {
		return err
	}
	band := lband & 0xe7
	if frequencyKHz >= 312000 {
		band |= 0x08
	}
	if err := device.WriteRegister(0x80ec4c, band); err != nil {
		return err
	}

	if err := device.writeValues([]regValue{
		{0x80ec4d, byte(synth.corrected)},
		{0x80ec4e, byte(synth.corrected >> 8)},
		{0x80015e, byte(synth.word)},
		{0x80015f, byte(synth.word >> 8)},
	}); err != nil {
		return err
	}
	if err := device.checkConfigured(); err != nil {
		return err
	}
	if err := device.WriteRegisterMask(0xd8cf, 0x01, 0x01); err != nil {
		return err
	}
	if err := device.WriteRegister(0x8001e3, 0); err != nil {
		return err
	}
	if err := device.writeValues([]regValue{
		{0x8001e1, byte(synth.shifted)},
		{0x8001e2, byte(synth.shifted >> 8)},
	}); err != nil {
		return err
	}
	// Releasing the demodulator starts acquisition; WaitTuning reports how it
	// went.
	return device.WriteRegister(0x800000, 0)
}

// WaitTuning waits for the demodulator to finish acquisition and reports whether
// it found a channel. A timeout counts as not found rather than an error, which
// is what a scan sweeping empty frequencies expects.
func (device *Device) WaitTuning(timeout time.Duration) (bool, error) {
	const interval = 40 * time.Millisecond
	// The chip's verdict only counts once it has held for two reads, so the
	// first comparison starts from the "empty" value.
	previous := byte(2)
	for elapsed := time.Duration(0); elapsed < timeout; elapsed += interval {
		state, err := device.ReadRegister(0x800047)
		if err != nil {
			return false, err
		}
		switch {
		case state == 1 && previous == 1:
			return true, nil
		case state == 2 && previous == 2:
			return false, nil
		}
		previous = state
		time.Sleep(interval)
	}
	return false, nil
}

// Statistics is what the demodulator reports about the channel it is on.
type Statistics struct {
	// TPSDLocked is the OFDM parameter decode; MPEGLocked adds transport
	// stream synchronisation on top of it.
	TPSDLocked bool
	MPEGLocked bool
	// Quality and Strength are the chip's own percentages.
	Quality  byte
	Strength byte
	// StrengthDBm is the input level the tuner measured.
	StrengthDBm int
	// SNR is the signal-to-noise ratio in dB.
	SNR byte
}

// Statistics reads the demodulator's lock flags and signal measurements.
func (device *Device) Statistics() (Statistics, error) {
	locks, err := device.ReadRegisters(0x80003c, 2)
	if err != nil {
		return Statistics{}, err
	}
	quality, err := device.ReadRegister(0x800049)
	if err != nil {
		return Statistics{}, err
	}
	strength, err := device.ReadRegisters(0x80013e, 2)
	if err != nil {
		return Statistics{}, err
	}
	snr, err := device.ReadRegister(0x8001c9)
	if err != nil {
		return Statistics{}, err
	}
	return Statistics{
		TPSDLocked:  locks[0]&0x01 != 0,
		MPEGLocked:  locks[1]&0x01 != 0,
		Quality:     quality,
		Strength:    strength[0],
		StrengthDBm: int(strength[1]) - 100,
		SNR:         snr,
	}, nil
}
