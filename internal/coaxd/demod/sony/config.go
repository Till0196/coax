// Copyright (c) Sony; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package sony

import "fmt"

// Config describes the board wiring -- everything this driver cannot ask the
// chip itself.
type Config struct {
	// DemodAddress is the chip's demodulator (SLV-T) seven-bit I2C address --
	// 0x64 on every card here, unshifted. The system, ATSC and ATSC-secondary
	// addresses are derived from it.
	DemodAddress byte
	// SystemAddress is the chip's system (SLV-X) I2C address. Zero derives it
	// as DemodAddress+2, right wherever the bus takes seven-bit addresses (as
	// every card on the pcie/tbs backend does). A board whose bus takes
	// eight-bit (already shifted) addresses must say so, since there the same
	// one-address step is four -- the PX-MLT boards' bridge shifts, so both of
	// their addresses are passed in whole.
	SystemAddress byte
	// TunerAddress is the tuner's own seven-bit I2C address, reached through
	// the demodulator's repeater. The tuner is a separate I2C device whether or
	// not it is a separate chip -- a TBS 6812 fits a CXD2868ER beside the
	// demodulator, a TBS 6205SE a CXD6802GL with one in the same package.
	TunerAddress byte
	// XtalCode is the SLVX clock-mode register value for the demodulator's
	// crystal. Use the Xtal constants; XtalKHz reads it back as a frequency,
	// which is what a driver computing an IF from the sampling rate needs.
	XtalCode byte
	// TunerXtal selects the tuner's own reference frequency table.
	TunerXtal TunerXtal
	// TunerI2C enables the chip's I2C bridge to the tuner at bring-up. Only the
	// CXD2856ER port asks; the CXD2857ER and CXD2878 ports turn it on
	// unconditionally.
	TunerI2C bool
	// TunerIndex is which of the card's tuners this is, counted from 1 (zero
	// reads as the first). It matters only on a card with more than one of
	// these chips: the first drives the crystal's reference output and the rest
	// take that reference in.
	TunerIndex byte
	// TLVMode configures the chip's output pins to carry MMT/TLV rather than
	// a transport stream, which is what ISDB-S3 needs. Only the CXD2857ER
	// has it.
	TLVMode bool
	// TSSerial says the board takes the transport stream one bit at a time
	// rather than a byte at a time -- purely board wiring: the same chip ships
	// both ways (TBS 6205SE parallel, 6209SE serial), differing only in which
	// pins the FPGA listens to. Getting it wrong locks a channel and carries
	// nothing.
	TSSerial bool
	// TSSerialData selects the data pin the serial output uses; every board
	// seen so far leaves it clear, the first pin. Meaningless when parallel.
	TSSerialData bool
	// TSClockMask is how many TS clock cycles are masked out around the gaps
	// in the parallel TS output, in bits 4:0 of registers 0x00/0xC6 and
	// 0x60/0x52. Zero skips both writes.
	TSClockMask byte
}

// The demodulator's own crystal, as its SLV-X clock-mode register takes it. The
// tuner's is separate (TunerXtal): a board can fit two different crystals.
const (
	Xtal16000KHz byte = 0
	Xtal24000KHz byte = 1
	Xtal32000KHz byte = 2
)

// XtalKHz is the demodulator's crystal frequency in kHz; an unrecognised code
// answers zero.
func (config Config) XtalKHz() uint32 {
	switch config.XtalCode {
	case Xtal16000KHz:
		return 16000
	case Xtal24000KHz:
		return 24000
	case Xtal32000KHz:
		return 32000
	default:
		return 0
	}
}

// TunerXtal is the tuner's crystal frequency.
type TunerXtal int

// The crystal frequencies the tuner's register tables are keyed by.
const (
	TunerXtal16000KHz TunerXtal = iota
	TunerXtal20500KHz
	TunerXtal24000KHz
	TunerXtal41000KHz
)

// KHz is the crystal's frequency, needed by tuner drivers that compute a PLL
// reference divider rather than index a table.
func (xtal TunerXtal) KHz() uint32 {
	switch xtal {
	case TunerXtal16000KHz:
		return 16000
	case TunerXtal20500KHz:
		return 20500
	case TunerXtal24000KHz:
		return 24000
	case TunerXtal41000KHz:
		return 41000
	default:
		return 0
	}
}

func (xtal TunerXtal) String() string {
	if khz := xtal.KHz(); khz != 0 {
		return fmt.Sprintf("%d kHz", khz)
	}
	return fmt.Sprintf("TunerXtal(%d)", int(xtal))
}

// BandwidthMHz rounds a bandwidth in Hz down to the whole megahertz the chip's
// tables are keyed by.
func BandwidthMHz(bandwidthHz uint32) uint32 { return bandwidthHz / 1_000_000 }
