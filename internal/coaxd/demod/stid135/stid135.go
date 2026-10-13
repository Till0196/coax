// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

// Package stid135 drives the demodulator side of the STMicroelectronics STiD135
// "Oxford", the eight-path DVB-S/S2/S2X part behind TBS's 6903X, 6909X, 6912
// and 6916 cards. Ported from the TBS linux_media tree's
// drivers/media/dvb-frontends/stid135.
//
// The chip's analogue front end digitises four whole satellite bands at once
// and hands them to a crossbar; each of the eight demodulators picks one of
// those inputs and tunes within it digitally. So nothing here sets an RF
// frequency: Tune programs a numerically controlled oscillator, and the only RF
// decision is which input the crossbar routes.
//
// Cold initialization is not ported, so this package refuses to run without
// AlreadyBooted: it would need a generated table of eight thousand register
// defaults, the front end's own ~1800-line sequence, a core voltage trim driven
// from the process fuses, and a per-input baseband filter calibration -- all
// already done by the vendor driver that owns the card. Initialize therefore
// does only a cut check, clock recovery and the crossbar setting.
//
// Also left out: blind search and satellite scanning, the wideband and sliced
// modes, DSS, transport stream output configuration and the STFE tagging
// merger, PID and generic-stream filtering, modcode filtering, error counters,
// the die temperature sensor, the STVVGLNA companion amplifiers, the FSK modem,
// network clock recovery, standby, and the constellation and spectrum readouts.
// Signal strength is the gain integrator rather than dBm. Low-noise block
// supply voltage is absent because on these cards a separate board regulator
// switches it. The 22 kHz tone and DiSEqC are the chip's.
package stid135

