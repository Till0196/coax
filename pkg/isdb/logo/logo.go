// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package logo reads station logos: the pictures a receiver shows beside a
// service's name.
//
// They come three ways. Terrestrial broadcasting sends them in the CDT, the
// advanced services in the MH-CDT, and satellite broadcasting, along with cable
// retransmission, as common data for all receivers (ARIB TR-B15, JCTEA
// STD-007): a software download announced by the SDTT for maker 0xFF, carried
// in a DSM-CC data carousel whose modules are named LOGO-00 .. LOGO-05 (BS),
// CS_LOGO-00 .. (CS) or, on cable, CATV_LOGO and the like. This package reads
// such a module (ParseModule), the data module of a CDT (ParseCommonData), and
// knows which download and which module names to look at.
//
// Whichever way it came, the PNG has no palette: it is an 8-bit indexed
// picture whose colours are the default CLUT of ARIB STD-B24 (Volume 2 Table
// 5-7), which every receiver already holds. A PNG decoder wants the palette in
// the file, so WithPalette puts it there.
package logo

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

// CommonMaker is the maker identifier of the common data download.
const CommonMaker = 0xff

// IsCommonDataModel reports whether an SDTT for this model, under maker 0xFF,
// announces the common data that logos are part of. 0xFE and 0xFD are BS, 0xFC
// and 0xFB are CS, 0xFA .. 0xF7 terrestrial, which is what a satellite receiver
// subscribes to; 0xF1 is the cable remultiplexing download that a cable
// set-top box takes its logos from.
func IsCommonDataModel(model byte) bool {
	return model == 0xf1 || (model >= 0xf7 && model <= 0xfe)
}

// IsLogoModule reports whether a module's moduleInfoBytes name it as a logo
// module: a name descriptor (tag 0x02) whose name has LOGO in it. That is
// LOGO-0n, CS_LOGO-0n, CATV_LOGO, JCHITS_LOGO; a cable receiver goes by the
// substring rather than by a list, and so does this.
func IsLogoModule(info []byte) bool {
	for at := 0; at+2 <= len(info); {
		tag := info[at]
		length := int(info[at+1])
		if at+2+length > len(info) {
			return false
		}
		if tag == 0x02 && bytes.Contains(info[at+2:at+2+length], []byte("LOGO")) {
			return true
		}
		at += 2 + length
	}
	return false
}

// Service names one service a logo belongs to.
type Service struct {
	OriginalNetworkID uint16
	TransportStreamID uint16
	ServiceID         uint16
}

// Entry is one logo in a logo module, and the services it belongs to.
type Entry struct {
	LogoID   uint16
	Services []Service
	// PNG is the picture, without a palette.
	PNG []byte
}

// Module is a logo module: every logo of one type.
type Module struct {
	// LogoType is 0 .. 5 (ARIB TR-B14: the sizes), numbered as the CDT
	// numbers them.
	LogoType byte
	Entries  []Entry
}

// ParseModule reads a logo module of the common data download: logo_type (8),
// number_of_loop (16), then per logo reserved (7), logo_id (9),
// number_of_services (8), the services at six bytes each, data_size (16) and
// the PNG. ok is false where the module stops short.
func ParseModule(data []byte) (Module, bool) {
	if len(data) < 3 {
		return Module{}, false
	}
	module := Module{LogoType: data[0]}
	count := int(binary.BigEndian.Uint16(data[1:]))
	module.Entries = make([]Entry, 0, min(count, 64))
	at := 3
	for range count {
		if at+3 > len(data) {
			return Module{}, false
		}
		entry := Entry{LogoID: binary.BigEndian.Uint16(data[at:]) & 0x01ff}
		services := int(data[at+2])
		at += 3
		if at+6*services+2 > len(data) {
			return Module{}, false
		}
		for range services {
			entry.Services = append(entry.Services, Service{
				OriginalNetworkID: binary.BigEndian.Uint16(data[at:]),
				TransportStreamID: binary.BigEndian.Uint16(data[at+2:]),
				ServiceID:         binary.BigEndian.Uint16(data[at+4:]),
			})
			at += 6
		}
		size := int(binary.BigEndian.Uint16(data[at:]))
		at += 2
		if at+size > len(data) {
			return Module{}, false
		}
		entry.PNG = data[at : at+size]
		at += size
		module.Entries = append(module.Entries, entry)
	}
	return module, true
}

// CommonData is the logo a CDT or MH-CDT data module holds: STD-B21 and
// STD-B60 6.7.1 lay it out as logo_type, logo_id, logo_version, data_size
// and the PNG.
type CommonData struct {
	LogoType    byte
	LogoID      uint16
	LogoVersion uint16
	PNG         []byte
}

