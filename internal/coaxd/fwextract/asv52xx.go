// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"bytes"
	"debug/elf"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
)

// PLEX ships the ASV52xx image as an ELF object symbol inside its Linux loader
// module rather than as a file.  The module also names the address the image
// has to be started at, which is the only source for that address when the
// build is not one recognised by digest.
const (
	asv52xxImageSymbol = "FirmBin"
	asv52xxStartSymbol = "FirmwareStartAddr"
	asv52xxImageSize   = 0x4000
)

// ExtractASV52xxFirmware recovers an ASV52xx image from a Linux loader module,
// or passes through a file that already is one.  The returned Index is the
// address the image has to be started at, taken from the digest when the build
// is a recognised one and otherwise from the module.
func ExtractASV52xxFirmware(driver []byte) (asv5211.Firmware, error) {
	image, index, indexKnown := driver, uint16(0), false
	if len(driver) != asv52xxImageSize {
		var err error
		if image, index, indexKnown, err = asv52xxFromELF(driver); err != nil {
			return asv5211.Firmware{}, err
		}
	}
	firmware, err := asv5211.FirmwareFrom(image, "")
	if err != nil {
		return asv5211.Firmware{}, err
	}
	if !firmware.Known && indexKnown {
		// A recognised digest pins the address to this exact build, so the
		// module's symbol is only a fallback.
		firmware.Index, firmware.Known = index, true
	}
	return firmware, nil
}

// asv52xxFromELF reads the image, and where possible its start address, out of
// a Linux loader module.
func asv52xxFromELF(driver []byte) ([]byte, uint16, bool, error) {
	file, err := elf.NewFile(bytes.NewReader(driver))
	if err != nil {
		return nil, 0, false, fmt.Errorf("neither a %#x-byte image nor an ELF module: %w", asv52xxImageSize, err)
	}
	defer file.Close()
	symbols, err := file.Symbols()
	if err != nil {
		return nil, 0, false, fmt.Errorf("read ELF symbols: %w", err)
	}
	image, err := elfSymbol(file, symbols, asv52xxImageSymbol, asv52xxImageSize)
	if err != nil {
		return nil, 0, false, err
	}
	// The address is a convenience, not a requirement: an older module may
	// carry the image without exporting where it starts.
	if start, err := elfSymbol(file, symbols, asv52xxStartSymbol, 2); err == nil {
		return image, uint16(start[0]) | uint16(start[1])<<8, true, nil
	}
	return image, 0, false, nil
}

// elfSymbol returns the bytes of a global object symbol of an exact size.
func elfSymbol(file *elf.File, symbols []elf.Symbol, name string, size uint64) ([]byte, error) {
	for _, symbol := range symbols {
		if symbol.Name != name || symbol.Size != size || symbol.Section >= elf.SHN_LORESERVE {
			continue
		}
		if int(symbol.Section) >= len(file.Sections) {
			continue
		}
		section := file.Sections[symbol.Section]
		data, err := section.Data()
		if err != nil {
			return nil, fmt.Errorf("read section holding %s: %w", name, err)
		}
		start := symbol.Value - section.Addr
		if start > uint64(len(data)) || size > uint64(len(data))-start {
			return nil, fmt.Errorf("%s is outside its ELF section", name)
		}
		return append([]byte(nil), data[start:start+size]...), nil
	}
	return nil, fmt.Errorf("ELF module has no %#x-byte %s symbol", size, name)
}
