// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import "math"

// The rest of what ARIB STD-B10 Table 6-1 allows in a network information
// table. The delivery system descriptors are in psi.go, beside the shapes they
// fill in; these are the others, which say things about a multiplex rather
// than where to find it.
//
// Every one is read as the standard writes it, and every one reports nothing
// where the bytes ran out rather than half a value: a descriptor that did not
// arrive whole is a descriptor that was not sent.

// Linkage is the linkage descriptor (ARIB STD-B10 6.2.7, tag 0x4A), which
// points at another service or transport stream. In a network table's first
// loop it names a service attached to the network operator -- information
// about the network, an electronic programme guide, a stream carrying the
// whole of the network's own information.
type Linkage struct {
	TransportStreamID uint16 `json:"transport_stream_id"`
	OriginalNetworkID uint16 `json:"original_network_id"`
	ServiceID         uint16 `json:"service_id"`
	// LinkageType says what the link is for. See the standard's table; a
	// scanner reports the number rather than naming a use it does not act on.
	LinkageType byte   `json:"linkage_type"`
	PrivateData []byte `json:"private_data,omitempty"`
}

// ParseLinkage reads the linkage descriptor.
func ParseLinkage(body []byte) *Linkage {
	if len(body) < 7 {
		return nil
	}
	return &Linkage{
		TransportStreamID: uint16(body[0])<<8 | uint16(body[1]),
		OriginalNetworkID: uint16(body[2])<<8 | uint16(body[3]),
		ServiceID:         uint16(body[4])<<8 | uint16(body[5]),
		LinkageType:       body[6],
		PrivateData:       append([]byte(nil), body[7:]...),
	}
}

// EmergencyInformation is the emergency information descriptor (ARIB STD-B10
// 6.2.24, tag 0xFC), which carries the emergency warning signal that used to
// be sent as a tone. It is mandatory while such a broadcast is being made, so
// its presence is the signal.
type EmergencyInformation struct {
	Services []EmergencyService `json:"services,omitempty"`
}

// EmergencyService is one service the emergency descriptor names.
type EmergencyService struct {
	ServiceID uint16 `json:"service_id"`
	// Started is the start_end_flag: true while the alarm has started or is
	// being broadcast, false once it has ended.
	Started bool `json:"started"`
	// SignalLevel is which of the two kinds of start signal this is.
	SignalLevel byte `json:"signal_level"`
	// AreaCodes are the areas the warning is for, twelve bits each.
	AreaCodes []uint16 `json:"area_codes,omitempty"`
}

// ParseEmergencyInformation reads the emergency information descriptor.
func ParseEmergencyInformation(body []byte) *EmergencyInformation {
	described := &EmergencyInformation{}
	for at := 0; at+4 <= len(body); {
		service := EmergencyService{
			ServiceID:   uint16(body[at])<<8 | uint16(body[at+1]),
			Started:     body[at+2]&0x80 != 0,
			SignalLevel: (body[at+2] >> 6) & 0x01,
		}
		codes := int(body[at+3])
		at += 4
		if at+codes > len(body) {
			// The area list runs past the end, so this service did not arrive
			// whole. What came before it stands; this is left off.
			break
		}
		// Twelve bits of area code and four reserved, so two bytes each.
		for offset := 0; offset+2 <= codes; offset += 2 {
			service.AreaCodes = append(service.AreaCodes,
				uint16(body[at+offset])<<4|uint16(body[at+offset+1]>>4))
		}
		at += codes
		described.Services = append(described.Services, service)
	}
	if len(described.Services) == 0 {
		return nil
	}
	return described
}

