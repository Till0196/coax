// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"math/bits"

	"github.com/Till0196/coax/pkg/mpegts"
)

// The rest of MMT-SI: the tables a PA message carries besides the MPT (the
// package list and the layout configuration), the MH tables that have a TS
// counterpart in tstables_download.go, the event message table with its
// descriptors, and the descriptors that time an MPU. STD-B60 Table 4-8 assigns
// the table identifiers and 7.4 the descriptor tags.
const (
	TableIDLCT    = 0x81
	TableIDMHSDTT = 0x9e
	TableIDMHCDT  = 0xa2

	// PacketIDCA is the MMTP packet identifier the CA message is sent on
	// (STD-B60 Table 4-11). The message's own identifier is MessageIDCA.
	PacketIDCA = 0x0001

	// TagMHEventMessage is the event message descriptor.
	TagMHEventMessage = 0xf003
	// TagMHUTCNPTReference relates normal play time to UTC.
	TagMHUTCNPTReference = 0x8021
	// TagMPUTimestamp gives the presentation time of an MPU's first access
	// unit as a network time.
	TagMPUTimestamp = 0x0001
	// TagMPUExtendedTimestamp gives the decoding times of every access unit of
	// an MPU relative to that presentation time.
	TagMPUExtendedTimestamp = 0x8026

	// MMTClockHz is the clock a transport stream counts presentation times in,
	// which is what MMT times are converted to here.
	MMTClockHz = 90_000
)

// MHSections are the sections laid end to end in an M2 section message, an M2
// short section message or a data transmission message, each checked. The
// message is padded to its length with bytes that cannot begin a section
// (0xFF), and nothing comes after them. A section whose check fails is left
// out: an MMT section arrives whole inside a message and nothing before this
// looks at its CRC.
func MHSections(payload []byte) [][]byte {
	var found [][]byte
	for at := 0; at+3 <= len(payload); {
		if payload[at] == 0xff {
			break
		}
		length := int(binary.BigEndian.Uint16(payload[at+1:]) & 0x0fff)
		next := at + 3 + length
		if next > len(payload) {
			break
		}
		if section := payload[at:next]; mpegts.ValidSection(section) {
			found = append(found, section)
		}
		at = next
	}
	return found
}

// MHControlSection is the one section of a TLV-SI control packet, checked. The
// section's own length is what is read: a sender may pad after it, and a check
// taken over the padding fails on a section that is perfectly good.
func MHControlSection(payload []byte) ([]byte, bool) {
	if len(payload) < 3 {
		return nil, false
	}
	length := int(binary.BigEndian.Uint16(payload[1:]) & 0x0fff)
	if 3+length > len(payload) {
		return nil, false
	}
	section := payload[:3+length]
	return section, mpegts.ValidSection(section)
}

// PackageMessageTable is one table of a PA message: its identifier and its
// bytes, header included.
type PackageMessageTable struct {
	TableID byte
	Bytes   []byte
}

// PackageMessageTables are the tables of a PA message. The message begins with
// an index of the tables it carries, and the tables follow it, each behind the
// same fields the index gave.
func PackageMessageTables(payload []byte) []PackageMessageTable {
	if len(payload) == 0 {
		return nil
	}
	var found []PackageMessageTable
	for at := 1 + 4*int(payload[0]); at+4 <= len(payload); {
		length := int(binary.BigEndian.Uint16(payload[at+2:]))
		if at+4+length > len(payload) {
			break
		}
		found = append(found, PackageMessageTable{TableID: payload[at], Bytes: payload[at : at+4+length]})
		at += 4 + length
	}
	return found
}

// Identity is what the stream says it is (TLV stream ID, original network ID),
// once the tables have said.
func (state *State) Identity() (tlvStreamID, originalNetworkID uint16, ok bool) {
	return state.TLVStreamID, state.OriginalNetworkID, state.TLVStreamID != 0
}

