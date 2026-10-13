// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// Microsoft Cabinet, the container the payload of an .msi is.  Only the MSZip
// and uncompressed folder types are read; the LZX and Quantum types have never
// been seen in the driver packages this is aimed at.
const (
	cabHeaderSize        = 36
	cabFolderSize        = 8
	cabFileSize          = 16
	cabDataSize          = 8
	cabFlagPrevCabinet   = 0x0001
	cabFlagNextCabinet   = 0x0002
	cabFlagReservePresen = 0x0004
	cabCompressNone      = 0
	cabCompressMSZip     = 1
	// MSZip resets the deflate stream per block but keeps the previous block
	// as the dictionary, so a window of history has to be carried forward.
	cabMSZipWindow = 32 * 1024
)

// cabSignature is the magic every cabinet starts with.
var cabSignature = []byte("MSCF")

func IsCabinet(data []byte) bool {
	return len(data) >= cabHeaderSize && bytes.HasPrefix(data, cabSignature)
}

// cabFolder is one compressed stream inside a cabinet.
type cabFolder struct {
	dataOffset int
	blocks     int
	compress   uint16
	// decoded caches the folder once it has been decompressed, because the
	// files in a folder are cut out of one shared stream.
	decoded []byte
}

// ExtractCabinet returns the files in a cabinet whose name is accepted by want.
//
// Files are filtered by name rather than returned wholesale because a driver
// cabinet holds tens of megabytes across many folders, and every folder that
// holds nothing of interest can then be left compressed.
//
// What is decompressed is charged to budget block by block, before it is
// kept.
func ExtractCabinet(data []byte, want func(name string) bool, budget *expansionBudget) ([]Member, error) {
	if !IsCabinet(data) {
		return nil, errors.New("not a cabinet")
	}
	fileOffset := cabInt(binary.LittleEndian.Uint32(data[0x10:]))
	folderCount := int(binary.LittleEndian.Uint16(data[0x1A:]))
	fileCount := int(binary.LittleEndian.Uint16(data[0x1C:]))
	flags := binary.LittleEndian.Uint16(data[0x1E:])

	offset := cabHeaderSize
	folderReserve, dataReserve := 0, 0
	if flags&cabFlagReservePresen != 0 {
		if offset+4 > len(data) {
			return nil, errors.New("cabinet header is truncated")
		}
		headerReserve := int(binary.LittleEndian.Uint16(data[offset:]))
		folderReserve = int(data[offset+2])
		dataReserve = int(data[offset+3])
		offset += 4 + headerReserve
		if offset > len(data) {
			return nil, errors.New("cabinet header is truncated")
		}
	}
	for _, flag := range []uint16{cabFlagPrevCabinet, cabFlagNextCabinet} {
		if flags&flag == 0 {
			continue
		}
		for i := 0; i < 2; i++ {
			end := bytes.IndexByte(data[offset:], 0)
			if end < 0 {
				return nil, errors.New("cabinet header is truncated")
			}
			offset += end + 1
		}
	}

	folders := make([]cabFolder, folderCount)
	for i := range folders {
		if offset+cabFolderSize+folderReserve > len(data) {
			return nil, errors.New("cabinet folder table is truncated")
		}
		folders[i] = cabFolder{
			dataOffset: cabInt(binary.LittleEndian.Uint32(data[offset:])),
			blocks:     int(binary.LittleEndian.Uint16(data[offset+4:])),
			compress:   binary.LittleEndian.Uint16(data[offset+6:]),
		}
		offset += cabFolderSize + folderReserve
	}

	var members []Member
	if int64(fileOffset) != int64(binary.LittleEndian.Uint32(data[0x10:])) || fileOffset > len(data) {
		return nil, errors.New("cabinet file table is outside the cabinet")
	}
	offset = fileOffset
	for i := 0; i < fileCount; i++ {
		if offset+cabFileSize > len(data) {
			return nil, errors.New("cabinet file table is truncated")
		}
		size := int64(binary.LittleEndian.Uint32(data[offset:]))
		start := int64(binary.LittleEndian.Uint32(data[offset+4:]))
		folder := int(binary.LittleEndian.Uint16(data[offset+8:]))
		end := bytes.IndexByte(data[offset+cabFileSize:], 0)
		if end < 0 {
			return nil, errors.New("cabinet file table is truncated")
		}
		name := string(data[offset+cabFileSize : offset+cabFileSize+end])
		offset += cabFileSize + end + 1
		if folder >= len(folders) || !want(name) {
			continue
		}
		if folders[folder].decoded == nil {
			decoded, err := decodeCabFolder(data, folders[folder], dataReserve, budget)
			if err != nil {
				return nil, fmt.Errorf("cabinet folder %d: %w", folder, err)
			}
			folders[folder].decoded = decoded
		}
		decoded := folders[folder].decoded
		if start+size > int64(len(decoded)) {
			return nil, fmt.Errorf("cabinet file %q runs past its folder", name)
		}
		members = append(members, Member{Name: name, Data: decoded[start : start+size]})
	}
	return members, nil
}

