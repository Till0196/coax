// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/pkg/mmttlv"
	"github.com/Till0196/coax/pkg/mpegts"
)

// The protocol values the synthetic streams below are built from. They are
// named here rather than in the package itself because only these tests build
// a stream by hand: everything else reads one, through the package that owns
// the value.
const (
	tlvSync              = mmttlv.SyncByte
	tlvTypeIPv6          = mmttlv.TypeIPv6
	tlvTypeControl       = mmttlv.TypeControl
	tlvTypeNull          = mmttlv.TypeNull
	mmtpPayloadSignaling = mmttlv.PayloadTypeSignaling
	mhSDTActual          = TableIDMHSDTActual
	mhServiceDescriptor  = TagMHService
)

// TestPSIAnalyzeReadsSDTAndDecodesARIBNames goes through Analyze rather
// than this package's Analyze: the section builders live here, and what is
// being pinned is that a service name in ARIB's own coding survives the read.
func TestPSIAnalyzeReadsSDTAndDecodesARIBNames(t *testing.T) {
	section := makeSDTSection(0x4010, 4, 101, 0x01, []byte{0x46, 0x7c})
	packets := [][]byte{makeSectionPacket(0x11, section, 0)}
	info := AnalyzePSI(packets)
	if info == nil {
		t.Fatal("no transport information")
	}
	if info.TransportStreamID != 0x4010 || info.OriginalNetworkID != 4 {
		t.Fatalf("unexpected IDs: %#v", info)
	}
	if len(info.Services) != 1 || info.Services[0].ServiceID != 101 || info.Services[0].Name != "日" {
		t.Fatalf("unexpected services: %#v", info.Services)
	}
	if info.Services[0].DecodeLossy {
		t.Fatal("ARIB service name was decoded lossily")
	}
}

func TestAnalyzeTSMFRelativeStream(t *testing.T) {
	header0 := makeTSMFPacket(0)
	header1 := makeTSMFPacket(1)
	sdt := makeSectionPacket(0x11, makeSDTSection(0x4020, 4, 201, 0x01, []byte{0x46, 0x7c}), 0)
	null := makeNullPacket()

	packets := make([][]byte, 0, 106)
	packets = append(packets, header0)
	for i := 0; i < 52; i++ {
		packets = append(packets, null)
	}
	packets = append(packets, header1, sdt)
	for i := 1; i < 52; i++ {
		packets = append(packets, null)
	}
	buffer := make([]byte, 0, len(packets)*tsPacketSize)
	for _, packet := range packets {
		buffer = append(buffer, packet...)
	}

	analysis := Analyze(buffer, "ts")
	if analysis == nil || analysis.TSMF == nil {
		t.Fatal("TSMF was not detected")
	}
	if len(analysis.TSMF.RelativeStreams) != 1 {
		t.Fatalf("relative streams = %#v", analysis.TSMF.RelativeStreams)
	}
	stream := analysis.TSMF.RelativeStreams[0]
	if stream.RelativeStream != 1 || stream.StreamID != 0x4020 || stream.OriginalNetworkID != 4 || stream.Format != "ts" {
		t.Fatalf("unexpected relative stream: %#v", stream)
	}
	if stream.Transport == nil || len(stream.Transport.Services) != 1 || stream.Transport.Services[0].Name != "日" {
		t.Fatalf("inner SDT was not parsed: %#v", stream.Transport)
	}
}

