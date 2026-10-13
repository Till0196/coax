// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices; MaxLinear; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

// Package mxl58x drives the MaxLinear MxL58x "Hydra" satellite demodulators, of
// which TBS's 6909 carries one MxL584 serving all eight of the card's tuners.
// Ported from TBS linux_media (dvb-frontends/mxl58x.c).
//
// The chip is one device with eight frontends, so a Demod here is a frontend
// rather than a chip: eight of them address the same I2C slave and differ only
// in Config.DemodIndex, which selects both the demodulator a tune command is
// aimed at and the 0x100-byte slice of the firmware's status area the readings
// come from. Registers go through 0xFB/0xFC framed transfers, while tuning,
// DiSEqC and the tone are messages posted through 0xFE, which answers nothing.
//
// Not ported: composing the transport output from a full MPEG-output parameter
// structure (the 6909's output is twelve literal register writes); the
// SPI-flash and EEPROM accessors; reading back what the firmware settled on,
// the temperature register and the abort-tune command; and the error counters.
package mxl58x

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// defaultAddress is where the chip answers on every board in this tree.
const defaultAddress byte = 0x60

// firmwareName is the firmware file this chip is loaded from.
const firmwareName = "dvb-fe-mxl5xx.fw"

// Framing bytes opening an I2C transfer: registers and firmware commands share
// the bus and are told apart by these.
const (
	plidRegisterRead  = 0xfb
	plidRegisterWrite = 0xfc
	plidCommandWrite  = 0xfe
)

// Firmware command IDs from MXL_HYDRA_HOST_CMD_ID_E; only those sent here.
const (
	commandDemodSetParam    = 8
	commandDiSEqCMessage    = 20
	commandDiSEqCConfig     = 22
	commandDeviceConfigSKU  = 58
	commandDiSEqCContinuous = 60
)

// Registers the driver reaches by address.
const (
	// regCrystalSetting says which crystal is fitted: 0 for 24 MHz, 1 for 27.
	regCrystalSetting = 0x3fffc5f0
	// regCrystalCap is the crystal's load capacitance in the firmware's units.
	regCrystalCap = 0x3fffeda4

	regResetTransportFIFO = 0x80030028
	regResetBaseband      = 0x80030024
	regResetCrossbar      = 0x80030020

	// regModulesClock2 gates the baseband, wideband, SerDes and transport clocks;
	// disableClock2 stops them while the firmware is written.
	regModulesClock2 = 0x8003001c
	disableClock2    = 0x0000000b

	// regRootClock is read for its side effect: it clears the software and host
	// interrupt status.
	regRootClock = 0x80030018

	// regFirmwareDownloadSign is zeroed before a download so a half-written image
	// cannot be mistaken for a good one.
	regFirmwareDownloadSign = 0x3fffeae0

	// regHeartbeat is a counter the running firmware advances -- the only way to ask
	// this chip whether it is up.
	regHeartbeat = 0x3fffeddc

	regFirmwareVersion = 0x3fffedb8

	// regCrossbarDemodSelect maps each demodulator onto a transport stream output,
	// three bits per demodulator.
	regCrossbarDemodSelect = 0x9070024c
)

// The firmware's per-demodulator status area: a 0x100-byte slice each, the
// addresses below being demodulator zero's.
const (
	demodStatusStride = 0x100

	regDemodLockStatus = 0x3fffc650
	regDemodSNR        = 0x3fffc670
	// regDemodStatusLock is a spinlock the host takes against the firmware so a
	// status update cannot land halfway through a read of the slice.
	regDemodStatusLock = 0x3fffc6b8
	regDemodInputPower = 0x3fffc708
)

// field is a named bitfield: register address, low bit position, width.
type field struct {
	register uint32
	lsb      uint8
	width    uint8
}

