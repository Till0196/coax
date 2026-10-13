// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// The TLV-NIT and MH-SDT tables, which are the MMT/TLV counterparts of NIT and
// SDT.
package isdb

import (
	"encoding/binary"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Till0196/coax/pkg/mpegts"
)

const (
	// MessageIDPA carries the tables that say what a TLV stream holds, the
	// package list among them.
	MessageIDPA             = 0x0000
	MessageIDM2Section      = 0x8000
	MessageIDM2ShortSection = 0x8002
	TableIDPLT              = 0x80
	TableIDTLVNITActual     = 0x40
	// TableIDTLVNITOther is the same table describing a network this stream does
	// not belong to. A cable carrying both generations sends one for the older
	// services beside its own, which lets one lock on a TLV carrier account for the
	// transport stream multiplexes on the lead.
	TableIDTLVNITOther = 0x41
	TableIDMHSDTActual = 0x9f
	TagMHService       = 0x8019
	// TagRemoteControlKey is the descriptor that says which button each
	// service is on, in the first loop of the TLV-NIT. See parseRemoteKeys.
	TagRemoteControlKey = 0xcd
)

type Service struct {
	ServiceID  uint16
	Type       byte
	Name       string
	Provider   string
	FreeCAMode bool
}

// Network is one network of the TLV-NIT.
type Network struct {
	NetworkID uint16
	Name      string
	Streams   []NetworkStream
	// RemoteKeys is which button each service is on, by service identifier. ARIB
	// TR-B39 36.1.2.3 requires the descriptor whenever the TLV-NIT [actual] is
	// transmitted. The older broadcasts have nothing like it.
	RemoteKeys map[uint16]byte
	// System is whether this network says it is a broadcast at all, where it
	// said. See SystemManagement.
	System *SystemManagement
	// Links are the services the network as a whole points at.
	Links []Linkage
}

// NetworkStream is one TLV stream a network carries.
type NetworkStream struct {
	TLVStreamID       uint16
	OriginalNetworkID uint16
	Services          []ServiceReference
	// Satellite is where the stream is carried, when the network said so. The
	// advanced services describe their transponders in the same descriptor the
	// older ones use, in a table of their own.
	Satellite *SatelliteDelivery
	// Cable is the same for a stream carried on a cable, where the table uses
	// the ordinary cable descriptor to say so.
	Cable *CableDelivery
	// ChannelBonding is what the Japanese operators actually send: one record
	// per carrier, which is how a multiplex split across several carriers
	// names all of them. See ParseChannelBondingCable.
	ChannelBonding []ChannelBondingCarrier
	// System is whether the stream says it is a broadcast at all.
	System *SystemManagement
	StreamDescription
}

// StreamDescription is what else the network table says about one stream: the
// same descriptors the transport stream NIT carries, read the same way. Tag
// 0xCD is the one exception -- in a TLV-NIT it is the remote control key
// descriptor, among the network's own descriptors, not the TS information
// descriptor of a stream.
type StreamDescription struct {
	Terrestrial                *TerrestrialDelivery         `json:"terrestrial,omitempty"`
	PartialReceptionServiceIDs []uint16                     `json:"partial_reception_service_ids,omitempty"`
	AdvancedCable              *AdvancedCableDelivery       `json:"advanced_cable,omitempty"`
	CableDivision              []CableDivisionCarrier       `json:"cable_division,omitempty"`
	Links                      []Linkage                    `json:"links,omitempty"`
	Emergency                  *EmergencyInformation        `json:"emergency,omitempty"`
	Connected                  *ConnectedTransmission       `json:"connected_transmission,omitempty"`
	ServiceGroups              []ServiceGroup               `json:"service_groups,omitempty"`
	AreaBroadcasting           *AreaBroadcastingInformation `json:"area_broadcasting,omitempty"`
}

// ServiceReference names a service the NIT lists for a stream. The NIT gives
// identifiers and types only; the name comes from an MH-SDT -- this stream's
// own, or table identifier 0xA0 for a neighbour. Empty where no table has named
// the service yet.
type ServiceReference struct {
	ServiceID uint16
	Type      byte
	Name      string
}

