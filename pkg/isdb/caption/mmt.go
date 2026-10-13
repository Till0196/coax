// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package caption

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"unicode/utf8"
)

// MMT captions and superimposed text (ARIB STD-B60 chapter 9) arrive as TTML
// documents in the MPUs of an stpp asset.
//
//   - The additional information of the asset's MH data coding descriptor
//     (0x8020) says the language, the timing mode and the resolution (table
//     9-3).
//   - An MPU is a run of MFUs: the first is the TTML document, the rest the
//     resources it refers to (fonts, pictures, sounds). The MFU header is
//     table 9-1.

const (
	// DescriptorDataCoding is the MH data coding descriptor; a caption's
	// data_component_id is DataComponentCaption.
	DescriptorDataCoding  = 0x8020
	DataComponentCaption  = 0x0020
	mostTTML              = 4 * 1024 * 1024
	subtitleTimeReference = 0b0010
)

// The MFU data_type values (table 9-2).
const (
	DataTTML     = 0b0000
	DataPNG      = 0b0001
	DataSVG      = 0b0010
	DataAIFF     = 0b0011
	DataMP3      = 0b0100
	DataAAC      = 0b0101
	DataFontSVG  = 0b0110
	DataFontWOFF = 0b0111
)

// SubtitleInfo is the additional ARIB subtitle information (table 9-3).
type SubtitleInfo struct {
	Tag     byte
	Version byte
	// Language is the ISO 639-2 code.
	Language string
	// Kind is table 9-4: 0 caption, 1 superimposed text.
	Kind   byte
	Format byte
	OPM    byte
	// TMD is the time control mode (table 9-7). 0b0010 reads the TTML times
	// from ReferenceStart; 0b1111 has no timing.
	TMD byte
	// DMF is the display mode (table 9-8).
	DMF byte
	// Resolution is table 9-9: 0 1920x1080, 1 3840x2160, 2 7680x4320.
	Resolution byte
	// Compression is table 9-10: 0 none, 1 zlib. Only the TTML document is
	// compressed.
	Compression         byte
	StartMPUSequence    uint32
	HasStartMPUSequence bool
	// ReferenceStart is the base time (a 64-bit NTP timestamp), where TMD is
	// 0b0010.
	ReferenceStart    uint64
	HasReferenceStart bool
}

// ParseSubtitleInfo reads a caption's additional information out of the
// body of an MH data coding descriptor; false where the data_component_id is
// not a caption's.
func ParseSubtitleInfo(body []byte) (SubtitleInfo, bool) {
	if len(body) < 2 || binary.BigEndian.Uint16(body) != DataComponentCaption {
		return SubtitleInfo{}, false
	}
	return ParseAdditionalSubtitleInfo(body[2:])
}

// ParseAdditionalSubtitleInfo reads additional_data_component_info itself
// (table 9-3).
func ParseAdditionalSubtitleInfo(info []byte) (SubtitleInfo, bool) {
	if len(info) < 8 {
		return SubtitleInfo{}, false
	}
	flags := info[1]
	s := SubtitleInfo{
		Tag:         info[0],
		Version:     flags >> 4,
		Language:    lossyString(info[2:5]),
		Kind:        info[5] >> 6,
		Format:      (info[5] >> 2) & 0x0f,
		OPM:         info[5] & 0x03,
		TMD:         info[6] >> 4,
		DMF:         info[6] & 0x0f,
		Resolution:  info[7] >> 4,
		Compression: info[7] & 0x0f,
	}
	at := 8
	if flags&0x08 != 0 {
		if at+4 > len(info) {
			return SubtitleInfo{}, false
		}
		s.StartMPUSequence, s.HasStartMPUSequence = binary.BigEndian.Uint32(info[at:]), true
		at += 4
	}
	if s.TMD == subtitleTimeReference {
		if at+8 > len(info) {
			return SubtitleInfo{}, false
		}
		s.ReferenceStart, s.HasReferenceStart = binary.BigEndian.Uint64(info[at:]), true
	}
	return s, true
}

func lossyString(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return string([]rune(string(b)))
}

// MFU is a caption MFU's header (table 9-1) and body.
type MFU struct {
	Tag           byte
	Sequence      byte
	Subsample     byte
	LastSubsample byte
	// DataType is one of the Data constants.
	DataType byte
	// Size is the body size the header says, which may differ from what
	// arrived.
	Size uint32
	Data []byte
}

// ParseMFU reads a caption MFU.
func ParseMFU(b []byte) (MFU, bool) {
	if len(b) < 5 {
		return MFU{}, false
	}
	m := MFU{Tag: b[0], Sequence: b[1], Subsample: b[2], LastSubsample: b[3], DataType: b[4] >> 4}
	long := b[4]&0x08 != 0
	listed := b[4]&0x04 != 0
	at := 5
	if long {
		if at+4 > len(b) {
			return MFU{}, false
		}
		m.Size = binary.BigEndian.Uint32(b[at:])
		at += 4
	} else {
		if at+2 > len(b) {
			return MFU{}, false
		}
		m.Size = uint32(binary.BigEndian.Uint16(b[at:]))
		at += 2
	}
	// The first MFU may list the kinds and sizes of the ones that follow.
	if m.Subsample == 0 && m.LastSubsample > 0 && listed {
		each := 3
		if long {
			each = 5
		}
		at += int(m.LastSubsample) * each
	}
	if at > len(b) {
		return MFU{}, false
	}
	m.Data = b[at:]
	return m, true
}

// TTMLText is the TTML document of an MFU, inflated where the additional
// information says it is compressed (Compression 1, zlib); false where the MFU
// is not a document (a picture or a sound) or does not inflate.
func TTMLText(m MFU, compressed bool) (string, bool) {
	if m.DataType != DataTTML {
		return "", false
	}
	body := m.Data
	if compressed {
		// A caption document is tens of kilobytes at most; a few bytes that
		// swell to gigabytes are not a document.
		r, err := zlib.NewReader(bytes.NewReader(m.Data))
		if err != nil {
			return "", false
		}
		defer r.Close()
		inflated, err := io.ReadAll(io.LimitReader(r, mostTTML+1))
		if err != nil || len(inflated) > mostTTML {
			return "", false
		}
		body = inflated
	}
	if !utf8.Valid(body) {
		return "", false
	}
	return string(body), true
}
