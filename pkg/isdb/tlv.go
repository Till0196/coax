// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"cmp"
	"fmt"
	"maps"
	"net"
	"slices"

	"github.com/Till0196/coax/pkg/mmttlv"
)

// TLVNetworkInfo is one network of the TLV-NIT.
type TLVNetworkInfo struct {
	NetworkID uint16 `json:"network_id"`
	Name      string `json:"name,omitempty"`
	// System is whether this network says it is a broadcast at all. See
	// SystemManagement.
	System *SystemManagement `json:"system,omitempty"`
	// Links are the services the network as a whole points at.
	Links []Linkage `json:"links,omitempty"`
	// Streams are the TLV streams the network carries, which is how a scan
	// learns of the ones it has not tuned yet.
	Streams []TLVNetworkStreamInfo `json:"streams,omitempty"`
}

// TLVNetworkStreamInfo is one TLV stream of a network.
type TLVNetworkStreamInfo struct {
	TLVStreamID       uint16 `json:"tlv_stream_id"`
	OriginalNetworkID uint16 `json:"original_network_id"`
	// Satellite is where the network said this stream is carried.
	Satellite *SatelliteDelivery `json:"satellite,omitempty"`
	// Cable is the same for a stream carried on a cable. The advanced
	// services go out on a satellite, but what arrives on a remodulated lead
	// is on the cable -- and an operator rewriting the table says where it put
	// the multiplex rather than where the broadcast was.
	Cable *CableDelivery `json:"cable,omitempty"`
	// ChannelBonding is what the Japanese operators send instead: one record
	// per carrier, so that a multiplex split across several names all of them
	// and says which group they belong to. See ParseChannelBondingCable.
	ChannelBonding []ChannelBondingCarrier `json:"channel_bonding,omitempty"`
	// System is whether the stream says it is a broadcast at all.
	System *SystemManagement `json:"system,omitempty"`
	// Services lists what the network says the stream carries. The NIT gives
	// identifiers and types only; the names come from the MH-SDT.
	Services []TLVNetworkServiceInfo `json:"services,omitempty"`
	StreamDescription
}

// TLVNetworkServiceInfo names a service the TLV-NIT lists for a stream. The
// name is what a service table called it -- this stream's own, or the one
// describing a neighbour -- and is empty where none has yet.
type TLVNetworkServiceInfo struct {
	ServiceID uint16 `json:"service_id"`
	Type      byte   `json:"service_type"`
	Name      string `json:"name,omitempty"`
}

// RemoteControlKeyInfo is a button on the receiver's remote and the service it
// selects. Only the advanced services say this: ARIB TR-B39 36.1.2.3 requires
// the descriptor whenever a TLV-NIT [actual] is transmitted, which makes it the
// one place an MMT/TLV service's button can be read rather than guessed at from
// its identifier.
type RemoteControlKeyInfo struct {
	RemoteControlKeyID byte   `json:"remote_control_key_id"`
	ServiceID          uint16 `json:"service_id"`
}

type TLVFlowInfo struct {
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	// CompressionContext names the header-compression context of a flow whose
	// addresses were never transmitted in the sample. It is stored one higher
	// than the context identifier so that the zero value stays absent from the
	// report.
	CompressionContext int    `json:"compression_context,omitempty"`
	Packets            uint64 `json:"packets"`
}

type MMTPPacketIDInfo struct {
	PacketID uint16 `json:"packet_id"`
	Packets  uint64 `json:"packets"`
}

type TLVInfo struct {
	Bytes               uint64        `json:"bytes"`
	Packets             uint64        `json:"packets"`
	NullPackets         uint64        `json:"null_packets"`
	ControlPackets      uint64        `json:"control_packets"`
	IPv4Packets         uint64        `json:"ipv4_packets"`
	IPv6Packets         uint64        `json:"ipv6_packets"`
	CompressedIPPackets uint64        `json:"compressed_ip_packets"`
	MMTPPackets         uint64        `json:"mmtp_packets"`
	CompressedNoContext uint64        `json:"compressed_no_context,omitempty"`
	InvalidPackets      uint64        `json:"invalid_packets"`
	BeforeSyncBytes     uint64        `json:"before_sync_bytes,omitempty"` // stepped over before the first packet; not errors
	NetworkID           uint16        `json:"network_id,omitempty"`
	TLVStreamID         uint16        `json:"tlv_stream_id,omitempty"`
	OriginalNetworkID   uint16        `json:"original_network_id,omitempty"`
	Services            []ServiceInfo `json:"services,omitempty"`
	// CarriedServiceIDs is what the package list table says this stream holds,
	// which is not what Services lists: the MH-SDT describes every service
	// channel of the network. Empty when no package list arrived.
	CarriedServiceIDs []uint16 `json:"carried_service_ids,omitempty"`
	// RemoteControlKeys is which button each service of this network is on.
	// The TLV-NIT [actual] describes the network the stream belongs to, so
	// there is one set of them however many networks the table mentions.
	RemoteControlKeys []RemoteControlKeyInfo `json:"remote_control_keys,omitempty"`
	Networks          []TLVNetworkInfo       `json:"networks,omitempty"`
	Flows             []TLVFlowInfo          `json:"flows,omitempty"`
	PacketIDs         []MMTPPacketIDInfo     `json:"packet_ids,omitempty"`
}

