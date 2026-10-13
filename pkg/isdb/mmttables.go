// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"net/netip"
	"time"
)

// The MMT-SI counterparts of the tables in tstables.go, and the tables that
// exist only here: the package table that says what a broadcast is made of,
// the address map that says which datagrams belong to which service, and the
// data transmission tables. None of them is acted on either. STD-B60 Table 4-8
// assigns the identifiers.
const (
	TableIDMPTSubsetLow  = 0x11
	TableIDMPTSubsetHigh = 0x1f
	TableIDMPTComplete   = 0x20
	TableIDMHECMLow      = 0x82
	TableIDMHECMHigh     = 0x83
	TableIDMHEMMLow      = 0x84
	TableIDMHEMMHigh     = 0x85
	TableIDMHCAT         = 0x86
	TableIDMHEITPresent  = 0x8b
	TableIDMHEITLow      = 0x8c
	TableIDMHEITHigh     = 0x9b
	TableIDMHAIT         = 0x9c
	TableIDMHBIT         = 0x9d
	TableIDMHSDTOther    = 0xa0
	TableIDMHTOT         = 0xa1
	TableIDDDMT          = 0xa3
	TableIDDAMT          = 0xa4
	TableIDDCCT          = 0xa5
	TableIDEMT           = 0xa6
	TableIDAMT           = 0xfe

	// MessageIDData carries the data transmission tables.
	MessageIDData = 0x8003
	// MessageIDCA carries the entitlement messages.
	MessageIDCA = 0x8001
)

// MMTDescriptor is one descriptor as MMT-SI writes it: a two-byte tag, and a
// length whose own width depends on the tag. See descriptorLengthBytes.
type MMTDescriptor struct {
	Tag  uint16 `json:"tag"`
	Body []byte `json:"body"`
}

// ParseMMTDescriptors reads an MMT-SI descriptor loop.
func ParseMMTDescriptors(buffer []byte) []MMTDescriptor {
	var described []MMTDescriptor
	for at := 0; at+3 <= len(buffer); {
		tag := binary.BigEndian.Uint16(buffer[at:])
		lengthBytes := descriptorLengthBytes(tag)
		if at+2+lengthBytes > len(buffer) {
			return described
		}
		length := 0
		for _, value := range buffer[at+2 : at+2+lengthBytes] {
			length = length<<8 | int(value)
		}
		start, next := at+2+lengthBytes, at+2+lengthBytes+length
		if next > len(buffer) {
			return described
		}
		described = append(described, MMTDescriptor{Tag: tag, Body: buffer[start:next]})
		at = next
	}
	return described
}

// FindMMTDescriptor is the descriptor with this tag, or nothing.
func FindMMTDescriptor(described []MMTDescriptor, tag uint16) (MMTDescriptor, bool) {
	for _, one := range described {
		if one.Tag == tag {
			return one, true
		}
	}
	return MMTDescriptor{}, false
}

// PackageTable is the MPT: the assets a package is made of and where each of
// them is carried. It arrives inside a PA message rather than as a section, so
// it has no check of its own and is parsed from the table bytes.
type PackageTable struct {
	TableID     byte            `json:"table_id"`
	Version     byte            `json:"version"`
	Mode        byte            `json:"mode"`
	PackageID   []byte          `json:"package_id"`
	Descriptors []MMTDescriptor `json:"descriptors,omitempty"`
	Assets      []Asset         `json:"assets,omitempty"`
}

// ServiceID is the package identifier's last two bytes, which is where a
// broadcast writes the service the package is.
func (table *PackageTable) ServiceID() uint16 {
	if len(table.PackageID) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(table.PackageID[len(table.PackageID)-2:])
}

// Asset is one component of a package: a video stream, an audio stream, a
// subtitle track or a data carousel.
type Asset struct {
	IdentifierType byte            `json:"identifier_type"`
	IDScheme       uint32          `json:"id_scheme"`
	ID             []byte          `json:"id"`
	Type           string          `json:"type"`
	ClockRelation  bool            `json:"clock_relation"`
	Locations      []AssetLocation `json:"locations,omitempty"`
	Descriptors    []MMTDescriptor `json:"descriptors,omitempty"`
}

// PacketID is where the asset is in this stream, when it is in this stream at
// all: location type 0 is the one that means here.
func (asset Asset) PacketID() (uint16, bool) {
	for _, location := range asset.Locations {
		if location.Type == 0x00 {
			return location.PacketID, true
		}
	}
	return 0, false
}

