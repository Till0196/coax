// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TurboSight's si2183 driver, which is out of tree: the Si2168 and
// Si2157 command protocol on a chip that adds DVB-S/S2 and ISDB-T, sharing
// their firmware download format and ready/error status bits.

// Package si2183 drives the Silicon Labs Si2183 combined terrestrial, cable
// and satellite demodulator over any i2c.Bus, gating the tuner bus behind it.
package si2183

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// defaultAddress is where an Si2183 answers unless the board moved it with the
// chip's address pins: TBS's 6508 and 6904X alternate between 0x64 and 0x67
// down the card. A board with one chip leaves Config.DemodAddress at zero.
const defaultAddress byte = 0x67

// Property IDs the chip's firmware exposes.
const (
	propTSMode     uint16 = 0x1001
	propDDIEN      uint16 = 0x1006
	propIntSense   uint16 = 0x1007
	propParTS      uint16 = 0x1009
	propSerTS      uint16 = 0x1008
	propFERResol   uint16 = 0x100c
	propSQIControl uint16 = 0x100f
	propParTSSec   uint16 = 0x1015
	propSerTSSec   uint16 = 0x1016
	propMode       uint16 = 0x100a
	propDVBCConst  uint16 = 0x1101
	propDVBCSR     uint16 = 0x1102
	propDVBTHier   uint16 = 0x1201
	propDVBT2Mode  uint16 = 0x1304
	propDVBS2SR    uint16 = 0x1401
	propDVBSSR     uint16 = 0x1501
	propMCNSConst  uint16 = 0x1601
	propMCNSSR     uint16 = 0x1602
)

// The B60 is the only revision this driver identifies.
const firmwareName = "dvb-demod-si2183-b60-01.fw"

// Demod is an Si2183 reached over an I2C bus.
type Demod struct {
	bus         i2c.Bus
	firmwareDir string
	config      Config

	loaded   bool
	system   string
	gateOpen bool
}

// Config describes how the board wired the chip.
type Config struct {
	// DemodAddress is the chip's seven-bit I2C address; zero means
	// defaultAddress.
	DemodAddress byte
	// TSMode is TSModeParallel or TSModeSerial.
	TSMode byte
	// TSClockGapped enables the gapped-clock output some boards need.
	TSClockGapped bool
	// TSClockInverted selects the transport stream clock polarity.
	TSClockInverted bool
	// AGCMode is the chip's AGC configuration byte. Every board seen so far
	// leaves this at 5.
	AGCMode byte
	// StartClockMode says the board starts the chip's clock the other way
	// round. It reaches two bytes of the initialization command and one byte
	// of the power-up, cold and warm alike. Only TBS's 6904X sets it.
	StartClockMode bool
	// AlreadyBooted says another driver has already loaded this chip's
	// firmware, so resume it rather than start it again. It is for the
	// Windows backends, where the cold sequence would drop a running chip
	// back into its bootloader and take down the transport stream the vendor
	// driver is carrying. Nothing on the chip records who booted it.
	AlreadyBooted bool
}

// TS_MODE property values.
const (
	TSModeSerial   byte = 0x03
	TSModeParallel byte = 0x06
)

// New addresses an Si2183 on the given bus. firmwareDir holds the optional
// firmware blob loaded at Initialize time.
func New(bus i2c.Bus, firmwareDir string, config Config) *Demod {
	if config.TSMode == 0 {
		config.TSMode = TSModeParallel
	}
	if config.DemodAddress == 0 {
		config.DemodAddress = defaultAddress
	}
	return &Demod{bus: bus, firmwareDir: firmwareDir, config: config}
}

const (
	statusError = 0x40
	statusReady = 0x80
)

