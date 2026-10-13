// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "encoding/binary"

// The tables a data transmission message carries, and the entitlement messages
// that ride beside them. Like the rest of the parse-only tables these are read
// and not acted on: a tuner hands the stream on as it came and does not
// present a data broadcast, but a capture holds them and a reader should be
// able to say what is in one.

// DataDirectory is the DDMT: the directory tree a data broadcast presents,
// with each node and file given the tag its content is carried under. STD-B60
// Table 9-6.
type DataDirectory struct {
	SessionID         byte            `json:"data_transmission_session_id"`
	Version           byte            `json:"version"`
	SectionNumber     byte            `json:"section_number"`
	LastSectionNumber byte            `json:"last_section_number"`
	BasePath          string          `json:"base_directory_path"`
	Nodes             []DirectoryNode `json:"nodes,omitempty"`
}

// DirectoryNode is one directory of the tree and the files directly in it.
type DirectoryNode struct {
	NodeTag uint16          `json:"node_tag"`
	Version byte            `json:"version"`
	Path    string          `json:"path"`
	Files   []DirectoryFile `json:"files,omitempty"`
}

// DirectoryFile is one file, named here and carried under its tag elsewhere.
type DirectoryFile struct {
	NodeTag uint16 `json:"node_tag"`
	Name    string `json:"name"`
}

// ParseDataDirectory reads a DDMT section.
func ParseDataDirectory(section []byte) *DataDirectory {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDDDMT || len(header.Body) < 2 {
		return nil
	}
	body := header.Body
	// The session identifier is the first byte of what a long-form section
	// calls the table identifier extension; the second is kept for later.
	table := &DataDirectory{
		SessionID:         byte(header.Extension >> 8),
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
	}
	at := 0
	pathLength := int(body[at])
	at++
	if at+pathLength+1 > len(body) {
		return nil
	}
	table.BasePath = string(body[at : at+pathLength])
	at += pathLength
	nodes := int(body[at])
	at++
	for range nodes {
		if at+4 > len(body) {
			return table
		}
		node := DirectoryNode{
			NodeTag: binary.BigEndian.Uint16(body[at : at+2]),
			Version: body[at+2],
		}
		nodePathLength := int(body[at+3])
		at += 4
		if at+nodePathLength+2 > len(body) {
			return table
		}
		node.Path = string(body[at : at+nodePathLength])
		at += nodePathLength
		files := int(binary.BigEndian.Uint16(body[at : at+2]))
		at += 2
		for range files {
			if at+3 > len(body) {
				return table
			}
			nameLength := int(body[at+2])
			if at+3+nameLength > len(body) {
				return table
			}
			node.Files = append(node.Files, DirectoryFile{
				NodeTag: binary.BigEndian.Uint16(body[at : at+2]),
				Name:    string(body[at+3 : at+3+nameLength]),
			})
			at += 3 + nameLength
		}
		table.Nodes = append(table.Nodes, node)
	}
	return table
}

// DataAssetManagement is the DAMT: which media processing units of a component
// carry which items, and how big each of them is. STD-B60 Table 9-7.
type DataAssetManagement struct {
	SessionID         byte          `json:"data_transmission_session_id"`
	Version           byte          `json:"version"`
	SectionNumber     byte          `json:"section_number"`
	LastSectionNumber byte          `json:"last_section_number"`
	TransactionID     uint32        `json:"transaction_id"`
	ComponentTag      uint16        `json:"component_tag"`
	DownloadID        uint32        `json:"download_id"`
	Units             []ManagedUnit `json:"units,omitempty"`
	ComponentInfo     []byte        `json:"component_info,omitempty"`
}

// ManagedUnit is one media processing unit and the items it carries. An index
// item holds the item table itself, in which case the items are named here
// only by their tags.
type ManagedUnit struct {
	SequenceNumber  uint32        `json:"mpu_sequence_number"`
	Size            uint32        `json:"mpu_size"`
	IndexItem       bool          `json:"index_item"`
	CompressionType byte          `json:"index_item_compression_type"`
	IndexItemID     uint32        `json:"index_item_id,omitempty"`
	Items           []ManagedItem `json:"items,omitempty"`
	Info            []byte        `json:"mpu_info,omitempty"`
}

