// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices GmbH; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

// Package stv091x drives the STMicroelectronics STV0910 DVB-S/DVB-S2 demodulator,
// ported from TBS linux_media (dvb-frontends/stv091x.c).
//
// The chip is two complete demodulators in one package, sharing an I2C address, a
// crystal, a master clock and a handful of chip-wide registers. The second path's
// register block starts at 0xf2xx/0xf3xx and the first path's 0x200 higher; every
// per-path register below is named after the second path, and Config.Index adds
// that offset. The DiSEqC block is the exception: its two copies are 0x40 apart,
// named after the first path.
//
// The chip needs no firmware, so Initialize can pick up a card in whatever state
// another driver left it -- but not without disturbing a half somebody else is
// driving, hence Config.AlreadyBooted.
//
// Deliberately not driven here: BER accumulation, DSS, the lock LED / SPI-flash /
// EEPROM accessors, the modcode filter (card FPGA) and the debugging knobs.
package stv091x

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/pkg/i2c"
)

// TSMode selects how the transport stream leaves the chip (cfg->parallel).
type TSMode int

const (
	// TSModeSerial is cfg->parallel == 0: the stream leaves on one data pin.
	TSModeSerial TSMode = iota
	// TSModeParallel is cfg->parallel == 1, which is what the cards here use.
	TSModeParallel
	// TSModeParallelPermuted is cfg->parallel == 2: TSFIFO_PERMPARAL set in
	// TSGENERAL, reordering the data pins for a board that wired them the other way.
	TSModeParallelPermuted
)

// RepeaterPath says which path's I2C repeater register carries the gate to the
// tuner (cfg->dual_tuner).
type RepeaterPath int

const (
	// RepeaterPerDemod is cfg->dual_tuner == 0: each demodulator opens its
	// own path's repeater.
	RepeaterPerDemod RepeaterPath = iota
	// RepeaterPath1 is cfg->dual_tuner == 1: both demodulators open the first path's
	// repeater, the only one wired to the tuner. The cards here do this.
	RepeaterPath1
	// RepeaterPath2 is cfg->dual_tuner == 2, the same on the other path.
	RepeaterPath2
)

// Config describes how the board wired the chip.
type Config struct {
	// DemodAddress is the chip's seven-bit I2C address -- 0x68 on the cards here.
	DemodAddress byte
	// Index selects the demodulator: 0 for the first path, 1 for the second.
	Index int
	// ClockHz is the external crystal frequency; New falls back to 30 MHz.
	ClockHz uint32
	TSMode  TSMode
	// RepeaterLevel is cfg->rptlvl, three bits of the repeater register.
	RepeaterLevel byte
	// Repeater says which path's repeater reaches the tuner.
	Repeater RepeaterPath
	// AlreadyBooted says another driver has already brought this chip up and may
	// be driving its other demodulator, so set up only this half. probe() belongs
	// to the package, so a plain Initialize would reset the other half's transport
	// stream, repeater and DiSEqC configuration. The warm path instead reads back
	// the master clock and refuses to continue if nobody has configured it.
	AlreadyBooted bool
}

// Chip-wide registers, belonging to the package rather than either demodulator.
const (
	regMID       = 0xf100
	regPADCFG    = 0xf11a
	regOUTCFG    = 0xf11c
	regI2CCFG    = 0xf129
	regP1I2CRPT  = 0xf12a
	regP2I2CRPT  = 0xf12b
	regNCOARSE   = 0xf1b3
	regNCOARSE1  = 0xf1b4
	regNCOARSE2  = 0xf1b5
	regSYNTCTRL  = 0xf1b6
	regTSGENERAL = 0xf630
	regCFGEXT    = 0xfa80
	regGENCFG    = 0xfa86
	regTSTRES0   = 0xff11
)

