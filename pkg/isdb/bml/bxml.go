// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package bml reads how data broadcasting rides a stream: the BML documents
// and their files in the DSM-CC carousel of a transport stream (ARIB STD-B24
// Volume 2 for the encoding, Volume 3 for the transmission, as operated by
// TR-B14 and TR-B15), and the data transmission of the advanced services
// (STD-B60, operated by TR-B39).
//
// The carousel itself is in package mpegts: it hands back modules, whole, and
// says nothing about them. What is here is what ARIB writes into the bytes
// around a module: the descriptor in the programme map that says a stream is a
// data broadcast and which document starts it (AdditionalInfo), the
// descriptors of a module that say what it is and whether it is compressed
// (Describe), the multipart body that packs several files into one module
// (Files), and the events sent in stream descriptors (Events).
package bml

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"strings"

	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/mpegts"
)

// DescriptorDataComponent is the data component descriptor's tag in a
// programme map.
const DescriptorDataComponent = 0xfd

// DataComponentCProfile is the data_component_id of the C profile: the
// one-segment service, which starts only when asked.
const DataComponentCProfile uint16 = 0x000d

// IsBML reports whether a data_component_id is one BML is broadcast under:
// terrestrial in two profiles, BS and CS. STD-B10 Part 2 Annex J Table J-1.
func IsBML(dataComponentID uint16) bool {
	switch dataComponentID {
	case 0x0007, 0x000b, 0x000c, 0x000d:
		return true
	}
	return false
}

// DataComponent is the data component descriptor: what coding the stream is,
// and the bytes the coding adds.
type DataComponent struct {
	ID         uint16
	Additional []byte
}

// FindDataComponent reads the data component descriptor out of a stream's
// descriptors.
func FindDataComponent(described []isdb.Descriptor) (DataComponent, bool) {
	found, ok := isdb.FindDescriptor(described, DescriptorDataComponent)
	if !ok || len(found.Body) < 2 {
		return DataComponent{}, false
	}
	return DataComponent{
		ID:         binary.BigEndian.Uint16(found.Body),
		Additional: found.Body[2:],
	}, true
}

// AdditionalInfo is what the additional information of a BML stream says.
// STD-B24 Volume 2 Part 2 clause 9.3, additional_arib_bxml_info.
type AdditionalInfo struct {
	// TransmissionFormat 0 is the carousel and event messages, which is all
	// that is broadcast.
	TransmissionFormat byte
	// EntryPoint says this is the component the service starts from. It is
	// the one under component tag 0x40, and its startup document is what a
	// receiver opens first.
	EntryPoint bool
	// AutoStart says the entry point starts by itself, rather than waiting
	// for the viewer to ask.
	AutoStart bool
	// DocumentResolution is the field as the standard numbers it; see
	// Resolution.
	DocumentResolution byte
	Independent        bool
	StyleForTV         bool
	// BMLVersion is the major and minor version.
	BMLVersion [2]uint16
	// Of the carousel: which event of the stream the data belongs to, and
	// whether the stream descriptors carry events.
	DataEventID       byte
	EventSection      bool
	OndemandRetrieval bool
	FileStorable      bool
	StartPriority     byte
}

// Resolution is the pixels a document of this stream is laid out for, and
// whether the picture is wide. ok is false where the stream says nothing or
// leaves it to the document (the C profile).
func (info AdditionalInfo) Resolution() (width, height uint32, wide, ok bool) {
	switch info.DocumentResolution {
	case 0b0001:
		return 1920, 1080, true, true
	case 0b0010:
		return 1280, 720, true, true
	case 0b0011:
		return 960, 540, true, true
	case 0b0100:
		return 640, 480, true, true
	case 0b0101:
		return 640, 480, false, true
	case 0b0110:
		return 320, 240, false, true
	}
	return 0, 0, false, false
}