type State struct {
	NetworkID         uint16
	TLVStreamID       uint16
	OriginalNetworkID uint16
	Services          map[uint16]Service
	// Carried is the services this TLV stream actually holds, from the package
	// list table. See parsePLT.
	Carried []uint16
	// Networks is what the TLV-NIT describes, keyed by network identifier.
	Networks map[uint16]*Network
	// names is what the service tables have called the services of every stream,
	// this one's and its neighbours'. The tables arrive in no particular order
	// against the network table, so the names are kept here and put onto the
	// listing when either side is read.
	names map[streamService]string
	// seen records the sections already folded in. A sub-table is repeated for
	// as long as the stream runs, and taking a repeat again would list its
	// streams twice.
	seen            map[mpegts.SectionKey]bool
	InvalidSections uint64
}

func NewState() *State {
	return &State{
		Services: make(map[uint16]Service),
		Networks: make(map[uint16]*Network),
		names:    make(map[streamService]string),
		seen:     make(map[mpegts.SectionKey]bool),
	}
}

func (state *State) PushTLVSection(section []byte) {
	if len(section) < 3 {
		state.InvalidSections++
		return
	}
	length := int(binary.BigEndian.Uint16(section[1:3]) & 0x0fff)
	if 3+length > len(section) || !mpegts.ValidSection(section[:3+length]) {
		state.InvalidSections++
		return
	}
	if section[0] == TableIDTLVNITActual || section[0] == TableIDTLVNITOther {
		state.parseNIT(section[:3+length])
	}
}

func (state *State) PushMessage(id uint16, payload []byte) {
	if id == MessageIDPA {
		state.parsePA(payload)
		return
	}
	if id != MessageIDM2Section && id != MessageIDM2ShortSection {
		return
	}
	for len(payload) >= 3 {
		// The message is padded to its length with bytes that cannot begin a
		// section. Nothing comes after them.
		if payload[0] == 0xff {
			return
		}
		length := int(binary.BigEndian.Uint16(payload[1:3]) & 0x0fff)
		total := 3 + length
		if total > len(payload) || total < 3 {
			state.InvalidSections++
			return
		}
		section := payload[:total]
		if !mpegts.ValidSection(section) {
			state.InvalidSections++
		} else if section[0] == TableIDMHSDTActual || section[0] == TableIDMHSDTOther {
			state.parseSDT(section)
		}
		payload = payload[total:]
	}
	if len(payload) != 0 && payload[0] != 0xff {
		state.InvalidSections++
	}
}

// parsePA walks the tables a PA message carries, for the package list among
// them. An index of the tables may come first, but it carries nothing the
// tables do not state again.
func (state *State) parsePA(payload []byte) {
	if len(payload) < 1 {
		state.InvalidSections++
		return
	}
	at := 1 + 4*int(payload[0])
	for at+4 <= len(payload) {
		length := int(binary.BigEndian.Uint16(payload[at+2 : at+4]))
		if at+4+length > len(payload) {
			state.InvalidSections++
			return
		}
		if payload[at] == TableIDPLT {
			state.parsePLT(payload[at : at+4+length])
		}
		at += 4 + length
	}
}

// parsePLT reads which services a TLV stream carries.
//
// This is the question the MH-SDT cannot answer: its actual table describes
// every service channel the network's TLV-NIT defines, not the ones on this
// stream -- ARIB TR-B39 34.16 says so, and a TLV carrier measured here declared
// seven services while carrying one. TR-B39 34.3 has num_of_package as the
// services transmitted in the concerned TLV stream, and table 5.5-1 makes
// service_id and MMT_package_id one number.
func (state *State) parsePLT(table []byte) {
	// table_id, version, length, num_of_package.
	if len(table) < 5 {
		state.InvalidSections++
		return
	}
	count := int(table[4])
	at := 5
	carried := make([]uint16, 0, count)
	for i := 0; i < count; i++ {
		if at >= len(table) {
			state.InvalidSections++
			return
		}
		length := int(table[at])
		at++
		if at+length > len(table) {
			state.InvalidSections++
			return
		}
		if length == 2 {
			carried = append(carried, binary.BigEndian.Uint16(table[at:at+2]))
		}
		at += length
		// MMT_general_location_info. Everything a broadcast puts here is a
		// packet of this same data flow; anything else is a location this
		// cannot follow, and the rest of the list cannot be walked past it.
		if at >= len(table) || table[at] != 0x00 {
			state.InvalidSections++
			return
		}
		at += 3
	}
	state.Carried = carried
}