// Per-demodulator registers, named after the second path. The first path's
// copy is 0x200 higher.
const (
	regPOWERI     = 0xf20a
	regAGCIQIN1   = 0xf20e
	regDEMOD      = 0xf210
	regDMDMODCOD  = 0xf211
	regDSTATUS    = 0xf212
	regDMDCFGMD   = 0xf214
	regDMDISTATE  = 0xf216
	regDMDSTATE   = 0xf21b
	regDMDCFG4    = 0xf21f
	regAGC2O      = 0xf22c
	regCARCFG     = 0xf238
	regCARHDR     = 0xf23e
	regCFRUP1     = 0xf242
	regCFRLOW1    = 0xf246
	regCFRINIT1   = 0xf248
	regTMGCFG2    = 0xf25a
	regSFRINIT1   = 0xf25e
	regSFR3       = 0xf264
	regTMGREG2    = 0xf268
	regTMGOBS     = 0xf26d
	regNNOSDATAT1 = 0xf280
	regNNOSPLHT1  = 0xf284
	regCAR3CFG    = 0xf292
	regACLC2S2Q   = 0xf297
	regACLC2S28   = 0xf298
	regACLC2S216A = 0xf299
	regACLC2S232A = 0xf29a
	regBCLC2S2Q   = 0xf29c
	regBCLC2S28   = 0xf29d
	regPLROOT2    = 0xf2ac
	regMODCODLST1 = 0xf2b1
	regMODCODLSTF = 0xf2bf
	regTNRCFG2    = 0xf2e1
	regFECM       = 0xf333
	regVITCURPUN  = 0xf33a
	regPRVIT      = 0xf33c
	regVSTATUSVIT = 0xf33e
	regTSPIDFLT1  = 0xf346
	regPDELCTRL1  = 0xf350
	regPDELCTRL2  = 0xf351
	regISIENTRY   = 0xf35e
	regISIBITENA  = 0xf35f
	regMATSTR1    = 0xf360
	regPDELSTATUS = 0xf369
	regPDELCTRL3  = 0xf36f
	regTSSTATEM   = 0xf370
	regTSCFGH     = 0xf372
	regTSCFGM     = 0xf373
	regTSCFGL     = 0xf374
	regTSSYNC     = 0xf375
	regTSSPEED    = 0xf380
	regERRCTRL1   = 0xf398
	regERRCTRL2   = 0xf39c
	regFBERCPT4   = 0xf3a8
)

// pathOffset is how far the first path's registers sit above the second path's.
const pathOffset = 0x200

// DiSEqC registers, named after the first path. The second path's copy is
// 0x40 higher, which is a different spacing from the rest of the chip.
const (
	regDISTXCFG    = 0xf702
	regDISTXSTATUS = 0xf703
	regDISTXFIFO   = 0xf705
	regDISTXF22    = 0xf706
	regDISRXCFG    = 0xf70a
)

// diseqcOffset is how far the second path's DiSEqC registers sit above the
// first path's.
const diseqcOffset = 0x40

// masterClockHz is what probe asks set_mclock for. Everything scaled by the
// master clock is derived from where the PLL actually lands, not from this
// number.
const masterClockHz = 135_000_000

// defaultClockHz is the crystal frequency assumed for a board that names none.
const defaultClockHz = 30_000_000

// searchRangeHz is state->SearchRange, the carrier offset swept either side of
// the tuner's frequency.
const searchRangeHz = 16_000_000

// receiveMode is enum ReceiveMode minus its automatic value.
type receiveMode int

const (
	modeNone receiveMode = iota
	modeDVBS
	modeDVBS2
)

// Tuner is the chip in front of this one, reached through the I2C gate this
// driver holds. Tune takes hertz and symbols per second.
type Tuner interface {
	Tune(frequencyHz, symbolRateSPS uint32) error
}

// gainCorrector is a tuner that can turn the demodulator's AGC reading into its
// own gain. Without it the reading is left raw and is then not a level in dBm.
type gainCorrector interface {
	RFGainCentiDB(agc uint16, frequencyKHz uint32) int32
}

