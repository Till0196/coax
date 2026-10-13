// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"time"
)

// The tables here are read but not acted on. Tuning and scanning need the
// network and the service description and nothing else; a program guide, the
// time, the broadcaster list and a program's own composition are somebody
// else's work. They are parsed anyway so that a caller holding a capture can
// see everything a stream carries, and so that the two clients read the same
// bytes the same way.

// Section identifiers, as STD-B10 and ISO/IEC 13818-1 assign them.
const (
	TableIDPAT             = 0x00
	TableIDCAT             = 0x01
	TableIDPMT             = 0x02
	TableIDSDTActual       = 0x42
	TableIDSDTOther        = 0x46
	TableIDEITActual       = 0x4e
	TableIDEITOther        = 0x4f
	TableIDEITScheduleLow  = 0x50
	TableIDEITScheduleHigh = 0x6f
	TableIDTDT             = 0x70
	TableIDTOT             = 0x73
	TableIDBIT             = 0xc4
)

// Descriptor is one descriptor as a transport stream writes it: a one-byte tag
// and a body whose length is one byte. The body is left as it arrived. A
// reader that knows the tag decodes it; the rest are kept so that nothing a
// table carried is thrown away silently.
type Descriptor struct {
	Tag  byte   `json:"tag"`
	Body []byte `json:"body"`
}

// ParseDescriptors reads a descriptor loop. A descriptor running past the end
// of the loop ends the reading: what follows it cannot be found without the
// length that was wrong.
func ParseDescriptors(buffer []byte) []Descriptor {
	var described []Descriptor
	for at := 0; at+2 <= len(buffer); {
		length := int(buffer[at+1])
		next := at + 2 + length
		if next > len(buffer) {
			return described
		}
		described = append(described, Descriptor{Tag: buffer[at], Body: buffer[at+2 : next]})
		at = next
	}
	return described
}

// FindDescriptor is the descriptor with this tag, or nothing. Where a loop
// carries the tag more than once the first is taken, which is what a table
// means by repeating a tag it defines once.
func FindDescriptor(described []Descriptor, tag byte) (Descriptor, bool) {
	for _, one := range described {
		if one.Tag == tag {
			return one, true
		}
	}
	return Descriptor{}, false
}

// sectionBody is the part of a long-form section between the header and the
// check: the fields a table defines for itself. The extension, version and
// numbering are returned with it because every long-form table has them.
type sectionBody struct {
	TableID    byte
	Extension  uint16
	Version    byte
	Number     byte
	LastNumber byte
	Body       []byte
}

func parseLongSection(section []byte) (sectionBody, bool) {
	if len(section) < 12 || section[1]&0x80 == 0 {
		return sectionBody{}, false
	}
	length := int(binary.BigEndian.Uint16(section[1:3]) & 0x0fff)
	end := 3 + length
	if end > len(section) || length < 9 {
		return sectionBody{}, false
	}
	return sectionBody{
		TableID:    section[0],
		Extension:  binary.BigEndian.Uint16(section[3:5]),
		Version:    section[5] >> 1 & 0x1f,
		Number:     section[6],
		LastNumber: section[7],
		Body:       section[8 : end-4],
	}, true
}

// ProgramMap is one program's composition: where its clock is and which
// elementary streams it is made of.
type ProgramMap struct {
	ProgramNumber uint16             `json:"program_number"`
	Version       byte               `json:"version"`
	PCRPID        uint16             `json:"pcr_pid"`
	Descriptors   []Descriptor       `json:"descriptors,omitempty"`
	Streams       []ElementaryStream `json:"streams,omitempty"`
}

// ElementaryStream is one stream of a program: what it carries and where.
type ElementaryStream struct {
	Type        byte         `json:"stream_type"`
	PID         uint16       `json:"pid"`
	Descriptors []Descriptor `json:"descriptors,omitempty"`
}