// ConnectedTransmission is the connected transmission descriptor (ARIB STD-B10
// 6.2.41, tag 0xDD), which says a terrestrial multiplex is one of a group sent
// together. Mandatory where that is being done, so it is how a scan learns
// that two multiplexes are halves of one thing.
type ConnectedTransmission struct {
	// GroupID labels the group. Multiplexes sharing it are sent together.
	GroupID uint16 `json:"group_id"`
	// SegmentType says how many layers there are and which this is.
	SegmentType byte `json:"segment_type"`
	// ModulationA, ModulationB and ModulationC are the three layers'
	// modulations, as the descriptor numbers them.
	ModulationA byte   `json:"modulation_a"`
	ModulationB byte   `json:"modulation_b"`
	ModulationC byte   `json:"modulation_c"`
	Extra       []byte `json:"extra,omitempty"`
}

// ParseConnectedTransmission reads the connected transmission descriptor.
func ParseConnectedTransmission(body []byte) *ConnectedTransmission {
	if len(body) < 3 {
		return nil
	}
	return &ConnectedTransmission{
		GroupID:     uint16(body[0])<<8 | uint16(body[1]),
		SegmentType: body[2] >> 6,
		ModulationA: (body[2] >> 4) & 0x03,
		ModulationB: (body[2] >> 2) & 0x03,
		ModulationC: body[2] & 0x03,
		Extra:       append([]byte(nil), body[3:]...),
	}
}

// ServiceGroup is the service group descriptor (ARIB STD-B10, tag 0xE0), which
// pairs services that belong together.
type ServiceGroup struct {
	// GroupType is what the grouping means. One is the only type with a shape
	// this reads; anything else carries private data.
	GroupType byte `json:"group_type"`
	// Pairs are the grouped services, for group type one.
	Pairs []ServiceGroupPair `json:"pairs,omitempty"`
	// PrivateData is what a group of any other type carried instead.
	PrivateData []byte `json:"private_data,omitempty"`
}

// ServiceGroupPair is one primary service and the secondary it is grouped with.
type ServiceGroupPair struct {
	PrimaryServiceID   uint16 `json:"primary_service_id"`
	SecondaryServiceID uint16 `json:"secondary_service_id"`
}

// ParseServiceGroup reads the service group descriptor.
func ParseServiceGroup(body []byte) *ServiceGroup {
	if len(body) < 1 {
		return nil
	}
	group := &ServiceGroup{GroupType: body[0] >> 4}
	rest := body[1:]
	if group.GroupType != 1 {
		group.PrivateData = append([]byte(nil), rest...)
		return group
	}
	for at := 0; at+4 <= len(rest); at += 4 {
		group.Pairs = append(group.Pairs, ServiceGroupPair{
			PrimaryServiceID:   uint16(rest[at])<<8 | uint16(rest[at+1]),
			SecondaryServiceID: uint16(rest[at+2])<<8 | uint16(rest[at+3]),
		})
	}
	return group
}

// AreaBroadcastingInformation is the area broadcasting information descriptor
// (ARIB STD-B10, tag 0xE1), which names the transmitting stations of an area
// broadcast -- the small local transmissions, as against a network's own.
type AreaBroadcastingInformation struct {
	Stations []AreaBroadcastingStation `json:"stations,omitempty"`
}

// AreaBroadcastingStation is one transmitting point.
type AreaBroadcastingStation struct {
	// StationID is 24 bits; the standard reserves everything above 0x3FFFFF.
	StationID    uint32 `json:"station_id"`
	LocationCode uint16 `json:"location_code"`
	// SignalFormat is which transmission format the station uses.
	SignalFormat byte   `json:"signal_format"`
	Extra        []byte `json:"extra,omitempty"`
}