// CarriedServices are the services this stream carries, which is not every
// service the network defines (see parsePLT). Where no package list has
// arrived, every named service is given; Carried being empty says which
// question was answered.
func (state *State) CarriedServices() []Service {
	var services []Service
	if len(state.Carried) == 0 {
		for _, service := range state.Services {
			services = append(services, service)
		}
		return services
	}
	for _, id := range state.Carried {
		if service, ok := state.Services[id]; ok {
			services = append(services, service)
		}
	}
	return services
}

// Namings is how many services the service tables have named so far, which
// says whether the naming is still going.
func (state *State) Namings() int {
	return len(state.names)
}

// MHEventMessageTable is an EMT read with MMT-SI descriptors (STD-B60 Table
// 11-1): a version, a group, and the messages in it. TR-B39 operates it as one
// section per table with one descriptor, an event message descriptor (group
// 0x001) or a UTC-NPT reference descriptor (group 0xFFF).
type MHEventMessageTable struct {
	DataEventID byte            `json:"data_event_id"`
	GroupID     uint16          `json:"event_msg_group_id"`
	Version     byte            `json:"version"`
	Descriptors []MMTDescriptor `json:"descriptors,omitempty"`
}

// ParseMHEventMessageTable reads an EMT section.
func ParseMHEventMessageTable(section []byte) *MHEventMessageTable {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDEMT {
		return nil
	}
	return &MHEventMessageTable{
		DataEventID: byte(header.Extension >> 12),
		GroupID:     header.Extension & 0x0fff,
		Version:     header.Version,
		Descriptors: ParseMMTDescriptors(header.Body),
	}
}

// EventMessageDescriptor is one event message (STD-B60 Table 11-3): when it
// fires, what it is called and what it carries. Time is the field the mode
// names: nothing for immediate, UTC as a network time for 0x01 and 0x05, NPT
// for 0x02, a relative time for 0x03.
type EventMessageDescriptor struct {
	GroupID  uint16 `json:"event_msg_group_id"`
	TimeMode byte   `json:"time_mode"`
	Time     uint64 `json:"time"`
	Kind     byte   `json:"event_msg_type"`
	ID       uint16 `json:"event_msg_id"`
	Private  []byte `json:"private_data,omitempty"`
}

// Immediate says the message fires as soon as it arrives (time mode 0x00).
func (message EventMessageDescriptor) Immediate() bool { return message.TimeMode == 0x00 }

// MessageID is what an application calls the message: the upper eight bits of
// event_msg_id (ARIB STD-B62 part 2, message_id).
func (message EventMessageDescriptor) MessageID() byte { return byte(message.ID >> 8) }

// MessageVersion is the message's own version, the lower eight bits of
// event_msg_id. An application fires once per version.
func (message EventMessageDescriptor) MessageVersion() byte { return byte(message.ID) }

// ParseEventMessageDescriptor reads the body of an event message descriptor.
func ParseEventMessageDescriptor(body []byte) (EventMessageDescriptor, bool) {
	if len(body) < 3+8+3 {
		return EventMessageDescriptor{}, false
	}
	return EventMessageDescriptor{
		GroupID:  binary.BigEndian.Uint16(body) >> 4,
		TimeMode: body[2],
		Time:     binary.BigEndian.Uint64(body[3:]),
		Kind:     body[11],
		ID:       binary.BigEndian.Uint16(body[12:]),
		Private:  append([]byte(nil), body[14:]...),
	}, true
}

// MPUTimestamp is one entry of an MPU timestamp descriptor: the presentation
// time of an MPU's first access unit, as a 64-bit network time (seconds in the
// high 32 bits, the fraction in the low 32).
type MPUTimestamp struct {
	Sequence uint32 `json:"mpu_sequence_number"`
	NTP      uint64 `json:"mpu_presentation_time"`
}

// ParseMPUTimestamps reads an MPU timestamp descriptor, a run of twelve-byte
// pairs. A trailing part-pair is ignored.
func ParseMPUTimestamps(body []byte) []MPUTimestamp {
	var times []MPUTimestamp
	for at := 0; at+12 <= len(body); at += 12 {
		times = append(times, MPUTimestamp{
			Sequence: binary.BigEndian.Uint32(body[at:]),
			NTP:      binary.BigEndian.Uint64(body[at+4:]),
		})
	}
	return times
}

