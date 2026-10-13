// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// PSI and DVB/ARIB service information carried in transport-stream sections:
// PAT, NIT, SDT, and the ARIB service descriptors.
package isdb

import (
	"encoding/hex"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/Till0196/coax/pkg/mpegts"
)

type ServiceInfo struct {
	ServiceID  uint16 `json:"service_id"`
	Type       byte   `json:"service_type"`
	Name       string `json:"name,omitempty"`
	Provider   string `json:"provider,omitempty"`
	FreeCAMode bool   `json:"free_ca_mode"`
	// PartialReception is set for a service the network says rides in the partial
	// reception layer of a terrestrial carrier -- one-segment. Its service type says
	// only "data", so the layer is the one thing that tells it apart.
	PartialReception bool   `json:"partial_reception,omitempty"`
	NameRawHex       string `json:"name_raw_hex,omitempty"`
	DecodeLossy      bool   `json:"name_decode_lossy,omitempty"`
}

type TransportInfo struct {
	TransportStreamID uint16        `json:"transport_stream_id,omitempty"`
	OriginalNetworkID uint16        `json:"original_network_id,omitempty"`
	NetworkID         uint16        `json:"network_id,omitempty"`
	Services          []ServiceInfo `json:"services,omitempty"`
	// Networks is what the NIT says, one entry per network it describes, including
	// multiplexes a scan has not tuned yet.
	Networks []NetworkInfo `json:"networks,omitempty"`
	// NetworksComplete says every section of the stream's own network table was
	// seen. A sub-table arrives one numbered section at a time, so a reader that
	// stops at the first lists three transponders of a satellite that carries
	// twelve.
	NetworksComplete bool `json:"networks_complete,omitempty"`
	// Programs is what the program association table lists and each program's
	// map says, in program number order.
	Programs []ProgramInfo `json:"programs,omitempty"`
}

// ProgramInfo is one program of the stream: where its map is and what the map
// says it is made of.
type ProgramInfo struct {
	ProgramNumber uint16 `json:"program_number"`
	// MapPID is where the program's map is sent.
	MapPID uint16 `json:"pmt_pid"`
	// Mapped says the map itself was seen; the rest is empty where it was not.
	Mapped  bool            `json:"mapped"`
	PCRPID  uint16          `json:"pcr_pid,omitempty"`
	Streams []ProgramStream `json:"streams,omitempty"`
	// System is the program's system management descriptor: STD-B10 6.2.21 puts
	// it in the map or the network table, and a program the operator carries for
	// itself says it is not a broadcast.
	System *SystemManagement `json:"system,omitempty"`
}

// ProgramStream is one elementary stream of a program.
type ProgramStream struct {
	Type byte   `json:"stream_type"`
	PID  uint16 `json:"pid"`
}

// TransmissionType is one loop of the TS information descriptor: a type and the
// services sent that way.
type TransmissionType struct {
	Type       byte     `json:"type"`
	ServiceIDs []uint16 `json:"service_ids,omitempty"`
}

// NetworkInfo is one network of the NIT.
type NetworkInfo struct {
	NetworkID uint16 `json:"network_id"`
	// Actual distinguishes the network this stream belongs to from the others.
	Actual     bool   `json:"actual"`
	Name       string `json:"name,omitempty"`
	NameRawHex string `json:"name_raw_hex,omitempty"`
	// System is whether this network says it is a broadcast at all, where it said.
	System *SystemManagement `json:"system,omitempty"`
	// Links are the services the network points at.
	Links      []Linkage          `json:"links,omitempty"`
	Transports []NetworkTransport `json:"transports,omitempty"`
}