// ParseProgramMap reads a PMT section.
func ParseProgramMap(section []byte) *ProgramMap {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDPMT || len(header.Body) < 4 {
		return nil
	}
	body := header.Body
	programLength := int(binary.BigEndian.Uint16(body[2:4]) & 0x0fff)
	if 4+programLength > len(body) {
		return nil
	}
	described := &ProgramMap{
		ProgramNumber: header.Extension,
		Version:       header.Version,
		PCRPID:        binary.BigEndian.Uint16(body[0:2]) & 0x1fff,
		Descriptors:   ParseDescriptors(body[4 : 4+programLength]),
	}
	for at := 4 + programLength; at+5 <= len(body); {
		length := int(binary.BigEndian.Uint16(body[at+3:at+5]) & 0x0fff)
		next := at + 5 + length
		if next > len(body) {
			return described
		}
		described.Streams = append(described.Streams, ElementaryStream{
			Type:        body[at],
			PID:         binary.BigEndian.Uint16(body[at+1:at+3]) & 0x1fff,
			Descriptors: ParseDescriptors(body[at+5 : next]),
		})
		at = next
	}
	return described
}

// ConditionalAccess is the CAT: where the entitlement messages for the whole
// stream are, as descriptors that name their identifiers and packets.
type ConditionalAccess struct {
	Version     byte         `json:"version"`
	Descriptors []Descriptor `json:"descriptors,omitempty"`
}

// ParseConditionalAccess reads a CAT section.
func ParseConditionalAccess(section []byte) *ConditionalAccess {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDCAT {
		return nil
	}
	return &ConditionalAccess{Version: header.Version, Descriptors: ParseDescriptors(header.Body)}
}

// ServiceDescription is an SDT, the table that names services. It comes in two
// kinds: one for the stream carrying it and one for a neighbour, told apart by
// the table identifier and not by anything inside.
type ServiceDescription struct {
	TableID           byte               `json:"table_id"`
	TransportStreamID uint16             `json:"transport_stream_id"`
	OriginalNetworkID uint16             `json:"original_network_id"`
	Version           byte               `json:"version"`
	Services          []DescribedService `json:"services,omitempty"`
}

// Actual says whether the table describes the stream that carried it.
func (table *ServiceDescription) Actual() bool { return table.TableID == TableIDSDTActual }

// DescribedService is one service of an SDT, with the descriptors that name it
// left for the caller to read.
type DescribedService struct {
	ServiceID           uint16       `json:"service_id"`
	EITSchedule         bool         `json:"eit_schedule"`
	EITPresentFollowing bool         `json:"eit_present_following"`
	RunningStatus       byte         `json:"running_status"`
	FreeCAMode          bool         `json:"free_ca_mode"`
	Descriptors         []Descriptor `json:"descriptors,omitempty"`
}

// ParseServiceDescription reads an SDT section of either kind.
func ParseServiceDescription(section []byte) *ServiceDescription {
	header, ok := parseLongSection(section)
	if !ok || (header.TableID != TableIDSDTActual && header.TableID != TableIDSDTOther) || len(header.Body) < 3 {
		return nil
	}
	body := header.Body
	table := &ServiceDescription{
		TableID:           header.TableID,
		TransportStreamID: header.Extension,
		OriginalNetworkID: binary.BigEndian.Uint16(body[0:2]),
		Version:           header.Version,
	}
	for at := 3; at+5 <= len(body); {
		length := int(binary.BigEndian.Uint16(body[at+3:at+5]) & 0x0fff)
		next := at + 5 + length
		if next > len(body) {
			return table
		}
		table.Services = append(table.Services, DescribedService{
			ServiceID:           binary.BigEndian.Uint16(body[at : at+2]),
			EITSchedule:         body[at+2]&0x02 != 0,
			EITPresentFollowing: body[at+2]&0x01 != 0,
			RunningStatus:       body[at+3] >> 5,
			// See the note in AnalyzePSI: set means the service is under
			// conditional access, so true is a pay service.
			FreeCAMode:  body[at+3]&0x10 != 0,
			Descriptors: ParseDescriptors(body[at+5 : next]),
		})
		at = next
	}
	return table
}