// ManagedItem is one item of a unit.
type ManagedItem struct {
	NodeTag  uint16 `json:"node_tag"`
	ItemID   uint32 `json:"item_id,omitempty"`
	Size     uint32 `json:"item_size,omitempty"`
	Version  byte   `json:"item_version,omitempty"`
	Checksum uint32 `json:"item_checksum,omitempty"`
	HasSum   bool   `json:"item_checksum_present"`
	Info     []byte `json:"item_info,omitempty"`
}

// ParseDataAssetManagement reads a DAMT section.
func ParseDataAssetManagement(section []byte) *DataAssetManagement {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDDAMT || len(header.Body) < 11 {
		return nil
	}
	body := header.Body
	table := &DataAssetManagement{
		SessionID:         byte(header.Extension >> 8),
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
		TransactionID:     binary.BigEndian.Uint32(body[0:4]),
		ComponentTag:      binary.BigEndian.Uint16(body[4:6]),
		DownloadID:        binary.BigEndian.Uint32(body[6:10]),
	}
	units := int(body[10])
	at := 11
	for range units {
		if at+9 > len(body) {
			return table
		}
		unit := ManagedUnit{
			SequenceNumber:  binary.BigEndian.Uint32(body[at : at+4]),
			Size:            binary.BigEndian.Uint32(body[at+4 : at+8]),
			IndexItem:       body[at+8]&0x80 != 0,
			CompressionType: body[at+8] >> 4 & 0x03,
		}
		hasIndexID := body[at+8]&0x40 != 0
		at += 9
		if unit.IndexItem && hasIndexID {
			if at+4 > len(body) {
				return table
			}
			unit.IndexItemID = binary.BigEndian.Uint32(body[at : at+4])
			at += 4
		}
		if at+2 > len(body) {
			return table
		}
		items := int(binary.BigEndian.Uint16(body[at : at+2]))
		at += 2
		for range items {
			if at+2 > len(body) {
				return table
			}
			item := ManagedItem{NodeTag: binary.BigEndian.Uint16(body[at : at+2])}
			at += 2
			// The unit that carries the index describes its items there
			// rather than here, so only the tag is written for those.
			if !unit.IndexItem {
				if at+10 > len(body) {
					return table
				}
				item.ItemID = binary.BigEndian.Uint32(body[at : at+4])
				item.Size = binary.BigEndian.Uint32(body[at+4 : at+8])
				item.Version = body[at+8]
				item.HasSum = body[at+9]&0x80 != 0
				at += 10
				if item.HasSum {
					if at+4 > len(body) {
						return table
					}
					item.Checksum = binary.BigEndian.Uint32(body[at : at+4])
					at += 4
				}
				if at+1 > len(body) {
					return table
				}
				infoLength := int(body[at])
				at++
				if at+infoLength > len(body) {
					return table
				}
				item.Info = body[at : at+infoLength]
				at += infoLength
			}
			unit.Items = append(unit.Items, item)
		}
		if at+1 > len(body) {
			return table
		}
		infoLength := int(body[at])
		at++
		if at+infoLength > len(body) {
			return table
		}
		unit.Info = body[at : at+infoLength]
		at += infoLength
		table.Units = append(table.Units, unit)
	}
	if at+1 > len(body) {
		return table
	}
	componentInfo := int(body[at])
	at++
	if at+componentInfo > len(body) {
		return table
	}
	table.ComponentInfo = body[at : at+componentInfo]
	return table
}

// DataContentConfiguration is the DCCT: how the files of a data broadcast are
// grouped into the units a receiver presents together. STD-B60 Table 9-8.
type DataContentConfiguration struct {
	SessionID         byte            `json:"data_transmission_session_id"`
	Version           byte            `json:"version"`
	SectionNumber     byte            `json:"section_number"`
	LastSectionNumber byte            `json:"last_section_number"`
	ContentID         uint16          `json:"content_id"`
	ContentVersion    byte            `json:"content_version"`
	ContentSize       uint32          `json:"content_size"`
	Units             []PresentedUnit `json:"units,omitempty"`
	// Nodes is the alternative to units: a content that names no presentation
	// unit lists its nodes flat instead.
	Nodes       []uint16     `json:"nodes,omitempty"`
	Descriptors []Descriptor `json:"descriptors,omitempty"`
}