// NetworkTransport is one multiplex of a network.
type NetworkTransport struct {
	TransportStreamID uint16 `json:"transport_stream_id"`
	OriginalNetworkID uint16 `json:"original_network_id"`
	// RemoteControlKeyID is the button a receiver puts this multiplex on, from
	// the ARIB TS information descriptor. Zero means the descriptor was absent.
	RemoteControlKeyID byte   `json:"remote_control_key_id,omitempty"`
	TSName             string `json:"ts_name,omitempty"`
	// TransmissionTypes is the rest of that descriptor: which services ride in
	// which kind of transmission. ARIB STD-B10 6.2.42 leaves the type byte to the
	// operating rules, so it is kept as sent; TR-B14 uses it for the hierarchy of
	// a terrestrial carrier.
	TransmissionTypes []TransmissionType `json:"transmission_types,omitempty"`
	Services          []NetworkService   `json:"services,omitempty"`
	// PartialReceptionServiceIDs is what the partial reception descriptor names.
	// ARIB TR-B14 30.4.3.3 has it in this loop whenever the carrier uses the
	// layer, so an empty list on a terrestrial carrier means the carrier does not
	// use it.
	PartialReceptionServiceIDs []uint16 `json:"partial_reception_service_ids,omitempty"`
	// Satellite is where the multiplex is carried, when the network said so.
	Satellite *SatelliteDelivery `json:"satellite,omitempty"`
	// Cable is the same for a network carried on a cable.
	Cable *CableDelivery `json:"cable,omitempty"`
	// AdvancedCable is where the multiplex is on an advanced (C2) cable.
	AdvancedCable *AdvancedCableDelivery `json:"advanced_cable,omitempty"`
	// CableDivision is the carriers a multiplex was divided over by the cable TS
	// division scheme, each with the services it holds.
	CableDivision []CableDivisionCarrier `json:"cable_division,omitempty"`
	// System is whether this multiplex says it is a broadcast at all, where it
	// said.
	System *SystemManagement `json:"system,omitempty"`
	// ChannelBonding is the carriers a multiplex too wide for one was split over. A
	// cable and a transport stream are both split this way, so it is read in both
	// tables. See ParseChannelBondingCable.
	ChannelBonding []ChannelBondingCarrier `json:"channel_bonding,omitempty"`
	// Links are the services this multiplex points at.
	Links []Linkage `json:"links,omitempty"`
	// Emergency is sent only while such a broadcast is being made.
	Emergency *EmergencyInformation `json:"emergency,omitempty"`
	// Connected says this multiplex is one of a group sent together.
	Connected *ConnectedTransmission `json:"connected_transmission,omitempty"`
	// ServiceGroups pairs services that belong together.
	ServiceGroups []ServiceGroup `json:"service_groups,omitempty"`
	// AreaBroadcasting names the transmitting stations of an area broadcast.
	AreaBroadcasting *AreaBroadcastingInformation `json:"area_broadcasting,omitempty"`
	// Terrestrial is where the multiplex goes out over the air, where the network
	// said so. It is the broadcaster's word: a cable passing the broadcast through
	// does not rewrite it, so a carrier received somewhere this does not name is a
	// carrier something moved.
	Terrestrial *TerrestrialDelivery `json:"terrestrial,omitempty"`
}

// TerrestrialDelivery is ARIB's terrestrial delivery system descriptor, which
// has a tag and a shape of its own rather than DVB's.
type TerrestrialDelivery struct {
	// AreaCode is the broadcast area the transmitters serve, as ARIB numbers them.
	// Two area codes on one lead are two areas' broadcasts.
	AreaCode uint16 `json:"area_code"`
	// CarriersHz is every transmitter of this multiplex, on the same reckoning as a
	// tune takes -- the plan's seventh of a megahertz included.
	CarriersHz []uint32 `json:"carriers_hz,omitempty"`
}

// CableDelivery is the cable delivery system descriptor. Unlike the satellite
// one, its frequency is the frequency to tune.
type CableDelivery struct {
	// FrequencyHz is where the multiplex is on the cable.
	FrequencyHz uint32 `json:"frequency_hz"`
	// Modulation is the descriptor's raw modulation field: 1 is 16QAM and each
	// step doubles, so 3 is 64QAM and 5 is 256QAM; 7 is 1024QAM.
	Modulation byte `json:"modulation"`
	// SymbolRate is in symbols per second.
	SymbolRate uint32 `json:"symbol_rate"`
	// FECOuter and FECInner are the descriptor's raw error-correction fields.
	FECOuter byte `json:"fec_outer"`
	FECInner byte `json:"fec_inner"`
}

// SatelliteDelivery is the satellite delivery system descriptor. The frequency is
// the downlink one rather than the one a tuner is given: a receiver subtracts its
// low-noise block's oscillator to get the intermediate frequency.
type SatelliteDelivery struct {
	// FrequencyKHz is the downlink frequency.
	FrequencyKHz uint32 `json:"frequency_khz"`
	// OrbitalPosition is in tenths of a degree, East true for the hemisphere.
	OrbitalPosition uint16 `json:"orbital_position"`
	East            bool   `json:"east"`
	// Polarisation is one of "H", "V", "L" or "R". Japanese satellite broadcasting
	// is circular, and the hand decides which oscillator the receiver used, and so
	// which intermediate frequency it arrives on.
	Polarisation string `json:"polarisation"`
	// Modulation is the descriptor's raw modulation field, read ARIB's way: the
	// low five bits as one number. DVB divides those same bits into three fields
	// and nothing says which was meant, so the reading belongs to whoever knows
	// what they tuned. See the DVB* methods, which offer the other one.
	Modulation byte `json:"modulation"`
	// SymbolRate is in symbols per second.
	SymbolRate uint32 `json:"symbol_rate"`
	// FECInner is the descriptor's raw inner error-correction field.
	FECInner byte `json:"fec_inner"`
}