func TestAnalyzeTLVInsideTSMFRelativeStream(t *testing.T) {
	header0 := makeTSMFTLVPacket(0)
	header1 := makeTSMFTLVPacket(1)
	slot := makeNullPacket()
	slot[1], slot[2], slot[3] = 0x40, 0x2d, 0x10
	// One null packet filling the slot: a start is taken unconfirmed only where
	// its packet ends exactly where the bytes do.
	copy(slot[4:], []byte{tlvSync, tlvTypeNull, 0x00, 0xb4})

	packets := make([][]byte, 0, 106)
	packets = append(packets, header0)
	for i := 0; i < 52; i++ {
		packets = append(packets, makeNullPacket())
	}
	packets = append(packets, header1, slot)
	for i := 1; i < 52; i++ {
		packets = append(packets, makeNullPacket())
	}
	buffer := make([]byte, 0, len(packets)*tsPacketSize)
	for _, packet := range packets {
		buffer = append(buffer, packet...)
	}

	analysis := Analyze(buffer, "ts")
	if analysis == nil || analysis.TSMF == nil || len(analysis.TSMF.RelativeStreams) != 1 {
		t.Fatalf("TSMF TLV stream was not detected: %#v", analysis)
	}
	stream := analysis.TSMF.RelativeStreams[0]
	if stream.Format != "tlv" || stream.TLV == nil || stream.TLV.Packets != 1 || stream.TLV.NullPackets != 1 {
		t.Fatalf("TSMF TLV payload was not parsed: %#v", stream)
	}
}

func TestAnalyzeNativeTLVMMTService(t *testing.T) {
	section := makeMHSDTSection(0xb112, 0x000a, 181, 0xa5, "４Ｋ放送")
	message := []byte{0x80, 0x00, 0x01, byte(len(section) >> 8), byte(len(section))}
	message = append(message, section...)
	signaling := append([]byte{0x00, 0x00}, message...)
	mmtp := make([]byte, 12)
	mmtp[1] = mmtpPayloadSignaling
	binary.BigEndian.PutUint16(mmtp[2:4], 0x8004)
	mmtp = append(mmtp, signaling...)

	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:2], 5000)
	binary.BigEndian.PutUint16(udp[2:4], 5001)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)+len(mmtp)))
	udp = append(udp, mmtp...)
	ip := make([]byte, 40)
	ip[0], ip[6], ip[7] = 0x60, 17, 64
	binary.BigEndian.PutUint16(ip[4:6], uint16(len(udp)))
	copy(ip[8:24], netIP16(1))
	copy(ip[24:40], netIP16(2))
	ip = append(ip, udp...)
	tlv := []byte{tlvSync, tlvTypeIPv6, byte(len(ip) >> 8), byte(len(ip))}
	tlv = append(tlv, ip...)

	analysis := Analyze(tlv, "tlv")
	if analysis == nil || analysis.TLV == nil {
		t.Fatal("native TLV was not analyzed")
	}
	info := analysis.TLV
	if info.Packets != 1 || info.IPv6Packets != 1 || info.MMTPPackets != 1 {
		t.Fatalf("unexpected TLV counters: %#v", info)
	}
	if info.TLVStreamID != 0xb112 || info.OriginalNetworkID != 0x000a {
		t.Fatalf("unexpected MH-SDT identity: %#v", info)
	}
	if len(info.Services) != 1 || info.Services[0].ServiceID != 181 || info.Services[0].Name != "４Ｋ放送" {
		t.Fatalf("unexpected MMT services: %#v", info.Services)
	}
	if len(info.Flows) != 1 || len(info.PacketIDs) != 1 || info.PacketIDs[0].PacketID != 0x8004 {
		t.Fatalf("unexpected MMTP summary: flows=%#v packet IDs=%#v", info.Flows, info.PacketIDs)
	}

}

func TestAnalyzeTLVControlNIT(t *testing.T) {
	descriptors := []byte{0x41, 0x03, 0x00, 0xb5, 0xa5}
	stream := []byte{0xb1, 0x12, 0x00, 0x0a, 0xf0, byte(len(descriptors))}
	stream = append(stream, descriptors...)
	body := []byte{0x00, 0x0a, 0xc1, 0x00, 0x00, 0xf0, 0x00, 0xf0, byte(len(stream))}
	body = append(body, stream...)
	section := makeLongSection(0x40, body)
	tlv := []byte{tlvSync, tlvTypeControl, byte(len(section) >> 8), byte(len(section))}
	tlv = append(tlv, section...)

	analysis := Analyze(tlv, "tlv")
	if analysis == nil || analysis.TLV == nil {
		t.Fatal("TLV control packet was not analyzed")
	}
	info := analysis.TLV
	// The NIT names the network but not which of its streams this is.
	if info.ControlPackets != 1 || info.NetworkID != 10 || info.TLVStreamID != 0 || info.OriginalNetworkID != 0 {
		t.Fatalf("unexpected TLV-NIT identity: %#v", info)
	}
	if len(info.Services) != 1 || info.Services[0].ServiceID != 181 || info.Services[0].Type != 0xa5 {
		t.Fatalf("unexpected TLV service list: %#v", info.Services)
	}
}