// analyzeTLV reads a TLV stream, and reports its rate separately because that
// is a property of the carrier rather than of what the carrier describes.
func analyzeTLV(buffer []byte) (*TLVInfo, *float64) {
	reader := mmttlv.NewReader(buffer)
	var clock mmttlv.Clock
	serviceState := NewState()
	assemblers := make(map[string]*mmttlv.Reassembler)
	flows := make(map[string]*TLVFlowInfo)
	packetIDs := make(map[uint16]uint64)
	var mmtpPackets, invalidPackets uint64

	for {
		packet, ok := reader.Next()
		if !ok {
			break
		}
		if packet.Type == mmttlv.TypeControl {
			serviceState.PushTLVSection(packet.Payload)
			continue
		}
		datagram, ok := reader.Datagram(packet)
		if !ok {
			if packet.Type != mmttlv.TypeNull {
				invalidPackets++
			}
			continue
		}
		if datagram.IsNTP() {
			continue
		}
		mmtpPacket, ok := mmttlv.Parse(datagram.Payload)
		if !ok {
			invalidPackets++
			continue
		}
		mmtpPackets++
		packetIDs[mmtpPacket.PacketID]++
		clock.Add(mmtpPacket.PacketID, packet.Offset, mmtpPacket.Timestamp)
		// A packet whose compression context has not been seen has no
		// addresses to group it by, so its context identifier stands in. That
		// keeps the streams apart without inventing an address for them.
		flowKey := fmt.Sprintf("%s:%d>%s:%d", net.IP(datagram.Source), datagram.SourcePort, net.IP(datagram.Destination), datagram.DestinationPort)
		known := len(datagram.Source) != 0 && len(datagram.Destination) != 0
		if !known {
			flowKey = fmt.Sprintf("cid:%d", datagram.CID)
		}
		flow := flows[flowKey]
		if flow == nil {
			flow = &TLVFlowInfo{}
			if known {
				flow.Source = fmt.Sprintf("%s:%d", net.IP(datagram.Source), datagram.SourcePort)
				flow.Destination = fmt.Sprintf("%s:%d", net.IP(datagram.Destination), datagram.DestinationPort)
			} else {
				flow.CompressionContext = int(datagram.CID) + 1
			}
			flows[flowKey] = flow
		}
		flow.Packets++
		if mmtpPacket.PayloadType != mmttlv.PayloadTypeSignaling || mmtpPacket.Scrambled {
			continue
		}
		assembler := assemblers[flowKey]
		if assembler == nil {
			assembler = mmttlv.NewReassembler()
			assemblers[flowKey] = assembler
		}
		for _, message := range assembler.Push(mmtpPacket.PacketID, mmtpPacket.Payload) {
			serviceState.PushMessage(message.ID, message.Payload)
		}
	}

	stats := reader.Stats()
	if stats.Packets == 0 {
		return nil, nil
	}
	info := &TLVInfo{
		Bytes:               stats.Bytes,
		Packets:             stats.Packets,
		NullPackets:         stats.NullPackets,
		ControlPackets:      stats.ControlPackets,
		IPv4Packets:         stats.IPv4Packets,
		IPv6Packets:         stats.IPv6Packets,
		CompressedIPPackets: stats.CompressedIPPackets,
		MMTPPackets:         mmtpPackets,
		CompressedNoContext: stats.CompressedNoContext,
		InvalidPackets:      stats.InvalidPackets + invalidPackets + serviceState.InvalidSections,
		BeforeSyncBytes:     stats.BeforeSync,
		NetworkID:           serviceState.NetworkID,
		TLVStreamID:         serviceState.TLVStreamID,
		OriginalNetworkID:   serviceState.OriginalNetworkID,
	}
	appendTLVFlows(info, flows)
	appendMMTPPacketIDs(info, packetIDs)
	appendTLVServices(info, serviceState.Services)
	info.CarriedServiceIDs = serviceState.Carried
	appendTLVNetworks(info, serviceState.Networks)
	appendRemoteControlKeys(info, serviceState.Networks)
	return info, megabitsPerSecond(clock.Bitrate())
}

