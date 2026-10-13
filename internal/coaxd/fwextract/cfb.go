// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Compound File Binary, the container an .msi is.  Only what is needed to read
// the streams back out is implemented.
const (
	cfbHeaderSize       = 512
	cfbDirectoryEntry   = 128
	cfbMaxRegularSector = 0xFFFFFFFA
	cfbEndOfChain       = 0xFFFFFFFE
	cfbStreamObject     = 2
	cfbRootObject       = 5
)

// cfbSignature is the magic every compound file starts with.
var cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

func IsCompoundFile(data []byte) bool {
	return len(data) >= cfbHeaderSize && bytes.HasPrefix(data, cfbSignature)
}

// ExtractCompoundFile returns the streams held in a compound file.
//
// Stream names inside an .msi are mangled with an MSI specific encoding, so the
// names returned here are the raw ones.  Callers that want a particular stream
// should recognise it by its content rather than by its name.
//
// Everything kept is charged to budget as it is read: a file can point any
// number of directory entries at the same long chain.
func ExtractCompoundFile(data []byte, budget *expansionBudget) ([]Member, error) {
	if !IsCompoundFile(data) {
		return nil, errors.New("not a compound file")
	}
	// The format has two sector sizes, 512 and 4096 bytes, and one mini sector
	// size. Anything else is not a compound file this reads.
	sectorShift := binary.LittleEndian.Uint16(data[0x1E:])
	miniSectorShift := binary.LittleEndian.Uint16(data[0x20:])
	if (sectorShift != 9 && sectorShift != 12) || miniSectorShift != 6 {
		return nil, fmt.Errorf("unsupported sector sizes 2^%d/2^%d", sectorShift, miniSectorShift)
	}
	sectorSize := 1 << sectorShift
	miniSectorSize := 1 << miniSectorShift
	miniCutoff := int64(binary.LittleEndian.Uint32(data[0x38:]))
	file := cfbFile{data: data, sectorSize: sectorSize, sectors: (len(data) + sectorSize - 1) / sectorSize}
	fat, err := file.readFAT()
	if err != nil {
		return nil, err
	}
	file.fat = fat
	directory, err := file.readChain(binary.LittleEndian.Uint32(data[0x30:]), -1)
	if err != nil {
		return nil, fmt.Errorf("directory: %w", err)
	}
	miniFATSectors, err := file.readChain(binary.LittleEndian.Uint32(data[0x3C:]), -1)
	if err != nil {
		return nil, fmt.Errorf("mini FAT: %w", err)
	}
	miniFAT := decodeUint32s(miniFATSectors)

	var miniStream []byte
	var members []Member
	for offset := 0; offset+cfbDirectoryEntry <= len(directory); offset += cfbDirectoryEntry {
		entry := directory[offset : offset+cfbDirectoryEntry]
		start := binary.LittleEndian.Uint32(entry[0x74:])
		declared := int64(binary.LittleEndian.Uint32(entry[0x78:]))
		if declared > int64(len(data)) {
			// No stream is longer than the file holding it.
			continue
		}
		size := int(declared)
		switch entry[0x42] {
		case cfbRootObject:
			// The root entry points at the stream the mini sectors live in.
			if miniStream, err = file.readChain(start, size); err != nil {
				return nil, fmt.Errorf("mini stream: %w", err)
			}
		case cfbStreamObject:
			var content []byte
			if declared < miniCutoff {
				content, err = readCFBMiniChain(miniStream, miniFAT, miniSectorSize, start, size)
			} else {
				content, err = file.readChain(start, size)
			}
			if err != nil {
				// One damaged or unusual stream must not hide the rest.
				continue
			}
			if err := budget.take(len(content)); err != nil {
				return nil, err
			}
			members = append(members, Member{Name: cfbEntryName(entry), Data: content})
		}
	}
	return members, nil
}

// cfbFile is a compound file being read: its bytes, its sector size, how many
// sectors the bytes can hold, and once read, its allocation table.
type cfbFile struct {
	data       []byte
	sectorSize int
	sectors    int
	fat        []uint32
}

// readFAT collects the file allocation table, following the DIFAT for the
// sectors that do not fit in the header. A file has no more FAT sectors, and
// no longer a DIFAT chain, than it has sectors.
func (f *cfbFile) readFAT() ([]uint32, error) {
	data, sectorSize := f.data, f.sectorSize
	sectors := make([]uint32, 0, 109)
	for i := 0; i < 109; i++ {
		sector := binary.LittleEndian.Uint32(data[0x4C+i*4:])
		if sector > cfbMaxRegularSector {
			break
		}
		sectors = append(sectors, sector)
	}
	perSector := sectorSize/4 - 1
	for next, steps := binary.LittleEndian.Uint32(data[0x44:]), 0; next <= cfbMaxRegularSector; steps++ {
		if steps > f.sectors {
			return nil, errors.New("DIFAT: sector chain loops")
		}
		block, err := f.sector(next)
		if err != nil {
			return nil, fmt.Errorf("DIFAT: %w", err)
		}
		for i := 0; i < perSector; i++ {
			if sector := binary.LittleEndian.Uint32(block[i*4:]); sector <= cfbMaxRegularSector {
				sectors = append(sectors, sector)
			}
		}
		if len(sectors) > f.sectors {
			return nil, errors.New("the FAT names more sectors than the file has")
		}
		next = binary.LittleEndian.Uint32(block[perSector*4:])
	}
	fat := make([]uint32, 0, len(sectors)*sectorSize/4)
	for _, sector := range sectors {
		block, err := f.sector(sector)
		if err != nil {
			return nil, fmt.Errorf("FAT: %w", err)
		}
		for i := 0; i < sectorSize/4; i++ {
			fat = append(fat, binary.LittleEndian.Uint32(block[i*4:]))
		}
	}
	if len(fat) == 0 {
		return nil, errors.New("compound file has no FAT")
	}
	return fat, nil
}