// AssetLocation is one place an asset is carried, which need not be this
// stream: MMT can point at another packet identifier, another broadcast, or a
// URL. The bytes are kept whole because only the first two kinds have a packet
// identifier to read.
type AssetLocation struct {
	Type     byte   `json:"type"`
	PacketID uint16 `json:"packet_id"`
	Raw      []byte `json:"raw"`
}

// ParsePackageTable reads an MP table, complete or subset.
func ParsePackageTable(table []byte) *PackageTable {
	if len(table) < 6 || table[0] < TableIDMPTSubsetLow || table[0] > TableIDMPTComplete {
		return nil
	}
	length := int(binary.BigEndian.Uint16(table[2:4]))
	if 4+length > len(table) {
		return nil
	}
	body := table[4 : 4+length]
	if len(body) < 2 {
		return nil
	}
	described := &PackageTable{TableID: table[0], Version: table[1], Mode: body[0] & 0x03}
	at := 1
	identifierLength := int(body[at])
	at++
	if at+identifierLength+2 > len(body) {
		return nil
	}
	described.PackageID = body[at : at+identifierLength]
	at += identifierLength
	descriptorsLength := int(binary.BigEndian.Uint16(body[at:]))
	at += 2
	if at+descriptorsLength+1 > len(body) {
		return nil
	}
	described.Descriptors = ParseMMTDescriptors(body[at : at+descriptorsLength])
	at += descriptorsLength
	count := int(body[at])
	at++
	for range count {
		asset, read, ok := parseAsset(body[at:])
		if !ok {
			return described
		}
		described.Assets = append(described.Assets, asset)
		at += read
	}
	return described
}

func parseAsset(buffer []byte) (Asset, int, bool) {
	if len(buffer) < 11 {
		return Asset{}, 0, false
	}
	asset := Asset{IdentifierType: buffer[0], IDScheme: binary.BigEndian.Uint32(buffer[1:5])}
	at := 5
	identifierLength := int(buffer[at])
	at++
	if at+identifierLength+6 > len(buffer) {
		return Asset{}, 0, false
	}
	asset.ID = buffer[at : at+identifierLength]
	at += identifierLength
	asset.Type = string(buffer[at : at+4])
	at += 4
	asset.ClockRelation = buffer[at]&0x01 != 0
	at++
	if asset.ClockRelation {
		// The timescale follows, and a flag saying whether an offset follows
		// it. Neither says anything about where the asset is, so both are
		// stepped over.
		if at+2 > len(buffer) {
			return Asset{}, 0, false
		}
		extended := buffer[at+1]&0x01 != 0
		at += 2
		if extended {
			if at+4 > len(buffer) {
				return Asset{}, 0, false
			}
			at += 4
		}
	}
	if at+1 > len(buffer) {
		return Asset{}, 0, false
	}
	locations := int(buffer[at])
	at++
	for range locations {
		location, read, ok := parseAssetLocation(buffer[at:])
		if !ok {
			return Asset{}, 0, false
		}
		asset.Locations = append(asset.Locations, location)
		at += read
	}
	if at+2 > len(buffer) {
		return Asset{}, 0, false
	}
	descriptorsLength := int(binary.BigEndian.Uint16(buffer[at:]))
	at += 2
	if at+descriptorsLength > len(buffer) {
		return Asset{}, 0, false
	}
	asset.Descriptors = ParseMMTDescriptors(buffer[at : at+descriptorsLength])
	return asset, at + descriptorsLength, true
}

func parseAssetLocation(buffer []byte) (AssetLocation, int, bool) {
	if len(buffer) < 1 {
		return AssetLocation{}, 0, false
	}
	size := 0
	switch buffer[0] {
	case 0x00: // this stream
		size = 1 + 2
	case 0x01: // another broadcast, over IPv4
		size = 1 + 4 + 4 + 2 + 2
	case 0x02: // another broadcast, over IPv6
		size = 1 + 16 + 16 + 2 + 2
	case 0x03: // another network, by its identifiers
		size = 1 + 2 + 2 + 2
	case 0x04: // another broadcast, over IPv6 with a flow label
		size = 1 + 16 + 16 + 2 + 2
	case 0x05: // a URL, whose length is the byte after the type
		if len(buffer) < 2 {
			return AssetLocation{}, 0, false
		}
		size = 2 + int(buffer[1])
	default:
		return AssetLocation{}, 0, false
	}
	if len(buffer) < size {
		return AssetLocation{}, 0, false
	}
	location := AssetLocation{Type: buffer[0], Raw: buffer[:size]}
	switch buffer[0] {
	case 0x00:
		location.PacketID = binary.BigEndian.Uint16(buffer[1:3])
	case 0x01, 0x02, 0x04:
		location.PacketID = binary.BigEndian.Uint16(buffer[size-2 : size])
	}
	return location, size, true
}

