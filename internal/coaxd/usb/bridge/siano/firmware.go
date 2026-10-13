// Copyright (c) 2005-2009 Siano Mobile Silicon, Uri Shkolnik, Anatoly Greenblat;
// 2008 Michael Krufky; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from the Linux siano driver (smscoreapi.c).

package siano

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// Modes the firmware can be initialised into. A chip serves one at a time, and
// switching between them means downloading a different image.
const (
	ModeNone     = -1
	ModeDVBT     = 0
	ModeDVBH     = 1
	ModeDABTDMB  = 2
	ModeDVBTBDA  = 4
	ModeISDBT    = 5
	ModeISDBTBDA = 6
)

// requestTimeout is how long the chip is given to answer. Firmware download runs
// thousands of requests, so it is kept short enough to fail fast.
const requestTimeout = 5 * time.Second

// Version is what the chip reports about the image it is running.
type Version struct {
	ChipModel uint16
	// FirmwareID is the mode the running image serves, or 255 while the chip
	// is still on its ROM and has no downloaded image at all.
	FirmwareID         byte
	SupportedProtocols byte
	// ROMVersion is the ROM's major and minor numbers packed as major<<8|minor.
	ROMVersion uint16
}

// Mode reports the mode the running image serves, or ModeNone when the chip is
// still running its ROM.
func (version Version) Mode() int {
	if version.FirmwareID == 255 {
		return ModeNone
	}
	return int(version.FirmwareID)
}

// Supports reports whether the running image already serves a mode, in which
// case no download is needed.
func (version Version) Supports(mode int) bool {
	if mode < 0 || mode > 7 {
		return false
	}
	return version.SupportedProtocols&(1<<mode) != 0
}

// Version asks the chip what it is running.
func (bridge *Device) Version() (Version, error) {
	body, err := bridge.Exchange(MsgGetVersionExReq, MsgGetVersionExRes, 0, nil, requestTimeout)
	if err != nil {
		return Version{}, err
	}
	if len(body) < 12 {
		return Version{}, fmt.Errorf("short Siano version response: %d bytes", len(body))
	}
	return Version{
		ChipModel:          binary.LittleEndian.Uint16(body),
		FirmwareID:         body[4],
		SupportedProtocols: body[5],
		ROMVersion:         uint16(body[10])<<8 | uint16(body[11]),
	}, nil
}

// Firmware is a decoded .inp image: a header naming the payload's size and where
// in the chip's memory it belongs, then the payload.
type Firmware struct {
	Length       uint32
	StartAddress uint32
	Payload      []byte
}

// ParseFirmware decodes an .inp image.
func ParseFirmware(data []byte) (Firmware, error) {
	const headerSize = 12
	if len(data) < headerSize {
		return Firmware{}, fmt.Errorf("firmware image is %d bytes, too short to hold a header", len(data))
	}
	// The leading word is a checksum the chip is not asked to verify; the
	// validity request in download uses the chip's own instead.
	length := binary.LittleEndian.Uint32(data[4:])
	payload := data[headerSize:]
	if int(length) > len(payload) {
		return Firmware{}, fmt.Errorf("firmware header claims %d bytes but only %d follow", length, len(payload))
	}
	return Firmware{
		Length:       length,
		StartAddress: binary.LittleEndian.Uint32(data[8:]),
		Payload:      payload,
	}, nil
}

// download writes an image into the chip's memory and starts it. warm takes the
// path for a chip already running a downloaded image, replaced in place rather
// than started from cold.
func (bridge *Device) download(image Firmware, warm bool) error {
	address := image.StartAddress
	if warm {
		if _, err := bridge.Exchange(MsgSWReloadStartReq, MsgSWReloadStartRes, 0, nil, requestTimeout); err != nil {
			return fmt.Errorf("start firmware reload: %w", err)
		}
		// A replacement image names its own load address inside the payload.
		if len(image.Payload) < 24 {
			return errors.New("firmware image is too short to carry a reload address")
		}
		address = binary.LittleEndian.Uint32(image.Payload[20:])
	}

	for offset := 0; offset < len(image.Payload); {
		size := min(maxPayload, len(image.Payload)-offset)
		payload := make([]byte, 4+size)
		binary.LittleEndian.PutUint32(payload, address)
		copy(payload[4:], image.Payload[offset:offset+size])
		if _, err := bridge.Exchange(MsgDataDownloadReq, MsgDataDownloadRes, 0, payload, requestTimeout); err != nil {
			return fmt.Errorf("download firmware at %#x: %w", address, err)
		}
		offset += size
		address += uint32(size)
	}

	if _, err := bridge.Exchange(MsgDataValidityReq, MsgDataValidityRes, 0,
		Words(image.StartAddress, image.Length, 0), requestTimeout); err != nil {
		return fmt.Errorf("verify downloaded firmware: %w", err)
	}

	if warm {
		// The reload path starts the replacement image itself and answers with
		// no response to wait for.
		return bridge.Send(MsgSWReloadExecReq, 0, nil)
	}
	// Priority, stack size, parameter and task ID the ROM starts the image with.
	if _, err := bridge.Exchange(MsgSWDownloadTriggerReq, MsgSWDownloadTriggerRes, 0,
		Words(image.StartAddress, 6, 0x200, 0, 4), requestTimeout); err != nil {
		return fmt.Errorf("start downloaded firmware: %w", err)
	}
	return nil
}

// SetMode brings the chip up in the requested mode, downloading the named
// firmware image first unless the running one already serves that mode.
// firmwareName is looked for where every other blob is (device.ReadFirmware):
// the configured directory, the system's places, and beside the program.
// It was read from the configured directory alone, which unconfigured is the
// relative "firmware" -- the working directory of whoever started the daemon,
// so a daemon ZeroWave started on macOS never found it.
func (bridge *Device) SetMode(mode int, options device.Options, firmwareName string) error {
	version, err := bridge.Version()
	if err != nil {
		return fmt.Errorf("read Siano firmware version: %w", err)
	}
	if version.Mode() == mode {
		return nil
	}
	if !version.Supports(mode) {
		path := firmwareName
		data, err := device.ReadFirmware(options.FirmwareDirOrDefault(), firmwareName)
		if err != nil {
			return fmt.Errorf("read required Siano firmware: %w", err)
		}
		image, err := ParseFirmware(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := bridge.download(image, version.Mode() != ModeNone); err != nil {
			return err
		}
		// The image needs a moment to come up before it answers again.
		time.Sleep(400 * time.Millisecond)

		// A ROM new enough to report a version takes an initialisation before
		// the one below.
		if version.ROMVersion >= 0x800 {
			if _, err := bridge.Exchange(MsgInitDeviceReq, MsgInitDeviceRes, 0,
				Words(uint32(mode)), requestTimeout); err != nil {
				return fmt.Errorf("initialize the downloaded firmware: %w", err)
			}
		}
	}
	if _, err := bridge.Exchange(MsgInitDeviceReq, MsgInitDeviceRes, 0,
		Words(uint32(mode)), requestTimeout); err != nil {
		return fmt.Errorf("set Siano bridge mode %d: %w", mode, err)
	}
	return nil
}