// ParseAdditionalInfo reads the additional information of a BML stream.
func ParseAdditionalInfo(data []byte) (AdditionalInfo, bool) {
	if len(data) == 0 {
		return AdditionalInfo{}, false
	}
	first := data[0]
	info := AdditionalInfo{
		TransmissionFormat: first >> 6,
		EntryPoint:         first&0x20 != 0,
		BMLVersion:         [2]uint16{1, 0},
	}
	at := 1
	if info.EntryPoint {
		info.AutoStart = first&0x10 != 0
		info.DocumentResolution = first & 0x0f
		if at >= len(data) {
			return AdditionalInfo{}, false
		}
		second := data[at]
		useXML := second&0x80 != 0
		defaultVersion := second&0x40 != 0
		info.Independent = second&0x20 != 0
		info.StyleForTV = second&0x10 != 0
		at++
		if !defaultVersion {
			if at+4 > len(data) {
				return AdditionalInfo{}, false
			}
			info.BMLVersion = [2]uint16{
				binary.BigEndian.Uint16(data[at:]),
				binary.BigEndian.Uint16(data[at+2:]),
			}
			at += 4
			if useXML {
				at += 4
			}
		}
	}
	if info.TransmissionFormat == 0 {
		if at+2 > len(data) {
			return AdditionalInfo{}, false
		}
		info.DataEventID = data[at] >> 4
		info.EventSection = data[at]&0x08 != 0
		info.OndemandRetrieval = data[at+1]&0x80 != 0
		info.FileStorable = data[at+1]&0x40 != 0
		info.StartPriority = data[at+1] >> 5 & 1
	}
	return info, true
}

// Described is what the descriptors a DII gives a module say. STD-B24 Volume 3
// Part 3 clause 6.2.3; only the type and the compression are operated (TR-B14
// Volume 3 Table 4-4).
type Described struct {
	// ContentType is the Type descriptor: a media type, multipart/mixed for a
	// module of several files. Empty where none was sent.
	ContentType string
	HasType     bool
	// The Compression Type descriptor: the method (0 is zlib) and how long
	// the module is once unpacked.
	Compressed      bool
	CompressionType byte
	OriginalSize    uint32
}

const (
	moduleDescriptorType        = 0x01
	moduleDescriptorCompression = 0xc2
	// In the private data of the DII, whether the documents go back to the
	// entry point when the data event changes.
	diiDescriptorPrivate = 0xf0
)

// Describe reads a module's descriptors.
func Describe(info []byte) Described {
	var described Described
	for _, descriptor := range isdb.ParseDescriptors(info) {
		switch {
		case descriptor.Tag == moduleDescriptorType:
			described.ContentType = strings.ToValidUTF8(string(descriptor.Body), "�")
			described.HasType = true
		case descriptor.Tag == moduleDescriptorCompression && len(descriptor.Body) >= 5:
			described.Compressed = true
			described.CompressionType = descriptor.Body[0]
			described.OriginalSize = binary.BigEndian.Uint32(descriptor.Body[1:])
		}
	}
	return described
}

// ReturnToEntry is the return_to_entry_flag of the DII's private data, where
// it was sent.
func ReturnToEntry(privateData []byte) (flag, ok bool) {
	found, ok := isdb.FindDescriptor(isdb.ParseDescriptors(privateData), diiDescriptorPrivate)
	if !ok || len(found.Body) == 0 {
		return false, false
	}
	return found.Body[0]&0x80 != 0, true
}

// MostUnpacked is the most a module unpacks to.
const MostUnpacked = 16 * 1024 * 1024

// Unpack gives a module's bytes as the documents see them: unpacked, where the
// DII said the module was compressed. ok is false where it said so and the
// bytes do not unpack, or unpack to another length than it said.
func Unpack(described Described, data []byte) ([]byte, bool) {
	if !described.Compressed {
		return data, true
	}
	if described.CompressionType != 0 {
		return nil, false
	}
	// No more than the DII said, and never more than a module can be: a few
	// bytes that unpack to gigabytes are not a module.
	most := MostUnpacked
	if described.OriginalSize != 0 && int64(described.OriginalSize) < int64(most) {
		most = int(described.OriginalSize)
	}
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	out, err := io.ReadAll(io.LimitReader(reader, int64(most)+1))
	if err != nil || len(out) > most {
		return nil, false
	}
	if described.OriginalSize != 0 && len(out) != int(described.OriginalSize) {
		return nil, false
	}
	return out, true
}

