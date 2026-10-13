// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package si2168 drives the Silicon Labs Si2168 DVB-T/T2/C demodulator over
// any i2c.Bus, gating the tuner bus behind it.
package si2168

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

const address byte = 0x64

// Delivery system codes the chip's tuning register expects. MCNS is the
// chip's name for ITU-T J.83 Annex B, the trellis-coded US cable system.
const (
	deliveryMCNS  byte = 0x10
	deliveryDVBT  byte = 0x20
	deliveryDVBC  byte = 0x30
	deliveryDVBT2 byte = 0x70
)

// J.83 Annex B channels are always 6MHz wide, whatever a channel file says.
const annexBBandwidthHz uint32 = 6_000_000

// step is one mailbox exchange; read is 0 when the chip answers nothing.
type step struct {
	write []byte
	read  int
}

// Params is a channel to lock onto. StreamID is the DVB-T2 PLP, negative to
// let the chip pick. Modulation is read only for J.83 Annex B, where it fixes
// the symbol rate.
type Params struct {
	DeliverySystem string
	BandwidthHz    uint32
	SymbolRate     uint32
	Modulation     string
	StreamID       int
}

// Demod is an Si2168 reached over an I2C bus.
type Demod struct {
	bus           i2c.Bus
	firmwareDir   string
	tsClockManual bool
	system        string
}

// New addresses an Si2168 on the given bus. firmwareDir holds the blob loaded
// at Initialize time. tsClockManual selects the TS_CLK_MANUAL sequence some
// boards (MyGica T230A, T230C2, T230C2 Lite) need instead of the plain
// parallel-TS one T230C uses; host wiring, not chip revision, decides it.
func New(bus i2c.Bus, firmwareDir string, tsClockManual bool) *Demod {
	return &Demod{bus: bus, firmwareDir: firmwareDir, tsClockManual: tsClockManual}
}

// Bits of the status byte that heads every command response.
const (
	statusError = 0x40
	statusReady = 0x80
)

func (demod *Demod) command(write []byte, readLength int) ([]byte, error) {
	opcode := byte(0)
	if len(write) != 0 {
		opcode = write[0]
		if err := demod.bus.Write(address, write); err != nil {
			return nil, fmt.Errorf("write Si2168 command %#02x: %w", opcode, err)
		}
	}
	if readLength == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(140 * time.Millisecond)
	for {
		response, err := demod.bus.Read(address, readLength)
		if err != nil {
			return nil, fmt.Errorf("read the Si2168 response to %#02x: %w", opcode, err)
		}
		if len(response) != readLength {
			return nil, fmt.Errorf("short Si2168 response to %#02x: %d of %d", opcode, len(response), readLength)
		}
		// The error bit only describes this command once the ready bit is
		// set; before that it still belongs to the previous command.
		if response[0]&statusReady != 0 {
			if response[0]&statusError != 0 {
				return nil, fmt.Errorf("Si2168 command %#02x rejected (status %#02x)", opcode, response[0])
			}
			return response, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("Si2168 command %#02x timed out (status %#02x)", opcode, response[0])
		}
		time.Sleep(3 * time.Millisecond)
	}
}

// reset issues the clock-configuration and power-up pair that puts the chip
// into its firmware-download state. Initialize runs it twice: to read the
// revision, then to clear the firmware state before downloading.
func (demod *Demod) reset() error {
	if _, err := demod.command([]byte{0xc0, 0x12, 0, 0x0c, 0, 0x0d, 0x16, 0, 0, 0, 0, 0, 0}, 0); err != nil {
		return err
	}
	_, err := demod.command([]byte{0xc0, 0x06, 0x01, 0x0f, 0, 0x20, 0x20, 0x01}, 1)
	return err
}

// Initialize resets the chip, downloads its firmware and starts it.
func (demod *Demod) Initialize() error {
	if err := demod.reset(); err != nil {
		return err
	}
	revision, err := demod.command([]byte{0x02}, 13)
	if err != nil {
		return err
	}
	firmwareName, err := firmwareForRevision(revision)
	if err != nil {
		return err
	}
	if err := demod.reset(); err != nil {
		return err
	}
	firmware, err := device.ReadFirmware(demod.firmwareDir, firmwareName)
	if err != nil {
		return fmt.Errorf("read required Si2168 firmware: %w", err)
	}
	if err := demod.download(firmware); err != nil {
		return fmt.Errorf("download Si2168 firmware: %w", err)
	}
	if _, err := demod.command([]byte{0x01, 0x01}, 1); err != nil {
		return err
	}
	if _, err := demod.command([]byte{0x11}, 10); err != nil {
		return err
	}
	return demod.setTSBus(true)
}