// AccessUnitTiming is one access unit of an MPU extended timestamp descriptor.
// Both offsets are counted in the descriptor's timescale.
type AccessUnitTiming struct {
	// DTSPTSOffset is how far the presentation time is after the decoding
	// time.
	DTSPTSOffset uint16 `json:"dts_pts_offset"`
	// PTSOffset is how long the access unit lasts, which separates its
	// decoding time from the next one's. Where the descriptor writes no
	// offset per access unit it is the descriptor's default.
	PTSOffset uint16 `json:"pts_offset"`
}

// ExtendedTimestampEntry is one MPU of an MPU extended timestamp descriptor.
type ExtendedTimestampEntry struct {
	Sequence uint32 `json:"mpu_sequence_number"`
	// Leap says whether a leap second falls inside this MPU.
	Leap byte `json:"leap_indicator"`
	// DecodingTimeOffset is how far the decoding time of the first access
	// unit is before the presentation time the MPU timestamp gave it.
	DecodingTimeOffset uint16             `json:"mpu_decoding_time_offset"`
	AccessUnits        []AccessUnitTiming `json:"access_units,omitempty"`
}

// ExtendedTimestamp is an MPU extended timestamp descriptor. The timescale and
// the default offset are written once for an asset and need not be repeated,
// so a descriptor that omits them means an earlier descriptor's still stand.
type ExtendedTimestamp struct {
	// PTSOffsetType is 1 for one default presentation offset for every access
	// unit, 2 for one written per access unit.
	PTSOffsetType byte `json:"pts_offset_type"`
	// Timescale is the unit the offsets are counted in, when HasTimescale.
	Timescale        uint32                   `json:"timescale,omitempty"`
	HasTimescale     bool                     `json:"has_timescale"`
	DefaultPTSOffset uint16                   `json:"default_pts_offset"`
	Entries          []ExtendedTimestampEntry `json:"entries,omitempty"`
	// Truncated says the descriptor ran out before its fields did, or gave a
	// timescale of zero. The entries read before that are kept, but a caller
	// that wants times should not trust any of it.
	Truncated bool `json:"truncated,omitempty"`
}

// Entry is the entry for an MPU.
func (stamp *ExtendedTimestamp) Entry(sequence uint32) (*ExtendedTimestampEntry, bool) {
	for i := range stamp.Entries {
		if stamp.Entries[i].Sequence == sequence {
			return &stamp.Entries[i], true
		}
	}
	return nil, false
}

// ParseExtendedTimestamp reads an MPU extended timestamp descriptor. The entries
// after the header are a run whose stride depends on PTSOffsetType: two bytes
// per access unit with a single default presentation offset, four when each
// writes its own.
func ParseExtendedTimestamp(body []byte) ExtendedTimestamp {
	if len(body) == 0 {
		return ExtendedTimestamp{Truncated: true}
	}
	read := ExtendedTimestamp{PTSOffsetType: body[0] >> 1 & 0x03}
	at := 1
	if body[0]&0x01 != 0 {
		if len(body)-at < 4 {
			read.Truncated = true
			return read
		}
		timescale := binary.BigEndian.Uint32(body[at:])
		if timescale == 0 {
			read.Truncated = true
			return read
		}
		read.Timescale, read.HasTimescale = timescale, true
		at += 4
	}
	if read.PTSOffsetType == 1 {
		if len(body)-at < 2 {
			read.Truncated = true
			return read
		}
		read.DefaultPTSOffset = binary.BigEndian.Uint16(body[at:])
		at += 2
	}
	perUnit := 2
	if read.PTSOffsetType == 2 {
		perUnit = 4
	}
	for at < len(body) {
		if len(body)-at < 8 {
			read.Truncated = true
			return read
		}
		entry := ExtendedTimestampEntry{
			Sequence:           binary.BigEndian.Uint32(body[at:]),
			Leap:               body[at+4] >> 6,
			DecodingTimeOffset: binary.BigEndian.Uint16(body[at+5:]),
		}
		count := int(body[at+7])
		at += 8
		if len(body)-at < count*perUnit {
			read.Truncated = true
			return read
		}
		entry.AccessUnits = make([]AccessUnitTiming, 0, count)
		for range count {
			unit := AccessUnitTiming{DTSPTSOffset: binary.BigEndian.Uint16(body[at:]), PTSOffset: read.DefaultPTSOffset}
			at += 2
			if read.PTSOffsetType == 2 {
				unit.PTSOffset = binary.BigEndian.Uint16(body[at:])
				at += 2
			}
			entry.AccessUnits = append(entry.AccessUnits, unit)
		}
		read.Entries = append(read.Entries, entry)
	}
	return read
}

