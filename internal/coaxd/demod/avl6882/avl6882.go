// Copyright (c) 2015 Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TBS linux_media (avl6882.c). The AVL6882 is a firmware machine:
// 24-bit address space, 1/2/4-byte registers, and a per-delivery-system image
// driven through a command mailbox. DVB-T/T2, DVB-C and DVB-S/S2 each need a
// different image uploaded, hence SetDeliverySystem is separate from Tune. An
// image is a bytecode "patch" script interpreted by patch.go.
package avl6882

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/i2c"
)

// firmwareName is the packed firmware file: all three delivery-system images
// behind a small header.
const firmwareName = "dvb-demod-avl6882.fw"

// maxI2CWriteSize is the largest single I2C write the bridge carries.
const maxI2CWriteSize = 62

const maxWordsPerWrite = (maxI2CWriteSize - 3) / 4

// System is a delivery system the chip has a firmware image for. One image
// covers both generations: DVB-T2 uses DVB-T's, DVB-S2 uses DVB-S's.
type System int

const (
	SystemUnspecified System = iota
	SystemDVBC
	SystemDVBS
	SystemDVBT
)

// String names the delivery system as the chip's documentation does.
func (system System) String() string {
	switch system {
	case SystemDVBC:
		return "DVB-C"
	case SystemDVBS:
		return "DVB-S/S2"
	case SystemDVBT:
		return "DVB-T/T2"
	default:
		return "unspecified"
	}
}

func (system System) firmwareIndex() int {
	switch system {
	case SystemDVBC:
		return 0
	case SystemDVBS:
		return 1
	default:
		return 2
	}
}

// activeMode is what rs_current_active_mode reports once an image has booted.
func (system System) activeMode() uint32 {
	switch system {
	case SystemDVBC:
		return 0 // AVL_DVBC
	case SystemDVBS:
		return 1 // AVL_DVBSX
	default:
		return 2 // AVL_DVBTX
	}
}

func systemFor(delivery string) (System, error) {
	switch tuning.DemodDeliverySystem(delivery) {
	case "DVBT", "DVBT2":
		return SystemDVBT, nil
	case "DVBC", "DVBC/ANNEXA":
		return SystemDVBC, nil
	case "DVBS", "DVBS2":
		return SystemDVBS, nil
	default:
		return SystemUnspecified, fmt.Errorf("AVL6882 does not support delivery system %q", delivery)
	}
}

// Config describes how the board wired the chip.
type Config struct {
	// Address is the chip's 8-bit I2C slave address.
	Address byte
}

// Demod is an AVL6882 reached over an I2C bus.
type Demod struct {
	bus         i2c.Bus
	firmwareDir string
	config      Config

	images   [3][]uint32
	system   System
	delivery string
	gateOpen bool
}

// New addresses an AVL6882 on bus; firmwareDir holds the packed firmware file.
func New(bus i2c.Bus, firmwareDir string, config Config) *Demod {
	return &Demod{bus: bus, firmwareDir: firmwareDir, config: config}
}

// read fetches a 1, 2 or 4-byte register; the 3-byte address is its own
// transfer, and the chip answers the read that follows.
func (demod *Demod) read(address uint32, size int) (uint32, error) {
	pointer := []byte{byte(address >> 16), byte(address >> 8), byte(address)}
	data, err := i2c.WriteRead(demod.bus, demod.config.Address, pointer, size)
	if err != nil {
		return 0, fmt.Errorf("AVL6882 read %#06x: %w", address, err)
	}
	var value uint32
	for _, b := range data {
		value = value<<8 | uint32(b)
	}
	return value, nil
}

func (demod *Demod) read8(address uint32) (uint32, error)  { return demod.read(address, 1) }
func (demod *Demod) read16(address uint32) (uint32, error) { return demod.read(address, 2) }
func (demod *Demod) read32(address uint32) (uint32, error) { return demod.read(address, 4) }