func firmwareForRevision(response []byte) (string, error) {
	if len(response) < 5 {
		return "", errors.New("short Si2168 revision response")
	}
	switch {
	case response[1] == 'A' && response[2] == 'D' && response[3] == '2' && response[4] == '0':
		return "dvb-demod-si2168-a20-01.fw", nil
	case response[1] == 'A' && response[2] == 'D' && response[3] == '3' && response[4] == '0':
		return "dvb-demod-si2168-a30-01.fw", nil
	case response[1] == 'B' && response[2] == 'D' && response[3] == '4' && response[4] == '0':
		return "dvb-demod-si2168-b40-01.fw", nil
	case response[1] == 'D' && response[2] == 'D' && response[3] == '6' && response[4] == '0':
		return "dvb-demod-si2168-d60-01.fw", nil
	default:
		return "", fmt.Errorf("unsupported Si2168 revision %q", response[1:5])
	}
}

func (demod *Demod) download(data []byte) error {
	if len(data) > 0 && len(data)%17 == 0 && data[0] > 5 {
		for offset := 0; offset < len(data); offset += 17 {
			length := int(data[offset])
			if length > 16 {
				return fmt.Errorf("invalid firmware record length %d", length)
			}
			if _, err := demod.command(data[offset+1:offset+1+length], 1); err != nil {
				return err
			}
		}
		return nil
	}
	if len(data) == 0 || len(data)%8 != 0 {
		return fmt.Errorf("unknown firmware format (%d bytes)", len(data))
	}
	for offset := 0; offset < len(data); offset += 8 {
		if _, err := demod.command(data[offset:offset+8], 1); err != nil {
			return err
		}
	}
	return nil
}

// OpenTunerGate opens or closes the I2C pass-through to the tuner, satisfying
// i2c.Gate.
func (demod *Demod) OpenTunerGate(open bool) error {
	value := byte(0)
	if open {
		value = 1
	}
	_, err := demod.command([]byte{0xc0, 0x0d, value}, 0)
	return err
}

// Tune programs the demodulator for a channel. The tuner must already sit on
// the requested frequency.
func (demod *Demod) Tune(parameters Params) error {
	delivery, err := deliveryCode(parameters.DeliverySystem)
	if err != nil {
		return err
	}
	bandwidthHz := parameters.BandwidthHz
	if delivery == deliveryMCNS {
		bandwidthHz = annexBBandwidthHz
	}
	bandwidth, err := bandwidthCode(bandwidthHz)
	if err != nil {
		return err
	}
	sequence := []step{{[]byte{0x88, 2, 2, 2, 2}, 5}}
	switch delivery {
	case deliveryDVBT:
		sequence = append(sequence, step{[]byte{0x89, 0x21, 0x06, 0x11, 0xff, 0x98}, 3})
	case deliveryDVBC:
		sequence = append(sequence, step{[]byte{0x89, 0x21, 0x06, 0x11, 0x89, 0xf0}, 3})
	case deliveryDVBT2:
		filter, stream := byte(0), byte(0)
		if parameters.StreamID >= 0 {
			filter, stream = 1, byte(parameters.StreamID)
		}
		sequence = append(sequence,
			step{[]byte{0x89, 0x21, 0x06, 0x11, 0x89, 0x20}, 3},
			step{[]byte{0x52, stream, filter}, 1})
	}
	sequence = append(sequence,
		step{[]byte{0x51, 3}, 12},
		step{[]byte{0x12, 8, 4}, 3},
		step{[]byte{0x14, 0, 0x0c, 0x10, 0x12, 0}, 4},
		step{[]byte{0x14, 0, 0x06, 0x10, 0x24, 0}, 4},
		step{[]byte{0x14, 0, 0x07, 0x10, 0, 0x24}, 4},
		step{[]byte{0x14, 0, 0x0a, 0x10, delivery | bandwidth, 0}, 4})
	// The symbol rate property differs per system: 0x1102 DVB-C, 0x1602 MCNS.
	switch delivery {
	case deliveryDVBC:
		rate := parameters.SymbolRate / 1000
		sequence = append(sequence, step{[]byte{0x14, 0, 0x02, 0x11, byte(rate), byte(rate >> 8)}, 4})
	case deliveryMCNS:
		rate := annexBSymbolRateKS(parameters)
		sequence = append(sequence, step{[]byte{0x14, 0, 0x02, 0x16, byte(rate), byte(rate >> 8)}, 4})
	}
	sequence = append(sequence,
		step{[]byte{0x14, 0, 0x0f, 0x10, 0x10, 0}, 4},
		step{[]byte{0x14, 0, 0x09, 0x10, 0xe3, 0x08}, 4},
		step{[]byte{0x14, 0, 0x08, 0x10, 0xd7, 0x05}, 4},
		step{[]byte{0x14, 0, 0x01, 0x12, 0, 0}, 4},
		step{[]byte{0x14, 0, 0x01, 0x03, 0x0c, 0}, 4},
		step{[]byte{0x85}, 1})
	for _, entry := range sequence {
		if _, err := demod.command(entry.write, entry.read); err != nil {
			return err
		}
	}
	if err := demod.setTSBus(true); err != nil {
		return err
	}
	demod.system = parameters.DeliverySystem
	return nil
}

// Status is one status query's result. The CNR arrives in the same response as
// the lock state, so reading it costs no extra traffic.
type Status struct {
	Locked bool
	Names  []string
	// CNRdB is converted from the chip's quarter-decibel count. HasCNR is
	// false when unlocked, which is when the chip stops measuring.
	CNRdB  float64
	HasCNR bool
}