// streamService is one service anywhere in the network: which stream of which
// network, and which service of it.
type streamService struct {
	originalNetworkID uint16
	tlvStreamID       uint16
	serviceID         uint16
}

func (state *State) parseSDT(section []byte) {
	if len(section) < 15 || section[1]&0x80 == 0 {
		state.InvalidSections++
		return
	}
	// Only the stream's own table says what this stream is. A table describing
	// a neighbour carries that neighbour's identifiers, and taking them would
	// make the stream answer to another one's name.
	own := section[0] == TableIDMHSDTActual
	streamID := binary.BigEndian.Uint16(section[3:5])
	networkID := binary.BigEndian.Uint16(section[8:10])
	if own {
		state.TLVStreamID = streamID
		state.OriginalNetworkID = networkID
	}
	end := len(section) - 4
	for offset := 11; offset+5 <= end; {
		id := binary.BigEndian.Uint16(section[offset : offset+2])
		length := int(binary.BigEndian.Uint16(section[offset+3:offset+5]) & 0x0fff)
		next := offset + 5 + length
		if next > end {
			state.InvalidSections++
			return
		}
		var service Service
		if own {
			service = state.Services[id]
		}
		service.ServiceID = id
		// Set means the service is under conditional access; see the note in
		// package psi, which reads the same field out of the SDT.
		service.FreeCAMode = section[offset+3]&0x10 != 0
		parseMHDescriptors(section[offset+5:next], &service)
		if own {
			state.Services[id] = service
		}
		state.nameService(streamService{networkID, streamID, id}, service.Name)
		offset = next
	}
}

// nameService records what a service table called a service and puts the name
// onto whatever the network table has already listed. The two tables arrive in
// no order against each other, so each side fills in what the other has.
func (state *State) nameService(at streamService, name string) {
	if name == "" {
		return
	}
	if _, seen := state.names[at]; seen {
		return
	}
	state.names[at] = name
	for _, network := range state.Networks {
		for index := range network.Streams {
			stream := &network.Streams[index]
			if stream.TLVStreamID != at.tlvStreamID || stream.OriginalNetworkID != at.originalNetworkID {
				continue
			}
			for service := range stream.Services {
				if stream.Services[service].ServiceID == at.serviceID {
					stream.Services[service].Name = name
				}
			}
		}
	}
}

// nameStream puts the names read so far onto a stream the network table has
// just listed.
func (state *State) nameStream(stream *NetworkStream) {
	for index := range stream.Services {
		service := &stream.Services[index]
		if name, ok := state.names[streamService{stream.OriginalNetworkID,
			stream.TLVStreamID, service.ServiceID}]; ok {
			service.Name = name
		}
	}
}

func parseMHDescriptors(buffer []byte, service *Service) {
	for len(buffer) >= 3 {
		tag := binary.BigEndian.Uint16(buffer[:2])
		lengthBytes := descriptorLengthBytes(tag)
		if len(buffer) < 2+lengthBytes {
			return
		}
		length := 0
		for _, value := range buffer[2 : 2+lengthBytes] {
			length = length<<8 | int(value)
		}
		start, next := 2+lengthBytes, 2+lengthBytes+length
		if next > len(buffer) {
			return
		}
		if tag == TagMHService {
			parseServiceDescriptor(buffer[start:next], service)
		}
		buffer = buffer[next:]
	}
}

func descriptorLengthBytes(tag uint16) int {
	switch {
	case tag >= 0x4000 && tag <= 0x6fff:
		return 2
	case tag >= 0x7000 && tag <= 0x7fff:
		return 4
	case tag >= 0xf000:
		return 2
	default:
		return 1
	}
}