// The bitfields this driver uses.
var (
	fieldXtalEnableClockOut = field{0x90200054, 23, 1}
	fieldAFEClockEnable     = field{0x80030014, 9, 1}
	fieldChipVersion        = field{0x80030000, 12, 4}
	fieldAFESoftResetN      = field{0x8003003c, 12, 1}
	fieldCPUSoftResetN      = field{0x8003003c, 0, 1}
)

// mask returns the field's bits within its register.
func (f field) mask() uint32 {
	if f.width >= 32 {
		return 0xffffffff
	}
	return (0xffffffff >> (32 - uint32(f.width))) << uint32(f.lsb)
}

// MultiswitchMode decides what the low-noise block controls on a frontend do.
type MultiswitchMode int

const (
	// Quattro is mode 0, the default and how the 6909 is meant to be fed: the card's
	// four inputs are each held at one fixed band and polarisation, and a frontend
	// picks a band by choosing which input to listen to.
	Quattro MultiswitchMode = iota
	// Direct is mode 1, one satellite lead per input, where the supply voltage and
	// the tone really are switched.
	Direct
)

// VoltageSetter is the board's low-noise block supply switch. An interface
// because the supply is not the chip's -- on the 6909 it is the FPGA's GPIO
// block.
type VoltageSetter interface {
	// SetInputVoltage switches one of the card's RF inputs. high selects 18 V, false
	// 13 V; off asks for no supply at all.
	SetInputVoltage(rfInput uint32, high, off bool) error
}

// Config describes how the board wired the chip and which frontend this is.
type Config struct {
	// DemodAddress is the chip's seven-bit I2C address. Zero means 0x60.
	DemodAddress byte
	// DemodIndex selects one of the chip's eight frontends, 0 to 7.
	DemodIndex uint32
	// ClockHz is the crystal fitted beside the chip. Zero means 24 MHz.
	ClockHz uint32
	// CrystalCap is the crystal's load capacitance in the firmware's own units, not
	// picofarads. The 6909 uses 12; MaxLinear's range is 10 to 33.
	CrystalCap uint32
	// DeviceType is the SKU the firmware is told it is running on, one of
	// MXL_HYDRA_DEVICE_E. The 6909 passes 1, the MxL584.
	DeviceType uint32
	// Multiswitch says how the card's four RF inputs are fed.
	Multiswitch MultiswitchMode
	// RFSource pins every frontend to one RF input in Direct mode, 1 to 4. Zero
	// leaves the input derived from DemodIndex.
	RFSource uint32
	// Voltage is the board's supply switch. Nil means this backend cannot reach the
	// card's GPIO, and the supply is left as whoever last owned the card set it.
	Voltage VoltageSetter
	// AlreadyBooted says another driver has already loaded this chip's firmware, so
	// resume it rather than start it again -- a cold sequence would take all eight
	// frontends' streams down. It is a safety catch rather than the mechanism:
	// Initialize always asks the heartbeat and picks up a chip that answers whatever
	// this says.
	AlreadyBooted bool
}

// Demod is one frontend of an MxL58x reached over an I2C bus. Nothing here is
// guarded: eight of these share one chip and one bus, so a per-instance mutex
// would not be the lock that matters and the bus serializes.
type Demod struct {
	bus         i2c.Bus
	firmwareDir string
	config      Config

	warm bool
	// rfInput is which of the card's four inputs this frontend listens to. In
	// Quattro mode SetVoltage and SetTone move it rather than switching anything.
	rfInput uint32
}

// New addresses one frontend of an MxL58x. firmwareDir holds the blob Initialize
// loads when it finds the chip cold.
func New(bus i2c.Bus, firmwareDir string, config Config) *Demod {
	if config.DemodAddress == 0 {
		config.DemodAddress = defaultAddress
	}
	if config.ClockHz == 0 {
		config.ClockHz = 24_000_000
	}
	demod := &Demod{bus: bus, firmwareDir: firmwareDir, config: config}
	if config.Multiswitch == Direct {
		// One lead per pair of demodulators, counting down: 0 and 1 take input 3.
		demod.rfInput = 3 - config.DemodIndex/2
		if config.RFSource > 0 && config.RFSource < 5 {
			demod.rfInput = 4 - config.RFSource
		}
	}
	return demod
}

