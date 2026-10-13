// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/win32"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// Standards is what one Digital Devices tuner can receive and what it is
// currently offering a filter for.
type Standards struct {
	// Tuner is which tuner of the card this is, as the driver numbers it.
	Tuner int
	// SupportedMask is what the hardware can do; EnabledMask is the subset the
	// driver built filters for. Both are one bit per standard, in this vendor's
	// own numbering.
	SupportedMask uint32
	EnabledMask   uint32
	// Supported and Enabled are those two, named.
	Supported []string
	Enabled   []string
}

// ReadStandards asks a Digital Devices tuner what it can receive. The hardware
// reports more than the card shows by default: a MAX M4 answers 0x0001073F
// (DVB-T, DVB-T2, DVB-C, DVB-C2, DVB-S, DVB-S2, ISDB-T, ISDB-C, ISDB-S, J83-B)
// while offering filters for only 0x36 until the mask is widened.
func ReadStandards(properties vendors.Properties) (Standards, error) {
	var standards Standards
	descriptor := make([]byte, ddDescriptorBytes)
	if _, err := properties.Get(Set, ddPropDescriptor, descriptor); err != nil {
		return Standards{}, fmt.Errorf("read the Digital Devices tuner descriptor: %w", err)
	}
	standards.SupportedMask = binary.LittleEndian.Uint32(descriptor[ddSupportedOffset:])

	mask, err := readWord(properties, Set, ddPropFilterMask)
	if err != nil {
		return Standards{}, err
	}
	standards.EnabledMask = mask

	number, err := readWord(properties, Set, ddPropTunerNumber)
	if err != nil {
		return Standards{}, err
	}
	standards.Tuner = int(number & 0xffff)

	standards.Supported = ModeNames(standards.SupportedMask)
	standards.Enabled = ModeNames(standards.EnabledMask)
	return standards, nil
}

// WriteFilterMask sets which standards a Digital Devices tuner offers and
// reports what the driver kept. It does not take effect on its own: the driver
// saves the mask and builds the filters at its next start, so the device must be
// restarted. Unsupported bits are dropped rather than refused, which is why the
// readback matters.
func WriteFilterMask(properties vendors.Properties, mask uint32) (uint32, error) {
	written := make([]byte, 4)
	binary.LittleEndian.PutUint32(written, mask)
	if err := properties.Set(Set, ddPropFilterMask, written); err != nil {
		return 0, fmt.Errorf("write the Digital Devices filter mask: %w", err)
	}
	return readWord(properties, Set, ddPropFilterMask)
}

// ModeNames names every standard a mask has a bit for.
func ModeNames(mask uint32) []string {
	var names []string
	for bit := 0; bit < 32; bit++ {
		if mask&(1<<bit) == 0 {
			continue
		}
		names = append(names, ModeName(DemodMode(bit)))
	}
	return names
}

// StandardOf reads the standard out of a filter's name, where the driver puts
// it: "Digital Devices ISDB-T Tuner 2". Names are tried longest first because
// one contains another (ISDB-S3 contains ISDB-S, DVB-T2 contains DVB-T), and
// matching the short one first would take a filter for the wrong standard.
func StandardOf(tuner Tuner) (DemodMode, bool) {
	name := strings.ToUpper(tuner.Name)
	for _, candidate := range []struct {
		text string
		mode DemodMode
	}{
		{"DVB-S2X", ModeDVBS2X},
		{"ISDB-S3", ModeISDBS3},
		{"ISDB-T", ModeISDBT},
		{"ISDB-C", ModeISDBC},
		{"ISDB-S", ModeISDBS},
		{"DVB-T2", ModeDVBT2},
		{"DVB-C2", ModeDVBC2},
		{"DVB-S2", ModeDVBS2},
		{"DVB-T", ModeDVBT},
		{"DVB-C", ModeDVBC},
		{"DVB-S", ModeDVBS},
		{"J83-B", ModeJ83B},
		{"J83B", ModeJ83B},
		{"ATSC3", ModeATSC3},
		{"ATSC", ModeATSC},
	} {
		if strings.Contains(name, candidate.text) {
			return candidate.mode, true
		}
	}
	return ModeUnknown, false
}

// readWord reads a four-byte item.
func readWord(properties vendors.Properties, set win32.GUID, id uint32) (uint32, error) {
	value := make([]byte, 4)
	if _, err := properties.Get(set, id, value); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(value), nil
}