func makeMHSDTSection(tlvStreamID, onid, serviceID uint16, serviceType byte, name string) []byte {
	descriptorBody := []byte{serviceType, 0x00, byte(len(name))}
	descriptorBody = append(descriptorBody, []byte(name)...)
	descriptor := []byte{byte(mhServiceDescriptor >> 8), byte(mhServiceDescriptor & 0xff), byte(len(descriptorBody))}
	descriptor = append(descriptor, descriptorBody...)
	service := []byte{byte(serviceID >> 8), byte(serviceID), 0xfc, byte(len(descriptor) >> 8), byte(len(descriptor))}
	service = append(service, descriptor...)
	body := []byte{byte(tlvStreamID >> 8), byte(tlvStreamID), 0xc1, 0x00, 0x00, byte(onid >> 8), byte(onid), 0xff}
	body = append(body, service...)
	return makeLongSection(mhSDTActual, body)
}

func netIP16(last byte) []byte {
	ip := make([]byte, 16)
	ip[0], ip[1], ip[15] = 0x20, 0x01, last
	return ip
}

func makeSDTSection(tsid uint16, onid uint16, serviceID uint16, serviceType byte, name []byte) []byte {
	descriptorBody := []byte{serviceType, 0x00, byte(len(name))}
	descriptorBody = append(descriptorBody, name...)
	descriptor := append([]byte{0x48, byte(len(descriptorBody))}, descriptorBody...)
	service := []byte{byte(serviceID >> 8), byte(serviceID), 0xfc, byte(len(descriptor) >> 8), byte(len(descriptor))}
	service = append(service, descriptor...)
	body := []byte{byte(tsid >> 8), byte(tsid), 0xc1, 0x00, 0x00, byte(onid >> 8), byte(onid), 0xff}
	body = append(body, service...)
	return makeLongSection(0x42, body)
}

func makeLongSection(tableID byte, body []byte) []byte {
	sectionLength := len(body) + 4
	section := []byte{tableID, 0xb0 | byte(sectionLength>>8), byte(sectionLength)}
	section = append(section, body...)
	crc := mpegts.CRC32(section)
	section = binary.BigEndian.AppendUint32(section, crc)
	return section
}

func makeSectionPacket(pid uint16, section []byte, cc byte) []byte {
	packet := make([]byte, tsPacketSize)
	for i := range packet {
		packet[i] = 0xff
	}
	packet[0] = 0x47
	packet[1] = 0x40 | byte(pid>>8)
	packet[2] = byte(pid)
	packet[3] = 0x10 | (cc & 0x0f)
	packet[4] = 0
	copy(packet[5:], section)
	return packet
}

func makeTSMFPacket(cc byte) []byte {
	packet := make([]byte, tsPacketSize)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x00, tsmfPID, 0x10|(cc&0x0f)
	payload := packet[4:]
	payload[0], payload[1], payload[2] = 0x1a, 0x86, 0x01
	payload[5], payload[6] = 0x40, 0x20
	payload[7], payload[8] = 0x00, 0x04
	for i := 0; i < 26; i++ {
		payload[69+i] = 0x11
	}
	payload[121] = 0x80 // Relative stream 1 is TS (bit value 1).
	payload[126] = 0x11
	crc := mpegts.CRC32(payload[:180])
	binary.BigEndian.PutUint32(payload[180:], crc)
	return packet
}

func makeTSMFTLVPacket(cc byte) []byte {
	packet := makeTSMFPacket(cc)
	payload := packet[4:]
	payload[121] = 0 // Relative stream 1 is TLV (bit value 0).
	crc := mpegts.CRC32(payload[:180])
	binary.BigEndian.PutUint32(payload[180:], crc)
	return packet
}

func makeNullPacket() []byte {
	packet := make([]byte, tsPacketSize)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x1f, 0xff, 0x10
	return packet
}