// write stores a register big-endian after the 3-byte address.
func (demod *Demod) write(address, value uint32, size int) error {
	payload := []byte{byte(address >> 16), byte(address >> 8), byte(address)}
	switch size {
	case 4:
		payload = append(payload, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
	case 2:
		payload = append(payload, byte(value>>8), byte(value))
	default:
		payload = append(payload, byte(value))
	}
	if err := demod.bus.Write(demod.config.Address, payload); err != nil {
		return fmt.Errorf("AVL6882 write %#06x: %w", address, err)
	}
	return nil
}

func (demod *Demod) write8(address, value uint32) error  { return demod.write(address, value, 1) }
func (demod *Demod) write16(address, value uint32) error { return demod.write(address, value, 2) }
func (demod *Demod) write32(address, value uint32) error { return demod.write(address, value, 4) }

// writeData bulk-loads 32-bit words, split to the bridge's write limit.
func (demod *Demod) writeData(address uint32, data []uint32) error {
	for len(data) > 0 {
		chunk := min(len(data), maxWordsPerWrite)
		payload := make([]byte, 3, 3+chunk*4)
		payload[0], payload[1], payload[2] = byte(address>>16), byte(address>>8), byte(address)
		for _, word := range data[:chunk] {
			payload = binary.BigEndian.AppendUint32(payload, word)
		}
		if err := demod.bus.Write(demod.config.Address, payload); err != nil {
			return fmt.Errorf("AVL6882 bulk write %#06x: %w", address, err)
		}
		data = data[chunk:]
		address += uint32(chunk) * 4
	}
	return nil
}

func (demod *Demod) gpioSet(pin, value uint32) error {
	return demod.write32(regGPIOBase+pin, value)
}

// OpenTunerGate opens or closes the I2C repeater to the tuner (i2c.Gate).
func (demod *Demod) OpenTunerGate(open bool) error {
	if demod.gateOpen == open {
		return nil
	}
	value := uint32(0x06)
	if open {
		value = 0x07
	}
	if err := demod.write32(0x118000+tunerI2CBitRptCntrl, value); err != nil {
		return err
	}
	demod.gateOpen = open
	return nil
}

// TunerBus exposes the tuner behind the chip's I2C repeater as a plain bus.
func (demod *Demod) TunerBus() i2c.Bus { return i2c.Gated(demod.bus, demod) }

// Initialize identifies the chip and reads the packed firmware into memory.
// SetDeliverySystem uploads whichever image the channel needs.
func (demod *Demod) Initialize() error {
	id, err := demod.read32(0x108000)
	if err != nil {
		return fmt.Errorf("read the AVL6882 chip ID: %w", err)
	}
	family, err := demod.read32(0x40000)
	if err != nil {
		return fmt.Errorf("read the AVL6882 family ID: %w", err)
	}
	if family != familyAVL68xx {
		return fmt.Errorf("AVL6882 family ID %#08x is not %#08x (chip ID %#08x)", family, familyAVL68xx, id)
	}
	return demod.loadFirmwareFile()
}

// loadFirmwareFile splits the packed firmware into its three images: a header
// of three little-endian (offset, size) pairs, then big-endian 32-bit bodies.
func (demod *Demod) loadFirmwareFile() error {
	data, err := device.ReadFirmware(demod.firmwareDir, firmwareName)
	if err != nil {
		return fmt.Errorf("read the AVL6882 firmware: %w", err)
	}
	const headerSize = 8 * 3
	if len(data) < headerSize {
		return fmt.Errorf("AVL6882 firmware %s is only %d bytes", firmwareName, len(data))
	}
	var total uint32
	type section struct{ offset, size uint32 }
	var sections [3]section
	for i := range sections {
		sections[i].offset = binary.LittleEndian.Uint32(data[i*8:])
		sections[i].size = binary.LittleEndian.Uint32(data[i*8+4:]) &^ 3
		total += sections[i].size
	}
	if total != uint32(len(data))-headerSize {
		return fmt.Errorf("AVL6882 firmware %s: sections total %d bytes, file body is %d",
			firmwareName, total, len(data)-headerSize)
	}
	for i, section := range sections {
		end := int(section.offset) + int(section.size)
		if int(section.offset) > len(data) || end > len(data) {
			return fmt.Errorf("AVL6882 firmware %s: section %d runs past the end of the file", firmwareName, i)
		}
		words := make([]uint32, section.size/4)
		for w := range words {
			words[w] = binary.BigEndian.Uint32(data[int(section.offset)+w*4:])
		}
		if len(words) == 0 || words[0]&0xf0000000 != 0x10000000 {
			return fmt.Errorf("AVL6882 firmware %s: section %d is not a valid patch image", firmwareName, i)
		}
		demod.images[i] = words
	}
	return nil
}

const (
	waitRetries     = 10
	waitInterval    = 20 * time.Millisecond
	bootWaitRetries = 100
)

// waitDemod polls the command mailbox until it clears: the previous command
// finished.
func (demod *Demod) waitDemod() error {
	for range waitRetries {
		command, err := demod.read16(0x200 + rcFWCommand)
		if err == nil && command == 0 {
			return nil
		}
		time.Sleep(waitInterval)
	}
	return errors.New("the AVL6882 firmware command mailbox never cleared")
}

func (demod *Demod) execute(command uint32) error {
	if err := demod.waitDemod(); err != nil {
		return err
	}
	if err := demod.write16(0x200+rcFWCommand, command); err != nil {
		return err
	}
	return demod.waitDemod()
}

// waitDemodBoot waits for the image to release the CPU and post its ready word.
func (demod *Demod) waitDemodBoot() error {
	for range bootWaitRetries {
		status, err := demod.read32(0x110840)
		if err == nil && status == 0 {
			ready, err := demod.read32(rsCoreReadyWord)
			if err == nil && ready == coreReadyWord {
				return nil
			}
		}
		time.Sleep(waitInterval)
	}
	return errors.New("the AVL6882 firmware never finished booting")
}

// setupPLL programs the three clock PLLs, once per firmware upload.
func (demod *Demod) setupPLL() error {
	for _, step := range [][2]uint32{
		// sys_pll
		{regPLLSysDivR, 2}, {regPLLSysDivF, 99}, {regPLLSysDivQ, 7},
		{regPLLSysRange, 1}, {regPLLSysDivQ2, 11}, {regPLLSysDivQ3, 13},
		{regPLLSysEnable2, 0}, {regPLLSysEnable3, 0},
		// mpeg_pll
		{regPLLMpegDivR, 0}, {regPLLMpegDivF, 35}, {regPLLMpegDivQ, 7},
		{regPLLMpegRange, 3}, {regPLLMpegDivQ2, 11}, {regPLLMpegDivQ3, 13},
		{regPLLMpegEnable2, 0}, {regPLLMpegEnable3, 0},
		// adc_pll
		{regPLLADCDivR, 2}, {regPLLADCDivF, 99}, {regPLLADCDivQ, 7},
		{regPLLADCRange, 1}, {regPLLADCDivQ2, 11}, {regPLLADCDivQ3, 13},
		{regPLLADCEnable2, 1}, {regPLLADCEnable3, 1},

		{regPLLReset, 0}, {regPLLReset, 1},
	} {
		if err := demod.write32(step[0], step[1]); err != nil {
			return err
		}
	}
	time.Sleep(20 * time.Millisecond)
	for _, step := range [][2]uint32{
		{regPLLDLLOutPhase, 96}, {regPLLDLLReadPhase, 0},
		{regPLLDeglitchMode, 1}, {regPLLDLLInit, 1}, {regPLLDLLInit, 0},
	} {
		if err := demod.write32(step[0], step[1]); err != nil {
			return err
		}
	}
	return nil
}

func (demod *Demod) uploadFirmware(system System) error {
	image := demod.images[system.firmwareIndex()]
	if len(image) == 0 {
		return fmt.Errorf("no AVL6882 %s firmware image was loaded", system)
	}
	if image[0]&0xff != 1 {
		return fmt.Errorf("AVL6882 %s patch version %d is unsupported (only 1)", system, image[0]&0xff)
	}
	if err := demod.write32(0x110010, 1); err != nil {
		return err
	}
	if err := demod.setupPLL(); err != nil {
		return err
	}
	if err := demod.write32(0x0a4+rsCoreReadyWord, 0); err != nil {
		return err
	}
	if err := demod.write32(0x110010, 0); err != nil {
		return err
	}
	if err := demod.runPatch(image); err != nil {
		return err
	}
	return demod.waitDemodBoot()
}

// Sleep parks the chip: nothing to do for this part, kept for symmetry with the
// other demodulator packages.
func (demod *Demod) Sleep() error { return nil }
