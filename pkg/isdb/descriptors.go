// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
)

// The descriptors a receiver reads to know what a stream is and what a
// programme is, in both the transport stream (ARIB STD-B10) and MMT-SI
// (ARIB STD-B60) forms. The two say the same things in different shapes -- a
// wider tag, a wider length, UTF-8 where the transport stream has the
// eight-unit code -- so each reader here comes in a pair (ParseX for the
// transport stream, ParseMHX for MMT) giving back the same type.

// The transport stream's descriptor tags (ARIB STD-B10 Part 2, Table 6-1).
const (
	DescriptorTagConditionalAccess        = 0x09
	DescriptorTagServiceList              = 0x41
	DescriptorTagService                  = 0x48
	DescriptorTagShortEvent               = 0x4d
	DescriptorTagExtendedEvent            = 0x4e
	DescriptorTagComponent                = 0x50
	DescriptorTagStreamIdentifier         = 0x52
	DescriptorTagContent                  = 0x54
	DescriptorTagParentalRating           = 0x55
	DescriptorTagPartialTS                = 0x63
	DescriptorTagHierarchicalTransmission = 0xc0
	DescriptorTagDigitalCopyControl       = 0xc1
	DescriptorTagNetworkIdentification    = 0xc2
	DescriptorTagPartialTSTime            = 0xc3
	DescriptorTagAudioComponent           = 0xc4
	DescriptorTagHyperlink                = 0xc5
	DescriptorTagTargetRegion             = 0xc6
	DescriptorTagDataContent              = 0xc7
	DescriptorTagVideoDecodeControl       = 0xc8
	DescriptorTagDownloadContent          = 0xc9
	DescriptorTagCAEMMTS                  = 0xca
	DescriptorTagCAContractInfo           = 0xcb
	DescriptorTagExtendedBroadcaster      = 0xce
	DescriptorTagLogoTransmission         = 0xcf
	DescriptorTagSeries                   = 0xd5
	DescriptorTagEventGroup               = 0xd6
	DescriptorTagSIParameter              = 0xd7
	DescriptorTagBroadcasterName          = 0xd8
	DescriptorTagComponentGroup           = 0xd9
	DescriptorTagContentAvailability      = 0xde
	DescriptorTagAccessControl            = 0xf6
	DescriptorTagDataComponent            = 0xfd
)

// MMT-SI's descriptor tags (ARIB STD-B60 Table 10-1): another space from the
// transport stream's, two bytes wide.
const (
	MHDescriptorTagAssetGroup            = 0x8000
	MHDescriptorTagAccessControl         = 0x8004
	MHDescriptorTagEmergencyInformation  = 0x8007
	MHDescriptorTagEventGroup            = 0x800c
	MHDescriptorTagServiceList           = 0x800d
	MHDescriptorTagVideoComponent        = 0x8010
	MHDescriptorTagStreamIdentifier      = 0x8011
	MHDescriptorTagContent               = 0x8012
	MHDescriptorTagParentalRating        = 0x8013
	MHDescriptorTagAudioComponent        = 0x8014
	MHDescriptorTagTargetRegion          = 0x8015
	MHDescriptorTagSeries                = 0x8016
	MHDescriptorTagSIParameter           = 0x8017
	MHDescriptorTagBroadcasterName       = 0x8018
	MHDescriptorTagService               = 0x8019
	MHDescriptorTagDataCoding            = 0x8020
	MHDescriptorTagComponentGroup        = 0x8024
	MHDescriptorTagLogoTransmission      = 0x8025
	MHDescriptorTagApplicationService    = 0x8034
	MHDescriptorTagContentCopyControl    = 0x8038
	MHDescriptorTagContentUsageControl   = 0x8039
	MHDescriptorTagRelatedBroadcaster    = 0x803e
	MHDescriptorTagMultimediaServiceInfo = 0x803f
	MHDescriptorTagEmergencyNews         = 0x8040
	MHDescriptorTagCAContractInfo        = 0x8041
	// MHDescriptorTagShortEvent and the tags above 0xf000 carry a two-byte
	// length.
	MHDescriptorTagShortEvent    = 0xf001
	MHDescriptorTagExtendedEvent = 0xf002
)

// The data_component_id of the subtitle and superimposed text coding (ARIB
// STD-B24), and the same coding as the one-segment broadcast numbers it.
const (
	DataComponentCaption       = 0x0008
	DataComponentCaptionOneSeg = 0x0012
)

const (
	noSimulcast = 0xff
	noReference = 0x1fff
)

// DescriptorText is the text of a transport stream descriptor, read out of the
// eight-unit code, with the extra symbols a font may not have written as the
// broadcast's own words ([NEW], [4K] ...). Surrounding space is trimmed.
func DescriptorText(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return strings.TrimSpace(SubstituteSymbols(DecodeString(b).Text))
}

// mhText is the text of an MMT-SI descriptor, UTF-8 by definition; bytes that
// are not are replaced rather than failing the whole text.
func mhText(b []byte) string {
	return strings.TrimSpace(strings.ToValidUTF8(string(b), "�"))
}

// mhName is an MMT-SI text kept only where it says something: control
// characters and replacements at either end are dropped.
func mhName(b []byte) string {
	text := strings.ToValidUTF8(string(b), "�")
	text = strings.TrimFunc(text, func(r rune) bool { return unicode.IsControl(r) || r == '�' })
	return strings.TrimSpace(text)
}

// language is three letters as the standard writes a language or a country.
func language(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			out.WriteByte(c)
		}
	}
	return out.String()
}

var symbolWords = map[rune]string{
	'🅏': "[WC]", '🆒': "[COOL]", '🆓': "[FREE]", '🆕': "[NEW]", '🆖': "[NG]",
	'🆗': "[OK]", '🆙': "[UP!]", '🆚': "[VS]", '🆛': "[3D]", '🆜': "[2ndScr]",
	'🆝': "[2K]", '🆞': "[4K]", '🆟': "[8K]", '🆠': "[5.1]", '🆡': "[7.1]",
	'🆢': "[22.2]", '🆣': "[60P]", '🆤': "[120P]", '🆥': "[d]", '🆦': "[HC]",
	'🆧': "[HDR]", '🆨': "[Hi-Res]", '🆩': "[Lossless]", '🆪': "[SHV]",
	'🆫': "[UHD]", '🆬': "[VOD]", '🈁': "[ココ]", '🈂': "[サ]", '🉐': "[得]",
	'🉑': "[可]",
}

