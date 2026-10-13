// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Firmware handling for the ASV52xx bridge.
//
// A cold board runs a boot ROM that takes a 0x4000-byte image over four vendor
// control transfers and then jumps to an address the host names. The right
// image at the wrong jump address wedges the board, and the address belongs to
// the firmware build rather than the board model, so images are identified by
// digest.
package asv5211

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

const (
	// FirmwareFile is where an image is looked for when none is named. It is
	// named for the bridge, not a board: every build serves the whole
	// PX-S3U/S3U2/W3U2/W3U3 family.
	FirmwareFile = "asv5211-firmware.bin"

	// The jump addresses of the four builds knownFirmware lists. They belong to
	// firmware revisions rather than board models; which one an operator has
	// depends only on the driver package it came from. LinuxFirmwareIndex is
	// PLEX's Linux loader.ko.
	LinuxFirmwareIndex = 0x5399
	// UnusedFirmwareIndex belongs to an image every PLEX Windows loader carries
	// but none of them uploads; it is listed so it can be named rather than
	// having its address asked for.
	UnusedFirmwareIndex = 0x515f
	// BDAFirmwareIndex is the image PLEX's Windows BDA loader uploads.
	BDAFirmwareIndex = 0x55d6
	// PBDAFirmwareIndex is the image PLEX's Windows PBDA loader uploads. It is
	// a sibling of the BDA build, not a different generation.
	PBDAFirmwareIndex = 0x55d8

	firmwareSize = 0x4000
	timeoutMS    = 1000
)

// knownFirmware maps the SHA-256 of an image to the address it starts at.
var knownFirmware = map[string]struct {
	index       uint16
	description string
}{
	"b45d510200a1690b3ca358d93de13f40e1d3567b663c17e773349ad96f597aa8": {
		LinuxFirmwareIndex, "the image in PLEX's Linux loader.ko",
	},
	"5db39facda140bdcb3a238dad10da5aac752a1ba3aaf17e2239f8d79a0ad6a2a": {
		BDAFirmwareIndex, "the image PLEX's Windows BDA loader uploads",
	},
	"f4848c8c091634897f9829e50d2ff8e5dc28792c6b20cf095d38d40379518c7a": {
		PBDAFirmwareIndex, "the image PLEX's Windows PBDA loader uploads",
	},
	"d134890df91427d405ad5619588e63e3220e21f972ede18a9701fec5686b4816": {
		UnusedFirmwareIndex, "an older image PLEX's Windows loaders carry but never upload",
	},
}

// firmwareHoles are the ranges the upload never transfers. Every image seen so
// far leaves them zeroed, which tells a firmware image apart from an arbitrary
// 0x4000-byte file before any of it reaches the hardware.
var firmwareHoles = [...]struct{ start, end int }{
	{0x0c00, 0x2000},
	{0x2400, 0x2800},
}

// Firmware is an image that has passed its structural check, together with the
// address it must be started at.
type Firmware struct {
	Image []byte
	Index uint16
	// Known reports whether Index was established rather than guessed. This
	// package sets it only from a recognised digest; fwextract may also set it
	// from the loader module an image came out of. When false, Index is zero and
	// the caller has to supply an address.
	Known bool
	// Description names a recognised image, and is empty otherwise.
	Description string
	// Source is where the image was read from, for diagnostics.
	Source string
}

// Digest is the SHA-256 of the image, as the operator would see it from
// sha256sum.
func (firmware Firmware) Digest() string {
	sum := sha256.Sum256(firmware.Image)
	return hex.EncodeToString(sum[:])
}

// ReadFirmware reads an ASV52xx image from a decoded 0x4000-byte file.
// Recognising the image is what supplies its jump address; an unrecognised one
// is still returned, with Known false, leaving the address to the caller. See
// docs/boards/px3.md.
func ReadFirmware(path string) (Firmware, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Firmware{}, fmt.Errorf("read ASV52xx firmware %s: %w", path, err)
	}
	return FirmwareFrom(content, path)
}

// FirmwareFrom is ReadFirmware over content already in memory. source names it
// in diagnostics.
func FirmwareFrom(content []byte, source string) (Firmware, error) {
	firmware := Firmware{Image: content, Source: source}
	if err := validateFirmware(firmware.Image, source); err != nil {
		return Firmware{}, err
	}
	if known, recognised := knownFirmware[firmware.Digest()]; recognised {
		// A recognised digest pins the address to this exact build.
		firmware.Index, firmware.Known, firmware.Description = known.index, true, known.description
	}
	return firmware, nil
}

// validateFirmware rejects a file that is not shaped like an ASV52xx image.
// Uploading the wrong 0x4000 bytes and jumping into them needs a replug to
// recover, so the cheap structural check is worth making.
func validateFirmware(image []byte, path string) error {
	if len(image) != firmwareSize {
		return fmt.Errorf("ASV52xx firmware %s is %#x bytes; want %#x", path, len(image), firmwareSize)
	}
	for _, hole := range firmwareHoles {
		for offset := hole.start; offset < hole.end; offset++ {
			if image[offset] != 0 {
				return fmt.Errorf("ASV52xx firmware %s has data at %#04x, inside the %#04x..%#04x gap the upload never transfers; it is unlikely to be a firmware image",
					path, offset, hole.start, hole.end)
			}
		}
	}
	return nil
}

type controlDevice interface {
	Control(requestType, request byte, value, index uint16, buffer []byte, timeoutMS uint32) (int, error)
}

type firmwareSegment struct {
	offset  uint16
	length  int
	request byte
}

var firmwareSegments = [...]firmwareSegment{
	{offset: 0x0000, length: 0x0c00, request: 0xab},
	{offset: 0x2000, length: 0x0400, request: 0xab},
	{offset: 0x2800, length: 0x1000, request: 0xab},
	{offset: 0x3800, length: 0x0800, request: 0xac},
}

// UploadFirmwareAt sends a decoded 0x4000-byte ASV52xx image with its firmware
// jump address. Zero-filled holes are deliberately not transferred. The final
// request starts the bridge and normally causes USB re-enumeration from the
// cold identifier to the board's own.
func UploadFirmwareAt(device controlDevice, firmware []byte, firmwareIndex uint16) error {
	if len(firmware) != firmwareSize {
		return fmt.Errorf("invalid ASV5211 firmware size %#x; want %#x", len(firmware), firmwareSize)
	}
	for _, segment := range firmwareSegments {
		start := int(segment.offset)
		data := firmware[start : start+segment.length]
		written, err := device.Control(0x40, segment.request, segment.offset, firmwareIndex, data, timeoutMS)
		if err != nil {
			return fmt.Errorf("ASV5211 firmware segment at %#04x: %w", segment.offset, err)
		}
		if written != len(data) {
			return fmt.Errorf("ASV5211 firmware segment at %#04x: wrote %#x bytes; want %#x", segment.offset, written, len(data))
		}
	}
	return nil
}
