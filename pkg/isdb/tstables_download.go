// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"time"
)

// The table IDs of the download and partial transport stream tables.
const (
	TableIDSDTT = 0xc3
	TableIDCDT  = 0xc8
	TableIDSIT  = 0x7f
	TableIDDIT  = 0x7e
)

// DownloadSchedule is when a download runs.
type DownloadSchedule struct {
	Start    time.Time     `json:"start_time"`
	Duration time.Duration `json:"duration"`
}

// DownloadContent is one piece of software an SDTT announces for one maker's
// model.
type DownloadContent struct {
	Group         byte   `json:"group"`
	TargetVersion uint16 `json:"target_version"`
	NewVersion    uint16 `json:"new_version"`
	// DownloadLevel is 0 arbitrary, 1 forced.
	DownloadLevel byte `json:"download_level"`
	// VersionIndicator is 0 all versions, 1 the target version and below, 2
	// the target version and above, 3 the target version alone.
	VersionIndicator byte               `json:"version_indicator"`
	TimeShift        byte               `json:"schedule_time_shift_information"`
	Schedules        []DownloadSchedule `json:"schedules,omitempty"`
	// Descriptors are those of an SDTT; MHDescriptors those of an MH-SDTT,
	// which are MMT-SI ones with two-byte tags (ARIB STD-B60 7.4).
	Descriptors   []Descriptor    `json:"descriptors,omitempty"`
	MHDescriptors []MMTDescriptor `json:"mh_descriptors,omitempty"`
}

// SoftwareDownload is an SDTT section: which model the software is for, and
// what is offered. For an MH-SDTT TransportStreamID holds the TLV stream ID.
type SoftwareDownload struct {
	TableID           byte              `json:"table_id"`
	MakerID           byte              `json:"maker_id"`
	ModelID           byte              `json:"model_id"`
	Version           byte              `json:"version"`
	SectionNumber     byte              `json:"section_number"`
	LastSectionNumber byte              `json:"last_section_number"`
	TransportStreamID uint16            `json:"transport_stream_id"`
	OriginalNetworkID uint16            `json:"original_network_id"`
	ServiceID         uint16            `json:"service_id"`
	Contents          []DownloadContent `json:"contents,omitempty"`
}

// ParseSoftwareDownload reads an SDTT section (ARIB STD-B21 12.2.1.1).
func ParseSoftwareDownload(section []byte) *SoftwareDownload {
	return parseSoftwareDownloadTable(section, TableIDSDTT, false)
}

// parseSoftwareDownloadTable reads the body an SDTT and an MH-SDTT share (ARIB
// STD-B60 Table 7-22 has the same layout with a TLV stream ID). mh reads the
// descriptors as MMT-SI ones.
func parseSoftwareDownloadTable(section []byte, tableID byte, mh bool) *SoftwareDownload {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != tableID || len(header.Body) < 7 {
		return nil
	}
	body := header.Body
	table := &SoftwareDownload{
		TableID:           header.TableID,
		MakerID:           byte(header.Extension >> 8),
		ModelID:           byte(header.Extension),
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
		TransportStreamID: binary.BigEndian.Uint16(body),
		OriginalNetworkID: binary.BigEndian.Uint16(body[2:]),
		ServiceID:         binary.BigEndian.Uint16(body[4:]),
	}
	at := 7
	for range int(body[6]) {
		if at+8 > len(body) {
			return nil
		}
		head := body[at : at+8]
		// content_description_length 12 | reserved 4 | schedule 12 | shift 4.
		contentLength := int(binary.BigEndian.Uint16(head[4:]) >> 4)
		scheduleLength := int(binary.BigEndian.Uint16(head[6:]) >> 4)
		content := DownloadContent{
			Group:            head[0] >> 4,
			TargetVersion:    uint16(head[0]&0x0f)<<8 | uint16(head[1]),
			NewVersion:       uint16(head[2])<<4 | uint16(head[3]>>4),
			DownloadLevel:    head[3] >> 2 & 0x03,
			VersionIndicator: head[3] & 0x03,
			TimeShift:        head[7] & 0x0f,
		}
		at += 8
		if at+scheduleLength > len(body) || contentLength < scheduleLength {
			return nil
		}
		for entry := at; entry+8 <= at+scheduleLength; entry += 8 {
			start, startOK := ParseBroadcastTime(body[entry : entry+5])
			duration, durationOK := parseDuration(body[entry+5 : entry+8])
			if startOK && durationOK {
				content.Schedules = append(content.Schedules, DownloadSchedule{Start: start, Duration: duration})
			}
		}
		at += scheduleLength
		described := contentLength - scheduleLength
		if at+described > len(body) {
			return nil
		}
		if mh {
			content.MHDescriptors = ParseMMTDescriptors(body[at : at+described])
		} else {
			content.Descriptors = ParseDescriptors(body[at : at+described])
		}
		at += described
		table.Contents = append(table.Contents, content)
	}
	return table
}