func parseServiceDescriptor(buffer []byte, service *Service) {
	if len(buffer) < 3 {
		return
	}
	service.Type = buffer[0]
	providerLength := int(buffer[1])
	if 2+providerLength >= len(buffer) {
		return
	}
	// Trimmed, as the transport stream side trims: a name of nothing but spaces is
	// a service that was not named, and a listing would show a blank where a
	// reader could otherwise tell that no table has said yet.
	nameOffset := 2 + providerLength
	nameLength := int(buffer[nameOffset])
	nameOffset++
	if nameOffset+nameLength > len(buffer) {
		return
	}
	// UTF-8, as everything in these tables is. Bytes that are not are no name
	// at all: a listing showing mojibake would read as a service that is named.
	if provider := buffer[2 : 2+providerLength]; utf8.Valid(provider) {
		service.Provider = strings.TrimSpace(string(provider))
	}
	if name := buffer[nameOffset : nameOffset+nameLength]; utf8.Valid(name) {
		service.Name = strings.TrimSpace(string(name))
	}
}

func (state *State) parseNIT(section []byte) {
	if len(section) < 16 {
		state.InvalidSections++
		return
	}
	networkID := binary.BigEndian.Uint16(section[3:5])
	if section[0] == TableIDTLVNITActual {
		// Only the stream's own table says which network the stream belongs to. The
		// other-network table describes a neighbour.
		state.NetworkID = networkID
	}
	key := mpegts.SectionKeyOf(section)
	if state.seen[key] {
		return
	}
	state.seen[key] = true
	network := state.Networks[networkID]
	if network == nil {
		network = &Network{NetworkID: networkID}
		state.Networks[networkID] = network
	}
	end := len(section) - 4
	offset := 8
	descriptorLength := int(binary.BigEndian.Uint16(section[offset:offset+2]) & 0x0fff)
	if start := offset + 2; start+descriptorLength <= end {
		parseTLVNetworkDescriptors(section[start:start+descriptorLength], network)
	}
	offset += 2 + descriptorLength
	if offset+2 > end {
		state.InvalidSections++
		return
	}
	loopLength := int(binary.BigEndian.Uint16(section[offset:offset+2]) & 0x0fff)
	offset += 2
	loopEnd := min(offset+loopLength, end)
	for offset+6 <= loopEnd {
		streamID := binary.BigEndian.Uint16(section[offset : offset+2])
		onid := binary.BigEndian.Uint16(section[offset+2 : offset+4])
		length := int(binary.BigEndian.Uint16(section[offset+4:offset+6]) & 0x0fff)
		next := offset + 6 + length
		if next > loopEnd {
			state.InvalidSections++
			return
		}
		// The table does not say which of its streams this one is: a
		// cable carrying them one to a carrier sends the same table on every
		// one. Only the SDT names the stream it came in on.
		stream := NetworkStream{TLVStreamID: streamID, OriginalNetworkID: onid}
		parseTLVDescriptors(section[offset+6:next], state.Services, &stream)
		state.nameStream(&stream)
		// A table is sent again for as long as the stream runs, and a new version
		// of it lists the same streams: one already described is left as it was.
		if !slices.ContainsFunc(network.Streams, func(known NetworkStream) bool {
			return known.TLVStreamID == stream.TLVStreamID
		}) {
			network.Streams = append(network.Streams, stream)
		}
		offset = next
	}
}