// EventTable is an EIT: what a service is showing now, next, or on a day in
// its schedule.
type EventTable struct {
	TableID                  byte    `json:"table_id"`
	ServiceID                uint16  `json:"service_id"`
	TransportStreamID        uint16  `json:"transport_stream_id"`
	OriginalNetworkID        uint16  `json:"original_network_id"`
	Version                  byte    `json:"version"`
	SectionNumber            byte    `json:"section_number"`
	LastSectionNumber        byte    `json:"last_section_number"`
	SegmentLastSectionNumber byte    `json:"segment_last_section_number"`
	LastTableID              byte    `json:"last_table_id"`
	Events                   []Event `json:"events,omitempty"`
}

// Actual says whether the events belong to the stream that carried the table.
func (table *EventTable) Actual() bool {
	return table.TableID == TableIDEITActual ||
		(table.TableID >= TableIDEITScheduleLow && table.TableID <= 0x5f)
}

// Schedule says whether the table is a day of the schedule rather than the
// present and following pair.
func (table *EventTable) Schedule() bool {
	return table.TableID >= TableIDEITScheduleLow && table.TableID <= TableIDEITScheduleHigh
}

// Event is one program. A start or a duration every bit of which is set is the
// table saying it does not know, which is why both are reported as told.
type Event struct {
	EventID       uint16        `json:"event_id"`
	Start         time.Time     `json:"start"`
	StartKnown    bool          `json:"start_known"`
	Duration      time.Duration `json:"duration"`
	DurationKnown bool          `json:"duration_known"`
	RunningStatus byte          `json:"running_status"`
	FreeCAMode    bool          `json:"free_ca_mode"`
	Descriptors   []Descriptor  `json:"descriptors,omitempty"`
}

// ParseEventTable reads an EIT section of any of its kinds.
func ParseEventTable(section []byte) *EventTable {
	header, ok := parseLongSection(section)
	if !ok || header.TableID < TableIDEITActual || header.TableID > TableIDEITScheduleHigh || len(header.Body) < 6 {
		return nil
	}
	body := header.Body
	table := &EventTable{
		TableID:                  header.TableID,
		ServiceID:                header.Extension,
		TransportStreamID:        binary.BigEndian.Uint16(body[0:2]),
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
		table.Events = append(table.Events, Event{
			EventID:       binary.BigEndian.Uint16(body[at : at+2]),
			Start:         start,
			StartKnown:    startKnown,
			Duration:      duration,
			DurationKnown: durationKnown,
			RunningStatus: body[at+10] >> 5,
			FreeCAMode:    body[at+10]&0x10 != 0,
			Descriptors:   ParseDescriptors(body[at+12 : next]),
		})
		at = next
	}
	return table
}

// TimeTable is the time the stream says it is: the date table on its own, or
// the offset table which carries the local offsets with it.
type TimeTable struct {
	TableID     byte         `json:"table_id"`
	Time        time.Time    `json:"time"`
	Descriptors []Descriptor `json:"descriptors,omitempty"`
}