// Status reports the lock state for the system last tuned to.
func (demod *Demod) Status() (Status, error) {
	var query []byte
	var length int
	switch {
	case tuning.IsJ83AnnexAC(demod.system):
		query, length = []byte{0x90, 1}, 9
	case tuning.Normalize(demod.system) == "DVBC/ANNEXB":
		query, length = []byte{0x98, 1}, 10
	case tuning.Normalize(demod.system) == "DVBT":
		query, length = []byte{0xa0, 1}, 13
	case tuning.Normalize(demod.system) == "DVBT2":
		query, length = []byte{0x50, 1}, 14
	default:
		return Status{}, fmt.Errorf("Si2168 does not support delivery system %q", demod.system)
	}
	response, err := demod.command(query, length)
	if err != nil {
		return Status{}, err
	}
	state := (response[2] >> 1) & 3
	if state == 3 {
		return Status{
			Locked: true,
			Names:  []string{"SIGNAL", "CARRIER", "VITERBI", "SYNC", "LOCK"},
			CNRdB:  float64(response[3]) / 4,
			HasCNR: true,
		}, nil
	}
	if state == 1 {
		return Status{Names: []string{"SIGNAL", "CARRIER"}}, nil
	}
	return Status{}, nil
}

// TS_MODE property values.
const (
	tsTristate  = 0x00
	tsParallel  = 0x06
	tsClkManual = 0x20
	tsModeBase  = 0x10 // starting value for the field
)

// setTSBus hands the transport stream bus to the bridge, or parks it. The value
// is a base of 0x10 plus the mode; manual-clock boards replace that base with
// the manual-clock bit rather than adding to it, and a wrong base leaves the
// parallel bus mis-configured.
func (demod *Demod) setTSBus(acquire bool) error {
	mode := byte(tsModeBase)
	if demod.tsClockManual {
		// The host clock rate has to be latched first.
		if _, err := demod.command([]byte{0x14, 0, 0x0d, 0x10, 0xe8, 0x03}, 4); err != nil {
			return err
		}
		mode = tsClkManual
	}
	if acquire {
		mode |= tsParallel
		if demod.tsClockManual {
			mode |= tsClkManual
		}
	} else {
		mode |= tsTristate
	}
	_, err := demod.command([]byte{0x14, 0, 0x01, 0x10, mode, 0}, 4)
	return err
}

func deliveryCode(value string) (byte, error) {
	// The annexes differ only in roll-off, which the tuner's channel filter
	// absorbs, so every A/C spelling reaches the one DVB-C mode.
	if tuning.IsJ83AnnexAC(value) {
		return deliveryDVBC, nil
	}
	switch tuning.Normalize(value) {
	case "DVBT":
		return deliveryDVBT, nil
	case "DVBT2":
		return deliveryDVBT2, nil
	// Annex B is a system of its own; the chip demodulates it as MCNS.
	case "DVBC/ANNEXB":
		return deliveryMCNS, nil
	default:
		return 0, fmt.Errorf("Si2168 does not support delivery system %q", value)
	}
}

// annexBSymbolRateKS reports the Annex B symbol rate in kilosymbols per second.
//
// An explicit SYMBOL_RATE is taken as meant, whatever the modulation says. The
// annex ties the rate to the constellation -- 64-QAM at 5.057 Msym/s, 256-QAM
// at 5.361 -- but not every Annex B carrier keeps to it: the optical
// distribution of the 124/128 degree east service runs 256-QAM at 5.6 Msym/s.
// Letting the modulation win put that carrier at 5.361 and it never locked;
// measured on a MyGica T230 on such a line, 112.75 MHz with QAM/256 and 5600000
// gave no lock, and the same carrier at 5.6 locked at 40.5 Mbit/s.
//
// Where no rate is given the constellation decides, the way the annex has it.
// This chip has no auto-detecting mode: QAM/AUTO or an unnamed modulation
// becomes 256-QAM, and 64-QAM must be named.
func annexBSymbolRateKS(parameters Params) uint32 {
	if parameters.SymbolRate != 0 {
		return parameters.SymbolRate / 1000
	}
	switch tuning.NormalizeModulation(parameters.Modulation) {
	case "QAM64":
		return 5057
	}
	return 5361
}

func bandwidthCode(value uint32) (byte, error) {
	if value == 0 {
		return 0, errors.New("BANDWIDTH_HZ is required by the Si2168")
	}
	switch {
	case value <= 2_000_000:
		return 0x02, nil
	case value <= 5_000_000:
		return 0x05, nil
	case value <= 6_000_000:
		return 0x06, nil
	case value <= 7_000_000:
		return 0x07, nil
	case value <= 8_000_000:
		return 0x08, nil
	case value <= 9_000_000:
		return 0x09, nil
	case value <= 10_000_000:
		return 0x0a, nil
	default:
		return 0x0f, nil
	}
}

// Supports reports whether the Si2168 can demodulate a delivery system.
func Supports(deliverySystem string) bool {
	_, err := deliveryCode(deliverySystem)
	return err == nil
}
