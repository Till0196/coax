// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package si2157 drives the Silicon Labs Si2141 and Si2157 terrestrial and
// cable tuners over any i2c.Bus, typically the gated bus behind an Si2168.
//
// The two parts share one command protocol and differ only in how they are
// powered up, which firmware they boot, and which IF port the board wired.
package si2157

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/i2c"
)

// defaultAddress is where one of these answers unless the board moved it with
// the part's address pin: TBS's 6504 and 6508 alternate between 0x60 and 0x61
// down the card.
const defaultAddress byte = 0x60

// Variant selects which member of the family is fitted. The values are the part
// numbers the chip reports for itself, so the revision query can be compared
// against the variant directly.
type Variant byte

const (
	// Si2141 is the tuner on the T230C family.
	Si2141 Variant = 41
	// Si2157 is the tuner on the plain T230 and on several DVBSky boards.
	Si2157 Variant = 57
)

func (variant Variant) String() string { return fmt.Sprintf("Si21%d", byte(variant)) }

// Config describes how the board wired and populated the tuner.
type Config struct {
	// Variant is the part fitted. The zero value is not valid: the power-up
	// sequence has to be chosen before the chip can be asked what it is.
	Variant Variant
	// IFPort selects which of the tuner's IF outputs feeds the demodulator.
	// The T230C family uses port 0 and the plain T230 uses port 1.
	IFPort byte
	// Address is the tuner's seven-bit I2C address. Zero means
	// defaultAddress.
	Address byte
}

// Tuner is an Si2141 or Si2157 reached over an I2C bus.
type Tuner struct {
	bus         i2c.Bus
	firmwareDir string
	config      Config
	ifFrequency uint32
}

// New addresses a tuner on the given bus. firmwareDir holds the optional
// firmware blob loaded at Initialize time.
func New(bus i2c.Bus, firmwareDir string, config Config) *Tuner {
	if config.Address == 0 {
		config.Address = defaultAddress
	}
	return &Tuner{bus: bus, firmwareDir: firmwareDir, config: config}
}

func (tuner *Tuner) Variant() Variant { return tuner.config.Variant }

// ErrRejected reports that the tuner raised the error bit of its status byte:
// the chip answered and refused, rather than not answering at all. The Si2141
// power-up raises it as a matter of course, which is why it has to be
// distinguishable from a bus that is not working.
var ErrRejected = errors.New("the tuner raised its error bit")

// status bits of the byte every response begins with.
const (
	statusError = 0x40 // the command was refused
	statusReady = 0x80 // clear to send: the command has finished
)

// command writes one command and waits for its response. The status byte is
// read until the ready bit appears, and only then is the error bit meaningful:
// before that it still belongs to whatever ran before.
func (tuner *Tuner) command(write []byte, readLength int) ([]byte, error) {
	part := tuner.config.Variant
	opcode := byte(0)
	if len(write) != 0 {
		opcode = write[0]
		if err := tuner.bus.Write(tuner.config.Address, write); err != nil {
			return nil, fmt.Errorf("write %s command %#02x: %w", part, opcode, err)
		}
	}
	if readLength == 0 {
		return nil, nil
	}
	deadline := time.Now().Add(80 * time.Millisecond)
	for {
		response, err := tuner.bus.Read(tuner.config.Address, readLength)
		if err != nil {
			return nil, fmt.Errorf("read the %s response to %#02x: %w", part, opcode, err)
		}
		if len(response) != readLength {
			return nil, fmt.Errorf("short %s response to %#02x: %d of %d", part, opcode, len(response), readLength)
		}
		if response[0]&statusReady != 0 {
			if response[0]&statusError != 0 {
				return nil, fmt.Errorf("%s command %#02x: %w (status %#02x)", part, opcode, ErrRejected, response[0])
			}
			return response, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s command %#02x timed out (status %#02x)", part, opcode, response[0])
		}
		time.Sleep(3 * time.Millisecond)
	}
}

// Initialize powers up the tuner, loads firmware when a blob is present and
// leaves it ready to tune. A tuner left warm by a previous process is reused.
func (tuner *Tuner) Initialize() error {
	part := tuner.config.Variant
	// A valid crystal trim means a previous process left the tuner warm.
	if response, err := tuner.command([]byte{0x15, 0, 2, 4}, 4); err == nil && uint16(response[2])|uint16(response[3])<<8 < 16 {
		return nil
	}
	if err := tuner.powerUp(); err != nil {
		return err
	}
	revision, err := tuner.command([]byte{2}, 13)
	if err != nil {
		return fmt.Errorf("read the %s revision: %w", part, err)
	}
	if len(revision) >= 13 && revision[2] != byte(part) {
		return fmt.Errorf("expected %s, found Si21%d", part, revision[2])
	}
	romID := revision[12]
	loaded, err := tuner.loadFirmware(romID)
	if err != nil {
		return err
	}
	if _, err := tuner.command([]byte{1, 1}, 1); err != nil {
		if loaded == "" {
			// Firmware is optional -- some parts run from ROM -- so a missing
			// file cannot be fatal any earlier than this.
			return fmt.Errorf("%w; no firmware was found in %s, and this tuner will not start from its ROM (tried %s)",
				err, tuner.firmwareDirOrDot(), strings.Join(firmwareNames(part, romID), ", "))
		}
		return fmt.Errorf("start the %s on firmware %s: %w", part, loaded, err)
	}
	if _, err := tuner.command([]byte{0x11}, 10); err != nil {
		return err
	}
	for _, command := range [][]byte{{0x14, 0, 1, 5, 1, 0}, {0x14, 0, 1, 6, 1, 0}, {0x14, 0, 1, 7, 1, 0}} {
		if _, err := tuner.command(command, 1); err != nil {
			return err
		}
	}
	return nil
}