// NTPTo90k is a network time as a count of the transport stream's clock from a
// base the same stream's other times are taken from.
func NTPTo90k(base, ntp uint64) int64 {
	delta := int64(ntp - base)
	seconds := delta >> 32
	fraction := uint64(uint32(delta))
	return seconds*MMTClockHz + int64(fraction*MMTClockHz>>32)
}

// TicksTo90k is a count of a descriptor's own timescale as a count of the
// transport stream's clock, rounded to the nearest tick the same way on either
// side of zero. A timescale of zero gives zero.
func TicksTo90k(ticks int64, timescale uint32) int64 {
	if timescale == 0 {
		return 0
	}
	magnitude := uint64(ticks)
	if ticks < 0 {
		magnitude = -magnitude
	}
	high, low := bits.Mul64(magnitude, MMTClockHz)
	low, carry := bits.Add64(low, uint64(timescale/2), 0)
	high += carry
	var rounded uint64
	if high >= uint64(timescale) {
		rounded = 1<<63 - 1
	} else {
		rounded, _ = bits.Div64(high, low, uint64(timescale))
		if rounded > 1<<63-1 {
			rounded = 1<<63 - 1
		}
	}
	if ticks < 0 {
		return -int64(rounded)
	}
	return int64(rounded)
}

// ClockBase is the one network time every other time of a stream is counted
// from. Which time does not matter, only that it is the same throughout: the
// first that arrives is kept, so the times handed on are one timeline.
type ClockBase struct {
	ntp   uint64
	taken bool
}

// Set takes this time as the base, if none has been taken.
func (base *ClockBase) Set(ntp uint64) {
	if !base.taken {
		base.ntp, base.taken = ntp, true
	}
}

// NTP is the time the base was taken from, once one has been.
func (base *ClockBase) NTP() (uint64, bool) { return base.ntp, base.taken }

// To90k is a network time as a count of the transport stream's clock from this
// base, or nothing while no base has been taken.
func (base *ClockBase) To90k(ntp uint64) (int64, bool) {
	if !base.taken {
		return 0, false
	}
	return NTPTo90k(base.ntp, ntp), true
}

// AccessUnitTimes is when one access unit is decoded and when it is presented,
// both counted in the transport stream's clock from a ClockBase.
type AccessUnitTimes struct {
	DTS int64 `json:"dts"`
	PTS int64 `json:"pts"`
}

// AccessUnitTimesOf gives the decoding and presentation times of one access
// unit of an MPU. ntp is the MPU's presentation time from its MPU timestamp
// descriptor, entry the same MPU's entry in an extended timestamp descriptor,
// and sampleNumber counts the access units from one. The timescale and the
// presentation offsets are passed rather than read from the entry because a
// descriptor need not repeat them.
//
// The decoding time of the first access unit is DecodingTimeOffset before the
// MPU's presentation time, each one after it is one presentation offset later,
// and the presentation time of any of them is its own DTSPTSOffset after its
// decoding time. Nothing comes back if the timescale is zero or the sample
// number is not one of the entry's.
func AccessUnitTimesOf(base *ClockBase, entry *ExtendedTimestampEntry, timescale uint32,
	ptsOffsetType byte, defaultPTSOffset uint16, ntp uint64, sampleNumber uint32) (AccessUnitTimes, bool) {
	if timescale == 0 || sampleNumber == 0 || entry == nil || int(sampleNumber) > len(entry.AccessUnits) {
		return AccessUnitTimes{}, false
	}
	index := int(sampleNumber) - 1
	offset := -int64(entry.DecodingTimeOffset)
	if ptsOffsetType == 2 {
		for _, unit := range entry.AccessUnits[:index] {
			offset += int64(unit.PTSOffset)
		}
	} else {
		offset += int64(index) * int64(defaultPTSOffset)
	}
	base.Set(ntp)
	origin, _ := base.To90k(ntp)
	return AccessUnitTimes{
		DTS: origin + TicksTo90k(offset, timescale),
		PTS: origin + TicksTo90k(offset+int64(entry.AccessUnits[index].DTSPTSOffset), timescale),
	}, true
}