// ParseCommonData reads the data module of a CDT (or MH-CDT) whose
// data_type is a logo.
func ParseCommonData(data []byte) (CommonData, bool) {
	if len(data) < 7 {
		return CommonData{}, false
	}
	size := int(binary.BigEndian.Uint16(data[5:]))
	if 7+size > len(data) {
		return CommonData{}, false
	}
	return CommonData{
		LogoType:    data[0],
		LogoID:      binary.BigEndian.Uint16(data[1:]) & 0x01ff,
		LogoVersion: binary.BigEndian.Uint16(data[3:]) & 0x0fff,
		PNG:         data[7 : 7+size],
	}, true
}

// DefaultCLUT is the default CLUT of ARIB STD-B24 Volume 2 Table 5-7: 128
// colours as R, G, B, A. Index 8 is transparent; 65 .. 127 are 0 .. 7 and
// 9 .. 63 again at half opacity.
var DefaultCLUT = func() [128][4]byte {
	opaque := [65][3]byte{
		{0, 0, 0}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
		{0, 0, 0}, {170, 0, 0}, {0, 170, 0}, {170, 170, 0},
		{0, 0, 170}, {170, 0, 170}, {0, 170, 170}, {170, 170, 170},
		{0, 0, 85}, {0, 85, 0}, {0, 85, 85}, {0, 85, 170},
		{0, 85, 255}, {0, 170, 85}, {0, 170, 255}, {0, 255, 85},
		{0, 255, 170}, {85, 0, 0}, {85, 0, 85}, {85, 0, 170},
		{85, 0, 255}, {85, 85, 0}, {85, 85, 85}, {85, 85, 170},
		{85, 85, 255}, {85, 170, 0}, {85, 170, 85}, {85, 170, 170},
		{85, 170, 255}, {85, 255, 0}, {85, 255, 85}, {85, 255, 170},
		{85, 255, 255}, {170, 0, 85}, {170, 0, 255}, {170, 85, 0},
		{170, 85, 85}, {170, 85, 170}, {170, 85, 255}, {170, 170, 85},
		{170, 170, 255}, {170, 255, 0}, {170, 255, 85}, {170, 255, 170},
		{170, 255, 255}, {255, 0, 85}, {255, 0, 170}, {255, 85, 0},
		{255, 85, 85}, {255, 85, 170}, {255, 85, 255}, {255, 170, 0},
		{255, 170, 85}, {255, 170, 170}, {255, 170, 255}, {255, 255, 85},
		{255, 255, 170},
	}
	var table [128][4]byte
	for i := range 65 {
		alpha := byte(255)
		if i == 8 {
			alpha = 0
		}
		table[i] = [4]byte{opaque[i][0], opaque[i][1], opaque[i][2], alpha}
	}
	// 65 .. 72 are 0 .. 7; 73 .. 127 are 9 .. 63.
	for i := 65; i < 128; i++ {
		from := i - 64
		if i < 73 {
			from = i - 65
		}
		table[i] = [4]byte{opaque[from][0], opaque[from][1], opaque[from][2], 128}
	}
	return table
}()

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// WithPalette puts the default CLUT into an indexed PNG that came without one:
// a PLTE of 256 entries and a tRNS of 256, right after the IHDR. A PNG that
// already has a palette, or is not indexed, comes back unchanged.
func WithPalette(png []byte) []byte {
	// The signature, then IHDR: length 13, "IHDR", 13 bytes, CRC.
	if len(png) < 33 || !bytes.Equal(png[:8], pngSignature) || string(png[12:16]) != "IHDR" {
		return png
	}
	if colourType := png[25]; colourType != 3 || hasChunk(png[33:], "PLTE") {
		return png
	}
	plte := make([]byte, 0, 768)
	trns := make([]byte, 0, 256)
	for i := range 256 {
		var colour [4]byte
		if i < len(DefaultCLUT) {
			colour = DefaultCLUT[i]
		}
		plte = append(plte, colour[0], colour[1], colour[2])
		trns = append(trns, colour[3])
	}
	out := make([]byte, 0, len(png)+12+768+12+256)
	out = append(out, png[:33]...)
	out = appendChunk(out, "PLTE", plte)
	out = appendChunk(out, "tRNS", trns)
	return append(out, png[33:]...)
}

func hasChunk(rest []byte, name string) bool {
	for len(rest) >= 12 {
		length := uint64(binary.BigEndian.Uint32(rest))
		kind := string(rest[4:8])
		if kind == name {
			return true
		}
		if kind == "IDAT" || kind == "IEND" {
			return false
		}
		if length+12 > uint64(len(rest)) {
			return false
		}
		rest = rest[length+12:]
	}
	return false
}

// appendChunk adds a PNG chunk; its CRC is the one of ISO 3309, which is
// crc32.IEEE.
func appendChunk(out []byte, name string, data []byte) []byte {
	out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
	start := len(out)
	out = append(out, name...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[start:]))
}