// Demod is one of an STV0910's two demodulators, reached over an I2C bus.
type Demod struct {
	bus    i2c.Bus
	config Config
	tuner  Tuner

	warm bool
	// mclkHz is where the master clock PLL actually landed.
	mclkHz uint32
	// repeater is the running value of the repeater register: the gate is opened and
	// closed by rewriting it rather than by a read-modify-write.
	repeater byte
	// tscfgh and tsgeneral are the board's transport stream settings.
	tscfgh    byte
	tsgeneral byte
	// demodReg is state->DEMOD, carried across writes because parts of it are set at
	// different times.
	demodReg byte

	started       bool
	mode          receiveMode
	firstTimeLock bool
	symbolRate    uint32
	// modCod, pilots and fecType are what the DVB-S2 header said, which the carrier
	// loop tuning depends on.
	modCod  byte
	pilots  bool
	fecType byte
}

// New addresses one demodulator of an STV0910. The tuner is attached separately,
// being reached through the gate this chip holds.
func New(bus i2c.Bus, config Config) *Demod {
	if config.ClockHz == 0 {
		config.ClockHz = defaultClockHz
	}
	demod := &Demod{bus: bus, config: config}
	// state->tscfgh, state->tsgeneral and state->i2crpt.
	demod.tscfgh = 0x20
	if config.TSMode == TSModeSerial {
		demod.tscfgh |= 0x40
	}
	if config.TSMode == TSModeParallelPermuted {
		demod.tsgeneral = 0x02
	}
	demod.repeater = 0x0a | (config.RepeaterLevel&0x07)<<4
	// Inversion is left on automatic, resetting to none.
	demod.demodReg = 0x10
	return demod
}

// AttachTuner names the chip in front of the demodulator. A demodulator with none
// is set up as far as it goes on its own.
func (demod *Demod) AttachTuner(tuner Tuner) { demod.tuner = tuner }

// path returns the address of a per-demodulator register for this half. The
// constants are named after the second path, so the first needs the offset.
func (demod *Demod) path(register uint16) uint16 {
	if demod.config.Index == 0 {
		return register + pathOffset
	}
	return register
}

// diseqc returns the address of a DiSEqC register for this half; here the second
// path needs the offset.
func (demod *Demod) diseqc(register uint16) uint16 {
	if demod.config.Index != 0 {
		return register + diseqcOffset
	}
	return register
}

func (demod *Demod) regWrite(register uint16, value byte) error {
	data := []byte{byte(register >> 8), byte(register), value}
	if err := demod.bus.Write(demod.config.DemodAddress, data); err != nil {
		return fmt.Errorf("STV0910 write %#04x: %w", register, err)
	}
	return nil
}

func (demod *Demod) regReadMulti(register uint16, length int) ([]byte, error) {
	data, err := i2c.WriteRead(demod.bus, demod.config.DemodAddress,
		[]byte{byte(register >> 8), byte(register)}, length)
	if err != nil {
		return nil, fmt.Errorf("STV0910 read %#04x: %w", register, err)
	}
	return data, nil
}