// MHServiceDescription is an MH-SDT, the TLV counterpart of the SDT. Like it,
// the table comes in a kind for this stream and a kind for a neighbour.
type MHServiceDescription struct {
	TableID           byte                 `json:"table_id"`
	TLVStreamID       uint16               `json:"tlv_stream_id"`
	OriginalNetworkID uint16               `json:"original_network_id"`
	Version           byte                 `json:"version"`
	Services          []MHDescribedService `json:"services,omitempty"`
}

// Actual says whether the table describes the stream that carried it.
func (table *MHServiceDescription) Actual() bool { return table.TableID == TableIDMHSDTActual }

// MHDescribedService is one service of an MH-SDT.
type MHDescribedService struct {
	ServiceID      uint16          `json:"service_id"`
	EITUserDefined byte            `json:"eit_user_defined"`
	EITSchedule    bool            `json:"eit_schedule"`
	EITPresent     bool            `json:"eit_present_following"`
	RunningStatus  byte            `json:"running_status"`
	FreeCAMode     bool            `json:"free_ca_mode"`
	Descriptors    []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHServiceDescription reads an MH-SDT section of either kind.
func ParseMHServiceDescription(section []byte) *MHServiceDescription {
	header, ok := parseLongSection(section)
	if !ok || (header.TableID != TableIDMHSDTActual && header.TableID != TableIDMHSDTOther) || len(header.Body) < 3 {
		return nil
	}
	body := header.Body
	table := &MHServiceDescription{
		TableID:           header.TableID,
		TLVStreamID:       header.Extension,
		OriginalNetworkID: binary.BigEndian.Uint16(body[0:2]),
		Version:           header.Version,
	}
	for at := 3; at+5 <= len(body); {
		length := int(binary.BigEndian.Uint16(body[at+3:at+5]) & 0x0fff)
		next := at + 5 + length
		if next > len(body) {
			return table
		}
		table.Services = append(table.Services, MHDescribedService{
			ServiceID:      binary.BigEndian.Uint16(body[at : at+2]),
			EITUserDefined: body[at+2] >> 2 & 0x07,
			EITSchedule:    body[at+2]&0x02 != 0,
			EITPresent:     body[at+2]&0x01 != 0,
			RunningStatus:  body[at+3] >> 5,
			FreeCAMode:     body[at+3]&0x10 != 0,
			Descriptors:    ParseMMTDescriptors(body[at+5 : next]),
		})
		at = next
	}
	return table
}

// MHEventTable is an MH-EIT, whose shape is the EIT's with MMT-SI
// descriptors in place of the transport stream's.
type MHEventTable struct {
	TableID                  byte      `json:"table_id"`
	ServiceID                uint16    `json:"service_id"`
	TLVStreamID              uint16    `json:"tlv_stream_id"`
	OriginalNetworkID        uint16    `json:"original_network_id"`
	Version                  byte      `json:"version"`
	SectionNumber            byte      `json:"section_number"`
	LastSectionNumber        byte      `json:"last_section_number"`
	SegmentLastSectionNumber byte      `json:"segment_last_section_number"`
	LastTableID              byte      `json:"last_table_id"`
	Events                   []MHEvent `json:"events,omitempty"`
}

// Schedule says whether the table is a day of the schedule rather than the
// present and following pair.
func (table *MHEventTable) Schedule() bool {
	return table.TableID >= TableIDMHEITLow && table.TableID <= TableIDMHEITHigh
}

// MHEvent is one program of an MH-EIT.
type MHEvent struct {
	EventID       uint16          `json:"event_id"`
	Start         time.Time       `json:"start"`
	StartKnown    bool            `json:"start_known"`
	Duration      time.Duration   `json:"duration"`
	DurationKnown bool            `json:"duration_known"`
	RunningStatus byte            `json:"running_status"`
	FreeCAMode    bool            `json:"free_ca_mode"`
	Descriptors   []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHEventTable reads an MH-EIT section.
func ParseMHEventTable(section []byte) *MHEventTable {
	header, ok := parseLongSection(section)
	if !ok || header.TableID < TableIDMHEITPresent || header.TableID > TableIDMHEITHigh || len(header.Body) < 6 {
		return nil
	}
	body := header.Body
	table := &MHEventTable{
		TableID:                  header.TableID,
		ServiceID:                header.Extension,
		TLVStreamID:              binary.BigEndian.Uint16(body[0:2]),
		OriginalNetworkID:        binary.BigEndian.Uint16(body[2:4]),
		Version:                  header.Version,
		SectionNumber:            header.Number,
		LastSectionNumber:        header.LastNumber,
		SegmentLastSectionNumber: body[4],
		LastTableID:              body[5],
	}
	for at := 6; at+12 <= len(body); {
		length := int(binary.BigEndian.Uint16(body[at+10:at+12]) & 0x0fff)
		next := at + 12 + length
		if next > len(body) {
			return table
		}
		start, startKnown := ParseBroadcastTime(body[at+2 : at+7])
		duration, durationKnown := parseDuration(body[at+7 : at+10])
		table.Events = append(table.Events, MHEvent{
			EventID:       binary.BigEndian.Uint16(body[at : at+2]),
			Start:         start,
			StartKnown:    startKnown,
			Duration:      duration,
			DurationKnown: durationKnown,
			RunningStatus: body[at+10] >> 5,
			FreeCAMode:    body[at+10]&0x10 != 0,
			Descriptors:   ParseMMTDescriptors(body[at+12 : next]),
		})
		at = next
	}
	return table
}

// MHTimeTable is the MH-TOT: the time the broadcast says it is, with the
// offsets that turn it into local time.
type MHTimeTable struct {
	Time        time.Time       `json:"time"`
	Descriptors []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHTimeTable reads an MH-TOT section, which like the TOT is written in
// the short form with a check after the descriptors.
func ParseMHTimeTable(section []byte) *MHTimeTable {
	if len(section) < 14 || section[0] != TableIDMHTOT {
		return nil
	}
	length := int(binary.BigEndian.Uint16(section[1:3]) & 0x0fff)
	if 3+length > len(section) {
		return nil
	}
	moment, known := ParseBroadcastTime(section[3:8])
	if !known {
		return nil
	}
	descriptorsLength := int(binary.BigEndian.Uint16(section[8:10]) & 0x0fff)
	table := &MHTimeTable{Time: moment}
	if 10+descriptorsLength > 3+length {
		return table
	}
	table.Descriptors = ParseMMTDescriptors(section[10 : 10+descriptorsLength])
	return table
}

// MHBroadcasterInformation is the MH-BIT.
type MHBroadcasterInformation struct {
	OriginalNetworkID uint16          `json:"original_network_id"`
	Version           byte            `json:"version"`
	ViewPropriety     bool            `json:"broadcast_view_propriety"`
	Descriptors       []MMTDescriptor `json:"descriptors,omitempty"`
	Broadcasters      []MHBroadcaster `json:"broadcasters,omitempty"`
}

// MHBroadcaster is one broadcaster of the MH-BIT.
type MHBroadcaster struct {
	BroadcasterID byte            `json:"broadcaster_id"`
	Descriptors   []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHBroadcasterInformation reads an MH-BIT section.
func ParseMHBroadcasterInformation(section []byte) *MHBroadcasterInformation {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDMHBIT || len(header.Body) < 2 {
		return nil
	}
	body := header.Body
	length := int(binary.BigEndian.Uint16(body[0:2]) & 0x0fff)
	if 2+length > len(body) {
		return nil
	}
	table := &MHBroadcasterInformation{
		OriginalNetworkID: header.Extension,
		Version:           header.Version,
		ViewPropriety:     body[0]&0x10 != 0,
		Descriptors:       ParseMMTDescriptors(body[2 : 2+length]),
	}
	for at := 2 + length; at+3 <= len(body); {
		loop := int(binary.BigEndian.Uint16(body[at+1:at+3]) & 0x0fff)
		next := at + 3 + loop
		if next > len(body) {
			return table
		}
		table.Broadcasters = append(table.Broadcasters, MHBroadcaster{
			BroadcasterID: body[at],
			Descriptors:   ParseMMTDescriptors(body[at+3 : next]),
		})
		at = next
	}
	return table
}

// MHApplicationInformation is the MH-AIT, which starts and stops the
// applications a broadcast carries.
type MHApplicationInformation struct {
	ApplicationType uint16          `json:"application_type"`
	Version         byte            `json:"version"`
	Descriptors     []MMTDescriptor `json:"descriptors,omitempty"`
	Applications    []Application   `json:"applications,omitempty"`
}

// Application is one application of the MH-AIT.
type Application struct {
	OrganizationID uint32          `json:"organization_id"`
	ApplicationID  uint16          `json:"application_id"`
	ControlCode    byte            `json:"control_code"`
	Descriptors    []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHApplicationInformation reads an MH-AIT section.
func ParseMHApplicationInformation(section []byte) *MHApplicationInformation {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDMHAIT || len(header.Body) < 2 {
		return nil
	}
	body := header.Body
	length := int(binary.BigEndian.Uint16(body[0:2]) & 0x0fff)
	if 2+length+2 > len(body) {
		return nil
	}
	table := &MHApplicationInformation{
		ApplicationType: header.Extension,
		Version:         header.Version,
		Descriptors:     ParseMMTDescriptors(body[2 : 2+length]),
	}
	at := 2 + length
	loop := int(binary.BigEndian.Uint16(body[at:at+2]) & 0x0fff)
	at += 2
	end := at + loop
	if end > len(body) {
		end = len(body)
	}
	for at+9 <= end {
		descriptors := int(binary.BigEndian.Uint16(body[at+7:at+9]) & 0x0fff)
		next := at + 9 + descriptors
		if next > end {
			return table
		}
		table.Applications = append(table.Applications, Application{
			OrganizationID: binary.BigEndian.Uint32(body[at : at+4]),
			ApplicationID:  binary.BigEndian.Uint16(body[at+4 : at+6]),
			ControlCode:    body[at+6],
			Descriptors:    ParseMMTDescriptors(body[at+9 : next]),
		})
		at = next
	}
	return table
}

// AddressMap is the AMT: which multicast group each service is carried on. A
// receiver that wants one service and not the rest reads this to know which
// datagrams to keep. STD-B60 Table 5-2.
type AddressMap struct {
	Extension uint16            `json:"table_id_extension"`
	Version   byte              `json:"version"`
	Services  []AddressMapEntry `json:"services,omitempty"`
}

// AddressMapEntry is one service's group. The masks are how many leading bits
// of each address count, so a mask shorter than the address matches a range.
type AddressMapEntry struct {
	ServiceID       uint16     `json:"service_id"`
	IPv6            bool       `json:"ipv6"`
	Source          netip.Addr `json:"source"`
	SourceMask      byte       `json:"source_mask"`
	Destination     netip.Addr `json:"destination"`
	DestinationMask byte       `json:"destination_mask"`
	Private         []byte     `json:"private,omitempty"`
}

// ParseAddressMap reads an AMT section.
func ParseAddressMap(section []byte) *AddressMap {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDAMT || len(header.Body) < 2 {
		return nil
	}
	body := header.Body
	table := &AddressMap{Extension: header.Extension, Version: header.Version}
	count := int(binary.BigEndian.Uint16(body[0:2]) >> 6)
	at := 2
	for range count {
		if at+4 > len(body) {
			return table
		}
		entry := AddressMapEntry{
			ServiceID: binary.BigEndian.Uint16(body[at : at+2]),
			IPv6:      body[at+2]&0x80 != 0,
		}
		loop := int(binary.BigEndian.Uint16(body[at+2:at+4]) & 0x03ff)
		next := at + 4 + loop
		if next > len(body) {
			return table
		}
		addresses := body[at+4 : next]
		width := 4
		if entry.IPv6 {
			width = 16
		}
		if len(addresses) < 2*width+2 {
			return table
		}
		entry.Source, _ = netip.AddrFromSlice(addresses[:width])
		entry.SourceMask = addresses[width]
		entry.Destination, _ = netip.AddrFromSlice(addresses[width+1 : 2*width+1])
		entry.DestinationMask = addresses[2*width+1]
		entry.Private = addresses[2*width+2:]
		table.Services = append(table.Services, entry)
		at = next
	}
	return table
}