// NetworkService names a service the NIT lists for a multiplex. The NIT gives
// identifiers and types only; the name comes from a service table -- this
// stream's own, or STD-B10's SDT [other] for a neighbour. Empty where no table
// named it, which on a short dwell is most of a slow-cycling neighbour table.
type NetworkService struct {
	ServiceID uint16 `json:"service_id"`
	Type      byte   `json:"service_type"`
	Name      string `json:"name,omitempty"`
	// DecodeLossy is set where something was dropped reading the name.
	DecodeLossy bool `json:"decode_lossy,omitempty"`
}

func AnalyzePSI(packets [][]byte) *TransportInfo {
	collectors := map[uint16]*mpegts.SectionCollector{0x00: {}, 0x10: {}, 0x11: {}}
	for _, packet := range packets {
		pid, payload, start, ok := mpegts.Payload(packet)
		collector := collectors[pid]
		if !ok || collector == nil {
			continue
		}
		collector.Push(payload, start)
	}

	info := &TransportInfo{}
	services := make(map[uint16]ServiceInfo)
	programs := associatePrograms(collectors[0x00].Sections(), info)
	info.Programs = mapPrograms(packets, programs)
	networks := make(map[uint16]*NetworkInfo)
	// Every section of a sub-table is retransmitted for as long as the stream runs,
	// so each is taken once per version rather than folded in again per repeat.
	seen := make(map[mpegts.SectionKey]bool)
	// The sections of the stream's own network table, for telling a table that has
	// arrived whole from one that has only begun.
	actual := newSubTable()
	for _, section := range collectors[0x10].Sections() {
		if len(section) >= 10 && (section[0] == 0x40 || section[0] == 0x41) && mpegts.ValidSection(section) {
			key := mpegts.SectionKeyOf(section)
			if section[0] == 0x40 {
				info.NetworkID = uint16(section[3])<<8 | uint16(section[4])
				actual.note(key.Version, key.Number, section[7])
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			parseNetworkSection(section, networks)
		}
	}
	info.NetworksComplete = actual.complete()
	// The service tables name what the network table only numbered. A satellite
	// stream carries one per multiplex, so a single lock can name most of the
	// network.
	named := map[serviceName]nameOfService{}
	for _, section := range collectors[0x11].Sections() {
		if !mpegts.ValidSection(section) {
			continue
		}
		takeServiceNames(section, named)
	}
	for _, id := range slices.Sorted(maps.Keys(networks)) {
		nameNetworkServices(networks[id], named)
		info.Networks = append(info.Networks, *networks[id])
	}
	identified := false
	for _, section := range collectors[0x11].Sections() {
		if len(section) < 12 || section[0] != 0x42 || !mpegts.ValidSection(section) {
			continue
		}
		// The stream's own table says what the stream is. Every repeat says the
		// same, so the first is kept.
		if !identified {
			identified = true
			info.TransportStreamID = uint16(section[3])<<8 | uint16(section[4])
			info.OriginalNetworkID = uint16(section[8])<<8 | uint16(section[9])
		}
		end := len(section) - 4
		for offset := 11; offset+5 <= end; {
			serviceID := uint16(section[offset])<<8 | uint16(section[offset+1])
			length := int(section[offset+3]&0x0f)<<8 | int(section[offset+4])
			next := offset + 5 + length
			if next > end {
				break
			}
			// free_CA_mode reads backwards: set means one or more of the service's
			// streams are under conditional access, so true is a pay service.
			// A section is sent again for as long as the stream runs, so a service
			// already described is left as it was.
			if _, described := services[serviceID]; !described {
				service := ServiceInfo{ServiceID: serviceID, FreeCAMode: section[offset+3]&0x10 != 0}
				parseServiceDescriptors(section[offset+5:next], &service)
				services[serviceID] = service
			}
			offset = next
		}
	}
	for _, id := range partialReception(info, networks) {
		if service, described := services[id]; described {
			service.PartialReception = true
			services[id] = service
		}
	}
	for _, id := range slices.Sorted(maps.Keys(services)) {
		info.Services = append(info.Services, services[id])
	}
	if info.TransportStreamID == 0 && info.OriginalNetworkID == 0 && info.NetworkID == 0 &&
		len(info.Services) == 0 && len(info.Networks) == 0 && len(info.Programs) == 0 {
		return nil
	}
	return info
}

// partialReception is the services this stream carries in its partial reception
// layer. Only the entry for this stream counts: the network's other carriers have
// layers of their own.
func partialReception(info *TransportInfo, networks map[uint16]*NetworkInfo) []uint16 {
	network := networks[info.NetworkID]
	if network == nil {
		return nil
	}
	for _, transport := range network.Transports {
		if transport.TransportStreamID == info.TransportStreamID {
			return transport.PartialReceptionServiceIDs
		}
	}
	return nil
}

// subTable is which sections of one sub-table have been seen. A version change
// starts it again: the sections of the old version describe a table that no
// longer stands.
type subTable struct {
	version  int
	last     byte
	sections map[byte]bool
}

func newSubTable() *subTable {
	return &subTable{version: -1, sections: map[byte]bool{}}
}

func (table *subTable) note(version, number, last byte) {
	if int(version) != table.version {
		table.version, table.sections = int(version), map[byte]bool{}
	}
	table.last = last
	table.sections[number] = true
}

// complete says every section from the first to the last has been seen. A
// table nothing has been seen of is not complete: there is no table.
func (table *subTable) complete() bool {
	if table.version < 0 {
		return false
	}
	for number := 0; number <= int(table.last); number++ {
		if !table.sections[byte(number)] {
			return false
		}
	}
	return true
}

// parseNetworkSection folds one NIT section into the networks seen so far. A
// network is described across as many sections as it needs, so each one adds to
// the entry rather than replacing it.
func parseNetworkSection(section []byte, networks map[uint16]*NetworkInfo) {
	networkID := uint16(section[3])<<8 | uint16(section[4])
	network := networks[networkID]
	if network == nil {
		network = &NetworkInfo{NetworkID: networkID, Actual: section[0] == 0x40}
		networks[networkID] = network
	}
	end := len(section) - 4
	descriptorsLength := int(section[8]&0x0f)<<8 | int(section[9])
	offset := 10 + descriptorsLength
	if offset+2 > end {
		return
	}
	parseNetworkDescriptors(section[10:offset], network)
	loopLength := int(section[offset]&0x0f)<<8 | int(section[offset+1])
	offset += 2
	loopEnd := min(offset+loopLength, end)
	for offset+6 <= loopEnd {
		transport := NetworkTransport{
			TransportStreamID: uint16(section[offset])<<8 | uint16(section[offset+1]),
			OriginalNetworkID: uint16(section[offset+2])<<8 | uint16(section[offset+3]),
		}
		length := int(section[offset+4]&0x0f)<<8 | int(section[offset+5])
		// An entry that says it runs past the loop is read as far as the loop goes.
		next := min(offset+6+length, loopEnd)
		parseTransportDescriptors(section[offset+6:next], &transport)
		offset = next
		// Kept apart by original network as well as by stream: a cable carries
		// several networks' tables, each numbering its streams for itself. The
		// first description is kept, so a table sent again under a new version
		// does not list the multiplex twice.
		if slices.ContainsFunc(network.Transports, func(known NetworkTransport) bool {
			return known.TransportStreamID == transport.TransportStreamID &&
				known.OriginalNetworkID == transport.OriginalNetworkID
		}) {
			continue
		}
		network.Transports = append(network.Transports, transport)
	}
}

// ParseSatelliteDelivery reads the satellite delivery system descriptor. It is
// exported because the same descriptor appears in the TLV network information
// table the advanced satellite services carry.
//
// Everything in it is binary-coded decimal, two digits to a byte, each field's
// decimal point at a fixed place: frequency in GHz after three digits, orbital
// position in degrees after three, symbol rate in megasymbols after three.
//
// The byte between the position and the symbol rate is where ARIB and DVB part
// company: DVB divides it into roll-off, system and type, while ARIB STD-B10
// gives five bits to one modulation field. This reads it ARIB's way.
func ParseSatelliteDelivery(body []byte) *SatelliteDelivery {
	if len(body) < 11 {
		return nil
	}
	frequency, ok := binaryCodedDecimal(body[0:4])
	if !ok {
		return nil
	}
	position, ok := binaryCodedDecimal(body[4:6])
	if !ok {
		return nil
	}
	// Seven digits of symbol rate then four bits of error correction, so the last
	// nibble is not a digit and the count is odd.
	symbolRate, ok := binaryCodedDecimalNibbles(body[7:11], 7)
	if !ok {
		return nil
	}
	polarisations := [4]string{"H", "V", "L", "R"}
	return &SatelliteDelivery{
		FrequencyKHz:    frequency * 10,
		OrbitalPosition: uint16(position),
		East:            body[6]&0x80 != 0,
		Polarisation:    polarisations[(body[6]>>5)&0x03],
		Modulation:      body[6] & 0x1f,
		SymbolRate:      symbolRate * 100,
		FECInner:        body[10] & 0x0f,
	}
}

// The DVB reading of the five bits ARIB gives to one modulation field. The bits are
// the same bits, so only the caller's knowledge of what it tuned tells them apart.
//
//	bits 4-3  roll_off           00 = 0.35, 01 = 0.25, 10 = 0.20
//	bit  2    modulation_system  0 = DVB-S, 1 = DVB-S2
//	bits 1-0  modulation_type    00 = auto, 01 = QPSK, 10 = 8PSK, 11 = 16-QAM

// DVBSecondGeneration reports whether the descriptor's modulation_system bit
// says DVB-S2 rather than DVB-S.
func (delivery *SatelliteDelivery) DVBSecondGeneration() bool {
	return delivery.Modulation&0x04 != 0
}

// DVBRollOff is the roll-off as a channel file writes it, and nothing for the
// reserved value or for DVB-S, where the field means nothing.
func (delivery *SatelliteDelivery) DVBRollOff() string {
	if !delivery.DVBSecondGeneration() {
		return ""
	}
	switch (delivery.Modulation >> 3) & 0x03 {
	case 0:
		return "35"
	case 1:
		return "25"
	case 2:
		return "20"
	}
	return ""
}

// DVBModulation is the modulation as a channel file writes it, and nothing
// where the descriptor said to work it out.
func (delivery *SatelliteDelivery) DVBModulation() string {
	switch delivery.Modulation & 0x03 {
	case 1:
		return "QPSK"
	case 2:
		return "PSK/8"
	case 3:
		return "QAM/16"
	}
	// Zero is "auto", the descriptor declining to say.
	return ""
}

// ParseCableDelivery reads the cable delivery system descriptor. Its frequency
// is megahertz with the point after four digits, which works out as hundreds of
// hertz.
func ParseCableDelivery(body []byte) *CableDelivery {
	if len(body) < 11 {
		return nil
	}
	frequency, ok := binaryCodedDecimal(body[0:4])
	if !ok {
		return nil
	}
	symbolRate, ok := binaryCodedDecimalNibbles(body[7:11], 7)
	if !ok {
		return nil
	}
	// Checked, because what is being read may not be this descriptor at all: a
	// length that let something else through should give nothing rather than a
	// wrapped number.
	if frequency > math.MaxUint32/100 {
		return nil
	}
	return &CableDelivery{
		FrequencyHz: frequency * 100,
		FECOuter:    body[5] & 0x0f,
		Modulation:  body[6],
		SymbolRate:  symbolRate * 100,
		FECInner:    body[10] & 0x0f,
	}
}

// ParseTerrestrialDelivery reads ARIB's terrestrial delivery system descriptor:
// an area code in twelve bits, three bits each of guard interval and
// transmission mode, then one frequency per transmitter. The frequencies are
// counted in sevenths of a megahertz: 3312 sevenths is channel 13 at 473142857
// Hz.
func ParseTerrestrialDelivery(body []byte) *TerrestrialDelivery {
	if len(body) < 2 {
		return nil
	}
	described := &TerrestrialDelivery{
		AreaCode: uint16(body[0])<<4 | uint16(body[1]>>4),
	}
	for at := 2; at+2 <= len(body); at += 2 {
		sevenths := uint64(body[at])<<8 | uint64(body[at+1])
		described.CarriersHz = append(described.CarriersHz, uint32(sevenths*1_000_000/7))
	}
	return described
}

// SystemManagement is the system management descriptor, which is how a table says
// whether what it describes is a broadcast at all.
//
// ARIB STD-B10 6.2.21 makes it mandatory in either the PMT or the network table.
// A multiplex an operator carries for its own purposes says non-broadcasting, and
// a scan that could not tell would file it beside the television.
type SystemManagement struct {
	// Broadcasting is what the top two bits say. False is the descriptor saying so
	// outright, not the descriptor being absent.
	Broadcasting bool `json:"broadcasting"`
	// BroadcastingIdentifier names the standard, in six bits.
	BroadcastingIdentifier byte `json:"broadcasting_identifier"`
	// AdditionalIdentification is the low byte of the identifier, Extra what
	// followed.
	AdditionalIdentification byte   `json:"additional_identification"`
	Extra                    []byte `json:"extra,omitempty"`
}

// ParseSystemManagement reads the system management descriptor.
//
//	system_management_id  16   broadcasting_flag(2) identifier(6) additional(8)
//	additional_identification_info  8 each, to the end
func ParseSystemManagement(body []byte) *SystemManagement {
	if len(body) < 2 {
		return nil
	}
	return &SystemManagement{
		// Zero is broadcasting; one and two are not, and three is undefined.
		Broadcasting:             body[0]>>6 == 0,
		BroadcastingIdentifier:   body[0] & 0x3f,
		AdditionalIdentification: body[1],
		Extra:                    append([]byte(nil), body[2:]...),
	}
}

// ChannelBondingCarrier is one carrier of the cable multiple carrier transmission
// delivery system descriptor, which is how a multiplex too wide for one cable
// carrier says which carriers it was split over.
//
// ARIB STD-B10 gives it tag 0xF3 and leaves the shape to the Ministry's 2015
// notification No. 92 and JCTEA STD-003; the cable UHDTV working party's report
// sets it out in section 6.4. One record per carrier, in carrier_sequence order.
// It is not the advanced cable delivery system descriptor, which is tag 0xF4.
type ChannelBondingCarrier struct {
	// FrequencyHz is where the carrier is on the cable, the frequency a tune takes.
	FrequencyHz uint32 `json:"frequency_hz"`
	// FrameType is which shape of multiplexing frame this is.
	FrameType byte `json:"frame_type"`
	FECOuter  byte `json:"fec_outer"`
	// Modulation is the raw field, numbered as the cable descriptor numbers it.
	Modulation byte   `json:"modulation"`
	SymbolRate uint32 `json:"symbol_rate"`
	// FECInner is 0xF by the specification. Reported as sent rather than assumed.
	FECInner byte `json:"fec_inner"`
	// GroupID names the group of carriers this one belongs to: every carrier of one
	// split multiplex carries the same group.
	GroupID byte `json:"group_id"`
}

// channelBondingCarrierBytes is one carrier's record.
const channelBondingCarrierBytes = 12

// ParseChannelBondingCable reads the cable multiple carrier transmission delivery
// system descriptor, which the TLV network table carries where the advanced
// services have been put on a cable.
//
// It is neither the satellite descriptor nor the ordinary cable one, and a reader
// looking for those finds an operator's table saying nothing about where anything
// is. On one feed every stream of the 高度BS network carried this and no other.
//
//	frequency               32  BCD, MHz with the point after four digits
//	reserved_for_future_use  8
//	frame_type               4
//	FEC_outer                4
//	modulation               8
//	symbol_rate             28  BCD, Msymbol/s with the point after three
//	FEC_inner                4
//	group_id                 8
func ParseChannelBondingCable(body []byte) []ChannelBondingCarrier {
	var carriers []ChannelBondingCarrier
	for at := 0; at+channelBondingCarrierBytes <= len(body); at += channelBondingCarrierBytes {
		record := body[at : at+channelBondingCarrierBytes]
		frequency, ok := binaryCodedDecimal(record[0:4])
		if !ok {
			return carriers
		}
		symbolRate, ok := binaryCodedDecimalNibbles(record[7:11], 7)
		if !ok || frequency > math.MaxUint32/100 {
			return carriers
		}
		carriers = append(carriers, ChannelBondingCarrier{
			// Megahertz with the point after four digits: hundreds of hertz.
			FrequencyHz: frequency * 100,
			FrameType:   record[5] >> 4,
			FECOuter:    record[5] & 0x0f,
			Modulation:  record[6],
			SymbolRate:  symbolRate * 100,
			FECInner:    record[10] & 0x0f,
			GroupID:     record[11],
		})
	}
	return carriers
}

// CableModulation is the modulation as a channel file writes it. The descriptor
// numbers them from 16-QAM upwards, doubling at each step, zero being "not
// defined"; JCTEA adds 7 for 1024-QAM.
func (delivery *CableDelivery) CableModulation() string {
	switch delivery.Modulation {
	case 1:
		return "QAM/16"
	case 2:
		return "QAM/32"
	case 3:
		return "QAM/64"
	case 4:
		return "QAM/128"
	case 5:
		return "QAM/256"
	case 7:
		return "QAM/1024"
	}
	return ""
}

// innerFECNames are the inner error-correction rates as a channel file writes
// them. Zero is the descriptor declining to say and fifteen is none at all.
var innerFECNames = map[byte]string{
	1: "1/2", 2: "2/3", 3: "3/4", 4: "5/6", 5: "7/8", 6: "8/9",
	7: "3/5", 8: "4/5", 9: "9/10", 15: "NONE",
}

// InnerFEC is the inner error-correction rate as a channel file writes it, and
// nothing where the descriptor did not say.
func (delivery *SatelliteDelivery) InnerFEC() string { return innerFECNames[delivery.FECInner] }

// InnerFEC is the same for a cable multiplex.
func (delivery *CableDelivery) InnerFEC() string { return innerFECNames[delivery.FECInner] }

// CableModulation and InnerFEC read one bonded carrier the cable descriptor's
// way.
func (carrier ChannelBondingCarrier) CableModulation() string {
	return (&CableDelivery{Modulation: carrier.Modulation}).CableModulation()
}

func (carrier ChannelBondingCarrier) InnerFEC() string { return innerFECNames[carrier.FECInner] }

// binaryCodedDecimal reads packed decimal digits, two to a byte.
func binaryCodedDecimal(data []byte) (uint32, bool) {
	return binaryCodedDecimalNibbles(data, len(data)*2)
}

// binaryCodedDecimalNibbles reads a given number of digits from the front of a
// field, for the one that does not fill its bytes. It reports nothing when a
// nibble is not a digit, which catches a descriptor that is not what it claims.
func binaryCodedDecimalNibbles(data []byte, digits int) (uint32, bool) {
	if digits > len(data)*2 {
		return 0, false
	}
	var value uint32
	for index := range digits {
		nibble := data[index/2] >> 4
		if index%2 == 1 {
			nibble = data[index/2] & 0x0f
		}
		if nibble > 9 {
			return 0, false
		}
		value = value*10 + uint32(nibble)
	}
	return value, true
}

// parseNetworkDescriptors reads the descriptors that describe the network as a
// whole, which is where its name lives.
func parseNetworkDescriptors(data []byte, network *NetworkInfo) {
	linked := len(network.Links) != 0
	var links []Linkage
	for offset := 0; offset+2 <= len(data); {
		length := int(data[offset+1])
		next := offset + 2 + length
		if next > len(data) {
			return
		}
		body := data[offset+2 : next]
		switch data[offset] {
		case 0x40: // network_name_descriptor
			if network.Name == "" {
				decoded := DecodeString(body)
				network.Name = strings.TrimSpace(decoded.Text)
				network.NameRawHex = hex.EncodeToString(body)
			}
		case 0xfe: // system_management_descriptor
			if network.System == nil {
				network.System = ParseSystemManagement(body)
			}
		case 0x4a: // linkage_descriptor
			// The network's links are taken from the first section that has any, so
			// a section sent again does not list them twice.
			if link := ParseLinkage(body); link != nil && !linked {
				links = append(links, *link)
			}
		case 0x42: // stuffing_descriptor
			// Padding, or a descriptor struck out by the one before it.
		}
		offset = next
	}
	if !linked {
		network.Links = links
	}
}

// parseTransportDescriptors reads what a network says about one of its
// multiplexes: which services it carries, and the button it sits on.
func parseTransportDescriptors(data []byte, transport *NetworkTransport) {
	for offset := 0; offset+2 <= len(data); {
		length := int(data[offset+1])
		// A descriptor that says it runs past the entry is read as far as the entry
		// goes, and is the last.
		next := min(offset+2+length, len(data))
		body := data[offset+2 : next]
		switch data[offset] {
		case 0x41: // service_list_descriptor
			for entry := 0; entry+3 <= len(body); entry += 3 {
				transport.Services = append(transport.Services, NetworkService{
					ServiceID: uint16(body[entry])<<8 | uint16(body[entry+1]),
					Type:      body[entry+2],
				})
			}
		case 0x43: // satellite_delivery_system_descriptor
			if satellite := ParseSatelliteDelivery(body); satellite != nil {
				transport.Satellite = satellite
			}
		case 0xfa: // ARIB's terrestrial_delivery_system_descriptor
			if terrestrial := ParseTerrestrialDelivery(body); terrestrial != nil {
				transport.Terrestrial = terrestrial
			}
		case 0x44: // cable_delivery_system_descriptor
			if cable := ParseCableDelivery(body); cable != nil {
				transport.Cable = cable
			}
		case 0xfb: // ARIB partial reception descriptor
			for entry := 0; entry+2 <= len(body); entry += 2 {
				transport.PartialReceptionServiceIDs = append(transport.PartialReceptionServiceIDs, uint16(body[entry])<<8|uint16(body[entry+1]))
			}
		case 0xf3: // channel_bonding_cable_delivery_system_descriptor
			if carriers := ParseChannelBondingCable(body); len(carriers) != 0 {
				transport.ChannelBonding = carriers
			}
		case 0x4a: // linkage_descriptor
			if link := ParseLinkage(body); link != nil {
				transport.Links = append(transport.Links, *link)
			}
		case 0xfc: // emergency_information_descriptor
			if warning := ParseEmergencyInformation(body); warning != nil {
				transport.Emergency = warning
			}
		case 0xdd: // connected_transmission_descriptor
			if connected := ParseConnectedTransmission(body); connected != nil {
				transport.Connected = connected
			}
		case 0xf4: // advanced_cable_delivery_system_descriptor
			if advanced := ParseAdvancedCableDelivery(body); advanced != nil {
				transport.AdvancedCable = advanced
			}
		case 0xf9: // cable_TS_division_system_descriptor
			if carriers := ParseCableTSDivision(body); len(carriers) != 0 {
				transport.CableDivision = carriers
			}
		case 0xfe: // system_management_descriptor
			if system := ParseSystemManagement(body); system != nil {
				transport.System = system
			}
		case 0xe0: // service_group_descriptor
			if group := ParseServiceGroup(body); group != nil {
				transport.ServiceGroups = append(transport.ServiceGroups, *group)
			}
		case 0xe1: // area_broadcasting_information_descriptor
			if area := ParseAreaBroadcastingInformation(body); area != nil {
				transport.AreaBroadcasting = area
			}
		case 0x42: // stuffing_descriptor
			// Padding, or a descriptor struck out by the one before it.
		case 0xcd: // ARIB TS information descriptor
			if len(body) < 2 {
				break
			}
			nameLength := int(body[1] >> 2)
			if 2+nameLength > len(body) {
				break
			}
			transport.RemoteControlKeyID = body[0]
			transport.TSName = strings.TrimSpace(DecodeString(body[2 : 2+nameLength]).Text)
			transport.TransmissionTypes = transmissionTypes(int(body[1]&0x03), body[2+nameLength:])
		}
		offset = next
	}
}

// serviceName is one service anywhere in the network. A service identifier is
// unique only within its multiplex, so all three parts are the key.
type serviceName struct {
	originalNetworkID uint16
	transportStreamID uint16
	serviceID         uint16
}

type nameOfService struct {
	name  string
	lossy bool
}

// takeServiceNames folds one service table into the names read so far. Both kinds
// count: this stream's own table and a neighbour's.
func takeServiceNames(section []byte, into map[serviceName]nameOfService) {
	table := ParseServiceDescription(section)
	if table == nil {
		return
	}
	for _, service := range table.Services {
		descriptor, ok := FindDescriptor(service.Descriptors, 0x48)
		if !ok || len(descriptor.Body) < 3 {
			continue
		}
		providerLength := int(descriptor.Body[1])
		if 2+providerLength >= len(descriptor.Body) {
			continue
		}
		nameLength := int(descriptor.Body[2+providerLength])
		start := 3 + providerLength
		if start+nameLength > len(descriptor.Body) {
			continue
		}
		read := DecodeString(descriptor.Body[start : start+nameLength])
		key := serviceName{table.OriginalNetworkID, table.TransportStreamID, service.ServiceID}
		// The first table to name a service is kept; every repeat says the same thing.
		if _, seen := into[key]; !seen {
			into[key] = nameOfService{name: strings.TrimSpace(read.Text), lossy: !read.Lossless()}
		}
	}
}

// nameNetworkServices puts the names onto the network's own listing.
func nameNetworkServices(network *NetworkInfo, named map[serviceName]nameOfService) {
	for index := range network.Transports {
		transport := &network.Transports[index]
		for at := range transport.Services {
			service := &transport.Services[at]
			read, ok := named[serviceName{transport.OriginalNetworkID,
				transport.TransportStreamID, service.ServiceID}]
			if !ok {
				continue
			}
			service.Name, service.DecodeLossy = read.name, read.lossy
		}
	}
}

func parseServiceDescriptors(data []byte, service *ServiceInfo) {
	for offset := 0; offset+2 <= len(data); {
		length := int(data[offset+1])
		next := offset + 2 + length
		if next > len(data) {
			return
		}
		if data[offset] == 0x48 && length >= 3 {
			body := data[offset+2 : next]
			service.Type = body[0]
			providerLength := int(body[1])
			if 2+providerLength >= len(body) {
				return
			}
			providerRaw := body[2 : 2+providerLength]
			nameLength := int(body[2+providerLength])
			nameStart := 3 + providerLength
			if nameStart+nameLength > len(body) {
				return
			}
			nameRaw := body[nameStart : nameStart+nameLength]
			provider, name := DecodeString(providerRaw), DecodeString(nameRaw)
			service.Provider = strings.TrimSpace(provider.Text)
			service.Name = strings.TrimSpace(name.Text)
			service.NameRawHex = hex.EncodeToString(nameRaw)
			service.DecodeLossy = !name.Lossless()
		}
		offset = next
	}
}