// The kinds of place an MMT_general_location_info names.
const (
	LocationHere = 0x00
	LocationIPv4 = 0x01
	LocationIPv6 = 0x02
	LocationTS   = 0x03
	LocationTSIP = 0x04
	LocationURL  = 0x05
)

// PackageLocation is where something is, as an MMT_general_location_info says:
// in this stream on a packet identifier, in an IP flow (a destination port and
// packet identifier), in an MPEG-2 transport stream, or at a URL. Kind is the
// location type; a kind this reader does not know has nothing else set.
type PackageLocation struct {
	Kind              byte   `json:"location_type"`
	PacketID          uint16 `json:"packet_id,omitempty"`
	Port              uint16 `json:"port,omitempty"`
	NetworkID         uint16 `json:"network_id,omitempty"`
	TransportStreamID uint16 `json:"transport_stream_id,omitempty"`
	PID               uint16 `json:"pid,omitempty"`
	URL               string `json:"url,omitempty"`
}

// ParseGeneralLocation reads an MMT_general_location_info and says how many
// bytes it took.
func ParseGeneralLocation(buffer []byte) (PackageLocation, int, bool) {
	if len(buffer) == 0 {
		return PackageLocation{}, 0, false
	}
	kind, rest := buffer[0], buffer[1:]
	location := PackageLocation{Kind: kind}
	need := func(n int) bool { return len(rest) >= n }
	switch kind {
	case LocationHere:
		if !need(2) {
			return PackageLocation{}, 0, false
		}
		location.PacketID = binary.BigEndian.Uint16(rest)
		return location, 3, true
	case LocationIPv4:
		if !need(12) {
			return PackageLocation{}, 0, false
		}
		location.Port = binary.BigEndian.Uint16(rest[8:])
		location.PacketID = binary.BigEndian.Uint16(rest[10:])
		return location, 13, true
	case LocationIPv6:
		if !need(36) {
			return PackageLocation{}, 0, false
		}
		location.Port = binary.BigEndian.Uint16(rest[32:])
		location.PacketID = binary.BigEndian.Uint16(rest[34:])
		return location, 37, true
	case LocationTS:
		if !need(6) {
			return PackageLocation{}, 0, false
		}
		location.NetworkID = binary.BigEndian.Uint16(rest)
		location.TransportStreamID = binary.BigEndian.Uint16(rest[2:])
		location.PID = binary.BigEndian.Uint16(rest[4:]) & 0x1fff
		return location, 7, true
	case LocationTSIP:
		if !need(36) {
			return PackageLocation{}, 0, false
		}
		location.PID = binary.BigEndian.Uint16(rest[34:]) & 0x1fff
		return location, 37, true
	case LocationURL:
		if !need(1) {
			return PackageLocation{}, 0, false
		}
		length := int(rest[0])
		if !need(1 + length) {
			return PackageLocation{}, 0, false
		}
		location.URL = string(rest[1 : 1+length])
		return location, 2 + length, true
	default:
		return location, 1, true
	}
}

// ListedPackage is one package a PLT lists.
type ListedPackage struct {
	PackageID []byte          `json:"package_id"`
	Location  PackageLocation `json:"location"`
}

// PackageList is a package list table (STD-B60 7.3.3.2), every location kind.
type PackageList struct {
	Version  byte            `json:"version"`
	Packages []ListedPackage `json:"packages,omitempty"`
}