func (demod *Demod) command(write []byte, readLength int) ([]byte, error) {
	opcode := byte(0)
	if len(write) != 0 {
		opcode = write[0]
		if err := demod.bus.Write(demod.config.DemodAddress, write); err != nil {
			return nil, fmt.Errorf("write Si2183 command %#02x: %w", opcode, err)
		}
	}
	if readLength == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		response, err := demod.bus.Read(demod.config.DemodAddress, readLength)
		if err != nil {
			return nil, fmt.Errorf("read the Si2183 response to %#02x: %w", opcode, err)
		}
		if len(response) != readLength {
			return nil, fmt.Errorf("short Si2183 response to %#02x: %d of %d", opcode, len(response), readLength)
		}
		if response[0]&statusReady != 0 {
			if response[0]&statusError != 0 {
				return nil, fmt.Errorf("Si2183 command %#02x rejected (status %#02x)", opcode, response[0])
			}
			return response, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("Si2183 command %#02x timed out (status %#02x)", opcode, response[0])
		}
		time.Sleep(3 * time.Millisecond)
	}
}

func (demod *Demod) setProp(prop uint16, value uint16) error {
	command := []byte{0x14, 0, byte(prop), byte(prop >> 8), byte(value), byte(value >> 8)}
	_, err := demod.command(command, 4)
	if err != nil {
		return fmt.Errorf("set Si2183 property %#04x: %w", prop, err)
	}
	return nil
}