// ParseAreaBroadcastingInformation reads the area broadcasting information
// descriptor.
func ParseAreaBroadcastingInformation(body []byte) *AreaBroadcastingInformation {
	if len(body) < 1 {
		return nil
	}
	described := &AreaBroadcastingInformation{}
	// The count is written out, but the loop is bounded by the bytes: a count
	// larger than what arrived is a descriptor that was cut short.
	at := 1
	for range int(body[0]) {
		if at+7 > len(body) {
			break
		}
		station := AreaBroadcastingStation{
			StationID:    uint32(body[at])<<16 | uint32(body[at+1])<<8 | uint32(body[at+2]),
			LocationCode: uint16(body[at+3])<<8 | uint16(body[at+4]),
			SignalFormat: body[at+5],
		}
		extra := int(body[at+6])
		at += 7
		if at+extra > len(body) {
			break
		}
		station.Extra = append([]byte(nil), body[at:at+extra]...)
		at += extra
		described.Stations = append(described.Stations, station)
	}
	if len(described.Stations) == 0 {
		return nil
	}
	return described
}

// AdvancedCableDelivery is the advanced cable delivery system descriptor (tag
// 0xF4): a C2 system and the data slices carrying the multiplex.
type AdvancedCableDelivery struct {
	TagExtension byte `json:"tag_extension"`
	PLPID        byte `json:"plp_id"`
	// OFDMSymbolDuration is 0 for 448 µs and 1 for 597.33 µs.
	OFDMSymbolDuration byte `json:"ofdm_symbol_duration"`
	// GuardInterval is 0 for 1/128 and 1 for 1/64.
	GuardInterval  byte                 `json:"guard_interval"`
	BundledChannel byte                 `json:"bundled_channel"`
	Slices         []AdvancedCableSlice `json:"slices,omitempty"`
}

// AdvancedCableSlice is one data slice of an advanced cable multiplex: where to
// tune for it.
type AdvancedCableSlice struct {
	DataSliceID byte `json:"data_slice_id"`
	// FrequencyHz is C2_tuning_frequency, read the way FrequencyType says.
	FrequencyHz uint32 `json:"frequency_hz"`
	// FrequencyType is 0 for the data slice's centre, 1 for the C2 system's
	// centre, 2 for the initial centre of a slice whose L1 cannot be read.
	FrequencyType byte `json:"frequency_type"`
	// OuterCode is 1 for none, 2 for RS(204,188), 3 for the advanced scheme.
	OuterCode byte `json:"outer_code"`
	// Modulation is numbered as the cable descriptor numbers it, or 0x80 for
	// the advanced scheme.
	Modulation byte `json:"modulation"`
	// InnerCode is 1 to 5 for the code rates 1/2 to 7/8, 8 for ISDB-S (TMCC), 9
	// for the advanced scheme (L1), 15 for none.
	InnerCode byte `json:"inner_code"`
}

// ParseAdvancedCableDelivery reads the advanced cable delivery system
// descriptor.
//
//	descriptor_tag_extension     8
//	plp_id                       8
//	active_OFDM_symbol_duration  3
//	guard_interval               3
//	bundled_channel              8
//	reserved                     2
//	per data slice:
//	  data_slice_id              8
//	  C2_tuning_frequency       32
//	  C2_tuning_frequency_type   2
//	  outer_code                 4
//	  modulation                 8
//	  inner_code                 4
//	  reserved                   6
func ParseAdvancedCableDelivery(body []byte) *AdvancedCableDelivery {
	if len(body) < 4 {
		return nil
	}
	read := &AdvancedCableDelivery{
		TagExtension:       body[0],
		PLPID:              body[1],
		OFDMSymbolDuration: body[2] >> 5,
		GuardInterval:      (body[2] >> 2) & 0x07,
		BundledChannel:     body[2]<<6 | body[3]>>2,
	}
	for at := 4; at+8 <= len(body); at += 8 {
		record := body[at : at+8]
		read.Slices = append(read.Slices, AdvancedCableSlice{
			DataSliceID:   record[0],
			FrequencyHz:   uint32(record[1])<<24 | uint32(record[2])<<16 | uint32(record[3])<<8 | uint32(record[4]),
			FrequencyType: record[5] >> 6,
			OuterCode:     (record[5] >> 2) & 0x0f,
			Modulation:    record[5]<<6 | record[6]>>2,
			InnerCode:     (record[6]&0x03)<<2 | record[7]>>6,
		})
	}
	return read
}

