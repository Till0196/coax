// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"bytes"
	"compress/zlib"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
)

// InstallShield stores its payload after the last PE section of the setup
// launcher, as a header naming the file count followed by one fixed size
// attribute record per file.  Each record is followed by the file itself.
const (
	installShieldSignature  = "InstallShield\x00"
	installShieldHeaderSize = 46
	installShieldRecordSize = 0x138
	installShieldNameSize   = 260 // _MAX_PATH
	installShieldFlagsAt    = 0x104
	installShieldLengthAt   = 0x10C
	installShieldDeflatedAt = 0x118
	installShieldObfuscated = 0x4
)

// installShieldKeyMagic is XORed into the file name to make the stream key.
var installShieldKeyMagic = [4]byte{0x13, 0x35, 0x86, 0x07}

// installShieldKeyPeriod is how often the key position restarts.  Missing this
// leaves everything past the first kilobyte of a file corrupt.
const installShieldKeyPeriod = 1024

// IsInstallShieldSetup reports whether data is a PE carrying an InstallShield
// payload.
func IsInstallShieldSetup(data []byte) bool {
	overlay, err := peOverlay(data)
	return err == nil && bytes.Contains(overlay, []byte(installShieldSignature))
}

// ExtractInstallShield returns the files packed into an InstallShield setup
// launcher.
//
// The payload is obfuscated rather than encrypted: the key is the file name,
// which the record stores in clear immediately before the data it protects.
// This is the container format InstallShield has used since DevStudio 9, not a
// protection measure, and general purpose extractors implement the same
// transform.
//
// What is deobfuscated or inflated is charged to budget before it is kept.
func ExtractInstallShield(data []byte, budget *expansionBudget) ([]Member, error) {
	overlay, err := peOverlay(data)
	if err != nil {
		return nil, err
	}
	start := bytes.Index(overlay, []byte(installShieldSignature))
	if start < 0 {
		return nil, errors.New("no InstallShield payload in this executable")
	}
	if start+installShieldHeaderSize > len(overlay) {
		return nil, errors.New("the InstallShield header runs past the end of the payload")
	}
	count := int(binary.LittleEndian.Uint16(overlay[start+len(installShieldSignature):]))
	offset := start + installShieldHeaderSize
	members := make([]Member, 0, count)
	for i := 0; i < count; i++ {
		if offset+installShieldRecordSize > len(overlay) {
			return nil, fmt.Errorf("InstallShield record %d runs past the end of the payload", i)
		}
		record := overlay[offset:]
		name := string(nullTerminated(record[:installShieldNameSize]))
		flags := binary.LittleEndian.Uint32(record[installShieldFlagsAt:])
		declared := int64(binary.LittleEndian.Uint32(record[installShieldLengthAt:]))
		deflated := binary.LittleEndian.Uint16(record[installShieldDeflatedAt:]) != 0
		if int64(offset+installShieldRecordSize)+declared > int64(len(overlay)) {
			return nil, fmt.Errorf("InstallShield file %q runs past the end of the payload", name)
		}
		length := int(declared)
		content := overlay[offset+installShieldRecordSize : offset+installShieldRecordSize+length]
		if flags&installShieldObfuscated != 0 {
			if err := budget.take(len(content)); err != nil {
				return nil, err
			}
			content = deobfuscateInstallShield(content, name)
		}
		if deflated {
			if content, err = inflate(content, budget); err != nil {
				return nil, fmt.Errorf("InstallShield file %q: %w", name, err)
			}
		}
		members = append(members, Member{Name: name, Data: content})
		offset += installShieldRecordSize + length
	}
	return members, nil
}

// deobfuscateInstallShield reverses the per byte transform: swap the nibbles,
// XOR with the key, invert.  The key position restarts every kilobyte.
func deobfuscateInstallShield(content []byte, name string) []byte {
	key := []byte(name)
	if len(key) == 0 {
		return content
	}
	for i := range key {
		key[i] ^= installShieldKeyMagic[i%len(installShieldKeyMagic)]
	}
	plain := make([]byte, len(content))
	for i, b := range content {
		plain[i] = ^(key[i%installShieldKeyPeriod%len(key)] ^ (b<<4 | b>>4))
	}
	return plain
}

// peOverlay returns the bytes following the last section of a PE file.
func peOverlay(data []byte) ([]byte, error) {
	file, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a PE file: %w", err)
	}
	defer file.Close()
	end := 0
	for _, section := range file.Sections {
		if section.Size == 0 {
			continue
		}
		if stop := int(section.Offset) + int(section.Size); stop > end {
			end = stop
		}
	}
	if end >= len(data) {
		return nil, errors.New("this executable has no overlay")
	}
	return data[end:], nil
}

func inflate(data []byte, budget *expansionBudget) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return budget.read(reader, nil)
}

func nullTerminated(field []byte) []byte {
	if end := bytes.IndexByte(field, 0); end >= 0 {
		return field[:end]
	}
	return field
}