// ParsePackageList reads a PLT. It arrives inside a PA message and is not a
// section.
func ParsePackageList(table []byte) *PackageList {
	if len(table) < 5 || table[0] != TableIDPLT {
		return nil
	}
	read := &PackageList{Version: table[1]}
	at := 5
	for range int(table[4]) {
		if at >= len(table) {
			return nil
		}
		length := int(table[at])
		if at+1+length > len(table) {
			return nil
		}
		packageID := append([]byte(nil), table[at+1:at+1+length]...)
		at += 1 + length
		location, used, ok := ParseGeneralLocation(table[at:])
		if !ok {
			return nil
		}
		at += used
		read.Packages = append(read.Packages, ListedPackage{PackageID: packageID, Location: location})
	}
	return read
}

// LayoutRegion is one region of a layout (STD-B60 7.3.3.3): the corners as
// percentages of the screen, and the stacking order.
type LayoutRegion struct {
	Number byte `json:"region_number"`
	Left   byte `json:"left_top_pos_x"`
	Top    byte `json:"left_top_pos_y"`
	Right  byte `json:"right_down_pos_x"`
	Bottom byte `json:"right_down_pos_y"`
	Layer  byte `json:"layer_order"`
}

// Layout is one layout of one device. Device 0 is the main one, the
// television itself.
type Layout struct {
	Number  byte           `json:"layout_number"`
	Device  byte           `json:"device_id"`
	Regions []LayoutRegion `json:"regions,omitempty"`
}

// ParseLayoutConfiguration reads an LCT: the layouts a multi-view service
// defines. Like the PLT it arrives inside a PA message.
func ParseLayoutConfiguration(table []byte) []Layout {
	if len(table) < 5 || table[0] != TableIDLCT {
		return nil
	}
	count := int(table[4])
	layouts := make([]Layout, 0, count)
	at := 5
	for range count {
		if at+3 > len(table) {
			return nil
		}
		layout := Layout{Number: table[at], Device: table[at+1]}
		regions := int(table[at+2])
		at += 3
		for range regions {
			if at+6 > len(table) {
				return nil
			}
			region := table[at : at+6]
			layout.Regions = append(layout.Regions, LayoutRegion{
				Number: region[0], Left: region[1], Top: region[2],
				Right: region[3], Bottom: region[4], Layer: region[5],
			})
			at += 6
		}
		layouts = append(layouts, layout)
	}
	return layouts
}

// ParseMHSoftwareDownload reads an MH-SDTT section (STD-B60 Table 7-22): the
// SDTT's layout with a TLV stream ID in place of the transport stream ID, and
// MMT-SI descriptors (two-byte tags, 7.4) in MHDescriptors.
func ParseMHSoftwareDownload(section []byte) *SoftwareDownload {
	return parseSoftwareDownloadTable(section, TableIDMHSDTT, true)
}

// MHCommonData is an MH-CDT section: common data, which in practice is a logo.
type MHCommonData struct {
	DownloadDataID    uint16          `json:"download_data_id"`
	Version           byte            `json:"version"`
	SectionNumber     byte            `json:"section_number"`
	LastSectionNumber byte            `json:"last_section_number"`
	OriginalNetworkID uint16          `json:"original_network_id"`
	DataType          byte            `json:"data_type"`
	Descriptors       []MMTDescriptor `json:"descriptors,omitempty"`
	// Data is the data module as sent. For a logo STD-B60 6.7.1 lays it out
	// as logo_type, logo_id, logo_version and the PNG.
	Data []byte `json:"data,omitempty"`
}

// ParseMHCommonData reads an MH-CDT section (STD-B60 7.3.3.24).
func ParseMHCommonData(section []byte) *MHCommonData {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDMHCDT || len(header.Body) < 5 {
		return nil
	}
	body := header.Body
	described := int(binary.BigEndian.Uint16(body[3:]) & 0x0fff)
	if 5+described > len(body) {
		return nil
	}
	return &MHCommonData{
		DownloadDataID:    header.Extension,
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
		OriginalNetworkID: binary.BigEndian.Uint16(body),
		DataType:          body[2],
		Descriptors:       ParseMMTDescriptors(body[5 : 5+described]),
		Data:              append([]byte(nil), body[5+described:]...),
	}
}