// ParseTimeTable reads a TDT or a TOT section. Neither is a long-form section:
// the date table has no fields past the time, and the offset table follows it
// with a descriptor loop and a check.
func ParseTimeTable(section []byte) *TimeTable {
	if len(section) < 8 || (section[0] != TableIDTDT && section[0] != TableIDTOT) {
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
	table := &TimeTable{TableID: section[0], Time: moment}
	if section[0] == TableIDTDT {
		return table
	}
	if len(section) < 12 {
		return nil
	}
	descriptorsLength := int(binary.BigEndian.Uint16(section[8:10]) & 0x0fff)
	if 10+descriptorsLength > 3+length {
		return table
	}
	table.Descriptors = ParseDescriptors(section[10 : 10+descriptorsLength])
	return table
}

// BroadcasterInformation is the BIT, which groups a network's services under
// the broadcasters that run them.
type BroadcasterInformation struct {
	OriginalNetworkID uint16        `json:"original_network_id"`
	Version           byte          `json:"version"`
	ViewPropriety     bool          `json:"broadcast_view_propriety"`
	Descriptors       []Descriptor  `json:"descriptors,omitempty"`
	Broadcasters      []Broadcaster `json:"broadcasters,omitempty"`
}

// Broadcaster is one broadcaster of the BIT.
type Broadcaster struct {
	BroadcasterID byte         `json:"broadcaster_id"`
	Descriptors   []Descriptor `json:"descriptors,omitempty"`
}

// ParseBroadcasterInformation reads a BIT section.
func ParseBroadcasterInformation(section []byte) *BroadcasterInformation {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDBIT || len(header.Body) < 2 {
		return nil
	}
	body := header.Body
	length := int(binary.BigEndian.Uint16(body[0:2]) & 0x0fff)
	if 2+length > len(body) {
		return nil
	}
	table := &BroadcasterInformation{
		OriginalNetworkID: header.Extension,
		Version:           header.Version,
		ViewPropriety:     body[0]&0x10 != 0,
		Descriptors:       ParseDescriptors(body[2 : 2+length]),
	}
	for at := 2 + length; at+3 <= len(body); {
		loop := int(binary.BigEndian.Uint16(body[at+1:at+3]) & 0x0fff)
		next := at + 3 + loop
		if next > len(body) {
			return table
		}
		table.Broadcasters = append(table.Broadcasters, Broadcaster{
			BroadcasterID: body[at],
			Descriptors:   ParseDescriptors(body[at+3 : next]),
		})
		at = next
	}
	return table
}

// ParseBroadcastTime reads the five bytes a table writes a moment in: the day
// as a modified Julian date and the time of day as binary-coded decimal. The
// zone is the broadcaster's own -- Japan writes local time, DVB writes UTC --
// so the moment is returned as the wall clock it was written as, and whose
// clock that is belongs to whoever knows the network.
func ParseBroadcastTime(field []byte) (time.Time, bool) {
	if len(field) < 5 {
		return time.Time{}, false
	}
	undefined := true
	for _, value := range field[:5] {
		if value != 0xff {
			undefined = false
		}
	}
	if undefined {
		return time.Time{}, false
	}
	day := int(binary.BigEndian.Uint16(field[0:2]))
	hour, ok := fromBCD(field[2])
	minute, minuteOK := fromBCD(field[3])
	second, secondOK := fromBCD(field[4])
	if !ok || !minuteOK || !secondOK {
		return time.Time{}, false
	}
	// Day 0 of the modified Julian date is 17 November 1858.
	epoch := time.Date(1858, time.November, 17, 0, 0, 0, 0, time.UTC)
	return epoch.AddDate(0, 0, day).Add(
		time.Duration(hour)*time.Hour +
			time.Duration(minute)*time.Minute +
			time.Duration(second)*time.Second), true
}

// parseDuration reads the three binary-coded decimal bytes a table writes a
// length of time in.
func parseDuration(field []byte) (time.Duration, bool) {
	if len(field) < 3 {
		return 0, false
	}
	if field[0] == 0xff && field[1] == 0xff && field[2] == 0xff {
		return 0, false
	}
	hour, ok := fromBCD(field[0])
	minute, minuteOK := fromBCD(field[1])
	second, secondOK := fromBCD(field[2])
	if !ok || !minuteOK || !secondOK {
		return 0, false
	}
	return time.Duration(hour)*time.Hour +
		time.Duration(minute)*time.Minute +
		time.Duration(second)*time.Second, true
}

// fromBCD reads one byte of binary-coded decimal. A nibble above nine is not a
// digit, and a table writing one has written something this is not.
func fromBCD(value byte) (int, bool) {
	high, low := int(value>>4), int(value&0x0f)
	if high > 9 || low > 9 {
		return 0, false
	}
	return high*10 + low, true
}