// SubstituteSymbols writes the marks a broadcast puts in the extra symbol set
// as the broadcast's own words, for a display whose font lacks them.
func SubstituteSymbols(text string) string {
	var out strings.Builder
	for _, r := range text {
		if word, ok := symbolWords[r]; ok {
			out.WriteString(word)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Assist says who a sound is for, out of two bits of the component type
// (ARIB STD-B10 Table 6-43, b6-b5).
type Assist byte

const (
	AssistNone Assist = iota
	// AssistVisual is the commentary for a viewer who cannot see.
	AssistVisual
	// AssistHearing is the sound for a viewer who cannot hear it as sent.
	AssistHearing
)

// AssistOf reads the two bits of a whole component type byte. The fourth value
// is reserved and read as nothing said.
func AssistOf(componentType byte) Assist {
	switch componentType >> 5 & 0x03 {
	case 0x01:
		return AssistVisual
	case 0x02:
		return AssistHearing
	}
	return AssistNone
}

// Name is what to call it for a viewer choosing between sounds; empty where the
// transmission said nothing.
func (a Assist) Name() string {
	switch a {
	case AssistVisual:
		return "解説"
	case AssistHearing:
		return "聴覚障害者用音声"
	}
	return ""
}

// AudioComponent is what an audio component descriptor (transport stream 0xC4,
// MMT 0x8014) says a sound stream is.
type AudioComponent struct {
	// Kind is the component type as sent: b7 dialogue control, b6-b5 who it is
	// for, b4-b0 the audio mode.
	Kind byte   `json:"component_type"`
	Tag  uint16 `json:"component_tag"`
	// Language is three letters; Second the second language of a dual mono
	// pair, empty where none was said.
	Language string `json:"language"`
	Second   string `json:"second_language,omitempty"`
	Main     bool   `json:"main"`
	// Name is what the transmission calls it, empty where it said nothing.
	Name string `json:"name,omitempty"`
	// Quality is the quality indicator, and Sampling the sampling frequency as
	// coded (Table 6-45).
	Quality  byte `json:"quality_indicator"`
	Sampling byte `json:"sampling_rate"`
	// Simulcast is the simulcast group, nil where the stream is not one of a
	// pair.
	Simulcast *byte `json:"simulcast_group_tag,omitempty"`
}

// DualMono reports two mono programmes in one stream (1/0 + 1/0 mode). The
// mode is the low five bits; a dual mono commentary arrives as 0x22.
func (c AudioComponent) DualMono() bool { return c.Kind&0x1f == 0x02 }

// Assist is who the sound is for.
func (c AudioComponent) Assist() Assist { return AssistOf(c.Kind) }

// DialogueControl reports whether the stream carries dialogue control
// information (b7).
func (c AudioComponent) DialogueControl() bool { return c.Kind&0x80 != 0 }

// Called is the name where it is worth reading: a mark such as [S] is not a
// name, and a text with nothing beyond ASCII is taken for a mark.
func (c AudioComponent) Called() string {
	name := strings.TrimSpace(c.Name)
	if name == "" || isListingMark(name) {
		return ""
	}
	return name
}

// Enrich takes what a description in the event table says about the same sound
// and this one does not: the text, who the sound is for, the second language.
func (c *AudioComponent) Enrich(described AudioComponent) {
	if c.Called() == "" {
		if name := described.Called(); name != "" {
			c.Name = name
		}
	}
	if c.Kind&0xe0 == 0 && described.Kind&0x1f == c.Kind&0x1f {
		c.Kind |= described.Kind & 0xe0
	}
	if c.Second == "" {
		c.Second = described.Second
	}
}

// SamplingHz is the sampling frequency declared, or false for a reserved value.
func (c AudioComponent) SamplingHz() (uint32, bool) {
	switch c.Sampling & 0x07 {
	case 0x01:
		return 16000, true
	case 0x02:
		return 22050, true
	case 0x03:
		return 24000, true
	case 0x05:
		return 32000, true
	case 0x06:
		return 44100, true
	case 0x07:
		return 48000, true
	}
	return 0, false
}

// Channels counts the channels of the arrangement out of the name Table 6-43
// gives the mode, or nought for a mode that is not there.
func (c AudioComponent) Channels() int {
	switch mode := c.Kind & 0x1f; {
	case mode == 0x01:
		return 1
	case mode == 0x02 || mode == 0x03:
		return 2
	case mode == 0x04 || mode == 0x05:
		return 3
	case mode == 0x06 || mode == 0x07:
		return 4
	case mode == 0x08:
		return 5
	case mode == 0x09:
		return 6
	case mode == 0x0a || mode == 0x0b:
		return 7
	case mode >= 0x0c && mode <= 0x0f:
		return 8
	case mode == 0x10:
		return 12
	case mode == 0x11:
		// 3/3/3-5/2/3-3/0/0.2, which is 22.2.
		return 24
	}
	return 0
}

// isListingMark reports a text that is a mark such as [S] or ［Ｓ］ rather than
// a name: nothing in it beyond ASCII once full-width ASCII is folded.
func isListingMark(text string) bool {
	for _, r := range text {
		folded := r
		switch {
		case r >= 0xff01 && r <= 0xff5e:
			folded = r - 0xff01 + 0x21
		case r == 0x3000:
			folded = ' '
		}
		if folded >= utf8.RuneSelf && !strings.ContainsRune("【】〔〕「」", r) {
			return false
		}
	}
	return true
}

// ParseAudioComponent reads an audio component descriptor (ARIB STD-B10 Table
// 6-42). The stream content and stream type are not kept: the programme map has
// said which coding it is.
func ParseAudioComponent(body []byte) (AudioComponent, bool) {
	return audioComponent(body, 0, func(b []byte) string { return DescriptorText(b) })
}

// ParseMHAudioComponent reads the MMT form (0x8014): the tag is two bytes, so
// everything after it is a byte further along, and the text is UTF-8.
func ParseMHAudioComponent(body []byte) (AudioComponent, bool) {
	return audioComponent(body, 1, mhName)
}

func audioComponent(body []byte, wide int, text func([]byte) string) (AudioComponent, bool) {
	if len(body) < 9+wide {
		return AudioComponent{}, false
	}
	tag := uint16(body[2])
	if wide == 1 {
		tag = binary.BigEndian.Uint16(body[2:4])
	}
	flags := body[5+wide]
	multi := flags&0x80 != 0
	read := AudioComponent{
		Kind:     body[1],
		Tag:      tag,
		Language: language(body[6+wide : 9+wide]),
		Main:     flags&0x40 != 0,
		Quality:  flags >> 4 & 0x03,
		Sampling: flags >> 1 & 0x07,
	}
	if simulcast := body[4+wide]; simulcast != noSimulcast {
		read.Simulcast = &simulcast
	}
	at := 9 + wide
	if multi {
		if len(body) >= 12+wide {
			read.Second = language(body[9+wide : 12+wide])
		}
		at = 12 + wide
	}
	if at < len(body) {
		read.Name = text(body[at:])
	}
	return read, true
}

// ComponentTag is the component tag of a stream, from the stream identifier
// descriptor or, failing that, the audio component descriptor.
func ComponentTag(described []Descriptor) (uint16, bool) {
	if found, ok := FindDescriptor(described, DescriptorTagStreamIdentifier); ok && len(found.Body) >= 1 {
		return uint16(found.Body[0]), true
	}
	if found, ok := FindDescriptor(described, DescriptorTagAudioComponent); ok && len(found.Body) >= 3 {
		return uint16(found.Body[2]), true
	}
	return 0, false
}

// MHComponentTag is the same for MMT, whose tags are sixteen bits.
func MHComponentTag(described []MMTDescriptor) (uint16, bool) {
	if found, ok := FindMMTDescriptor(described, MHDescriptorTagStreamIdentifier); ok && len(found.Body) >= 2 {
		return binary.BigEndian.Uint16(found.Body), true
	}
	if found, ok := FindMMTDescriptor(described, MHDescriptorTagAudioComponent); ok && len(found.Body) >= 4 {
		return binary.BigEndian.Uint16(found.Body[2:4]), true
	}
	return 0, false
}

// PictureComponent is what a component descriptor (0x50) says of a picture.
type PictureComponent struct {
	StreamContent byte   `json:"stream_content"`
	ComponentType byte   `json:"component_type"`
	Tag           uint16 `json:"component_tag"`
	Name          string `json:"name,omitempty"`
}

// StreamContentVideo is the stream_content values that mean a picture (Table
// 6-5): MPEG-2, H.264 and H.265.
var StreamContentVideo = [3]byte{0x01, 0x05, 0x09}

// ParsePictureComponent reads a component descriptor.
func ParsePictureComponent(body []byte) (PictureComponent, bool) {
	if len(body) < 6 {
		return PictureComponent{}, false
	}
	return PictureComponent{
		StreamContent: body[0] & 0x0f,
		ComponentType: body[1],
		Tag:           uint16(body[2]),
		Name:          DescriptorText(body[6:]),
	}, true
}

// VideoComponent is what an MH video component descriptor (ARIB STD-B60 Table
// 7-47) says of a picture, including whether it is HDR.
type VideoComponent struct {
	Resolution  byte   `json:"video_resolution"`
	Aspect      byte   `json:"video_aspect_ratio"`
	Progressive bool   `json:"progressive"`
	FrameRate   byte   `json:"video_frame_rate"`
	Tag         uint16 `json:"component_tag"`
	// Transfer is the transfer characteristics as coded (Table 7-51): 4 is PQ
	// and 5 is HLG; nought is unsaid.
	Transfer byte `json:"video_transfer_characteristics"`
}

// HDR reports whether the picture is HDR, and false for ok where the
// transmission did not say.
func (v VideoComponent) HDR() (hdr, ok bool) {
	switch v.Transfer {
	case 0:
		return false, false
	case 4, 5:
		return true, true
	}
	return false, true
}

// ParseMHVideoComponent reads an MH video component descriptor.
func ParseMHVideoComponent(body []byte) (VideoComponent, bool) {
	if len(body) < 5 {
		return VideoComponent{}, false
	}
	return VideoComponent{
		Resolution:  body[0] >> 4,
		Aspect:      body[0] & 0x0f,
		Progressive: body[1]&0x80 != 0,
		FrameRate:   body[1] & 0x1f,
		Tag:         binary.BigEndian.Uint16(body[2:4]),
		Transfer:    body[4] >> 4,
	}, true
}

// Hierarchy is where a stream sits in a rain-fade pair: level nought for the
// picture the transmission leads with in both transmissions.
type Hierarchy struct {
	Level byte `json:"level"`
	// Reference is the other stream of the pair, where a transport stream named
	// it; Group the MMT asset group.
	Reference *uint16 `json:"reference_pid,omitempty"`
	Group     *byte   `json:"group,omitempty"`
}

// ParseHierarchicalTransmission reads a hierarchical transmission descriptor
// (0xC0). Its quality level is one for the picture to show; it is turned over
// so that nought means the one to play, as in MMT.
func ParseHierarchicalTransmission(body []byte) (Hierarchy, bool) {
	if len(body) < 3 {
		return Hierarchy{}, false
	}
	read := Hierarchy{}
	if body[0]&0x01 == 0 {
		read.Level = 1
	}
	if pid := uint16(body[1]&0x1f)<<8 | uint16(body[2]); pid != noReference {
		read.Reference = &pid
	}
	return read, true
}

// ParseAssetGroup reads an asset group descriptor (MMT 0x8000): the group and
// the place in it, nought first.
func ParseAssetGroup(body []byte) (Hierarchy, bool) {
	if len(body) < 2 {
		return Hierarchy{}, false
	}
	group := body[0]
	return Hierarchy{Level: body[1], Group: &group}, true
}

// ParseConditionalAccessDescriptor reads a conditional access descriptor
// (0x09): the system and the PID its messages ride on.
func ParseConditionalAccessDescriptor(body []byte) (system, pid uint16, ok bool) {
	if len(body) < 4 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint16(body), binary.BigEndian.Uint16(body[2:]) & 0x1fff, true
}

// ParseDataComponentID reads the data_component_id of a data component
// descriptor (0xFD) or a data content descriptor (0xC7).
func ParseDataComponentID(body []byte) (uint16, bool) {
	if len(body) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(body), true
}

// EMTLocation is where one event message table rides.
type EMTLocation struct {
	Tag      byte   `json:"emt_tag"`
	PacketID uint16 `json:"packet_id"`
}

// ApplicationService is the application service descriptor (ARIB STD-B60 Table
// 7-87): where a data broadcast's entry, files and event messages ride.
type ApplicationService struct {
	Resolution byte   `json:"document_resolution"`
	AIT        uint16 `json:"ait_packet_id"`
	// Data is the packet ID of the data transmission messages, nil where the
	// flag says there are none.
	Data *uint16       `json:"data_transmission_packet_id,omitempty"`
	EMTs []EMTLocation `json:"emts,omitempty"`
}

// EMTTag is the tag of the event message table on this packet ID.
func (a ApplicationService) EMTTag(packetID uint16) (byte, bool) {
	for _, emt := range a.EMTs {
		if emt.PacketID == packetID {
			return emt.Tag, true
		}
	}
	return 0, false
}

// ParseApplicationService reads an application service descriptor. Only
// location type 0 (a packet ID in this stream) is operated; any other ends the
// reading, since its length depends on its type.
func ParseApplicationService(body []byte) (ApplicationService, bool) {
	if len(body) < 3 {
		return ApplicationService{}, false
	}
	location := func(at *int) (uint16, bool) {
		if *at+3 > len(body) || body[*at] != 0x00 {
			return 0, false
		}
		id := binary.BigEndian.Uint16(body[*at+1:])
		*at += 3
		return id, true
	}
	at := 3
	ait, ok := location(&at)
	if !ok {
		return ApplicationService{}, false
	}
	read := ApplicationService{Resolution: body[1] >> 4, AIT: ait}
	if body[2]&0x40 != 0 {
		if data, ok := location(&at); ok {
			read.Data = &data
		}
	}
	for range int(body[2] & 0x0f) {
		if at >= len(body) {
			break
		}
		tag := body[at]
		at++
		id, ok := location(&at)
		if !ok {
			break
		}
		read.EMTs = append(read.EMTs, EMTLocation{Tag: tag, PacketID: id})
	}
	return read, true
}

// ServiceNaming is what a service descriptor says: the kind of service, who
// provides it and what it is called.
type ServiceNaming struct {
	Kind     byte   `json:"service_type"`
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name"`
}

// ParseServiceDescriptor reads a service descriptor (0x48).
func ParseServiceDescriptor(body []byte) (ServiceNaming, bool) {
	kind, provider, name, ok := serviceFields(body)
	if !ok {
		return ServiceNaming{}, false
	}
	return ServiceNaming{Kind: kind, Provider: DescriptorText(provider), Name: DescriptorText(name)}, true
}

// ParseMHServiceDescriptor reads an MH service descriptor (0x8019): the same
// layout with the text in UTF-8.
func ParseMHServiceDescriptor(body []byte) (ServiceNaming, bool) {
	kind, provider, name, ok := serviceFields(body)
	if !ok {
		return ServiceNaming{}, false
	}
	return ServiceNaming{Kind: kind, Provider: mhText(provider), Name: mhText(name)}, true
}

func serviceFields(body []byte) (byte, []byte, []byte, bool) {
	if len(body) < 3 {
		return 0, nil, nil, false
	}
	provider := int(body[1])
	if 2+provider >= len(body) {
		return 0, nil, nil, false
	}
	name := int(body[2+provider])
	from := 3 + provider
	if from+name > len(body) {
		return 0, nil, nil, false
	}
	return body[0], body[2 : 2+provider], body[from : from+name], true
}

// ShortEvent is what a short event descriptor says: the title and a line of
// text.
type ShortEvent struct {
	Language string `json:"language"`
	Title    string `json:"title"`
	Text     string `json:"text,omitempty"`
}

// ParseShortEvent reads a short event descriptor (0x4D).
func ParseShortEvent(body []byte) (ShortEvent, bool) {
	if len(body) < 5 {
		return ShortEvent{}, false
	}
	title := int(body[3])
	from := 4 + title
	if from >= len(body) {
		return ShortEvent{}, false
	}
	at := from + 1
	detail := int(body[from])
	if at+detail > len(body) {
		return ShortEvent{}, false
	}
	return ShortEvent{
		Language: language(body[:3]),
		Title:    DescriptorText(body[4 : 4+title]),
		Text:     DescriptorText(body[at : at+detail]),
	}, true
}

// ParseMHShortEvent reads an MH short event descriptor (0xF001), whose text
// length is two bytes.
func ParseMHShortEvent(body []byte) (ShortEvent, bool) {
	if len(body) < 4 {
		return ShortEvent{}, false
	}
	title := int(body[3])
	from := 4 + title
	if from+2 > len(body) {
		return ShortEvent{}, false
	}
	detail := int(binary.BigEndian.Uint16(body[from:]))
	at := from + 2
	if at+detail > len(body) {
		return ShortEvent{}, false
	}
	return ShortEvent{
		Language: language(body[:3]),
		Title:    mhText(body[4 : 4+title]),
		Text:     mhText(body[at : at+detail]),
	}, true
}

// ExtendedEventItem is one headed item of an extended event descriptor.
type ExtendedEventItem struct {
	Heading string `json:"heading"`
	Value   string `json:"value"`
}

// ExtendedEventRaw is the same before decoding. A descriptor holds some 220
// bytes of text, so an item may run over several: one whose heading is empty
// continues the last, and the pieces are joined as bytes before decoding,
// since the cut falls anywhere in the eight-unit code.
type ExtendedEventRaw struct {
	Heading []byte
	Value   []byte
}

// ParseExtendedEvent reads the items of one extended event descriptor (0x4E).
func ParseExtendedEvent(body []byte) []ExtendedEventItem {
	var items []ExtendedEventItem
	for _, raw := range ExtendedEventBytes(body) {
		items = append(items, ExtendedEventItem{Heading: DescriptorText(raw.Heading), Value: DescriptorText(raw.Value)})
	}
	return items
}

// ExtendedEventBytes is the items of an extended event descriptor as sent.
func ExtendedEventBytes(body []byte) []ExtendedEventRaw {
	if len(body) < 5 {
		return nil
	}
	end := 5 + int(body[4])
	if end > len(body) {
		return nil
	}
	var found []ExtendedEventRaw
	for at := 5; at+2 <= end; {
		heading := int(body[at])
		next := at + 1 + heading
		if next >= end {
			break
		}
		length := int(body[next])
		from := next + 1
		if from+length > end {
			break
		}
		found = append(found, ExtendedEventRaw{Heading: body[at+1 : next], Value: body[from : from+length]})
		at = from + length
	}
	return found
}

// JoinExtendedEvents reads the items of every extended event descriptor of an
// event, in the order sent, joining an item that runs over several descriptors
// as bytes before decoding it.
func JoinExtendedEvents(bodies [][]byte) []ExtendedEventItem {
	var raw []ExtendedEventRaw
	for _, body := range bodies {
		for _, item := range ExtendedEventBytes(body) {
			if len(item.Heading) == 0 && len(raw) > 0 {
				last := &raw[len(raw)-1]
				last.Value = append(append([]byte(nil), last.Value...), item.Value...)
				continue
			}
			raw = append(raw, item)
		}
	}
	items := make([]ExtendedEventItem, 0, len(raw))
	for _, item := range raw {
		items = append(items, ExtendedEventItem{Heading: DescriptorText(item.Heading), Value: DescriptorText(item.Value)})
	}
	return items
}

// ParseMHExtendedEvent reads an MH extended event descriptor (0xF002): the
// length of all the items is two bytes, and so is each value's.
func ParseMHExtendedEvent(body []byte) []ExtendedEventItem {
	if len(body) < 6 {
		return nil
	}
	end := 6 + int(binary.BigEndian.Uint16(body[4:]))
	if end > len(body) {
		return nil
	}
	var found []ExtendedEventItem
	for at := 6; at+3 <= end; {
		heading := int(body[at])
		next := at + 1 + heading
		if next+2 > end {
			break
		}
		length := int(binary.BigEndian.Uint16(body[next:]))
		from := next + 2
		if from+length > end {
			break
		}
		found = append(found, ExtendedEventItem{Heading: mhText(body[at+1 : next]), Value: mhText(body[from : from+length])})
		at = from + length
	}
	return found
}

// ContentGenre is one entry of a content descriptor (0x54, MMT 0x8012).
type ContentGenre struct {
	Large  byte `json:"content_nibble_level_1"`
	Middle byte `json:"content_nibble_level_2"`
	// User is the two user nibbles, meaningful only under large genre 0xE.
	User byte `json:"user_nibble"`
}

// ParseContentDescriptor reads a content descriptor; the MH form is the same
// bytes.
func ParseContentDescriptor(body []byte) []ContentGenre {
	var genres []ContentGenre
	for at := 0; at+2 <= len(body); at += 2 {
		genres = append(genres, ContentGenre{Large: body[at] >> 4, Middle: body[at] & 0x0f, User: body[at+1]})
	}
	return genres
}

// EventGroupCommon is the group type that means the same event is carried on
// other services of the network (ARIB STD-B10 Part 2 6.2.34).
const EventGroupCommon = 1

// EventReference names an event by service and event ID.
type EventReference struct {
	ServiceID uint16 `json:"service_id"`
	EventID   uint16 `json:"event_id"`
}

// EventGroup is what an event group descriptor says.
type EventGroup struct {
	Type   byte             `json:"group_type"`
	Events []EventReference `json:"events,omitempty"`
}

// ParseEventGroup reads an event group descriptor (0xD6, MMT 0x800C).
func ParseEventGroup(body []byte) (EventGroup, bool) {
	if len(body) < 1 {
		return EventGroup{}, false
	}
	read := EventGroup{Type: body[0] >> 4}
	count := int(body[0] & 0x0f)
	for at := 1; at+4 <= len(body) && len(read.Events) < count; at += 4 {
		read.Events = append(read.Events, EventReference{
			ServiceID: binary.BigEndian.Uint16(body[at:]),
			EventID:   binary.BigEndian.Uint16(body[at+2:]),
		})
	}
	return read, true
}

// ParentalRating is one country's rating as coded.
type ParentalRating struct {
	Country string `json:"country"`
	Rating  byte   `json:"rating"`
}

// RatingAge is the minimum age a rating means: 0x01-0x0F is the age less three;
// nought is unsaid and the rest the broadcaster's own.
func RatingAge(rating byte) (byte, bool) {
	if rating >= 1 && rating <= 0x0f {
		return rating + 3, true
	}
	return 0, false
}

// ParseParentalRating reads a parental rating descriptor (0x55, MMT 0x8013).
func ParseParentalRating(body []byte) []ParentalRating {
	var ratings []ParentalRating
	for at := 0; at+4 <= len(body); at += 4 {
		ratings = append(ratings, ParentalRating{Country: language(body[at : at+3]), Rating: body[at+3]})
	}
	return ratings
}

// ComponentCopyControl is one component's copy control.
type ComponentCopyControl struct {
	Tag     uint16 `json:"component_tag"`
	Control byte   `json:"digital_recording_control_data"`
	// MaximumBitrate is in units of a quarter megabit, nil where not given.
	MaximumBitrate *byte `json:"maximum_bitrate,omitempty"`
}

// CopyControl is what a digital copy control descriptor (0xC1, MMT content copy
// control 0x8038) says of the whole and of each component.
type CopyControl struct {
	Control        byte                   `json:"digital_recording_control_data"`
	MaximumBitrate *byte                  `json:"maximum_bitrate,omitempty"`
	Components     []ComponentCopyControl `json:"components,omitempty"`
}

// CopyControlName says a digital_recording_control_data value in the
// standard's words (Table 6-39).
func CopyControlName(control byte) string {
	switch control & 0x03 {
	case 0:
		return "制約条件なしにコピー可"
	case 1:
		return "事業者定義"
	case 2:
		return "一世代のみコピー可"
	}
	return "コピー禁止"
}

// ParseDigitalCopyControl reads a digital copy control descriptor (ARIB
// STD-B10 6.2.23).
func ParseDigitalCopyControl(body []byte) (CopyControl, bool) {
	return copyControl(body, 1, 1)
}

// ParseMHContentCopyControl reads a content copy control descriptor (ARIB
// STD-B60 7.4.3.41): a reserved byte after the flags and two-byte tags.
func ParseMHContentCopyControl(body []byte) (CopyControl, bool) {
	return copyControl(body, 2, 2)
}

func copyControl(body []byte, start, tagBytes int) (CopyControl, bool) {
	if len(body) < 1 {
		return CopyControl{}, false
	}
	head := body[0]
	read := CopyControl{Control: head >> 6}
	at := start
	if head&0x20 != 0 {
		if at >= len(body) {
			return CopyControl{}, false
		}
		bitrate := body[at]
		read.MaximumBitrate = &bitrate
		at++
	}
	if head&0x10 != 0 {
		if at >= len(body) {
			return CopyControl{}, false
		}
		end := min(at+1+int(body[at]), len(body))
		at++
		entry := tagBytes + 1
		if tagBytes == 2 {
			// The MMT form has a reserved byte after each component's flags.
			entry = 4
		}
		for at+entry <= end {
			flags := body[at+tagBytes]
			component := ComponentCopyControl{Tag: uint16(body[at]), Control: flags >> 6}
			if tagBytes == 2 {
				component.Tag = binary.BigEndian.Uint16(body[at:])
			}
			at += entry
			if flags&0x20 != 0 {
				if at >= len(body) {
					return CopyControl{}, false
				}
				bitrate := body[at]
				component.MaximumBitrate = &bitrate
				at++
			}
			read.Components = append(read.Components, component)
		}
	}
	return read, true
}

// ContentAvailability is what a content availability descriptor (0xDE, MMT
// content usage control 0x8039) says about keeping and copying the programme.
type ContentAvailability struct {
	CopyRestriction    bool `json:"copy_restriction_mode"`
	ImageUnconstrained bool `json:"image_constraint_token"`
	NoRetention        bool `json:"retention_mode"`
	RetentionState     byte `json:"retention_state"`
	Encryption         bool `json:"encryption_mode"`
	// RemoteView is MMT only.
	RemoteView *bool `json:"remote_view_mode,omitempty"`
}

// RetentionName is how long temporary storage may keep it (Table 6-88).
func (c ContentAvailability) RetentionName() string {
	return [...]string{"無制限", "1週間", "1日", "12時間", "6時間", "3時間", "1時間30分", "予備"}[c.RetentionState&0x07]
}

// ParseContentAvailability reads a content availability descriptor (ARIB
// STD-B10 6.2.45).
func ParseContentAvailability(body []byte) (ContentAvailability, bool) {
	if len(body) < 1 {
		return ContentAvailability{}, false
	}
	flags := body[0]
	return ContentAvailability{
		CopyRestriction:    flags&0x40 != 0,
		ImageUnconstrained: flags&0x20 != 0,
		NoRetention:        flags&0x10 != 0,
		RetentionState:     flags >> 1 & 0x07,
		Encryption:         flags&0x01 != 0,
	}, true
}

// ParseMHContentUsageControl reads a content usage control descriptor (ARIB
// STD-B60 7.4.3.42).
func ParseMHContentUsageControl(body []byte) (ContentAvailability, bool) {
	if len(body) < 2 {
		return ContentAvailability{}, false
	}
	remote := body[0]&0x80 != 0
	return ContentAvailability{
		RemoteView:         &remote,
		CopyRestriction:    body[0]&0x40 != 0,
		ImageUnconstrained: body[0]&0x20 != 0,
		NoRetention:        body[1]&0x10 != 0,
		RetentionState:     body[1] >> 1 & 0x07,
		Encryption:         body[1]&0x01 != 0,
	}, true
}

// CAContract is what a CA contract information descriptor (0xCB, MMT 0x8041;
// ARIB STD-B25) says: which components form a charge unit and what the fee is
// called.
type CAContract struct {
	CASystemID uint16 `json:"ca_system_id"`
	// Unit is the CA_unit_id: 0 no charge, 1 the unit of the main components.
	Unit         byte     `json:"ca_unit_id"`
	Components   []uint16 `json:"component_tags,omitempty"`
	Verification []byte   `json:"contract_verification_info,omitempty"`
	FeeName      string   `json:"fee_name,omitempty"`
}

// ParseCAContractInfo reads a CA contract information descriptor.
func ParseCAContractInfo(body []byte) (CAContract, bool) {
	return caContract(body, false, func(b []byte) string { return DescriptorText(b) })
}

// ParseMHCAContractInfo reads an MH-CA contract information descriptor (ARIB
// STD-B60 7.4.3.44).
func ParseMHCAContractInfo(body []byte) (CAContract, bool) {
	return caContract(body, true, mhText)
}

func caContract(body []byte, wide bool, text func([]byte) string) (CAContract, bool) {
	if len(body) < 3 {
		return CAContract{}, false
	}
	read := CAContract{CASystemID: binary.BigEndian.Uint16(body), Unit: body[2] >> 4}
	at := 3
	for range int(body[2] & 0x0f) {
		if wide {
			if at+2 > len(body) {
				return CAContract{}, false
			}
			read.Components = append(read.Components, binary.BigEndian.Uint16(body[at:]))
			at += 2
		} else {
			if at >= len(body) {
				return CAContract{}, false
			}
			read.Components = append(read.Components, uint16(body[at]))
			at++
		}
	}
	if at >= len(body) {
		return CAContract{}, false
	}
	length := int(body[at])
	at++
	if at+length > len(body) {
		return CAContract{}, false
	}
	read.Verification = append([]byte(nil), body[at:at+length]...)
	at += length
	if at >= len(body) {
		return CAContract{}, false
	}
	length = int(body[at])
	at++
	if at+length > len(body) {
		return CAContract{}, false
	}
	read.FeeName = text(body[at : at+length])
	return read, true
}

// Series is what a series descriptor (0xD5, MMT 0x8016) says: which series the
// programme belongs to and where in it.
type Series struct {
	ID uint16 `json:"series_id"`
	// RepeatLabel tells a rerun's series from the original's; 0 is the
	// original.
	RepeatLabel byte `json:"repeat_label"`
	Pattern     byte `json:"program_pattern"`
	// ExpireMJD is when the series ends, as a modified Julian date.
	ExpireMJD *uint16 `json:"expire_date,omitempty"`
	// Episode and LastEpisode count the episodes; 0 is not counted.
	Episode     uint16 `json:"episode_number"`
	LastEpisode uint16 `json:"last_episode_number"`
	Name        string `json:"series_name,omitempty"`
}

// PatternName is the programme pattern in words (Table 6-66).
func (s Series) PatternName() string {
	return [...]string{"不定期", "帯番組", "週一回", "月一回", "一日に複数回", "長時間番組の分割", "定期・不定期の蓄積用", "予備"}[s.Pattern&0x07]
}

// ParseSeries reads a series descriptor (ARIB STD-B10 6.2.33).
func ParseSeries(body []byte) (Series, bool) {
	return series(body, func(b []byte) string { return DescriptorText(b) })
}

// ParseMHSeries reads an MH-series descriptor (ARIB STD-B60 7.4.3.25).
func ParseMHSeries(body []byte) (Series, bool) { return series(body, mhText) }

func series(body []byte, text func([]byte) string) (Series, bool) {
	if len(body) < 8 {
		return Series{}, false
	}
	read := Series{
		ID:          binary.BigEndian.Uint16(body),
		RepeatLabel: body[2] >> 4,
		Pattern:     body[2] >> 1 & 0x07,
		Episode:     uint16(body[5])<<4 | uint16(body[6]>>4),
		LastEpisode: uint16(body[6]&0x0f)<<8 | uint16(body[7]),
		Name:        text(body[8:]),
	}
	if body[2]&0x01 != 0 {
		expire := binary.BigEndian.Uint16(body[3:])
		read.ExpireMJD = &expire
	}
	return read, true
}

// ComponentGroupUnit is one charge unit of a component group: which components
// it holds.
type ComponentGroupUnit struct {
	CAUnit     byte     `json:"ca_unit_id"`
	Components []uint16 `json:"component_tags,omitempty"`
}

// ComponentGroup is one group of a component group descriptor: one view of a
// multi-view programme.
type ComponentGroup struct {
	ID    byte                 `json:"component_group_id"`
	Units []ComponentGroupUnit `json:"units,omitempty"`
	// TotalBitrate is in units of a quarter megabit, nil where not given.
	TotalBitrate *byte  `json:"total_bit_rate,omitempty"`
	Name         string `json:"name,omitempty"`
}

// ComponentGroups is what a component group descriptor (0xD9, MMT 0x8024)
// says: the groups an event is split into (group type 0 is multi-view TV).
type ComponentGroups struct {
	GroupType byte             `json:"component_group_type"`
	Groups    []ComponentGroup `json:"groups,omitempty"`
}

// ParseComponentGroup reads a component group descriptor (ARIB STD-B10 6.2.37).
func ParseComponentGroup(body []byte) (ComponentGroups, bool) {
	return componentGroups(body, false, func(b []byte) string { return DescriptorText(b) })
}

// ParseMHComponentGroup reads an MH-component group descriptor (ARIB STD-B60
// 7.4.3.33).
func ParseMHComponentGroup(body []byte) (ComponentGroups, bool) {
	return componentGroups(body, true, mhText)
}

func componentGroups(body []byte, wide bool, text func([]byte) string) (ComponentGroups, bool) {
	if len(body) < 1 {
		return ComponentGroups{}, false
	}
	head := body[0]
	read := ComponentGroups{GroupType: head >> 5}
	at := 1
	next := func() (byte, bool) {
		if at >= len(body) {
			return 0, false
		}
		at++
		return body[at-1], true
	}
	for range int(head & 0x0f) {
		b, ok := next()
		if !ok {
			return ComponentGroups{}, false
		}
		group := ComponentGroup{ID: b >> 4}
		for range int(b & 0x0f) {
			u, ok := next()
			if !ok {
				return ComponentGroups{}, false
			}
			unit := ComponentGroupUnit{CAUnit: u >> 4}
			for range int(u & 0x0f) {
				if wide {
					if at+2 > len(body) {
						return ComponentGroups{}, false
					}
					unit.Components = append(unit.Components, binary.BigEndian.Uint16(body[at:]))
					at += 2
				} else {
					c, ok := next()
					if !ok {
						return ComponentGroups{}, false
					}
					unit.Components = append(unit.Components, uint16(c))
				}
			}
			group.Units = append(group.Units, unit)
		}
		if head&0x10 != 0 {
			bitrate, ok := next()
			if !ok {
				return ComponentGroups{}, false
			}
			group.TotalBitrate = &bitrate
		}
		length, ok := next()
		if !ok || at+int(length) > len(body) {
			return ComponentGroups{}, false
		}
		group.Name = text(body[at : at+int(length)])
		at += int(length)
		read.Groups = append(read.Groups, group)
	}
	return read, true
}

// The logo transmission types.
const (
	// LogoDownloaded is a logo in the CDT, found by download data ID.
	LogoDownloaded = 0x01
	// LogoReferenced is a logo in the CDT, by logo ID alone.
	LogoReferenced = 0x02
	// LogoText is a short text in place of a picture.
	LogoText = 0x03
)

// LogoTransmission is what a logo transmission descriptor (0xCF, MMT 0x8025)
// says: where a service's logo is, or what to write in its place. Which fields
// mean anything follows Type.
type LogoTransmission struct {
	Type           byte   `json:"logo_transmission_type"`
	LogoID         uint16 `json:"logo_id,omitempty"`
	Version        uint16 `json:"logo_version,omitempty"`
	DownloadDataID uint16 `json:"download_data_id,omitempty"`
	Text           string `json:"logo_char,omitempty"`
}

// ParseLogoTransmission reads a logo transmission descriptor (ARIB STD-B10
// 6.2.44).
func ParseLogoTransmission(body []byte) (LogoTransmission, bool) {
	return logoTransmission(body, func(b []byte) string { return DescriptorText(b) })
}

// ParseMHLogoTransmission reads an MH-logo transmission descriptor (ARIB
// STD-B60 7.4.3.34).
func ParseMHLogoTransmission(body []byte) (LogoTransmission, bool) {
	return logoTransmission(body, mhText)
}

func logoTransmission(body []byte, text func([]byte) string) (LogoTransmission, bool) {
	if len(body) < 1 {
		return LogoTransmission{}, false
	}
	read := LogoTransmission{Type: body[0]}
	rest := body[1:]
	switch body[0] {
	case LogoDownloaded:
		if len(rest) < 6 {
			return LogoTransmission{}, false
		}
		read.LogoID = uint16(rest[0]&0x01)<<8 | uint16(rest[1])
		read.Version = uint16(rest[2]&0x0f)<<8 | uint16(rest[3])
		read.DownloadDataID = binary.BigEndian.Uint16(rest[4:])
	case LogoReferenced:
		if len(rest) < 2 {
			return LogoTransmission{}, false
		}
		read.LogoID = uint16(rest[0]&0x01)<<8 | uint16(rest[1])
	case LogoText:
		read.Text = text(rest)
	}
	return read, true
}

// ParseBroadcasterName reads a broadcaster name descriptor (ARIB STD-B10
// 6.2.36).
func ParseBroadcasterName(body []byte) string { return DescriptorText(body) }

// ParseMHBroadcasterName reads the MMT form (0x8018).
func ParseMHBroadcasterName(body []byte) string { return mhText(body) }

// BroadcasterReference names a broadcaster on a network.
type BroadcasterReference struct {
	NetworkID     uint16 `json:"network_id"`
	BroadcasterID byte   `json:"broadcaster_id"`
}

// ExtendedBroadcaster is what an extended broadcaster descriptor (0xCE, ARIB
// STD-B10 6.2.43) says of a terrestrial broadcaster.
type ExtendedBroadcaster struct {
	// Kind is 1 television, 2 sound; anything else carries nothing readable.
	Kind                     byte                   `json:"broadcaster_type"`
	TerrestrialBroadcasterID uint16                 `json:"terrestrial_broadcaster_id"`
	Affiliations             []byte                 `json:"affiliation_ids,omitempty"`
	Related                  []BroadcasterReference `json:"broadcasters,omitempty"`
}

// ParseExtendedBroadcaster reads an extended broadcaster descriptor.
func ParseExtendedBroadcaster(body []byte) (ExtendedBroadcaster, bool) {
	if len(body) < 1 {
		return ExtendedBroadcaster{}, false
	}
	read := ExtendedBroadcaster{Kind: body[0] >> 4}
	if read.Kind != 1 && read.Kind != 2 {
		return read, true
	}
	if len(body) < 4 {
		return ExtendedBroadcaster{}, false
	}
	read.TerrestrialBroadcasterID = binary.BigEndian.Uint16(body[1:])
	affiliations, others := int(body[3]>>4), int(body[3]&0x0f)
	at := 4
	if at+affiliations > len(body) {
		return ExtendedBroadcaster{}, false
	}
	read.Affiliations = append([]byte(nil), body[at:at+affiliations]...)
	at += affiliations
	for range others {
		if at+3 > len(body) {
			return ExtendedBroadcaster{}, false
		}
		read.Related = append(read.Related, BroadcasterReference{NetworkID: binary.BigEndian.Uint16(body[at:]), BroadcasterID: body[at+2]})
		at += 3
	}
	return read, true
}

// RelatedBroadcaster is what a related broadcaster descriptor (MMT 0x803E,
// ARIB STD-B60 7.4.3.46) says: the broadcasters on other networks that share
// this one's NVRAM.
type RelatedBroadcaster struct {
	Broadcasters     []BroadcasterReference `json:"broadcasters,omitempty"`
	Affiliations     []byte                 `json:"affiliation_ids,omitempty"`
	OriginalNetworks []uint16               `json:"original_network_ids,omitempty"`
}

// ParseRelatedBroadcaster reads a related broadcaster descriptor.
func ParseRelatedBroadcaster(body []byte) (RelatedBroadcaster, bool) {
	if len(body) < 2 {
		return RelatedBroadcaster{}, false
	}
	broadcasters, affiliations, networks := int(body[0]>>4), int(body[0]&0x0f), int(body[1]>>4)
	var read RelatedBroadcaster
	at := 2
	for range broadcasters {
		if at+3 > len(body) {
			return RelatedBroadcaster{}, false
		}
		read.Broadcasters = append(read.Broadcasters, BroadcasterReference{NetworkID: binary.BigEndian.Uint16(body[at:]), BroadcasterID: body[at+2]})
		at += 3
	}
	if at+affiliations > len(body) {
		return RelatedBroadcaster{}, false
	}
	read.Affiliations = append([]byte(nil), body[at:at+affiliations]...)
	at += affiliations
	for range networks {
		if at+2 > len(body) {
			return RelatedBroadcaster{}, false
		}
		read.OriginalNetworks = append(read.OriginalNetworks, binary.BigEndian.Uint16(body[at:]))
		at += 2
	}
	return read, true
}

// CAEMMTS is what a CA_EMM_TS descriptor (0xCA, ARIB STD-B25) says.
type CAEMMTS struct {
	CASystemID        uint16 `json:"ca_system_id"`
	TransportStreamID uint16 `json:"transport_stream_id"`
	OriginalNetworkID uint16 `json:"original_network_id"`
	// PowerSupplyPeriod is in minutes.
	PowerSupplyPeriod byte `json:"power_supply_period"`
}

// ParseCAEMMTS reads a CA_EMM_TS descriptor.
func ParseCAEMMTS(body []byte) (CAEMMTS, bool) {
	if len(body) < 7 {
		return CAEMMTS{}, false
	}
	return CAEMMTS{
		CASystemID:        binary.BigEndian.Uint16(body),
		TransportStreamID: binary.BigEndian.Uint16(body[2:]),
		OriginalNetworkID: binary.BigEndian.Uint16(body[4:]),
		PowerSupplyPeriod: body[6],
	}, true
}

// VideoDecodeControl is what a video decode control descriptor (0xC8, ARIB
// STD-B10 6.2.30) says of a video stream.
type VideoDecodeControl struct {
	StillPicture    bool `json:"still_picture_flag"`
	SequenceEndCode bool `json:"sequence_end_code_flag"`
	// EncodeFormat is the video_encode_format (Table 6-64).
	EncodeFormat byte `json:"video_encode_format"`
	// Transfer is the transfer characteristics: 0 unsaid, 1 SDR BT.709, 2 IEC
	// 61966-2-4, 3 HLG.
	Transfer byte `json:"transfer_characteristics"`
}

// FormatName is the encode format in words, empty for a value not defined.
func (v VideoDecodeControl) FormatName() string {
	names := [...]string{"1080p", "1080i", "720p", "480p", "480i", "240p", "120p", "2160p", "180p", "4320p"}
	if int(v.EncodeFormat) < len(names) {
		return names[v.EncodeFormat]
	}
	return ""
}

// ParseVideoDecodeControl reads a video decode control descriptor.
func ParseVideoDecodeControl(body []byte) (VideoDecodeControl, bool) {
	if len(body) < 1 {
		return VideoDecodeControl{}, false
	}
	return VideoDecodeControl{
		StillPicture:    body[0]&0x80 != 0,
		SequenceEndCode: body[0]&0x40 != 0,
		EncodeFormat:    body[0] >> 2 & 0x0f,
		Transfer:        body[0] & 0x03,
	}, true
}

// AccessControl is what an access control descriptor (0xF6) says: the CA
// system and the PID its messages ride on.
type AccessControl struct {
	CASystemID       uint16 `json:"ca_system_id"`
	PID              uint16 `json:"pid"`
	TransmissionType byte   `json:"transmission_type"`
}

// ParseAccessControl reads an access control descriptor.
func ParseAccessControl(body []byte) (AccessControl, bool) {
	if len(body) < 4 {
		return AccessControl{}, false
	}
	return AccessControl{
		CASystemID:       binary.BigEndian.Uint16(body),
		PID:              uint16(body[2]&0x1f)<<8 | uint16(body[3]),
		TransmissionType: body[2] >> 5,
	}, true
}

// ParseMHAccessControl reads an MH access control descriptor (0x8004, ARIB
// STD-B60 7.4.3.7): the CA system and where its messages are.
func ParseMHAccessControl(body []byte) (system uint16, location AssetLocation, ok bool) {
	if len(body) < 2 {
		return 0, AssetLocation{}, false
	}
	location, _, ok = parseAssetLocation(body[2:])
	if !ok {
		return 0, AssetLocation{}, false
	}
	return binary.BigEndian.Uint16(body), location, true
}

// NetworkIdentification is what a network identification descriptor (0xC2, in
// a SIT) says: which network a partial transport stream was recorded from.
type NetworkIdentification struct {
	Country     string `json:"country_code"`
	MediaType   uint16 `json:"media_type"`
	NetworkID   uint16 `json:"network_id"`
	PrivateData []byte `json:"private_data,omitempty"`
}

// ParseNetworkIdentification reads a network identification descriptor.
func ParseNetworkIdentification(body []byte) (NetworkIdentification, bool) {
	if len(body) < 7 {
		return NetworkIdentification{}, false
	}
	return NetworkIdentification{
		Country:     language(body[:3]),
		MediaType:   binary.BigEndian.Uint16(body[3:]),
		NetworkID:   binary.BigEndian.Uint16(body[5:]),
		PrivateData: append([]byte(nil), body[7:]...),
	}, true
}

// PartialTSTime is what a partial transport stream time descriptor (0xC3, in a
// SIT) says: when the recorded event was, and where in it the recording starts.
type PartialTSTime struct {
	EventVersion byte           `json:"event_version_number"`
	EventStart   *time.Time     `json:"event_start_time,omitempty"`
	Duration     *time.Duration `json:"duration,omitempty"`
	// Offset is how far from the event's start the stream begins.
	Offset      *time.Duration `json:"offset,omitempty"`
	OffsetFlag  bool           `json:"offset_flag"`
	OthersValid bool           `json:"other_descriptor_status"`
	// JSTTime is the wall clock at the start of the recording, where said.
	JSTTime *time.Time `json:"jst_time,omitempty"`
}

// ParsePartialTSTime reads a partial transport stream time descriptor.
func ParsePartialTSTime(body []byte) (PartialTSTime, bool) {
	if len(body) < 13 {
		return PartialTSTime{}, false
	}
	flags := body[12]
	read := PartialTSTime{EventVersion: body[0], OffsetFlag: flags&0x04 != 0, OthersValid: flags&0x02 != 0}
	if start, ok := ParseBroadcastTime(body[1:6]); ok {
		read.EventStart = &start
	}
	if duration, ok := parseDuration(body[6:9]); ok {
		read.Duration = &duration
	}
	if offset, ok := parseDuration(body[9:12]); ok {
		read.Offset = &offset
	}
	if flags&0x01 != 0 && len(body) >= 18 {
		if clock, ok := ParseBroadcastTime(body[13:18]); ok {
			read.JSTTime = &clock
		}
	}
	return read, true
}

// PartialTS is what a partial transport stream descriptor (0x63, in a SIT)
// says of the stream's rate, in units of 400 bit/s.
type PartialTS struct {
	PeakRate               uint32 `json:"peak_rate"`
	MinimumSmoothingRate   uint32 `json:"minimum_overall_smoothing_rate"`
	MaximumSmoothingBuffer uint16 `json:"maximum_overall_smoothing_buffer"`
}

// ParsePartialTS reads a partial transport stream descriptor.
func ParsePartialTS(body []byte) (PartialTS, bool) {
	if len(body) < 8 {
		return PartialTS{}, false
	}
	three := func(b []byte) uint32 { return uint32(b[0]&0x3f)<<16 | uint32(b[1])<<8 | uint32(b[2]) }
	return PartialTS{
		PeakRate:               three(body[0:3]),
		MinimumSmoothingRate:   three(body[3:6]),
		MaximumSmoothingBuffer: uint16(body[6]&0x3f)<<8 | uint16(body[7]),
	}, true
}

// DownloadContentInfo is what a download content descriptor (0xC9, ARIB
// STD-B21) says of one piece of software: enough to show, not to fetch it.
type DownloadContentInfo struct {
	Reboot        bool   `json:"reboot"`
	AddOn         bool   `json:"add_on"`
	ComponentSize uint32 `json:"component_size"`
	DownloadID    uint32 `json:"download_id"`
	TimeoutDII    uint32 `json:"time_out_value_dii"`
	// LeakRate is in units of 50 bytes/s.
	LeakRate     uint32 `json:"leak_rate"`
	ComponentTag byte   `json:"component_tag"`
	// Language and Text are the text, where the descriptor carried one.
	Language string `json:"language,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ParseDownloadContent reads a download content descriptor, skipping the
// compatibility and module loops.
func ParseDownloadContent(body []byte) (DownloadContentInfo, bool) {
	if len(body) < 17 {
		return DownloadContentInfo{}, false
	}
	flags := body[0]
	read := DownloadContentInfo{
		Reboot:        flags&0x80 != 0,
		AddOn:         flags&0x40 != 0,
		ComponentSize: binary.BigEndian.Uint32(body[1:]),
		DownloadID:    binary.BigEndian.Uint32(body[5:]),
		TimeoutDII:    binary.BigEndian.Uint32(body[9:]),
		LeakRate:      uint32(body[13]&0x3f)<<16 | uint32(body[14])<<8 | uint32(body[15]),
		ComponentTag:  body[16],
	}
	at := 17
	if flags&0x20 != 0 {
		if at+2 > len(body) {
			return DownloadContentInfo{}, false
		}
		at += 2 + int(binary.BigEndian.Uint16(body[at:]))
	}
	if flags&0x10 != 0 {
		if at+2 > len(body) {
			return DownloadContentInfo{}, false
		}
		count := int(binary.BigEndian.Uint16(body[at:]))
		at += 2
		for range count {
			if at+7 > len(body) {
				return DownloadContentInfo{}, false
			}
			at += 7 + int(body[at+6])
		}
	}
	if at >= len(body) {
		return DownloadContentInfo{}, false
	}
	at += 1 + int(body[at])
	if flags&0x08 != 0 {
		if at+4 > len(body) {
			return DownloadContentInfo{}, false
		}
		length := int(body[at+3])
		if at+4+length > len(body) {
			return DownloadContentInfo{}, false
		}
		read.Language = language(body[at : at+3])
		read.Text = downloadText(body[at+4 : at+4+length])
	}
	return read, true
}

// downloadText reads the text of a download content descriptor. Broadcasters
// send it as EUC-JP rather than the eight-unit code, and some makers' notices
// as UTF-8; it is taken as UTF-8 when it is that and has a byte above 0x7f, as
// EUC-JP when it decodes cleanly as that and shows nothing of the eight-unit
// code (escapes, C1 controls, kanji in seven-bit pairs), and as the eight-unit
// code otherwise.
func downloadText(b []byte) string {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if !ascii && utf8.Valid(b) {
		return string(b)
	}
	eightBit := false
	for _, c := range b {
		if c == 0x1b || c >= 0x80 && c <= 0xa0 {
			eightBit = true
			break
		}
	}
	// Text with nothing above 0x7f is plain ASCII or the eight-unit code in
	// its initial state, kanji in two seven-bit bytes: a Japanese notice has
	// kana and punctuation, whose first bytes are 0x21-0x25.
	sevenBitKanji := ascii
	if sevenBitKanji {
		runs := 0
		for _, run := range splitBelow(b, 0x21) {
			runs++
			if len(run)%2 != 0 {
				sevenBitKanji = false
				break
			}
			lead := false
			for i := 0; i < len(run); i += 2 {
				if run[i] >= 0x21 && run[i] <= 0x25 {
					lead = true
				}
			}
			if !lead {
				sevenBitKanji = false
				break
			}
		}
		if runs == 0 {
			sevenBitKanji = false
		}
	}
	if !eightBit && !sevenBitKanji {
		if decoded, err := japanese.EUCJP.NewDecoder().Bytes(b); err == nil && !strings.ContainsRune(string(decoded), '�') {
			return string(decoded)
		}
	}
	return DescriptorText(b)
}

func splitBelow(b []byte, limit byte) [][]byte {
	var runs [][]byte
	start := -1
	for i, c := range b {
		if c < limit {
			if start >= 0 {
				runs = append(runs, b[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		runs = append(runs, b[start:])
	}
	return runs
}

// TargetRegion is what a target region descriptor (0xC6, MMT 0x8015) names.
type TargetRegion struct {
	// Kind is the region_spec_type: 1 is the prefecture bitmap of BS digital.
	Kind byte `json:"region_spec_type"`
	// Prefectures is the bits that were set, numbered from one in the order of
	// ARIB STD-B10 Annex G Table G-2.
	Prefectures []byte `json:"prefectures,omitempty"`
	// Raw is the specifier as sent, for a kind this reader does not know.
	Raw []byte `json:"target_region_spec,omitempty"`
}

var prefectureNames = [...]string{
	"北海道（東）", "北海道（西）", "青森", "岩手", "宮城", "秋田", "山形", "福島",
	"茨城", "栃木", "群馬", "埼玉", "千葉", "東京（島部を除く）", "神奈川", "新潟",
	"富山", "石川", "福井", "山梨", "長野", "岐阜", "静岡", "愛知", "三重", "滋賀",
	"京都", "大阪", "兵庫", "奈良", "和歌山", "鳥取", "島根", "岡山", "広島", "山口",
	"徳島", "香川", "愛媛", "高知", "福岡", "佐賀", "長崎", "熊本", "大分", "宮崎",
	"鹿児島（南西諸島を除く）", "沖縄", "東京（島部）", "鹿児島（南西諸島）",
}

// PrefectureName is the name behind a bit of Table G-2, numbered from one.
func PrefectureName(bit byte) (string, bool) {
	if bit == 0 || int(bit) > len(prefectureNames) {
		return "", false
	}
	return prefectureNames[bit-1], true
}

// ParseTargetRegion reads a target region descriptor; the MH form is the same
// bytes.
func ParseTargetRegion(body []byte) (TargetRegion, bool) {
	if len(body) < 1 {
		return TargetRegion{}, false
	}
	spec := body[1:]
	read := TargetRegion{Kind: body[0], Raw: append([]byte(nil), spec...)}
	if body[0] == 0x01 {
		for index, b := range spec[:min(len(spec), 7)] {
			for bit := range 8 {
				if b&(0x80>>bit) != 0 {
					read.Prefectures = append(read.Prefectures, byte(index*8+bit+1))
				}
			}
		}
	}
	return read, true
}

// The destinations a hyperlink descriptor points at (link_destination_type).
const (
	LinkService       = 0x01
	LinkEvent         = 0x02
	LinkModule        = 0x03
	LinkContent       = 0x04
	LinkContentModule = 0x05
	LinkERTNode       = 0x06
	LinkStored        = 0x07
)

// Hyperlink is what a hyperlink descriptor (0xC5, ARIB STD-B10 6.2.29) points
// at. Which fields mean anything follows Destination; a destination this
// reader does not know keeps its selector as sent.
type Hyperlink struct {
	// Kind is the hyper_linkage_type (Table 6-50).
	Kind                byte   `json:"hyper_linkage_type"`
	Destination         byte   `json:"link_destination_type"`
	NetworkID           uint16 `json:"original_network_id,omitempty"`
	TransportStreamID   uint16 `json:"transport_stream_id,omitempty"`
	ServiceID           uint16 `json:"service_id,omitempty"`
	EventID             uint16 `json:"event_id,omitempty"`
	ComponentTag        byte   `json:"component_tag,omitempty"`
	ModuleID            uint16 `json:"module_id,omitempty"`
	ContentID           uint32 `json:"content_id,omitempty"`
	InformationProvider uint16 `json:"information_provider_id,omitempty"`
	EventRelation       uint16 `json:"event_relation_id,omitempty"`
	Node                uint16 `json:"node_id,omitempty"`
	URI                 string `json:"uri,omitempty"`
	Selector            []byte `json:"selector,omitempty"`
	PrivateData         []byte `json:"private_data,omitempty"`
}

// ParseHyperlink reads a hyperlink descriptor.
func ParseHyperlink(body []byte) (Hyperlink, bool) {
	if len(body) < 3 {
		return Hyperlink{}, false
	}
	length := int(body[2])
	if 3+length > len(body) {
		return Hyperlink{}, false
	}
	selector := body[3 : 3+length]
	read := Hyperlink{Kind: body[0], Destination: body[1], PrivateData: append([]byte(nil), body[3+length:]...)}
	need := map[byte]int{LinkService: 6, LinkEvent: 8, LinkModule: 11, LinkContent: 10, LinkContentModule: 13, LinkERTNode: 6}
	if n, ok := need[body[1]]; ok && len(selector) < n {
		return Hyperlink{}, false
	}
	be16 := func(at int) uint16 { return binary.BigEndian.Uint16(selector[at:]) }
	switch body[1] {
	case LinkService, LinkEvent, LinkModule, LinkContent, LinkContentModule:
		read.NetworkID, read.TransportStreamID, read.ServiceID = be16(0), be16(2), be16(4)
	}
	switch body[1] {
	case LinkEvent:
		read.EventID = be16(6)
	case LinkModule:
		read.EventID, read.ComponentTag, read.ModuleID = be16(6), selector[8], be16(9)
	case LinkContent:
		read.ContentID = binary.BigEndian.Uint32(selector[6:])
	case LinkContentModule:
		read.ContentID, read.ComponentTag, read.ModuleID = binary.BigEndian.Uint32(selector[6:]), selector[10], be16(11)
	case LinkERTNode:
		read.InformationProvider, read.EventRelation, read.Node = be16(0), be16(2), be16(4)
	case LinkStored:
		read.URI = strings.ToValidUTF8(string(selector), "�")
	case LinkService:
	default:
		read.Selector = append([]byte(nil), selector...)
	}
	return read, true
}

// MultimediaService is what a multimedia service information descriptor (MMT
// 0x803F, ARIB STD-B60 7.4.3.47) says of an HTML5 data service.
type MultimediaService struct {
	// DataComponentID is 0x0020 for the application and 0x0021 for a
	// programme's additional data.
	DataComponentID uint16  `json:"data_component_id"`
	ComponentTag    *uint16 `json:"component_tag,omitempty"`
	Language        string  `json:"language,omitempty"`
	Text            string  `json:"text,omitempty"`
	// AssociatedContents is for 0x0021: whether there is content tied to the
	// programme.
	AssociatedContents *bool  `json:"associated_contents_flag,omitempty"`
	Selector           []byte `json:"selector,omitempty"`
}

// ParseMultimediaServiceInfo reads a multimedia service information
// descriptor.
func ParseMultimediaServiceInfo(body []byte) (MultimediaService, bool) {
	if len(body) < 2 {
		return MultimediaService{}, false
	}
	read := MultimediaService{DataComponentID: binary.BigEndian.Uint16(body)}
	at := 2
	switch read.DataComponentID {
	case 0x0020:
		if at+6 > len(body) {
			return MultimediaService{}, false
		}
		tag := binary.BigEndian.Uint16(body[at:])
		read.ComponentTag = &tag
		read.Language = language(body[at+2 : at+5])
		length := int(body[at+5])
		if at+6+length > len(body) {
			return MultimediaService{}, false
		}
		read.Text = mhText(body[at+6 : at+6+length])
		at += 6 + length
	case 0x0021:
		if at >= len(body) {
			return MultimediaService{}, false
		}
		associated := body[at]&0x80 != 0
		read.AssociatedContents = &associated
		at++
	}
	if at >= len(body) {
		return MultimediaService{}, false
	}
	length := int(body[at])
	if at+1+length > len(body) {
		return MultimediaService{}, false
	}
	read.Selector = append([]byte(nil), body[at+1:at+1+length]...)
	return read, true
}

// ParseEmergencyNews reads an emergency news descriptor (MMT 0x8040, ARIB
// STD-B60 7.4.3.43): when the bulletin was first sent, as an NTP timestamp,
// the same for as long as the same bulletin runs.
func ParseEmergencyNews(body []byte) (uint64, bool) {
	if len(body) < 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(body), true
}

// SIParameterTable is one table's description in an SI parameter descriptor.
type SIParameterTable struct {
	TableID     byte   `json:"table_id"`
	Description []byte `json:"table_description"`
}

// SIParameters is what an SI parameter descriptor (0xD7, MMT 0x8017) says: a
// version, when it last changed (MJD), and per table the operator's own
// description bytes.
type SIParameters struct {
	Version   byte               `json:"parameter_version"`
	UpdateMJD uint16             `json:"update_time"`
	Tables    []SIParameterTable `json:"tables,omitempty"`
}

// ParseSIParameter reads an SI parameter descriptor.
func ParseSIParameter(body []byte) (SIParameters, bool) {
	if len(body) < 3 {
		return SIParameters{}, false
	}
	read := SIParameters{Version: body[0], UpdateMJD: binary.BigEndian.Uint16(body[1:])}
	for at := 3; at+2 <= len(body); {
		length := int(body[at+1])
		if at+2+length > len(body) {
			break
		}
		read.Tables = append(read.Tables, SIParameterTable{TableID: body[at], Description: append([]byte(nil), body[at+2:at+2+length]...)})
		at += 2 + length
	}
	return read, true
}