// appendTLVNetworks records what the TLV-NIT described, in network order so
// that repeated scans of the same carrier report the same thing.
func appendTLVNetworks(info *TLVInfo, networks map[uint16]*Network) {
	for _, id := range sortedKeys(networks) {
		network := networks[id]
		entry := TLVNetworkInfo{
			NetworkID: network.NetworkID,
			Name:      network.Name,
			System:    network.System,
			Links:     network.Links,
		}
		for _, stream := range network.Streams {
			reported := TLVNetworkStreamInfo{
				TLVStreamID:       stream.TLVStreamID,
				OriginalNetworkID: stream.OriginalNetworkID,
				Satellite:         stream.Satellite,
				Cable:             stream.Cable,
				ChannelBonding:    stream.ChannelBonding,
				System:            stream.System,
				StreamDescription: stream.StreamDescription,
			}
			for _, service := range stream.Services {
				reported.Services = append(reported.Services, TLVNetworkServiceInfo{
					ServiceID: service.ServiceID,
					Type:      service.Type,
					Name:      service.Name,
				})
			}
			entry.Streams = append(entry.Streams, reported)
		}
		info.Networks = append(info.Networks, entry)
	}
}

func appendTLVFlows(info *TLVInfo, flows map[string]*TLVFlowInfo) {
	for _, key := range sortedKeys(flows) {
		info.Flows = append(info.Flows, *flows[key])
	}
}

func appendMMTPPacketIDs(info *TLVInfo, packetIDs map[uint16]uint64) {
	for _, id := range sortedKeys(packetIDs) {
		info.PacketIDs = append(info.PacketIDs, MMTPPacketIDInfo{PacketID: id, Packets: packetIDs[id]})
	}
}

func appendTLVServices(info *TLVInfo, services map[uint16]Service) {
	for _, id := range sortedKeys(services) {
		service := services[id]
		info.Services = append(info.Services, ServiceInfo{
			ServiceID:  service.ServiceID,
			Type:       service.Type,
			Name:       service.Name,
			Provider:   service.Provider,
			FreeCAMode: service.FreeCAMode,
		})
	}
}

// Carried is the services this TLV stream actually holds, out of the ones its
// service table lists.
//
// The MH-SDT is no help on its own: its actual table describes every service
// channel the network defines, not the ones on this stream -- ARIB TR-B39 34.16
// says so, and a TLV carrier measured here declared seven services while
// carrying one. The package list table answers exactly that question.
//
// Until a package list arrives, describing a service is the only sign that the
// service is really here, and a sample too short for any description keeps the
// lot.
func (info *TLVInfo) Carried() []ServiceInfo {
	if info == nil {
		return nil
	}
	if len(info.CarriedServiceIDs) > 0 {
		held := make(map[uint16]bool, len(info.CarriedServiceIDs))
		for _, id := range info.CarriedServiceIDs {
			held[id] = true
		}
		return keepServices(info.Services, func(service ServiceInfo) bool {
			return held[service.ServiceID]
		})
	}
	described := func(service ServiceInfo) bool {
		return service.Name != "" || service.Provider != ""
	}
	for _, service := range info.Services {
		if described(service) {
			return keepServices(info.Services, described)
		}
	}
	return info.Services
}

func keepServices(services []ServiceInfo, keep func(ServiceInfo) bool) []ServiceInfo {
	kept := make([]ServiceInfo, 0, len(services))
	for _, service := range services {
		if keep(service) {
			kept = append(kept, service)
		}
	}
	return kept
}

// sortedKeys is the order a report lists what it gathered in a map. A list
// built by walking a Go map comes out in a different order every run, so
// sorting is what makes two scans of one carrier report the same thing.
func sortedKeys[K cmp.Ordered, V any](values map[K]V) []K {
	return slices.Sorted(maps.Keys(values))
}

// appendRemoteControlKeys records which button each service is on, in service
// order so that repeated scans of the same carrier report the same thing.
func appendRemoteControlKeys(info *TLVInfo, networks map[uint16]*Network) {
	gathered := map[uint16]byte{}
	for _, id := range sortedKeys(networks) {
		maps.Copy(gathered, networks[id].RemoteKeys)
	}
	for _, serviceID := range sortedKeys(gathered) {
		info.RemoteControlKeys = append(info.RemoteControlKeys,
			RemoteControlKeyInfo{RemoteControlKeyID: gathered[serviceID], ServiceID: serviceID})
	}
}