// CommonData is a CDT section: one piece of common data, which in practice is
// a logo.
type CommonData struct {
	DownloadDataID    uint16 `json:"download_data_id"`
	Version           byte   `json:"version"`
	SectionNumber     byte   `json:"section_number"`
	LastSectionNumber byte   `json:"last_section_number"`
	OriginalNetworkID uint16 `json:"original_network_id"`
	// DataType 0x01 is a logo.
	DataType    byte         `json:"data_type"`
	Descriptors []Descriptor `json:"descriptors,omitempty"`
	// For a logo: the kind (0 large .. 5 small, ARIB TR-B14), the ID and the
	// version, and Data is the PNG. For anything else Data is the data module
	// as sent.
	LogoType    byte   `json:"logo_type,omitempty"`
	LogoID      uint16 `json:"logo_id,omitempty"`
	LogoVersion uint16 `json:"logo_version,omitempty"`
	Data        []byte `json:"data,omitempty"`
}

// ParseCommonData reads a CDT section (ARIB STD-B21 12.2.2.2).
func ParseCommonData(section []byte) *CommonData {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDCDT || len(header.Body) < 5 {
		return nil
	}
	body := header.Body
	described := int(binary.BigEndian.Uint16(body[3:]) & 0x0fff)
	if 5+described > len(body) {
		return nil
	}
	table := &CommonData{
		DownloadDataID:    header.Extension,
		Version:           header.Version,
		SectionNumber:     header.Number,
		LastSectionNumber: header.LastNumber,
		OriginalNetworkID: binary.BigEndian.Uint16(body),
		DataType:          body[2],
		Descriptors:       ParseDescriptors(body[5 : 5+described]),
	}
	rest := body[5+described:]
	if table.DataType != 0x01 {
		table.Data = append([]byte(nil), rest...)
		return table
	}
	if len(rest) < 7 {
		return nil
	}
	table.LogoType = rest[0]
	table.LogoID = binary.BigEndian.Uint16(rest[1:]) & 0x01ff
	table.LogoVersion = binary.BigEndian.Uint16(rest[3:]) & 0x0fff
	size := int(binary.BigEndian.Uint16(rest[5:]))
	if 7+size > len(rest) {
		return nil
	}
	table.Data = append([]byte(nil), rest[7:7+size]...)
	return table
}

// SelectedService is one service of a SIT, with the descriptors that describe
// it (a service descriptor, and the event's own descriptors).
type SelectedService struct {
	ServiceID     uint16       `json:"service_id"`
	RunningStatus byte         `json:"running_status"`
	Descriptors   []Descriptor `json:"descriptors,omitempty"`
}

// SelectionInformation is a SIT section: what a partial transport stream (a
// recording of one service) carries in place of the NIT, SDT and EIT. The
// transmission descriptors name the network (network identification) and the
// stream's rate (partial transport stream); the services carry what the SDT
// and EIT would have said.
type SelectionInformation struct {
	Version     byte              `json:"version"`
	Descriptors []Descriptor      `json:"descriptors,omitempty"`
	Services    []SelectedService `json:"services,omitempty"`
}

// ParseSelectionInformation reads a SIT section.
func ParseSelectionInformation(section []byte) *SelectionInformation {
	header, ok := parseLongSection(section)
	if !ok || header.TableID != TableIDSIT || len(header.Body) < 2 {
		return nil
	}
	body := header.Body
	described := int(binary.BigEndian.Uint16(body) & 0x0fff)
	if 2+described > len(body) {
		return nil
	}
	table := &SelectionInformation{Version: header.Version, Descriptors: ParseDescriptors(body[2 : 2+described])}
	for at := 2 + described; at+4 <= len(body); {
		length := int(binary.BigEndian.Uint16(body[at+2:]) & 0x0fff)
		next := at + 4 + length
		if next > len(body) {
			break
		}
		table.Services = append(table.Services, SelectedService{
			ServiceID:     binary.BigEndian.Uint16(body[at:]),
			RunningStatus: body[at+2] >> 4 & 0x07,
			Descriptors:   ParseDescriptors(body[at+4 : next]),
		})
		at = next
	}
	return table
}

// ParseDiscontinuityInformation reads a DIT section: whether the partial
// transport stream is about to change (transition_flag). A DIT has no check,
// so the section is taken as it is.
func ParseDiscontinuityInformation(section []byte) (transition, ok bool) {
	if len(section) < 4 || section[0] != TableIDDIT {
		return false, false
	}
	return section[3]&0x80 != 0, true
}