func (demod *Demod) regRead(register uint16) (byte, error) {
	data, err := demod.regReadMulti(register, 1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

// regUpdate is a read-modify-write of one register; mask names the bits replaced.
func (demod *Demod) regUpdate(register uint16, mask, value byte) error {
	current, err := demod.regRead(register)
	if err != nil {
		return err
	}
	return demod.regWrite(register, i2c.Mask(current, value, mask))
}

// OpenTunerGate satisfies i2c.Gate (gate_ctrl). Which register the enable and
// stop bits go in depends on the board (Config.Repeater), not on which
// demodulator asks.
func (demod *Demod) OpenTunerGate(open bool) error {
	value := demod.repeater &^ 0x86
	if open {
		value |= 0x80
	} else {
		value |= 0x02
	}
	if err := demod.regWrite(demod.repeaterRegister(), value); err != nil {
		return err
	}
	demod.repeater = value
	return nil
}

// setMasterClock is set_mclock: it programs the PLL and records what the dividers
// actually produced.
func (demod *Demod) setMasterClock(wantHz uint32) error {
	// Only the feedback divider is worked out; the charge pump follows from it.
	const inputDivider, outputDivider = 1, 4
	quartzMHz := demod.config.ClockHz / 1_000_000
	wantMHz := wantHz / 1_000_000
	if quartzMHz == 0 {
		return errors.New("STV0910 needs a crystal frequency of at least 1 MHz")
	}
	feedback := wantMHz * outputDivider * inputDivider / quartzMHz

	// The charge pump steps up every eight counts of the feedback divider above 71.
	chargePump := uint32(7)
	if feedback >= 72 && feedback <= 225 {
		chargePump = 8 + (feedback-72)/8
	}

	if err := demod.regWrite(regNCOARSE, byte(chargePump<<3|inputDivider)); err != nil {
		return err
	}
	if err := demod.regWrite(regNCOARSE2, outputDivider); err != nil {
		return err
	}
	if err := demod.regWrite(regNCOARSE1, byte(feedback)); err != nil {
		return err
	}

	vcoMHz := quartzMHz * 2 * feedback / inputDivider
	demod.mclkHz = vcoMHz / (2 * outputDivider) * 1_000_000
	return nil
}

// initDiSEqC is init_diseqc: hold both receivers off, reset both transmitters,
// and give each the divider that turns the master clock into 22 kHz. Both
// paths.
func (demod *Demod) initDiSEqC() error {
	// Half a period of 22 kHz added before the division, both scaled by 32.
	divider := byte((demod.mclkHz + 11000*32) / (22000 * 32))
	for _, step := range []struct {
		register uint16
		value    byte
	}{
		{regDISRXCFG, 0x00},
		{regDISRXCFG + diseqcOffset, 0x00},
		{regDISTXCFG, 0x82},
		{regDISTXCFG, 0x02},
		{regDISTXCFG + diseqcOffset, 0x82},
		{regDISTXCFG + diseqcOffset, 0x02},
		{regDISTXF22, divider},
		{regDISTXF22 + diseqcOffset, divider},
	} {
		if err := demod.regWrite(step.register, step.value); err != nil {
			return err
		}
	}
	return nil
}

// identity is what the chip answers in MID.
const identity = 0x51

// Initialize identifies the chip and runs the package's probe sequence, covering
// both demodulators and the chip-wide registers between them. A host owning the
// card outright must call it on both halves before tuning either. A caller that
// cannot promise that sets Config.AlreadyBooted and gets resume instead.
//
// No reset here: the board's reset line is pulled by the card driver.
func (demod *Demod) Initialize() error {
	if demod.config.Index != 0 && demod.config.Index != 1 {
		return fmt.Errorf("an STV0910 has demodulators 0 and 1, not %d", demod.config.Index)
	}
	found, err := demod.regRead(regMID)
	if err != nil {
		return err
	}
	if found != identity {
		return fmt.Errorf("STV0910 at %#02x answered %#02x, not a STV0910",
			demod.config.DemodAddress, found)
	}
	if demod.config.AlreadyBooted {
		return demod.resume()
	}

	for _, step := range []struct {
		register uint16
		value    byte
	}{
		// Both repeaters off, and the I2C oversampling ratio the chip needs.
		{regP1I2CRPT, 0x24},
		{regP2I2CRPT, 0x24},
		{regI2CCFG, 0x88},

		{regOUTCFG, 0x00},
		// The RF AGC pads' drive level.
		{regPADCFG, 0x05},
		{regSYNTCTRL, 0x02},
		{regTSGENERAL, demod.tsgeneral},
		{regCFGEXT, 0x02},
		// Dual mode, LDPC decoder in broadcast rather than
		// as-soon-as-possible mode.
		{regGENCFG, 0x15},

		// IQSWAP off on both paths.
		{regTNRCFG2, 0x02},
		{regTNRCFG2 + pathOffset, 0x02},

		{regCAR3CFG, 0x02},
		{regCAR3CFG + pathOffset, 0x02},
		{regDMDCFG4, 0x04},
		{regDMDCFG4 + pathOffset, 0x04},

		// BCH error checking on. The two paths are asymmetric here.
		{regPDELCTRL2 + pathOffset, 0x00},
		{regPDELCTRL2, 0x20},
		{regPDELCTRL3 + pathOffset, 0x00},
		{regPDELCTRL3, 0x00},

		// Reset the LDPC decoder.
		{regTSTRES0, 0x80},
		{regTSTRES0, 0x00},

		{regTSPIDFLT1 + pathOffset, 0x00},
		{regTSPIDFLT1, 0x00},

		{regTMGCFG2 + pathOffset, 0x80},
		{regTMGCFG2, 0x80},
	} {
		if err := demod.regWrite(step.register, step.value); err != nil {
			return err
		}
	}

	if err := demod.setMasterClock(masterClockHz); err != nil {
		return err
	}

	for _, step := range []struct {
		register uint16
		value    byte
	}{
		// Transport stream speed control left on automatic.
		{regTSCFGM + pathOffset, 0x00},
		{regTSCFGM, 0x00},
		{regTSCFGL + pathOffset, 0x20},
		{regTSCFGL, 0x20},
		{regTSSPEED + pathOffset, 0x20},
		{regTSSPEED, 0x20},

		// Reset each path's stream merger by pulsing the low bit of its config.
		{regTSCFGH + pathOffset, demod.tscfgh | 0x01},
		{regTSCFGH + pathOffset, demod.tscfgh},
		{regTSCFGH, demod.tscfgh | 0x01},
		{regTSCFGH, demod.tscfgh},

		{regP1I2CRPT, demod.repeater},
		{regP2I2CRPT, demod.repeater},
	} {
		if err := demod.regWrite(step.register, step.value); err != nil {
			return err
		}
	}

	if err := demod.initDiSEqC(); err != nil {
		return err
	}
	demod.warm = true
	return nil
}

// MasterClockHz reports what the PLL settled on, which the symbol rate and
// carrier search registers are scaled against.
func (demod *Demod) MasterClockHz() uint32 { return demod.mclkHz }

// ErrNotInitialized is returned by calls that need a woken chip before
// Initialize has run.
var ErrNotInitialized = errors.New("STV0910 is not initialized")

// stop is Stop: hold the transport stream output off, release the DVB-S2 packet
// delineator's reset, put the AGC back to blind search and halt the demodulator.
func (demod *Demod) stop() error {
	if demod.started {
		if err := demod.regWrite(demod.path(regTSCFGH), demod.tscfgh|0x01); err != nil {
			return err
		}
		if err := demod.regUpdate(demod.path(regPDELCTRL1), 0x01, 0x00); err != nil {
			return err
		}
		if err := demod.regWrite(demod.path(regAGC2O), 0x5b); err != nil {
			return err
		}
		if err := demod.regWrite(demod.path(regDMDISTATE), 0x5c); err != nil {
			return err
		}
		demod.started = false
	}
	demod.mode = modeNone
	return nil
}

// Sleep stops the demodulator, leaving the chip configured but not searching.
func (demod *Demod) Sleep() error {
	if !demod.warm {
		return ErrNotInitialized
	}
	return demod.stop()
}

// Bounds on the wait for the DiSEqC transmitter, from wait_dis.
const (
	waitDiSEqCAttempts = 10
	waitDiSEqCInterval = 10 * time.Millisecond
)

// perPathInit is probe()'s writes for one path. The cold path does not use it,
// but a test asserts a cold Initialize leaves every register named here holding
// the value named here, so the two cannot drift apart. tscfgh is passed in
// because the warm path reads it back off the chip while the cold path imposes
// the board's.
func (demod *Demod) perPathInit(tscfgh byte) []struct {
	register uint16
	value    byte
} {
	// Packet-delineator control differs per path: first 0x00, second 0x20.
	pdelctrl2 := byte(0x00)
	if demod.config.Index != 0 {
		pdelctrl2 = 0x20
	}
	return []struct {
		register uint16
		value    byte
	}{
		// IQSWAP off.
		{demod.path(regTNRCFG2), 0x02},
		{demod.path(regCAR3CFG), 0x02},
		{demod.path(regDMDCFG4), 0x04},
		// BCH error checking on.
		{demod.path(regPDELCTRL2), pdelctrl2},
		{demod.path(regPDELCTRL3), 0x00},
		{demod.path(regTSPIDFLT1), 0x00},
		{demod.path(regTMGCFG2), 0x80},
		// Transport stream speed control left on automatic.
		{demod.path(regTSCFGM), 0x00},
		{demod.path(regTSCFGL), 0x20},
		{demod.path(regTSSPEED), 0x20},
		// Reset this path's stream merger by pulsing the low bit of its config.
		{demod.path(regTSCFGH), tscfgh | 0x01},
		{demod.path(regTSCFGH), tscfgh},
	}
}

// resume picks up a chip another driver already brought up, setting up this half
// alone: probe()'s per-path share plus this demodulator's share of init_diseqc.
//
// It skips every chip-wide write in probe() -- oversampling ratio, output and pad
// configuration, synthesizer control, stream permutation, dual-mode and LDPC
// scheduling, the master clock PLL, the LDPC reset and both repeater registers.
// All are shared, and the LDPC reset would drop the other half's stream outright.
func (demod *Demod) resume() error {
	if err := demod.readBackMasterClock(); err != nil {
		return err
	}

	// Read back rather than imposed: a driver that knows the board has already told
	// the chip. The low bit is the merger's reset, pulsed rather than held.
	tscfgh, err := demod.regRead(demod.path(regTSCFGH))
	if err != nil {
		return err
	}
	demod.tscfgh = tscfgh &^ 0x01

	// Same for the repeater: rewriting it would reach a register the other half's
	// tuner accesses go through on the cards here.
	repeater, err := demod.regRead(demod.repeaterRegister())
	if err != nil {
		return err
	}
	demod.repeater = repeater

	for _, step := range demod.perPathInit(demod.tscfgh) {
		if err := demod.regWrite(step.register, step.value); err != nil {
			return err
		}
	}
	if err := demod.initDiSEqCPath(); err != nil {
		return err
	}
	demod.warm = true
	return nil
}

// readBackMasterClock recovers the master clock from the PLL dividers the chip
// is already running. A warm start never sets the PLL, so assuming 135 MHz is a
// guess.
func (demod *Demod) readBackMasterClock() error {
	coarse, err := demod.regRead(regNCOARSE)
	if err != nil {
		return err
	}
	feedback, err := demod.regRead(regNCOARSE1)
	if err != nil {
		return err
	}
	output, err := demod.regRead(regNCOARSE2)
	if err != nil {
		return err
	}
	// IDF is the low three bits of the coarse register, ODF the low six of its own.
	inputDivider := uint32(coarse & 0x07)
	outputDivider := uint32(output & 0x3f)
	if inputDivider == 0 || outputDivider == 0 || feedback == 0 {
		return fmt.Errorf("STV0910 was declared already booted but its master clock PLL reads back as"+
			" unconfigured (dividers %d/%d/%d); a cold Initialize is needed, which would reset the"+
			" whole package including its other demodulator",
			inputDivider, feedback, outputDivider)
	}
	quartzMHz := demod.config.ClockHz / 1_000_000
	if quartzMHz == 0 {
		return errors.New("STV0910 needs a crystal frequency of at least 1 MHz")
	}
	vcoMHz := quartzMHz * 2 * uint32(feedback) / inputDivider
	demod.mclkHz = vcoMHz / (2 * outputDivider) * 1_000_000
	return nil
}

// repeaterRegister is which of the two repeater registers this board's gate is
// in.
func (demod *Demod) repeaterRegister() uint16 {
	switch demod.config.Repeater {
	case RepeaterPath1:
		return regP1I2CRPT
	case RepeaterPath2:
		return regP2I2CRPT
	default:
		if demod.config.Index != 0 {
			return regP2I2CRPT
		}
		return regP1I2CRPT
	}
}

// initDiSEqCPath is init_diseqc's share for this demodulator. The cold path
// writes both paths; this writes only ours.
func (demod *Demod) initDiSEqCPath() error {
	divider := byte((demod.mclkHz + 11000*32) / (22000 * 32))
	for _, step := range []struct {
		register uint16
		value    byte
	}{
		{demod.diseqc(regDISRXCFG), 0x00},
		{demod.diseqc(regDISTXCFG), 0x82},
		{demod.diseqc(regDISTXCFG), 0x02},
		{demod.diseqc(regDISTXF22), divider},
	} {
		if err := demod.regWrite(step.register, step.value); err != nil {
			return err
		}
	}
	return nil
}