// File is one file of a module.
type File struct {
	// Location is the Content-Location, the name a document refers to it by:
	// empty for a module that is one file with no name.
	Location string
	// ContentType is the media type, lower-cased, without its parameters.
	ContentType string
	Body        []byte
}

// Files are the files of a module: the parts of a multipart/mixed body, or the
// module itself as one file. STD-B24 Volume 2 Part 2 clause 9.
//
// A module of several files is an entity: it begins with its own headers, and
// the Content-Type among them carries the boundary the parts are cut at; the
// DII's type descriptor, where it is sent at all, says only multipart/mixed. A
// module of one file is the file's bytes with nothing in front. contentType
// empty stands for multipart/mixed.
func Files(contentType string, data []byte) []File {
	if contentType == "" {
		contentType = "multipart/mixed"
	}
	kind, _ := mediaType(contentType)
	if kind != "multipart/mixed" {
		return []File{{ContentType: kind, Body: data}}
	}
	headers, body, ok := entityHeaders(data)
	if !ok {
		return nil
	}
	var declared string
	found := false
	for _, header := range headers {
		if header.name == "content-type" {
			declared, found = header.value, true
			break
		}
	}
	if !found {
		return nil
	}
	kind, parameters := mediaType(declared)
	if kind != "multipart/mixed" {
		return nil
	}
	boundary, ok := parameters["boundary"]
	if !ok {
		return nil
	}
	delimiter := []byte("--" + boundary + "\r\n")
	next := []byte("\r\n--" + boundary + "\r\n")
	closing := []byte("\r\n--" + boundary + "--\r\n")
	var out []File
	start := bytes.Index(body, delimiter)
	if start < 0 {
		return out
	}
	at := start + len(delimiter)
	for {
		end, last := seek(body, next, at), false
		if end < 0 {
			end, last = seek(body, closing, at), true
			if end < 0 {
				return out
			}
		}
		if file, ok := entity(body[at:end]); ok {
			out = append(out, file)
		}
		if last {
			return out
		}
		at = end + len(next)
	}
}

type header struct{ name, value string }

// entityHeaders are the header lines of an entity (names lower-cased, values
// trimmed) and the bytes after the empty line that ends them.
func entityHeaders(part []byte) ([]header, []byte, bool) {
	var headers []header
	at := 0
	for {
		end := seek(part, []byte("\r\n"), at)
		if end < 0 {
			return nil, nil, false
		}
		if end == at {
			return headers, part[at+2:], true
		}
		line := part[at:end]
		at = end + 2
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		headers = append(headers, header{
			name:  strings.ToLower(strings.TrimSpace(lossy(line[:colon]))),
			value: strings.TrimSpace(lossy(line[colon+1:])),
		})
	}
}

// entity is one body part: its headers, then its bytes.
func entity(part []byte) (File, bool) {
	headers, body, ok := entityHeaders(part)
	if !ok {
		return File{}, false
	}
	var file File
	var hasLocation, hasType bool
	for _, header := range headers {
		switch header.name {
		case "content-location":
			file.Location, hasLocation = header.value, true
		case "content-type":
			file.ContentType, _ = mediaType(header.value)
			hasType = true
		}
	}
	if !hasLocation || !hasType {
		return File{}, false
	}
	file.Body = body
	return file, true
}