// ErrNotInitialized is returned by calls that need a running chip.
var ErrNotInitialized = errors.New("MxL58x is not initialized")

// statusOffset is HYDRA_DMD_STATUS_OFFSET for this frontend.
func (demod *Demod) statusOffset() uint32 {
	return demod.config.DemodIndex * demodStatusStride
}

// regWrite writes one 32-bit register: PLID, length, address, value,
// little-endian.
func (demod *Demod) regWrite(register, value uint32) error {
	frame := []byte{plidRegisterWrite, 0x08, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(frame[2:], register)
	binary.LittleEndian.PutUint32(frame[6:], value)
	if err := demod.bus.Write(demod.config.DemodAddress, frame); err != nil {
		return fmt.Errorf("MxL58x write %#08x: %w", register, err)
	}
	return nil
}

// regRead reads one 32-bit register: addressed read request, four bytes back.
func (demod *Demod) regRead(register uint32) (uint32, error) {
	frame := []byte{plidRegisterRead, 0x04, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(frame[2:], register)
	data, err := i2c.WriteRead(demod.bus, demod.config.DemodAddress, frame, 4)
	if err != nil {
		return 0, fmt.Errorf("MxL58x read %#08x: %w", register, err)
	}
	return binary.LittleEndian.Uint32(data), nil
}

// regReadField reads one mnemonic's bits, read_by_mnemonic.
func (demod *Demod) regReadField(f field) (uint32, error) {
	value, err := demod.regRead(f.register)
	if err != nil {
		return 0, err
	}
	return (value & f.mask()) >> uint32(f.lsb), nil
}

// regWriteField replaces one mnemonic's bits, update_by_mnemonic; read first, so
// every other field survives.
func (demod *Demod) regWriteField(f field, value uint32) error {
	current, err := demod.regRead(f.register)
	if err != nil {
		return err
	}
	mask := f.mask()
	return demod.regWrite(f.register, current&^mask|(value<<uint32(f.lsb))&mask)
}

// maxBlockWrite is the most payload the chip takes in one block transfer.
const maxBlockWrite = 248

// writeBlock writes a run of bytes from a register address, passed through
// untouched.
func (demod *Demod) writeBlock(register uint32, data []byte) error {
	if len(data) > maxBlockWrite {
		return fmt.Errorf("MxL58x block write of %d bytes exceeds %d", len(data), maxBlockWrite)
	}
	frame := make([]byte, 6, 6+len(data))
	frame[0] = plidRegisterWrite
	frame[1] = byte(len(data) + 4)
	binary.LittleEndian.PutUint32(frame[2:], register)
	frame = append(frame, data...)
	if err := demod.bus.Write(demod.config.DemodAddress, frame); err != nil {
		return fmt.Errorf("MxL58x block write %#08x: %w", register, err)
	}
	return nil
}

// sendCommand posts one message to the chip's firmware, which answers nothing:
// success means the bytes reached the chip, not that the firmware liked them.
func (demod *Demod) sendCommand(id byte, payload []byte) error {
	size := len(payload)
	if size > 251 {
		return fmt.Errorf("MxL58x command %d payload of %d bytes is too long", id, size)
	}
	frame := make([]byte, 6, 6+size)
	frame[0] = plidCommandWrite
	frame[1] = byte(size + 4)
	frame[2] = byte(size)
	frame[3] = id
	frame = append(frame, payload...)
	if err := demod.bus.Write(demod.config.DemodAddress, frame); err != nil {
		return fmt.Errorf("MxL58x command %d: %w", id, err)
	}
	return nil
}

// words marshals a command payload: every one of this firmware's command
// structures is a run of little-endian 32-bit fields.
func words(values ...uint32) []byte {
	payload := make([]byte, 4*len(values))
	for index, value := range values {
		binary.LittleEndian.PutUint32(payload[4*index:], value)
	}
	return payload
}

// heartbeatInterval is the gap between the two heartbeat reads.
const heartbeatInterval = 20 * time.Millisecond

// firmwareIsAlive reads the heartbeat counter twice and reports whether it
// moved -- the chip's only answer to "are you already running".
func (demod *Demod) firmwareIsAlive() (bool, error) {
	first, err := demod.regRead(regHeartbeat)
	if err != nil {
		return false, err
	}
	time.Sleep(heartbeatInterval)
	second, err := demod.regRead(regHeartbeat)
	if err != nil {
		return false, err
	}
	return first != second, nil
}

// FirmwareVersion is the version word the running firmware reports, meaningful
// only once Initialize has found or made a running chip.
func (demod *Demod) FirmwareVersion() (uint32, error) {
	if !demod.warm {
		return 0, ErrNotInitialized
	}
	return demod.regRead(regFirmwareVersion)
}

// Initialize brings the chip up if it is not already up, and settles this
// frontend either way. The gate is the heartbeat: eight frontends share one
// chip, so the first Initialize does the work and the other seven find the
// firmware answering.
func (demod *Demod) Initialize() error {
	alive, err := demod.firmwareIsAlive()
	if err != nil {
		return err
	}
	if alive {
		demod.warm = true
		return nil
	}
	if demod.config.AlreadyBooted {
		return errors.New("MxL58x was declared already booted but its firmware heartbeat is not advancing;" +
			" downloading firmware now would reset the chip and take all eight of its frontends down")
	}
	if err := demod.coldStart(); err != nil {
		return err
	}
	demod.warm = true
	return nil
}

// coldStart is probe()'s "firmware is not alive" branch: wake the analogue
// front end, read the chip version, settle the crystal, write the firmware,
// configure the four DiSEqC controllers and the transport stream output.
func (demod *Demod) coldStart() error {
	if err := demod.regWriteField(fieldAFEClockEnable, 1); err != nil {
		return err
	}
	if err := demod.regWriteField(fieldAFESoftResetN, 1); err != nil {
		return err
	}
	// The version is unused, but the read is the first thing to touch the
	// just-released analogue block, and a chip that cannot answer it will not take
	// a firmware image.
	if _, err := demod.regReadField(fieldChipVersion); err != nil {
		return err
	}
	if err := demod.configureCrystal(); err != nil {
		return err
	}
	if err := demod.loadFirmware(); err != nil {
		return err
	}
	// All four DiSEqC controllers, used or not: they belong to the chip rather
	// than to a frontend.
	for id := uint32(0); id < 4; id++ {
		if err := demod.configureDiSEqC(id); err != nil {
			return err
		}
	}
	return demod.configureTransportStream()
}

// configureCrystal is cfg_dev_xtal: switch off the clock output on the 1.8 V pin,
// tell the firmware which crystal is fitted, hand over the load capacitance.
func (demod *Demod) configureCrystal() error {
	if err := demod.regWriteField(fieldXtalEnableClockOut, 0); err != nil {
		return err
	}
	setting := uint32(1)
	if demod.config.ClockHz == 24_000_000 {
		setting = 0
	}
	if err := demod.regWrite(regCrystalSetting, setting); err != nil {
		return err
	}
	return demod.regWrite(regCrystalCap, demod.config.CrystalCap)
}

// configureDiSEqC is config_dis: one of the four controllers, in tone mode,
// speaking DiSEqC 1.x on a 22 kHz carrier.
func (demod *Demod) configureDiSEqC(id uint32) error {
	const (
		toneMode     = 1 // MXL_HYDRA_DISEQC_TONE_MODE
		version1X    = 0 // MXL_HYDRA_DISEQC_1_X
		carrier22KHz = 0 // MXL_HYDRA_DISEQC_CARRIER_FREQ_22KHZ
	)
	return demod.sendCommand(commandDiSEqCConfig, words(id, toneMode, version1X, carrier22KHz))
}

// transportStreamSetup is the twelve register writes that configure the
// transport stream output, in order: three-wire serial, most significant bit
// first, a continuous clock capped at 139 MHz and shifted 180 degrees, sync and
// valid active high, a one-bit sync pulse, no error indication, all eight
// demodulators to all eight outputs.
var transportStreamSetup = []struct {
	register uint32
	value    uint32
	why      string
}{
	// XPT_STREAM_MUXMODE0 and 1 both take mux mode 1.
	{0x90700008, 0x00000005, "stream mux mode"},
	// PAD_MUX_DIGIO_08..15 and 16..23 all to selector 0, as cfg_ts_pad_mux writes.
	{0x90000170, 0x00000000, "transport pad mux, DIGIO 8 to 15"},
	{0x90000174, 0x00000000, "transport pad mux, DIGIO 16 to 23"},
	// XPT_NCO_COUNT_MIN, bits 16 to 23, takes 3: MXL_HYDRA_NCO_CLK of 418 MHz
	// divided by the 139 MHz maximum transport clock.
	{0x90700044, 0x00030000, "minimum NCO count for a 139 MHz clock"},
	{0x90700238, 0x03030303, "per-output NCO counts"},
	{0x9070023c, 0x00030303, "per-output NCO counts, upper slice"},
	// XPT_TS_CLK_OUT_EN0..7 set, the parallel one clear.
	{0x907001d4, 0x000000ff, "continuous clock on all eight outputs"},
	// XPT_SYNC_FULL_BYTE0..7 clear (a one-bit sync pulse), XPT_SYNC_POLARITY0..7
	// set.
	{0x90700010, 0x0000ff00, "sync width and polarity"},
	// XPT_VALID_POLARITY0..7 set, error-replace-valid clear.
	{0x90700014, 0x000000ff, "valid polarity"},
	// XPT_TS_CLK_PHASE0..7 each take 2, a 180 degree shift.
	{0x90700018, 0x22222222, "clock phase"},
	// XPT_ENABLE_OUTPUT0..7 set; LSB-first and error-replace-sync clear.
	{0x9070000c, 0x000000ff, "enable the eight outputs"},
	// XPT_ENABLE_INPUT0..7 set; the DSS input and encapsulation modes clear.
	{0x90700000, 0x000000ff, "enable the eight inputs"},
}

func (demod *Demod) configureTransportStream() error {
	for _, line := range transportStreamSetup {
		if err := demod.regWrite(line.register, line.value); err != nil {
			return fmt.Errorf("MxL58x transport stream setup (%s): %w", line.why, err)
		}
	}
	return nil
}

// InitializeMultiswitch sets the card's four RF inputs to the four band and
// polarisation combinations a quattro low-noise block presents. Separate from
// Initialize because doing it to a running chip would move all four inputs' bands
// under whoever owns them. The voltage half needs Config.Voltage.
func (demod *Demod) InitializeMultiswitch() error {
	if !demod.warm {
		return ErrNotInitialized
	}
	if demod.config.Multiswitch != Quattro {
		return nil
	}
	// Inputs 3 and 2 carry the low supply voltage, 1 and 0 the high, and the tone
	// alternates. Bit 1 is voltage and bit 0 is tone, which is how SetVoltage and
	// SetTone choose an input.
	if demod.config.Voltage != nil {
		for _, input := range []struct {
			index uint32
			high  bool
		}{{3, false}, {2, false}, {1, true}, {0, true}} {
			if err := demod.config.Voltage.SetInputVoltage(input.index, input.high, false); err != nil {
				return err
			}
		}
	}
	for _, input := range []struct {
		index uint32
		tone  bool
	}{{3, false}, {2, true}, {1, false}, {0, true}} {
		if err := demod.setInputTone(input.index, input.tone); err != nil {
			return err
		}
	}
	return nil
}