// readChain walks a sector chain.  A size below zero reads to the end of the
// chain, which is what the directory and FAT chains need. A chain longer than
// the file has sectors goes round in a loop.
func (f *cfbFile) readChain(start uint32, size int) ([]byte, error) {
	var chain []byte
	for sector, steps := start, 0; sector <= cfbMaxRegularSector; steps++ {
		if steps > f.sectors {
			return nil, errors.New("sector chain loops")
		}
		block, err := f.sector(sector)
		if err != nil {
			return nil, err
		}
		chain = append(chain, block...)
		if size >= 0 && len(chain) >= size {
			break
		}
		if int64(sector) >= int64(len(f.fat)) {
			return nil, fmt.Errorf("sector %d is outside the FAT", sector)
		}
		sector = f.fat[sector]
	}
	return truncateChain(chain, size)
}

func readCFBMiniChain(miniStream []byte, miniFAT []uint32, miniSectorSize int, start uint32, size int) ([]byte, error) {
	var chain []byte
	for sector, steps := start, 0; sector <= cfbMaxRegularSector; steps++ {
		offset := int64(sector) * int64(miniSectorSize)
		if steps*miniSectorSize > len(miniStream) || offset+int64(miniSectorSize) > int64(len(miniStream)) {
			return nil, fmt.Errorf("mini sector %d is outside the mini stream", sector)
		}
		chain = append(chain, miniStream[offset:offset+int64(miniSectorSize)]...)
		if size >= 0 && len(chain) >= size {
			break
		}
		if int64(sector) >= int64(len(miniFAT)) {
			return nil, fmt.Errorf("mini sector %d is outside the mini FAT", sector)
		}
		sector = miniFAT[sector]
	}
	return truncateChain(chain, size)
}

// truncateChain cuts a chain back to the declared stream size.
func truncateChain(chain []byte, size int) ([]byte, error) {
	if size < 0 {
		return chain, nil
	}
	if len(chain) < size {
		return nil, fmt.Errorf("chain is %d bytes, stream declares %d", len(chain), size)
	}
	return chain[:size], nil
}

// sector returns one sector by number.  Sector zero starts just past the
// header.
func (f *cfbFile) sector(sector uint32) ([]byte, error) {
	start := (int64(sector) + 1) * int64(f.sectorSize)
	if start >= int64(len(f.data)) {
		return nil, fmt.Errorf("sector %d is outside the file", sector)
	}
	if stop := start + int64(f.sectorSize); stop <= int64(len(f.data)) {
		return f.data[start:stop], nil
	}
	// The last sector is allowed to be short: not every writer pads the file
	// out to a whole sector. What is missing reads as zeros, as it would from a
	// padded one.
	padded := make([]byte, f.sectorSize)
	copy(padded, f.data[start:])
	return padded, nil
}

func decodeUint32s(data []byte) []uint32 {
	values := make([]uint32, len(data)/4)
	for i := range values {
		values[i] = binary.LittleEndian.Uint32(data[i*4:])
	}
	return values
}

func cfbEntryName(entry []byte) string {
	length := int(binary.LittleEndian.Uint16(entry[0x40:]))
	if length < 2 || length > 64 {
		return ""
	}
	units := make([]uint16, 0, length/2-1)
	for i := 0; i < length-2; i += 2 {
		units = append(units, binary.LittleEndian.Uint16(entry[i:]))
	}
	return decodeMSIName(string(utf16.Decode(units)))
}

// msiNameAlphabet is the 6 bit alphabet MSI packs stream names into.
const msiNameAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz._"

// decodeMSIName unpacks the encoding an .msi uses for its stream names, where a
// single code unit in 0x3800..0x483F stands for one or two characters.  Names
// outside that range are already readable and pass through unchanged.
func decodeMSIName(name string) string {
	var out strings.Builder
	for _, r := range name {
		switch {
		case r >= 0x3800 && r < 0x4800:
			packed := r - 0x3800
			out.WriteByte(msiNameAlphabet[packed&0x3F])
			out.WriteByte(msiNameAlphabet[(packed>>6)&0x3F])
		case r >= 0x4800 && r < 0x4840:
			out.WriteByte(msiNameAlphabet[(r-0x4800)&0x3F])
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