// Initialize powers up the chip, loads firmware when a blob is present and
// leaves it ready to tune. A chip left warm by a previous process is reused.
func (demod *Demod) Initialize() error {
	clock := []byte{0xc0, 0x12, 0x00, 0x0c, 0x00, 0x0d, 0x16, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if demod.config.StartClockMode {
		clock[3], clock[5] = 0x00, 0x06
	}
	if _, err := demod.command(clock, 0); err != nil {
		return fmt.Errorf("initialize the Si2183 clock: %w", err)
	}
	if demod.config.AlreadyBooted {
		return demod.resume()
	}
	powerUp := []byte{0xc0, 0x06, 0x01, 0x0f, 0x00, 0x20, 0x20, 0x01}
	if demod.config.StartClockMode {
		powerUp[6] = 0x30
	}
	if _, err := demod.command(powerUp, 1); err != nil {
		return fmt.Errorf("power up the Si2183: %w", err)
	}
	revision, err := demod.command([]byte{2}, 13)
	if err != nil {
		return fmt.Errorf("read the Si2183 revision: %w", err)
	}
	if len(revision) < 13 || revision[2] != 'B' || revision[1] != '6' || revision[3] != '0' {
		return fmt.Errorf("expected Si2183-B60, found Si218%c-%c%c%c",
			revision[2], revision[1], revision[3], revision[4])
	}
	loaded, err := demod.loadFirmware()
	if err != nil {
		return err
	}
	if _, err := demod.command([]byte{1, 1}, 1); err != nil {
		if !loaded {
			return fmt.Errorf("%w; no firmware was found in %s (tried %s)",
				err, demod.firmwareDirOrDot(), firmwareName)
		}
		return fmt.Errorf("start the Si2183: %w", err)
	}
	if _, err := demod.command([]byte{0x11}, 10); err != nil {
		return err
	}
	tsMode := 0x10 | demod.config.TSMode
	if demod.config.TSClockGapped {
		tsMode |= 0x40
	}
	if err := demod.setProp(propTSMode, uint16(tsMode)); err != nil {
		return err
	}
	if err := demod.setProp(propFERResol, 0x12); err != nil {
		return err
	}
	if err := demod.setProp(propDDIEN, 0x00); err != nil {
		return err
	}
	if err := demod.setProp(propIntSense, 0x2000); err != nil {
		return err
	}
	if err := demod.setProp(propSQIControl, 0x1e); err != nil {
		return err
	}
	parallel := uint16(0x0104)
	serial := uint16(0x230c)
	if !demod.config.TSClockInverted {
		parallel |= 0x1000
		serial |= 0x1000
	}
	if err := demod.setProp(propParTS, parallel); err != nil {
		return err
	}
	if err := demod.setProp(propSerTS, serial); err != nil {
		return err
	}
	if err := demod.setProp(propParTSSec, 0x08e3); err != nil {
		return err
	}
	if err := demod.setProp(propSerTSSec, 0x01c7); err != nil {
		return err
	}
	demod.loaded = true
	return nil
}

func (demod *Demod) firmwareDirOrDot() string {
	if demod.firmwareDir == "" {
		return "the current directory"
	}
	return demod.firmwareDir
}

func (demod *Demod) loadFirmware() (bool, error) {
	data, err := device.ReadFirmware(demod.firmwareDir, firmwareName)
	if err != nil {
		return false, nil
	}
	if len(data) == 0 || len(data)%17 != 0 {
		return false, fmt.Errorf("invalid Si2183 firmware size %d", len(data))
	}
	for offset := 0; offset < len(data); offset += 17 {
		length := int(data[offset])
		if length > 16 {
			return false, fmt.Errorf("invalid firmware record length %d", length)
		}
		if _, err := demod.command(data[offset+1:offset+1+length], 1); err != nil {
			return true, fmt.Errorf("download Si2183 firmware: %w", err)
		}
	}
	return true, nil
}

// OpenTunerGate opens or closes the I2C pass-through to the tuner (i2c.Gate).
func (demod *Demod) OpenTunerGate(open bool) error {
	if demod.gateOpen == open {
		return nil
	}
	value := byte(0)
	if open {
		value = 1
	}
	if _, err := demod.command([]byte{0xc0, 0x0d, value}, 0); err != nil {
		return err
	}
	demod.gateOpen = open
	return nil
}

// modeValue is the MODE property value for a delivery system.
func modeValue(delivery string) (uint16, error) {
	switch tuning.DemodDeliverySystem(delivery) {
	case "DVBT":
		return 0x20, nil
	case "DVBT2":
		return 0x70, nil
	case "ISDBT":
		return 0x40, nil
	case "DVBC", "DVBC/ANNEXA":
		return 0x38, nil
	case "DVBC/ANNEXB":
		return 0x18, nil
	case "DVBS":
		return 0x88, nil
	case "DVBS2":
		return 0x98, nil
	default:
		return 0, fmt.Errorf("Si2183 does not support delivery system %q", delivery)
	}
}

// Params is a channel to lock onto.
type Params struct {
	DeliverySystem string
	BandwidthHz    uint32
	SymbolRate     uint32
	Modulation     string
	// StreamID is the DVB-T2 PLP or the DVB-S2 input stream, or negative to
	// let the chip pick.
	StreamID int
	// ScramblingIndex is the DVB-S2 physical layer scrambling sequence, as
	// the gold sequence's index rather than the word it seeds. Zero is the
	// default sequence.
	ScramblingIndex uint32
}

// Tune moves the demodulator to a channel: the RF tuner must already be on
// frequency, as this only programs the delivery-system settings and restarts
// acquisition.
func (demod *Demod) Tune(params Params) error {
	mode, err := modeValue(params.DeliverySystem)
	if err != nil {
		return err
	}
	agcCommand := []byte{0x89, (demod.config.AGCMode&0x07)<<4 | 0x01, 0x06, 0x12, 0, 0}
	normalized := tuning.DemodDeliverySystem(params.DeliverySystem)
	if normalized == "DVBS" || normalized == "DVBS2" {
		agcCommand = []byte{0x8a, demod.config.AGCMode | 0x18, 0x12, 0, 0, 0}
	}
	if _, err := demod.command(agcCommand, 3); err != nil {
		return fmt.Errorf("set Si2183 AGC mode: %w", err)
	}

	switch normalized {
	case "DVBT", "DVBT2":
		bandwidth, err := bandwidthCode(params.BandwidthHz)
		if err != nil {
			return err
		}
		if err := demod.setProp(propMode, mode|uint16(bandwidth)); err != nil {
			return err
		}
		if err := demod.setProp(propDVBTHier, 0); err != nil {
			return err
		}
		if normalized == "DVBT2" {
			plp := byte(0)
			filter := byte(0)
			if params.StreamID >= 0 {
				plp, filter = byte(params.StreamID), 1
			}
			if _, err := demod.command([]byte{0x52, plp, filter}, 1); err != nil {
				return fmt.Errorf("select Si2183 DVB-T2 PLP: %w", err)
			}
			if err := demod.setProp(propDVBT2Mode, 0); err != nil {
				return err
			}
		}
	case "ISDBT":
		bandwidth, err := bandwidthCode(params.BandwidthHz)
		if err != nil {
			return err
		}
		if err := demod.setProp(propMode, mode|uint16(bandwidth)); err != nil {
			return err
		}
	case "DVBC", "DVBC/ANNEXA":
		if err := demod.setProp(propMode, mode); err != nil {
			return err
		}
		if err := demod.setProp(propDVBCConst, dvbcConstellation(params.Modulation)); err != nil {
			return err
		}
		if err := demod.setProp(propDVBCSR, uint16(params.SymbolRate/1000)); err != nil {
			return err
		}
	case "DVBC/ANNEXB":
		// Annex B (US cable, "MCNS") ties the constellation to the symbol
		// rate -- 64-QAM at 5.057 Msym/s, 256-QAM at 5.361 -- but not every
		// carrier keeps to it: the optical distribution of the 124/128 degree
		// east service runs 256-QAM at 5.6 Msym/s. So an explicit rate is
		// taken as meant, and only without one does the constellation decide.
		// The chip has no auto-detecting mode, unlike the LG demodulators
		// whose files often say QAM/AUTO or nothing, so an unnamed
		// constellation becomes 256-QAM and a 64-QAM channel has to say so.
		if err := demod.setProp(propMode, mode); err != nil {
			return err
		}
		constellation, symbolRate := annexBConstellation(params.Modulation, params.SymbolRate)
		if err := demod.setProp(propMCNSConst, constellation); err != nil {
			return err
		}
		if err := demod.setProp(propMCNSSR, symbolRate); err != nil {
			return err
		}
	case "DVBS", "DVBS2":
		if err := demod.setProp(propMode, mode); err != nil {
			return err
		}
		symbolRateProp := propDVBSSR
		if normalized == "DVBS2" {
			symbolRateProp = propDVBS2SR
		}
		if err := demod.setProp(symbolRateProp, uint16(params.SymbolRate/1000)); err != nil {
			return err
		}
		if normalized == "DVBS2" {
			streamID := byte(0)
			filter := byte(0)
			if params.StreamID >= 0 {
				streamID, filter = byte(params.StreamID), 1
			}
			if _, err := demod.command([]byte{0x71, streamID, filter}, 1); err != nil {
				return fmt.Errorf("select Si2183 DVB-S2 stream ID: %w", err)
			}
			seed := uint32(0)
			if params.ScramblingIndex != 0 {
				seed = goldSequenceSeed(params.ScramblingIndex)
			}
			enable := byte(0)
			if seed > 0 {
				enable = 1
			}
			if _, err := demod.command([]byte{
				0x73, enable, 0, 0,
				byte(seed), byte(seed >> 8), byte(seed >> 16), byte(seed >> 24),
			}, 1); err != nil {
				return fmt.Errorf("set Si2183 DVB-S2 scrambling sequence: %w", err)
			}
		}
	default:
		return fmt.Errorf("Si2183 does not support delivery system %q", params.DeliverySystem)
	}

	if _, err := demod.command([]byte{0x85}, 1); err != nil {
		return fmt.Errorf("restart Si2183 acquisition: %w", err)
	}
	demod.system = normalized
	return nil
}

func bandwidthCode(bandwidthHz uint32) (byte, error) {
	if bandwidthHz == 0 {
		return 0, errors.New("BANDWIDTH_HZ is required by the Si2183")
	}
	switch {
	case bandwidthHz <= 2_000_000:
		return 0x02, nil
	case bandwidthHz <= 5_000_000:
		return 0x05, nil
	case bandwidthHz <= 6_000_000:
		return 0x06, nil
	case bandwidthHz <= 7_000_000:
		return 0x07, nil
	case bandwidthHz <= 8_000_000:
		return 0x08, nil
	case bandwidthHz <= 9_000_000:
		return 0x09, nil
	case bandwidthHz <= 10_000_000:
		return 0x0a, nil
	default:
		return 0x0f, nil
	}
}

// annexBConstellation reports the MCNS constellation value and the symbol rate
// in kilosymbols: symbolRateSPS when the channel gives one, otherwise the rate
// the annex pairs with the constellation. See Tune's Annex B branch.
func annexBConstellation(modulation string, symbolRateSPS uint32) (uint16, uint16) {
	constellation, symbolRateKS := uint16(11), uint16(5361)
	if tuning.NormalizeModulation(modulation) == "QAM64" {
		constellation, symbolRateKS = 9, 5057
	}
	if symbolRateSPS != 0 {
		symbolRateKS = uint16(symbolRateSPS / 1000)
	}
	return constellation, symbolRateKS
}

func dvbcConstellation(modulation string) uint16 {
	switch tuning.NormalizeModulation(modulation) {
	case "QAM16":
		return 7
	case "QAM32":
		return 8
	case "QAM64":
		return 9
	case "QAM128":
		return 10
	case "QAM256":
		return 11
	default:
		return 0 // auto
	}
}

// Status is one status query's result.
type Status struct {
	Locked bool
	Names  []string
	// HasCNR is false when the chip is not locked, which is when it stops
	// measuring the CNR.
	CNRdB  float64
	HasCNR bool
}

// statusCommands maps a delivery system to its status command and its length.
var statusCommands = map[string]struct {
	command []byte
	length  int
}{
	"DVBT":        {[]byte{0xa0, 1}, 13},
	"DVBC":        {[]byte{0x90, 1}, 9},
	"DVBC/ANNEXA": {[]byte{0x90, 1}, 9},
	"DVBC/ANNEXB": {[]byte{0x98, 1}, 10},
	"DVBT2":       {[]byte{0x50, 1}, 14},
	"DVBS":        {[]byte{0x60, 1}, 10},
	"DVBS2":       {[]byte{0x70, 1}, 13},
	"ISDBT":       {[]byte{0xa4, 1}, 14},
}

// Status reports the lock state and CNR for the system last tuned to.
func (demod *Demod) Status() (Status, error) {
	spec, ok := statusCommands[demod.system]
	if !ok {
		return Status{}, fmt.Errorf("Si2183: no status command for %q", demod.system)
	}
	response, err := demod.command(spec.command, spec.length)
	if err != nil {
		return Status{}, err
	}
	state := (response[2] >> 1) & 0x03
	if state == 3 {
		return Status{
			Locked: true,
			Names:  []string{"SIGNAL", "CARRIER", "VITERBI", "SYNC", "LOCK"},
			// The reading is a quarter-decibel count.
			CNRdB:  float64(response[3]) / 4,
			HasCNR: true,
		}, nil
	}
	if state == 1 {
		return Status{Names: []string{"SIGNAL", "CARRIER"}}, nil
	}
	return Status{}, nil
}

// resume picks up a chip another driver has already booted: a power-up command
// differing from the cold one in the mode and clock bytes, then a one-byte
// state read. The firmware and the transport stream properties are left alone,
// the other driver having already set them the way it wants.
func (demod *Demod) resume() error {
	powerUp := []byte{0xc0, 0x06, 0x08, 0x0f, 0x00, 0x20, 0x21, 0x01}
	if demod.config.StartClockMode {
		powerUp[6] = 0x31
	}
	if _, err := demod.command(powerUp, 1); err != nil {
		return fmt.Errorf("resume the Si2183: %w", err)
	}
	if _, err := demod.command([]byte{0x85}, 1); err != nil {
		return fmt.Errorf("read the resumed Si2183's state: %w", err)
	}
	demod.loaded = true
	return nil
}

// goldSequenceSeed turns a scrambling sequence index into the eighteen-bit word
// the chip wants. The chip takes the gold code's starting state (x_init) rather
// than its index, so the shift register is run forward index times and read out.
func goldSequenceSeed(index uint32) uint32 {
	var bits [19]byte
	bits[0] = 1
	for step := uint32(0); step < index; step++ {
		bits[18] = (bits[0] + bits[7]) % 2
		copy(bits[:18], bits[1:19])
	}
	seed := uint32(0)
	for i := 0; i < 18; i++ {
		seed |= uint32(bits[i]) << i
	}
	return seed
}
