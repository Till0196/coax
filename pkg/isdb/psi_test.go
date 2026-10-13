// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/pkg/mpegts"
)

func TestAnalyzeNIT(t *testing.T) {
	body := []byte{0, 4, 0xc1, 0, 0, 0xf0, 0, 0xf0, 0}
	sectionLength := len(body) + 4
	section := []byte{0x40, 0xb0 | byte(sectionLength>>8), byte(sectionLength)}
	section = append(section, body...)
	section = binary.BigEndian.AppendUint32(section, mpegts.CRC32(section))
	packet := make([]byte, mpegts.PacketSize)
	for index := range packet {
		packet[index] = 0xff
	}
	packet[0], packet[1], packet[2], packet[3], packet[4] = 0x47, 0x40, 0x10, 0x10, 0
	copy(packet[5:], section)
	info := AnalyzePSI([][]byte{packet})
	if info == nil || info.NetworkID != 4 {
		t.Fatalf("info = %#v", info)
	}
}

// nitSection builds a NIT section with a network name and one transport.
func nitSection(tableID byte, networkID uint16) []byte {
	name := []byte{0x33, 0x33} // two ARIB characters; the exact text is not the point
	networkDescriptors := append([]byte{0x40, byte(len(name))}, name...)

	serviceList := []byte{0x41, 6, 0x07, 0x00, 0x01, 0x07, 0x01, 0xc0}
	tsInfo := []byte{0xcd, 4, 0x06, byte(2 << 2), 0x33, 0x33}
	transportDescriptors := append(append([]byte{}, serviceList...), tsInfo...)
	transport := []byte{0x07, 0x00, 0x7c, 0x10,
		byte(len(transportDescriptors)>>8) & 0x0f, byte(len(transportDescriptors))}
	transport = append(transport, transportDescriptors...)

	body := []byte{
		byte(networkID >> 8), byte(networkID), 0xc1, 0x00, 0x00,
		byte(len(networkDescriptors)>>8) & 0x0f, byte(len(networkDescriptors)),
	}
	body = append(body, networkDescriptors...)
	body = append(body, byte(len(transport)>>8)&0x0f, byte(len(transport)))
	body = append(body, transport...)

	length := len(body) + 4 // the CRC that follows
	section := []byte{tableID, 0xf0 | byte(length>>8), byte(length)}
	section = append(section, body...)
	crc := mpegts.CRC32(section)
	return append(section, byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
}

// TestParseNetworkSectionRecordsTheNetwork covers what a NIT is scanned for:
// the multiplexes of a network and the services on each, which is how a scan
// learns about carriers it has not tuned.
func TestParseNetworkSectionRecordsTheNetwork(t *testing.T) {
	networks := map[uint16]*NetworkInfo{}
	parseNetworkSection(nitSection(0x40, 0x7c10), networks)

	network := networks[0x7c10]
	if network == nil {
		t.Fatal("the network was not recorded")
	}
	if !network.Actual {
		t.Error("table 0x40 describes the network the stream belongs to")
	}
	if network.Name == "" || network.NameRawHex == "" {
		t.Errorf("the network name was not decoded: %+v", network)
	}
	if len(network.Transports) != 1 {
		t.Fatalf("recorded %d transports, want 1", len(network.Transports))
	}
	transport := network.Transports[0]
	if transport.TransportStreamID != 0x0700 || transport.OriginalNetworkID != 0x7c10 {
		t.Errorf("transport = %#v", transport)
	}
	if transport.RemoteControlKeyID != 6 {
		t.Errorf("remote control key = %d, want 6", transport.RemoteControlKeyID)
	}
	if len(transport.Services) != 2 {
		t.Fatalf("recorded %d services, want 2", len(transport.Services))
	}
	if transport.Services[0].ServiceID != 0x0700 || transport.Services[1].Type != 0xc0 {
		t.Errorf("services = %#v", transport.Services)
	}
}

// TestParseNetworkSectionMergesSections checks that a network described across
// several sections accumulates rather than replacing itself, and that a
// multiplex described again -- a table sent again under a new version -- is
// not listed twice.
func TestParseNetworkSectionMergesSections(t *testing.T) {
	networks := map[uint16]*NetworkInfo{}
	parseNetworkSection(nitSection(0x40, 0x7c10), networks)
	parseNetworkSection(withTransportStream(nitSection(0x40, 0x7c10), 0x0701), networks)
	if got := len(networks[0x7c10].Transports); got != 2 {
		t.Fatalf("recorded %d transports across two sections, want 2", got)
	}
	again := nitSection(0x40, 0x7c10)
	again[5] = 0xc1 | 3<<1
	parseNetworkSection(withCRC(again), networks)
	if got := len(networks[0x7c10].Transports); got != 2 {
		t.Fatalf("a multiplex described again made %d transports, want 2", got)
	}
}

// withTransportStream renames the one multiplex nitSection describes.
func withTransportStream(section []byte, id uint16) []byte {
	// The header (10), the network's name (4) and the loop length (2) come first.
	section[16], section[17] = byte(id>>8), byte(id)
	return withCRC(section)
}

func withCRC(section []byte) []byte {
	crc := mpegts.CRC32(section[:len(section)-4])
	section[len(section)-4] = byte(crc >> 24)
	section[len(section)-3] = byte(crc >> 16)
	section[len(section)-2] = byte(crc >> 8)
	section[len(section)-1] = byte(crc)
	return section
}

// TestParseNetworkSectionMarksOtherNetworks checks that a network merely
// described by this stream is not mistaken for the one it belongs to.
func TestParseNetworkSectionMarksOtherNetworks(t *testing.T) {
	networks := map[uint16]*NetworkInfo{}
	parseNetworkSection(nitSection(0x41, 0x1234), networks)
	if networks[0x1234].Actual {
		t.Error("table 0x41 describes another network, not this one")
	}
}

// A sub-table is retransmitted for as long as the stream runs, so a sample
// contains the same section many times. Folding each in would report a network
// as carrying the same multiplex over and over, which is what real hardware
// showed before this was fixed.
func TestAnalyzeTakesEachNITSectionOnce(t *testing.T) {
	networks := map[uint16]*NetworkInfo{}
	seen := map[mpegts.SectionKey]bool{}
	section := nitSection(0x40, 0x7c10)
	for repeat := 0; repeat < 5; repeat++ {
		key := mpegts.SectionKeyOf(section)
		if seen[key] {
			continue
		}
		seen[key] = true
		parseNetworkSection(section, networks)
	}
	if got := len(networks[0x7c10].Transports); got != 1 {
		t.Fatalf("a section repeated five times produced %d transports, want 1", got)
	}
}

// sectionedNIT is one section of a sub-table split into several, so that a
// reader can be shown a table that has only begun to arrive.
func sectionedNIT(networkID uint16, number, last byte) []byte {
	section := nitSection(0x40, networkID)
	section[6], section[7] = number, last
	crc := mpegts.CRC32(section[:len(section)-4])
	section[len(section)-4] = byte(crc >> 24)
	section[len(section)-3] = byte(crc >> 16)
	section[len(section)-2] = byte(crc >> 8)
	section[len(section)-1] = byte(crc)
	return section
}

// TestANetworkTableIsCompleteOnlyWhenEverySectionHasArrived.
//
// A satellite network describes its transponders over several sections. A
// reader that stopped at the first listed three transponders of a network
// carrying twelve, and the nine it never mentioned were never scanned.
func TestANetworkTableIsCompleteOnlyWhenEverySectionHasArrived(t *testing.T) {
	first := makeSectionPacket(0x10, sectionedNIT(4, 0, 1), 0)
	second := makeSectionPacket(0x10, sectionedNIT(4, 1, 1), 1)

	if info := AnalyzePSI([][]byte{first}); info != nil && info.NetworksComplete {
		t.Error("one section of two was taken as the whole table")
	}
	if info := AnalyzePSI([][]byte{first, second}); info == nil || !info.NetworksComplete {
		t.Error("both sections arrived and the table was still not complete")
	}
	// A table of one section is complete as soon as it lands.
	if info := AnalyzePSI([][]byte{makeSectionPacket(0x10, sectionedNIT(4, 0, 0), 0)}); info == nil || !info.NetworksComplete {
		t.Error("a single-section table was not complete")
	}
}

// One-segment services are sent as the plain data service type. The network
// tells them apart by naming them in the partial reception descriptor of the
// carrier's own entry, so a service named there is marked.
func TestPartialReceptionServicesAreMarked(t *testing.T) {
	descriptors := []byte{0x41, 6, 0x07, 0x00, 0x01, 0x07, 0x08, 0xc0, 0xfb, 2, 0x07, 0x08}
	transport := []byte{0x07, 0x00, 0x7c, 0x10, byte(len(descriptors) >> 8), byte(len(descriptors))}
	transport = append(transport, descriptors...)
	body := []byte{0x7c, 0x10, 0xc1, 0, 0, 0xf0, 0, byte(len(transport) >> 8), byte(len(transport))}
	body = append(body, transport...)
	nit := crcSection(0x40, body)

	services := []byte{0x07, 0x00, 0xfc, 0x80, 0, 0x07, 0x08, 0xfc, 0x80, 0}
	sdt := crcSection(0x42, append([]byte{0x07, 0x00, 0xc1, 0, 0, 0x7c, 0x10, 0xff}, services...))

	info := AnalyzePSI([][]byte{sectionPacket(0x10, nit), sectionPacket(0x11, sdt)})
	if info == nil || len(info.Services) != 2 {
		t.Fatalf("info = %#v", info)
	}
	if info.Services[0].PartialReception {
		t.Errorf("the full-segment service was marked as one-segment")
	}
	if !info.Services[1].PartialReception {
		t.Errorf("the one-segment service was not marked: %#v", info.Services[1])
	}
}

// crcSection wraps a section body in the long-form header and its CRC. The
// body starts at the table identifier extension, as every table here does.
func crcSection(tableID byte, body []byte) []byte {
	length := len(body) + 4
	section := []byte{tableID, 0xf0 | byte(length>>8), byte(length)}
	section = append(section, body...)
	return binary.BigEndian.AppendUint32(section, mpegts.CRC32(section))
}

// sectionPacket is one section in one packet, which is all these sections need.
func sectionPacket(pid byte, section []byte) []byte {
	packet := make([]byte, mpegts.PacketSize)
	for index := range packet {
		packet[index] = 0xff
	}
	packet[0], packet[1], packet[2], packet[3], packet[4] = 0x47, 0x40, pid, 0x10, 0
	copy(packet[5:], section)
	return packet
}