import (
	"errors"
	"fmt"
	"math/bits"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// MultiswitchMode says how the card's RF inputs are fed. A card with a supply
// switch -- the 6909X and 6916 -- takes either mode and defaults to Quattro;
// one without -- the 6903X and 6912 -- is forced to Direct.
type MultiswitchMode int

const (
	// Quattro is mode 0: one quattro low-noise block whose four outputs are held
	// at the four combinations of polarisation and band, one per RF input.
	// Choosing a band only moves this demodulator onto the input that already
	// carries it.
	Quattro MultiswitchMode = iota
	// Direct is mode 1, one satellite lead per input: the tone really is
	// switched on the lead and the supply is the card's to change.
	Direct
)

// VoltageSetter is the card's low-noise block supply switch, which the STiD135
// has no part in: on the 6909X and 6916 it is the FPGA's GPIO block; on the
// 6903X and 6912 a separate SEC frontend owns the regulator.
type VoltageSetter interface {
	// SetInputVoltage switches one of the card's RF inputs: high selects 18 V,
	// off asks for no supply. The FPGA path never raises the disable bit, so a
	// board may ignore off.
	SetInputVoltage(rfInput int, high, off bool) error
}

// Config describes the wiring and which of the eight demodulators this is.
type Config struct {
	// DemodAddress is the chip's seven-bit I2C address, 0x68 on every card.
	DemodAddress byte
	// Demod is the zero-based demodulator path, 0 to 7. The chip numbers its
	// paths from one; the conversion happens inside.
	Demod int
	// RFInput is the crossbar setting: zero-based RF input 0 to 3, the adapter
	// number halved on these cards. Direct mode only -- Quattro composes the input
	// from SetVoltage and SetTone. MxL58x calls this RFSource and counts from one.
	RFInput int
	// Multiswitch says how the card is fed. It must match the mode the card's
	// own driver runs in: the two disagree about what an RF input index means.
	Multiswitch MultiswitchMode
	// Voltage is how a Direct-mode caller reaches the card's supply switch. If
	// nil, SetVoltage errors rather than reporting a success that did not
	// happen. Unused in Quattro mode.
	Voltage VoltageSetter
	// Suppress22kHz keeps this chip off the tone. Set for exactly one
	// configuration, the second STiD135 of the 6916: two chips putting a tone
	// on the same installation at once spoils the signal.
	Suppress22kHz bool
	// ReferenceClockMHz is the crystal feeding the chip, 27 on every card here.
	// Nothing programs it; it is what the front end's PLL divider ratios are
	// multiplied by to recover the local oscillator.
	ReferenceClockMHz uint32
	// AlreadyBooted says another driver has already brought this chip up. It
	// must be set: the cold path is not ported (see the package comment).
	AlreadyBooted bool
}

// Demod is one of the eight demodulators of an STiD135 reached over an I2C bus.
type Demod struct {
	bus    i2c.Bus
	config Config

	warm bool
	// masterClockHz and loFrequencyHz come from the front end's PLL at
	// Initialize time; every frequency and symbol rate programmed here is
	// scaled by one of them.
	masterClockHz uint32
	loFrequencyHz uint32
	// searchRangeHz is search_range: the carrier boundaries the demodulator
	// may wander between.
	searchRangeHz uint32
	// rfInput is the input this demodulator is listening to now: Config.RFInput in
	// Direct mode, moved by SetVoltage and SetTone in Quattro. Everything indexed
	// by input follows this rather than the configured value.
	rfInput int
}

// searchRangeHz is the fixed 10 MHz search range around the requested carrier.
const searchRangeHz = 10_000_000

// New addresses one demodulator of an STiD135 on the given bus.
func New(bus i2c.Bus, config Config) *Demod {
	if config.ReferenceClockMHz == 0 {
		config.ReferenceClockMHz = 27
	}
	return &Demod{bus: bus, config: config, searchRangeHz: searchRangeHz}
}

// ErrNotInitialized is returned by calls that need a woken chip.
var ErrNotInitialized = errors.New("STiD135 is not initialized")

// The I2C-to-STBus bridge in front of the register file takes a transaction
// byte, a big-endian sixteen-bit address, then data. The transaction byte
// encodes the number of address bytes, whether the address auto-increments, the
// datum width and the direction.
const (
	stbusRead       = 0x01
	stbusAddr2Byte  = 0x02
	stbusStreamIncr = 0x08

	transactionWriteOne = stbusAddr2Byte
	transactionWriteRun = stbusAddr2Byte | stbusStreamIncr
	transactionReadOne  = stbusAddr2Byte | stbusRead
	transactionReadRun  = stbusAddr2Byte | stbusRead | stbusStreamIncr
)

func (demod *Demod) regReadMulti(address uint16, length int) ([]byte, error) {
	transaction := byte(transactionReadOne)
	if length > 1 {
		transaction = transactionReadRun
	}
	header := []byte{transaction, byte(address >> 8), byte(address)}
	data, err := i2c.WriteRead(demod.bus, demod.config.DemodAddress, header, length)
	if err != nil {
		return nil, fmt.Errorf("STiD135 read %#04x: %w", address, err)
	}
	return data, nil
}

func (demod *Demod) regRead(address uint16) (byte, error) {
	data, err := demod.regReadMulti(address, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (demod *Demod) regWriteMulti(address uint16, values ...byte) error {
	transaction := byte(transactionWriteOne)
	if len(values) > 1 {
		transaction = transactionWriteRun
	}
	frame := append([]byte{transaction, byte(address >> 8), byte(address)}, values...)
	if err := demod.bus.Write(demod.config.DemodAddress, frame); err != nil {
		return fmt.Errorf("STiD135 write %#04x: %w", address, err)
	}
	return nil
}

func (demod *Demod) regWrite(address uint16, value byte) error {
	return demod.regWriteMulti(address, value)
}

// fieldRead returns one field of a register, shifted down to bit zero. Every
// field is unsigned: the generated FLD_ macros' sign bit is zero throughout
// this register map.
func (demod *Demod) fieldRead(address uint16, mask byte) (byte, error) {
	value, err := demod.regRead(address)
	if err != nil {
		return 0, err
	}
	return (value & mask) >> bits.TrailingZeros8(mask), nil
}

type field struct {
	mask  byte
	value byte
}

// fieldWrite is a read-modify-write. The reference driver instead merges into a
// cached image of the register file; the two agree on a chip another driver
// brought up, so the cache is not reproduced.
func (demod *Demod) fieldWrite(address uint16, mask, value byte) error {
	return demod.fieldsWrite(address, field{mask, value})
}

func (demod *Demod) fieldsWrite(address uint16, fields ...field) error {
	current, err := demod.regRead(address)
	if err != nil {
		return err
	}
	for _, one := range fields {
		shifted := one.value << bits.TrailingZeros8(one.mask)
		current = current&^one.mask | shifted&one.mask
	}
	return demod.regWrite(address, current)
}

// path is the chip's one-based demodulator number.
func (demod *Demod) path() int { return demod.config.Demod + 1 }

func (demod *Demod) demodReg(offset uint16) uint16 {
	return demodBase[demod.config.Demod] + offset
}

func (demod *Demod) viterbiReg(offset uint16) uint16 {
	return viterbiBase[demod.config.Demod] + offset
}

func (demod *Demod) hwareReg(offset uint16) uint16 {
	return hwareBase[demod.config.Demod] + offset
}

func (demod *Demod) pktdelinReg(offset uint16) uint16 {
	return pktdelinBase[demod.config.Demod] + offset
}

func (demod *Demod) fecspyReg(offset uint16) uint16 {
	return fecspyBase[demod.config.Demod] + offset
}

// agcrfReg and diseqcReg address blocks the chip provides four of, one per RF
// input, so both follow the current input rather than the configured one.
func (demod *Demod) agcrfReg(offset uint16) uint16 {
	return agcrfBase[demod.rfInput] + offset
}

func (demod *Demod) diseqcReg(offset uint16) uint16 {
	return diseqcBase[demod.rfInput] + offset
}

func (demod *Demod) afeReg(offset uint16) uint16 { return afeBase + offset }

// cutIDCut2 is what the AFE identification register's cut field must read; no
// earlier silicon is supported.
const cutIDCut2 = 0x08

// Initialize adopts a running chip: it checks the cut, recovers the local
// oscillator and master clock from the PLL divider ratios another driver
// programmed, and points the crossbar at this path's RF input. Nothing else is
// written; see the package comment.
func (demod *Demod) Initialize() error {
	if !demod.config.AlreadyBooted {
		return errors.New("STiD135 needs AlreadyBooted: this driver resumes a chip another driver has brought up and cannot cold-start one")
	}
	if demod.config.Demod < 0 || demod.config.Demod > 7 {
		return fmt.Errorf("STiD135 has demodulators 0 to 7, not %d", demod.config.Demod)
	}
	if demod.config.RFInput < 0 || demod.config.RFInput > 3 {
		return fmt.Errorf("STiD135 has RF inputs 0 to 3, not %d", demod.config.RFInput)
	}
	identity, err := demod.regRead(demod.afeReg(regAFEID))
	if err != nil {
		return err
	}
	if identity&fldCUTID != cutIDCut2 {
		return fmt.Errorf("STiD135 at %#02x answered identity %#02x, which is not a cut 2 part",
			demod.config.DemodAddress, identity)
	}
	// The wired input applies only in Direct mode; in Quattro every frontend
	// starts at input 0 and the band request composes the rest.
	demod.rfInput = 0
	if demod.config.Multiswitch == Direct {
		demod.rfInput = demod.config.RFInput
	}
	if err := demod.readClocks(); err != nil {
		return err
	}
	if demod.masterClockHz == 0 {
		return errors.New("STiD135 reports a stopped front-end PLL: the chip has not been brought up")
	}
	if err := demod.setRFMuxPath(); err != nil {
		return err
	}
	demod.warm = true
	return nil
}

// setRFMuxPath points the crossbar. The eight RFMUX registers sit consecutively
// at the crossbar's base, one per demodulator, each holding that demodulator's
// zero-based RF input. Every tune calls this with the current input, which is
// what makes Quattro work.
func (demod *Demod) setRFMuxPath() error {
	return demod.fieldWrite(rfmuxBase+uint16(demod.config.Demod),
		fldRFMUXDEMODSEL, byte(demod.rfInput))
}

// RFInput reports which of the card's four inputs this demodulator is on; in
// Quattro mode it moves as bands are chosen.
func (demod *Demod) RFInput() int { return demod.rfInput }