// PresentedUnit is one presentation unit and the nodes that make it up.
type PresentedUnit struct {
	Tag         byte         `json:"pu_tag"`
	Size        uint32       `json:"pu_size"`
	Nodes       []uint16     `json:"nodes,omitempty"`
	Descriptors []Descriptor `json:"descriptors,omitempty"`
}

// ParseDataContentConfiguration reads a DCCT section.
func ParseDataContentConfiguration(section []byte) *DataContentConfiguration {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDDCCT || len(header.Body) < 8 {
		return nil
	}
	body := header.Body
	table := &DataContentConfiguration{
		SessionID:         byte(header.Extension >> 8),
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
		ContentID:         binary.BigEndian.Uint16(body[0:2]),
		ContentVersion:    body[2],
		ContentSize:       binary.BigEndian.Uint32(body[3:7]),
	}
	units := body[7]&0x80 != 0
	content := body[7]&0x40 != 0
	at := 8
	if units {
		if at+1 > len(body) {
			return table
		}
		count := int(body[at])
		at++
		for range count {
			if at+6 > len(body) {
				return table
			}
			unit := PresentedUnit{
				Tag:  body[at],
				Size: binary.BigEndian.Uint32(body[at+1 : at+5]),
			}
			members := int(body[at+5])
			at += 6
			if at+2*members+1 > len(body) {
				return table
			}
			for index := range members {
				unit.Nodes = append(unit.Nodes, binary.BigEndian.Uint16(body[at+2*index:]))
			}
			at += 2 * members
			descriptors := int(body[at])
			at++
			if at+descriptors > len(body) {
				return table
			}
			unit.Descriptors = ParseDescriptors(body[at : at+descriptors])
			at += descriptors
			table.Units = append(table.Units, unit)
		}
	} else {
		if at+2 > len(body) {
			return table
		}
		count := int(binary.BigEndian.Uint16(body[at : at+2]))
		at += 2
		if at+2*count > len(body) {
			return table
		}
		for index := range count {
			table.Nodes = append(table.Nodes, binary.BigEndian.Uint16(body[at+2*index:]))
		}
		at += 2 * count
	}
	if content {
		if at+1 > len(body) {
			return table
		}
		length := int(body[at])
		at++
		if at+length > len(body) {
			return table
		}
		table.Descriptors = ParseDescriptors(body[at : at+length])
	}
	return table
}

// EntitlementMessage is an ECM or an EMM: the header a receiver routes it by,
// and a body only the conditional access system can read. STD-B60 Tables 7-13
// and 7-14.
type EntitlementMessage struct {
	TableID           byte   `json:"table_id"`
	Extension         uint16 `json:"table_id_extension"`
	Version           byte   `json:"version"`
	SectionNumber     byte   `json:"section_number"`
	LastSectionNumber byte   `json:"last_section_number"`
	Data              []byte `json:"data"`
}

// Control says whether the message carries the key for a program rather than
// an entitlement for a receiver.
func (message *EntitlementMessage) Control() bool {
	return message.TableID <= TableIDMHECMHigh
}

// ParseEntitlementMessage reads an ECM or EMM section.
func ParseEntitlementMessage(section []byte) *EntitlementMessage {
	header, ok := parseLongSection(section)
	if !ok || header.TableID < TableIDMHECMLow || header.TableID > TableIDMHEMMHigh {
		return nil
	}
	return &EntitlementMessage{
		TableID:           header.TableID,
		Extension:         header.Extension,
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
		Data:              header.Body,
	}
}

// MHConditionalAccess is the MH-CAT, which says where the entitlement messages
// of the package are. It is an MMT table rather than a section: a version and
// a two-byte length, and then descriptors. STD-B60 Table 7-17.
type MHConditionalAccess struct {
	Version     byte            `json:"version"`
	Descriptors []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHConditionalAccess reads an MH-CAT table.
func ParseMHConditionalAccess(table []byte) *MHConditionalAccess {
	if len(table) < 4 || table[0] != TableIDMHCAT {
		return nil
	}
	length := int(binary.BigEndian.Uint16(table[2:4]))
	if 4+length > len(table) {
		return nil
	}
	return &MHConditionalAccess{Version: table[1], Descriptors: ParseMMTDescriptors(table[4 : 4+length])}
}