// powerUp sends the variant's power-up sequence. The Si2141 takes a shorter
// command, needs a wake-up after it, and raises its error bit even on success.
func (tuner *Tuner) powerUp() error {
	part := tuner.config.Variant
	if part != Si2141 {
		// clock_mode = XTAL, xout disabled.
		if _, err := tuner.command([]byte{0xc0, 0, 0x0c, 0, 0, 1, 1, 1, 1, 1, 1, 2, 0, 0, 1}, 1); err != nil {
			return fmt.Errorf("power up the %s: %w", part, err)
		}
		return nil
	}
	// Only the refusal is tolerated; a transport failure here is real.
	if _, err := tuner.command([]byte{0xc0, 0, 0x0d, 0x0e, 0, 1, 1, 1, 1, 3}, 1); err != nil && !errors.Is(err, ErrRejected) {
		return fmt.Errorf("power up the %s: %w", part, err)
	}
	if _, err := tuner.command([]byte{0xc0, 8, 1, 2, 0, 0, 1}, 1); err != nil {
		return fmt.Errorf("wake the %s: %w", part, err)
	}
	return nil
}

// firmwareNames lists the files this variant and ROM revision may load from, in
// the order they are tried.
func firmwareNames(variant Variant, romID byte) []string {
	alternate := "dvb-tuner-si2157-a30-01.fw"
	if variant == Si2141 {
		alternate = "dvb-tuner-si2141-a10-01.fw"
	}
	return []string{
		fmt.Sprintf("dvb_driver_si21%d_rom%02x.fw", byte(variant), romID),
		alternate,
	}
}

// firmwareDirOrDot names the directory searched, so a message stays actionable
// when none was configured.
func (tuner *Tuner) firmwareDirOrDot() string {
	if tuner.firmwareDir == "" {
		return "the current directory"
	}
	return tuner.firmwareDir
}

// loadFirmware downloads the first firmware file present and reports which. An
// empty name means none was found, which is not a failure: the tuner may still
// start from its ROM.
func (tuner *Tuner) loadFirmware(romID byte) (string, error) {
	for _, name := range firmwareNames(tuner.config.Variant, romID) {
		data, readErr := device.ReadFirmware(tuner.firmwareDir, name)
		if readErr != nil {
			continue
		}
		if err := tuner.download(data); err != nil {
			return name, fmt.Errorf("download %s firmware %s: %w", tuner.config.Variant, name, err)
		}
		return name, nil
	}
	return "", nil
}

func (tuner *Tuner) download(data []byte) error {
	if len(data) == 0 || len(data)%17 != 0 {
		return fmt.Errorf("invalid %s firmware size %d", tuner.config.Variant, len(data))
	}
	for offset := 0; offset < len(data); offset += 17 {
		length := int(data[offset])
		if length > 16 {
			return fmt.Errorf("invalid firmware record length %d", length)
		}
		if _, err := tuner.command(data[offset+1:offset+1+length], 1); err != nil {
			return err
		}
	}
	return nil
}

// SignalStrength reports the input level in dBm. Being a tuner rather than a
// demodulator measurement, it is meaningful even on a channel that never locks.
func (tuner *Tuner) SignalStrength() (float64, error) {
	response, err := tuner.command([]byte{0x42, 0}, 12)
	if err != nil {
		return 0, err
	}
	// The level is a signed byte of whole dBm.
	return float64(int8(response[3])), nil
}

// Tune moves the tuner to a frequency in Hz. Completion is advisory: the
// demodulator's lock is what actually decides whether the channel is there.
func (tuner *Tuner) Tune(frequency, bandwidth uint32, deliverySystem string) error {
	bw := byte(8)
	switch {
	case bandwidth <= 1_700_000:
		bw = 9
	case bandwidth <= 6_000_000:
		bw = 6
	case bandwidth <= 6_100_000:
		bw = 10
	case bandwidth <= 7_000_000:
		bw = 7
	}
	delivery := byte(0x20)
	// Every J.83 annex, Annex B included, shares the tuner's cable setting:
	// the trellis coding that sets Annex B apart is the demodulator's alone.
	if tuning.IsJ83(deliverySystem) {
		delivery = 0x30
	}
	commands := [][]byte{{0x14, 0, 3, 7, delivery | bw, 0}, {0x14, 0, 2, 7, tuner.config.IFPort, 0}}
	if tuner.ifFrequency != 5_000_000 {
		commands = append(commands, []byte{0x14, 0, 6, 7, 0x88, 0x13})
		tuner.ifFrequency = 5_000_000
	}
	for _, command := range commands {
		if _, err := tuner.command(command, 4); err != nil {
			return err
		}
	}
	command := []byte{0x41, 0, 0, 0, byte(frequency), byte(frequency >> 8), byte(frequency >> 16), byte(frequency >> 24)}
	if _, err := tuner.command(command, 1); err != nil {
		return err
	}
	deadline := time.Now().Add(40 * time.Millisecond)
	for time.Now().Before(deadline) {
		response, err := tuner.bus.Read(tuner.config.Address, 1)
		if err != nil {
			return err
		}
		if response[0]&0x81 == 0x81 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}
