// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package bml

import (
	"encoding/binary"

	"github.com/Till0196/coax/pkg/isdb"
)

// The data transmission of the advanced services: how the files of an
// ARIB-HTML5 data broadcast are carried. What a DSM-CC carousel does on a
// transport stream is split three ways on MMT (STD-B60 10.3, operated by
// TR-B39 Part 3 chapter 6):
//
//   - the data directory management table (DDMT, 0xA3, isdb.ParseDataDirectory)
//     is the directory tree, each node tagged;
//   - the data asset management table (DAMT, 0xA4,
//     isdb.ParseDataAssetManagement) says which media processing unit is
//     which node (the MPU node descriptor in its mpu_info) and its version,
//     which is what a DII says;
//   - the index item, the first item (item_id 0) of each unit, names the
//     unit's files and says their type, compression and size.
//
// The files themselves arrive as the items of a unit without a timeline.

// MessageDataTransmission is the data transmission message's message_id.
const MessageDataTransmission uint16 = 0x8003

// DescriptorMPUNode is the MPU node descriptor: placed in the DAMT's mpu_info,
// it ties a unit to a node of the DDMT.
const DescriptorMPUNode uint16 = 0x8035

// MPUNode is the node of the directory tree a unit of the DAMT is tied to, by
// the MPU node descriptor in its mpu_info.
func MPUNode(unit isdb.ManagedUnit) (uint16, bool) {
	found, ok := isdb.FindMMTDescriptor(isdb.ParseMMTDescriptors(unit.Info), DescriptorMPUNode)
	if !ok || len(found.Body) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(found.Body), true
}

// IndexedItem is one file the index item names.
type IndexedItem struct {
	ID uint32
	// Size is the size carried (after compression); 0 where it is not
	// known.
	Size    uint32
	Version byte
	Name    string
	// Checksum is set where HasChecksum says one was sent.
	Checksum    uint32
	HasChecksum bool
	// Kind is the media type: application/javascript and the like.
	Kind string
	// Compression 0x00 is zlib, 0xFF is none.
	Compression  byte
	OriginalSize uint32
}

// Compressed reports whether the file is carried compressed.
func (item IndexedItem) Compressed() bool { return item.Compression == 0x00 }

// ParseIndexItem reads an index item (the item whose item_id is 0). TR-B39
// Part 3 Table 6-21.
func ParseIndexItem(data []byte) ([]IndexedItem, bool) {
	if len(data) < 2 {
		return nil, false
	}
	count := int(binary.BigEndian.Uint16(data))
	at := 2
	items := make([]IndexedItem, 0, min(count, 256))
	text := func(length int) (string, bool) {
		if at+length > len(data) {
			return "", false
		}
		value := lossy(data[at : at+length])
		at += length
		return value, true
	}
	for range count {
		if at+10 > len(data) {
			return nil, false
		}
		item := IndexedItem{
			ID:      binary.BigEndian.Uint32(data[at:]),
			Size:    binary.BigEndian.Uint32(data[at+4:]),
			Version: data[at+8],
		}
		nameLength := int(data[at+9])
		at += 10
		var ok bool
		if item.Name, ok = text(nameLength); !ok || at >= len(data) {
			return nil, false
		}
		flag := data[at]
		at++
		if flag&0x80 != 0 {
			if at+4 > len(data) {
				return nil, false
			}
			item.Checksum = binary.BigEndian.Uint32(data[at:])
			item.HasChecksum = true
			at += 4
		}
		if at >= len(data) {
			return nil, false
		}
		kindLength := int(data[at])
		at++
		if item.Kind, ok = text(kindLength); !ok || at >= len(data) {
			return nil, false
		}
		item.Compression = data[at]
		at++
		// The original size is sent only for a compressed file; carried as it
		// is (0xFF), it is the size carried.
		if item.Compression == 0xff {
			item.OriginalSize = item.Size
		} else {
			if at+4 > len(data) {
				return nil, false
			}
			item.OriginalSize = binary.BigEndian.Uint32(data[at:])
			at += 4
		}
		items = append(items, item)
	}
	return items, true
}