// mediaType is type/subtype, lower-cased, and its parameters with the quotes
// taken off the values.
func mediaType(text string) (string, map[string]string) {
	pieces := strings.Split(text, ";")
	kind := strings.ToLower(strings.TrimSpace(pieces[0]))
	parameters := map[string]string{}
	for _, piece := range pieces[1:] {
		name, value, ok := strings.Cut(piece, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if _, had := parameters[name]; !had {
			parameters[name] = value
		}
	}
	return kind, parameters
}

func seek(haystack, needle []byte, from int) int {
	if len(needle) == 0 || from > len(haystack) {
		return -1
	}
	found := bytes.Index(haystack[from:], needle)
	if found < 0 {
		return -1
	}
	return from + found
}

func lossy(data []byte) string { return strings.ToValidUTF8(string(data), "�") }

// Timing says when a general event is for.
type Timing struct {
	Kind TimingKind
	// Value is the normal play time, the time of day (MJD and JST, as the
	// tables write them) or the delay, by Kind.
	Value uint64
}

// TimingKind tells the timings apart.
type TimingKind int

const (
	// TimingImmediate is on arrival.
	TimingImmediate TimingKind = iota
	// TimingNPT is at a normal play time.
	TimingNPT
	// TimingClock is at a time of day, which is specified and not operated.
	TimingClock
	// TimingRelative is after a while, likewise.
	TimingRelative
)

// NPTReference is the NPT reference descriptor: where the stream's clock
// stands against the normal play time the events are timed in. ISO/IEC
// 13818-6.
type NPTReference struct {
	PostDiscontinuity bool
	ContentID         byte
	STC               uint64
	NPT               int64
	Scale             [2]int16
}

// GeneralEvent is the general event descriptor: a message to the documents,
// sent now or at a time.
type GeneralEvent struct {
	GroupID     uint16
	Time        Timing
	Kind        byte
	ID          uint16
	PrivateData []byte
}

// Event is one event of a stream descriptors section: exactly one of the two
// is set.
type Event struct {
	NPTReference *NPTReference
	General      *GeneralEvent
}

const (
	streamDescriptorNPTReference = 0x17
	streamDescriptorGeneralEvent = 0x40
)

// Events are the events of one stream descriptors section, with the data event
// they belong to and the group they are in (the two halves of the section's
// table_id_extension). STD-B24 Volume 3 Part 3 clause 8.
func Events(section mpegts.StreamDescriptors) (dataEventID byte, groupID uint16, events []Event) {
	dataEventID = byte(section.Extension >> 12)
	groupID = section.Extension & 0x0fff
	for _, descriptor := range isdb.ParseDescriptors(section.Descriptors) {
		if event, ok := readEvent(descriptor); ok {
			events = append(events, event)
		}
	}
	return dataEventID, groupID, events
}

func readEvent(descriptor isdb.Descriptor) (Event, bool) {
	body := descriptor.Body
	switch {
	case descriptor.Tag == streamDescriptorNPTReference && len(body) >= 18:
		npt := int64(binary.BigEndian.Uint32(body[10:]))
		if body[9]&1 != 0 {
			npt -= 1 << 32
		}
		return Event{NPTReference: &NPTReference{
			PostDiscontinuity: body[0]&0x80 != 0,
			ContentID:         body[0] & 0x7f,
			STC:               uint64(body[1]&1)<<32 | uint64(binary.BigEndian.Uint32(body[2:])),
			NPT:               npt,
			Scale: [2]int16{
				int16(binary.BigEndian.Uint16(body[14:])),
				int16(binary.BigEndian.Uint16(body[16:])),
			},
		}}, true
	case descriptor.Tag == streamDescriptorGeneralEvent && len(body) >= 11:
		var time Timing
		switch body[2] {
		case 0x00:
			time = Timing{Kind: TimingImmediate}
		case 0x01, 0x05:
			time = Timing{Kind: TimingClock, Value: uint64(body[3])<<32 | uint64(binary.BigEndian.Uint32(body[4:]))}
		case 0x02:
			time = Timing{Kind: TimingNPT, Value: uint64(body[3]&1)<<32 | uint64(binary.BigEndian.Uint32(body[4:]))}
		case 0x03:
			time = Timing{Kind: TimingRelative, Value: uint64(body[3]&0x0f)<<32 | uint64(binary.BigEndian.Uint32(body[4:]))}
		default:
			return Event{}, false
		}
		return Event{General: &GeneralEvent{
			GroupID:     binary.BigEndian.Uint16(body) >> 4,
			Time:        time,
			Kind:        body[8],
			ID:          binary.BigEndian.Uint16(body[9:]),
			PrivateData: body[11:],
		}}, true
	}
	return Event{}, false
}