// cabInt is a 32-bit field as an int. On a 32-bit build half of them do not fit
// and would come out negative, slipping under every bound. Anything past
// maxCabInt comes back as maxCabInt, past any slice there, and small enough that
// two added together cannot wrap round.
func cabInt(value uint32) int {
	if uint64(value) > maxCabInt {
		return maxCabInt
	}
	return int(value)
}

const maxCabInt = math.MaxInt / 2

func decodeCabFolder(data []byte, folder cabFolder, dataReserve int, budget *expansionBudget) ([]byte, error) {
	compress := folder.compress & 0x000F
	if compress != cabCompressNone && compress != cabCompressMSZip {
		return nil, fmt.Errorf("compression type %d is not supported", compress)
	}
	var decoded []byte
	offset := folder.dataOffset
	if offset < 0 || offset > len(data) {
		return nil, errors.New("folder starts outside the cabinet")
	}
	for block := 0; block < folder.blocks; block++ {
		if offset+cabDataSize+dataReserve > len(data) {
			return nil, errors.New("block header is truncated")
		}
		compressed := int(binary.LittleEndian.Uint16(data[offset+4:]))
		plain := int(binary.LittleEndian.Uint16(data[offset+6:]))
		offset += cabDataSize + dataReserve
		if offset+compressed > len(data) {
			return nil, errors.New("block runs past the end of the cabinet")
		}
		payload := data[offset : offset+compressed]
		offset += compressed
		if compress == cabCompressNone {
			if err := budget.take(len(payload)); err != nil {
				return nil, err
			}
			decoded = append(decoded, payload...)
			continue
		}
		// A block is not let expand past the size its header gives, so that
		// is what is charged before the work is done.
		if err := budget.take(plain); err != nil {
			return nil, err
		}
		if len(payload) < 2 || payload[0] != 'C' || payload[1] != 'K' {
			return nil, fmt.Errorf("block %d is not MSZip", block)
		}
		dictionary := decoded
		if len(dictionary) > cabMSZipWindow {
			dictionary = dictionary[len(dictionary)-cabMSZipWindow:]
		}
		reader := flate.NewReaderDict(bytes.NewReader(payload[2:]), dictionary)
		expanded, err := io.ReadAll(io.LimitReader(reader, int64(plain)+1))
		reader.Close()
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", block, err)
		}
		if len(expanded) != plain {
			return nil, fmt.Errorf("block %d expanded to %d bytes, header says %d", block, len(expanded), plain)
		}
		decoded = append(decoded, expanded...)
	}
	return decoded, nil
}

// hasExtension reports whether name ends in the given extension, allowing the
// trailing digits InstallShield appends when one cabinet carries several builds
// of the same driver (it9175bda.sys1, .sys2 and so on).
func hasExtension(name, extension string) bool {
	lower := strings.ToLower(name)
	index := strings.LastIndex(lower, extension)
	if index < 0 {
		return false
	}
	for _, r := range lower[index+len(extension):] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