// CableDivisionCarrier is one carrier of the cable TS division system
// descriptor (tag 0xF9): where part of a divided multiplex is, and which
// services that part holds.
type CableDivisionCarrier struct {
	// FrequencyHz is where the carrier is on the cable.
	FrequencyHz uint32 `json:"frequency_hz"`
	FrameType   byte   `json:"frame_type"`
	FECOuter    byte   `json:"fec_outer"`
	// Modulation is numbered as the cable descriptor numbers it.
	Modulation byte   `json:"modulation"`
	SymbolRate uint32 `json:"symbol_rate"`
	FECInner   byte   `json:"fec_inner"`
	// Extra is future_use_data, kept as sent where the descriptor carried any.
	Extra      []byte   `json:"extra,omitempty"`
	ServiceIDs []uint16 `json:"service_ids,omitempty"`
}

// CableModulation reads a divided carrier's modulation the cable descriptor's
// way.
func (carrier CableDivisionCarrier) CableModulation() string {
	return (&CableDelivery{Modulation: carrier.Modulation}).CableModulation()
}

// InnerFEC is the same for its inner error correction.
func (carrier CableDivisionCarrier) InnerFEC() string { return innerFECNames[carrier.FECInner] }

// ParseCableTSDivision reads the cable TS division system descriptor.
//
//	per carrier:
//	  frequency               32  BCD, MHz with four decimals
//	  reserved                 7
//	  future_use_data_flag     1  nought means the data follows
//	  frame_type               4
//	  FEC_outer                4
//	  modulation               8
//	  symbol_rate             28  BCD, Msymbol/s with four decimals
//	  FEC_inner                4
//	  if the flag is nought: length 8, then that many bytes
//	  number_of_service        8
//	  service_id              16 each
func ParseCableTSDivision(body []byte) []CableDivisionCarrier {
	var carriers []CableDivisionCarrier
	for at := 0; at+11 <= len(body); {
		head := body[at : at+11]
		frequency, ok := binaryCodedDecimal(head[0:4])
		if !ok || frequency > math.MaxUint32/100 {
			break
		}
		symbolRate, ok := binaryCodedDecimalNibbles(head[7:11], 7)
		if !ok {
			break
		}
		carrier := CableDivisionCarrier{
			FrequencyHz: frequency * 100,
			FrameType:   head[5] >> 4,
			FECOuter:    head[5] & 0x0f,
			Modulation:  head[6],
			SymbolRate:  symbolRate * 100,
			FECInner:    head[10] & 0x0f,
		}
		at += 11
		if head[4]&0x01 == 0 {
			if at >= len(body) {
				break
			}
			length := int(body[at])
			at++
			if at+length > len(body) {
				break
			}
			carrier.Extra = append([]byte(nil), body[at:at+length]...)
			at += length
		}
		if at >= len(body) {
			break
		}
		count := int(body[at])
		at++
		for range count {
			if at+2 > len(body) {
				break
			}
			carrier.ServiceIDs = append(carrier.ServiceIDs, uint16(body[at])<<8|uint16(body[at+1]))
			at += 2
		}
		carriers = append(carriers, carrier)
	}
	return carriers
}

// transmissionTypes reads the transmission type loops of a TS information
// descriptor: a type, a count, and that many service identifiers, count times.
// Read as far as the bytes go rather than as far as the counts say.
func transmissionTypes(count int, rest []byte) []TransmissionType {
	var found []TransmissionType
	for range count {
		if len(rest) < 2 {
			break
		}
		kind, services := rest[0], int(rest[1])
		rest = rest[2:]
		read := TransmissionType{Type: kind}
		for range services {
			if len(rest) < 2 {
				break
			}
			read.ServiceIDs = append(read.ServiceIDs, uint16(rest[0])<<8|uint16(rest[1]))
			rest = rest[2:]
		}
		found = append(found, read)
	}
	return found
}