func parseTLVDescriptors(buffer []byte, services map[uint16]Service, stream *NetworkStream) {
	for offset := 0; offset+2 <= len(buffer); {
		// A descriptor that says it runs past the entry is read as far as the
		// entry goes, and is the last -- as the transport stream NIT reads it.
		next := min(offset+2+int(buffer[offset+1]), len(buffer))
		body := buffer[offset+2 : next]
		switch buffer[offset] {
		case 0x41: // service_list_descriptor
			for list := body; len(list) >= 3; list = list[3:] {
				id := binary.BigEndian.Uint16(list[:2])
				service := services[id]
				service.ServiceID, service.Type = id, list[2]
				services[id] = service
				stream.Services = append(stream.Services, ServiceReference{ServiceID: id, Type: list[2]})
			}
		case 0x43: // satellite_delivery_system_descriptor
			if satellite := ParseSatelliteDelivery(body); satellite != nil {
				stream.Satellite = satellite
			}
		case 0x44: // cable_delivery_system_descriptor
			if cable := ParseCableDelivery(body); cable != nil {
				stream.Cable = cable
			}
		case 0xf3: // channel_bonding_cable_delivery_system_descriptor
			if carriers := ParseChannelBondingCable(body); len(carriers) != 0 {
				stream.ChannelBonding = carriers
			}
		case 0xfe: // system_management_descriptor
			if system := ParseSystemManagement(body); system != nil {
				stream.System = system
			}
		case 0xfa: // terrestrial_delivery_system_descriptor
			if terrestrial := ParseTerrestrialDelivery(body); terrestrial != nil {
				stream.Terrestrial = terrestrial
			}
		case 0xfb: // partial_reception_descriptor
			for entry := 0; entry+2 <= len(body); entry += 2 {
				stream.PartialReceptionServiceIDs = append(stream.PartialReceptionServiceIDs,
					binary.BigEndian.Uint16(body[entry:entry+2]))
			}
		case 0xf4: // advanced_cable_delivery_system_descriptor
			if advanced := ParseAdvancedCableDelivery(body); advanced != nil {
				stream.AdvancedCable = advanced
			}
		case 0xf9: // cable_TS_division_system_descriptor
			if carriers := ParseCableTSDivision(body); len(carriers) != 0 {
				stream.CableDivision = carriers
			}
		case 0x4a: // linkage_descriptor
			if link := ParseLinkage(body); link != nil {
				stream.Links = append(stream.Links, *link)
			}
		case 0xfc: // emergency_information_descriptor
			if warning := ParseEmergencyInformation(body); warning != nil {
				stream.Emergency = warning
			}
		case 0xdd: // connected_transmission_descriptor
			if connected := ParseConnectedTransmission(body); connected != nil {
				stream.Connected = connected
			}
		case 0xe0: // service_group_descriptor
			if group := ParseServiceGroup(body); group != nil {
				stream.ServiceGroups = append(stream.ServiceGroups, *group)
			}
		case 0xe1: // area_broadcasting_information_descriptor
			if area := ParseAreaBroadcastingInformation(body); area != nil {
				stream.AreaBroadcasting = area
			}
		}
		offset = next
	}
}

// parseTLVNetworkDescriptors reads the descriptors describing the network itself,
// which is where its name lives.
func parseTLVNetworkDescriptors(buffer []byte, network *Network) {
	for len(buffer) >= 2 {
		length := int(buffer[1])
		if 2+length > len(buffer) {
			return
		}
		if buffer[0] == TagRemoteControlKey {
			parseRemoteKeys(buffer[2:2+length], network)
		}
		if buffer[0] == 0xfe {
			if system := ParseSystemManagement(buffer[2 : 2+length]); system != nil {
				network.System = system
			}
		}
		if buffer[0] == 0x4a {
			if link := ParseLinkage(buffer[2 : 2+length]); link != nil {
				network.Links = append(network.Links, *link)
			}
		}
		if buffer[0] == 0x40 && network.Name == "" {
			// TLV-SI spells text in UTF-8, not the ARIB 8-unit code the terrestrial
			// tables use. Running it through the ARIB decoder produces plausible-looking
			// kana that is entirely wrong.
			name := string(buffer[2 : 2+length])
			if utf8.ValidString(name) {
				network.Name = strings.TrimSpace(name)
			}
		}
		buffer = buffer[2+length:]
	}
}

// parseRemoteKeys reads which button each service is on. The descriptor is a
// count and then that many entries of a key, the service it belongs to, and two
// reserved bytes -- ARIB STD-B60 5.3.1.5. The count is read as a bound rather
// than trusted.
func parseRemoteKeys(buffer []byte, network *Network) {
	if len(buffer) < 1 {
		return
	}
	const entrySize = 5
	entries := buffer[1:]
	for index := 0; index < int(buffer[0]) && (index+1)*entrySize <= len(entries); index++ {
		entry := entries[index*entrySize:]
		key, serviceID := entry[0], binary.BigEndian.Uint16(entry[1:3])
		if key == 0 || serviceID == 0 {
			continue
		}
		if network.RemoteKeys == nil {
			network.RemoteKeys = make(map[uint16]byte)
		}
		// The first word on a service stands: the table is repeated, and a
		// later entry for the same service is not a correction.
		if _, said := network.RemoteKeys[serviceID]; !said {
			network.RemoteKeys[serviceID] = key
		}
	}
}
