// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"slices"
	"testing"
)

// networkSection is a network table of one multiplex with the given transport
// descriptors.
func networkSection(networkID, streamID uint16, descriptors []byte) []byte {
	transport := binary.BigEndian.AppendUint16(nil, streamID)
	transport = binary.BigEndian.AppendUint16(transport, networkID)
	transport = binary.BigEndian.AppendUint16(transport, 0xf000|uint16(len(descriptors)))
	transport = append(transport, descriptors...)
	body := binary.BigEndian.AppendUint16(nil, networkID)
	body = append(body, 0xc1, 0x00, 0x00, 0xf0, 0x00)
	body = binary.BigEndian.AppendUint16(body, 0xf000|uint16(len(transport)))
	body = append(body, transport...)
	return crcSection(0x40, body)
}

func onlyTransport(t *testing.T, section []byte) NetworkTransport {
	t.Helper()
	networks := map[uint16]*NetworkInfo{}
	parseNetworkSection(section, networks)
	for _, network := range networks {
		if len(network.Transports) != 1 {
			t.Fatalf("described %d multiplexes, want 1", len(network.Transports))
		}
		return network.Transports[0]
	}
	t.Fatal("no network was described")
	return NetworkTransport{}
}

// What a terrestrial or cable network says about a multiplex it does not have
// to say where to find: the button, which services ride in which transmission,
// the one-segment layer, and where it is on a cable.
func TestANetworkNamesItsMultiplexesAndTheirTransmissionTypes(t *testing.T) {
	name := []byte{0x0e, 'S', 'A', 'M', 'P', 'L', 'E'}
	// Two transmission types: the full-segment layer with two services and the
	// one-segment layer with one (TR-B14).
	information := []byte{0x04, byte(len(name))<<2 | 0x02}
	information = append(information, name...)
	information = append(information, 0x0f, 0x02, 0x04, 0x00, 0x04, 0x01, 0xaf, 0x01, 0x04, 0x08)
	descriptors := append([]byte{0xcd, byte(len(information))}, information...)
	descriptors = append(descriptors, 0xfb, 0x02, 0x04, 0x08)
	// 471.1428 MHz, 5.274 Msymbol/s, 256QAM.
	descriptors = append(descriptors, 0x44, 11, 0x04, 0x71, 0x14, 0x28, 0x00, 0x00, 0x05, 0x00, 0x52, 0x74, 0x02)

	transport := onlyTransport(t, networkSection(0x7fe0, 0x4010, descriptors))
	if transport.RemoteControlKeyID != 4 {
		t.Errorf("button %d, want 4", transport.RemoteControlKeyID)
	}
	want := []TransmissionType{
		{Type: 0x0f, ServiceIDs: []uint16{0x0400, 0x0401}},
		{Type: 0xaf, ServiceIDs: []uint16{0x0408}},
	}
	if !slices.EqualFunc(transport.TransmissionTypes, want, func(a, b TransmissionType) bool {
		return a.Type == b.Type && slices.Equal(a.ServiceIDs, b.ServiceIDs)
	}) {
		t.Errorf("transmission types %+v, want %+v", transport.TransmissionTypes, want)
	}
	if !slices.Equal(transport.PartialReceptionServiceIDs, []uint16{0x0408}) {
		t.Errorf("partial reception %v", transport.PartialReceptionServiceIDs)
	}
	cable := transport.Cable
	if cable == nil || cable.FrequencyHz != 471_142_800 || cable.SymbolRate != 5_274_000 || cable.CableModulation() != "QAM/256" {
		t.Errorf("cable %+v", cable)
	}
}

// The connected transmission descriptor is tag 0xDD. 0xFD is the data component
// descriptor, which says nothing about a group of multiplexes.
func TestConnectedTransmissionIsReadFromItsOwnTag(t *testing.T) {
	connected := []byte{0x00, 0x2a, 0x10, 0x40, 0x80, 0x00}
	read := onlyTransport(t, networkSection(0x7fe0, 0x4010, append([]byte{0xdd, byte(len(connected))}, connected...)))
	if read.Connected == nil || read.Connected.GroupID != 0x002a {
		t.Errorf("connected transmission %+v", read.Connected)
	}
	other := onlyTransport(t, networkSection(0x7fe0, 0x4010, append([]byte{0xfd, byte(len(connected))}, connected...)))
	if other.Connected != nil {
		t.Errorf("a data component descriptor was read as connected transmission: %+v", other.Connected)
	}
}

// A descriptor that says it runs past its entry is read as far as the entry
// goes, and what came before it is kept.
func TestADescriptorThatRunsPastItsEntryIsReadAsFarAsItGoes(t *testing.T) {
	descriptors := []byte{0x41, 3, 0x04, 0x00, 0x01, 0xfb, 9, 0x04, 0x08}
	read := onlyTransport(t, networkSection(0x7fe0, 0x4010, descriptors))
	if len(read.Services) != 1 || read.Services[0].ServiceID != 0x0400 {
		t.Errorf("services %+v", read.Services)
	}
	if !slices.Equal(read.PartialReceptionServiceIDs, []uint16{0x0408}) {
		t.Errorf("partial reception %v", read.PartialReceptionServiceIDs)
	}
}

func TestBinaryCodedDecimalReadsAsTheDigitsItSpells(t *testing.T) {
	for _, test := range []struct {
		data   []byte
		digits int
		want   uint32
		ok     bool
	}{
		{[]byte{0x11, 0x72, 0x74, 0x80}, 8, 11_727_480, true},
		{[]byte{0x00, 0x00}, 4, 0, true},
		// Seven digits of a field whose last nibble is not one.
		{[]byte{0x02, 0x84, 0x41, 0x0f}, 7, 284_410, true},
		// A nibble that is not a digit is a descriptor that is not this one.
		{[]byte{0x1a, 0x00}, 4, 0, false},
		{[]byte{0x11}, 4, 0, false},
	} {
		got, ok := binaryCodedDecimalNibbles(test.data, test.digits)
		if ok != test.ok || (ok && got != test.want) {
			t.Errorf("% x (%d digits) = %d, %v, want %d, %v", test.data, test.digits, got, ok, test.want, test.ok)
		}
	}
}

// 1024QAM is 7, and a frequency too large for hertz is a descriptor that is not
// this one rather than a wrapped number.
func TestCableModulationAndFrequency(t *testing.T) {
	if got := (&CableDelivery{Modulation: 7}).CableModulation(); got != "QAM/1024" {
		t.Errorf("modulation 7 = %q", got)
	}
	if read := ParseCableDelivery([]byte{0x99, 0x99, 0x99, 0x99, 0x00, 0x00, 0x05, 0x00, 0x52, 0x74, 0x02}); read != nil {
		t.Errorf("an overflowing frequency was read: %+v", read)
	}
	bonded := ParseChannelBondingCable([]byte{0x99, 0x99, 0x99, 0x99, 0xff, 0x22, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x01})
	if len(bonded) != 0 {
		t.Errorf("an overflowing carrier was read: %+v", bonded)
	}
}

func TestAnAdvancedCableDescriptorNamesItsSlices(t *testing.T) {
	// Extension 0, PLP 1, symbol duration 1 (597.33 µs), guard 1/128, bundled 5,
	// one slice:
	// 771 MHz, type 1, RS outer code, 256QAM, inner code 3/4.
	body := []byte{0x00, 0x01, 0x20, 0x14, 0x07, 0x2d, 0xf4, 0x86, 0xc0, 0x48, 0x14, 0xc0}
	read := ParseAdvancedCableDelivery(body)
	if read == nil {
		t.Fatal("not read")
	}
	if read.PLPID != 1 || read.OFDMSymbolDuration != 1 || read.GuardInterval != 0 || read.BundledChannel != 5 {
		t.Errorf("system %+v", read)
	}
	if len(read.Slices) != 1 {
		t.Fatalf("%d slices, want 1", len(read.Slices))
	}
	slice := read.Slices[0]
	if slice != (AdvancedCableSlice{DataSliceID: 7, FrequencyHz: 771_000_000, FrequencyType: 1, OuterCode: 2, Modulation: 5, InnerCode: 3}) {
		t.Errorf("slice %+v", slice)
	}
	transport := onlyTransport(t, networkSection(0x7fe0, 0x4010, append([]byte{0xf4, byte(len(body))}, body...)))
	if transport.AdvancedCable == nil {
		t.Error("the network table did not read it")
	}
}

func TestACableDivisionDescriptorNamesEachCarriersServices(t *testing.T) {
	// 471.1428 MHz, flag 1 (no extra data), frame 1, outer 0, 256QAM, 5.274
	// Msymbol/s, inner 15, two services.
	body := []byte{0x04, 0x71, 0x14, 0x28, 0x01, 0x10, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x02, 0x04, 0x00, 0x04, 0x01}
	// The second: flag 0 with two bytes of extra data, and no services.
	body = append(body, 0x04, 0x77, 0x14, 0x28, 0x00, 0x10, 0x05, 0x00, 0x52, 0x74, 0x0f, 0x02, 0xaa, 0xbb, 0x00)
	read := ParseCableTSDivision(body)
	if len(read) != 2 {
		t.Fatalf("%d carriers, want 2", len(read))
	}
	if read[0].FrequencyHz != 471_142_800 || read[0].SymbolRate != 5_274_000 || read[0].CableModulation() != "QAM/256" {
		t.Errorf("first %+v", read[0])
	}
	if !slices.Equal(read[0].ServiceIDs, []uint16{0x0400, 0x0401}) {
		t.Errorf("first services %v", read[0].ServiceIDs)
	}
	if read[1].FrequencyHz != 477_142_800 || !slices.Equal(read[1].Extra, []byte{0xaa, 0xbb}) || len(read[1].ServiceIDs) != 0 {
		t.Errorf("second %+v", read[1])
	}
}

// The association table names the programs and where their maps are; each map
// is read, and a program the operator carries for itself says so in its system
// management descriptor.
func TestProgramsAreReadFromTheirMaps(t *testing.T) {
	pat := crcSection(0x00, []byte{0x40, 0x10, 0xc1, 0x00, 0x00,
		0x00, 0x00, 0xe0, 0x10, // the network table
		0x04, 0x00, 0xe1, 0xf0, // program 0x0400 at 0x1F0
		0x04, 0x08, 0xe1, 0xf8, // program 0x0408 at 0x1F8, whose map is not sent
	})
	pmt := crcSection(0x02, []byte{0x04, 0x00, 0xc1, 0x00, 0x00,
		0xe1, 0x00, // PCR on 0x100
		0xf0, 0x04, 0xfe, 0x02, 0x40, 0x00, // system management: not a broadcast
		0x02, 0xe1, 0x00, 0xf0, 0x00, // MPEG-2 video on 0x100
		0x0f, 0xe1, 0x10, 0xf0, 0x00, // AAC on 0x110
	})
	info := AnalyzePSI([][]byte{makeSectionPacket(0x00, pat, 0), makeSectionPacket(0x1f0, pmt, 0)})
	if info == nil || info.TransportStreamID != 0x4010 {
		t.Fatalf("info %+v", info)
	}
	if len(info.Programs) != 2 {
		t.Fatalf("%d programs, want 2", len(info.Programs))
	}
	first, second := info.Programs[0], info.Programs[1]
	if first.ProgramNumber != 0x0400 || first.MapPID != 0x1f0 || !first.Mapped || first.PCRPID != 0x100 {
		t.Errorf("first %+v", first)
	}
	if first.System == nil || first.System.Broadcasting {
		t.Errorf("system %+v", first.System)
	}
	if !slices.Equal(first.Streams, []ProgramStream{{Type: 0x02, PID: 0x100}, {Type: 0x0f, PID: 0x110}}) {
		t.Errorf("streams %+v", first.Streams)
	}
	if second.ProgramNumber != 0x0408 || second.Mapped {
		t.Errorf("second %+v", second)
	}
}

// A service table is sent for as long as the stream runs. The first
// description of a service is kept, as is the first identity.
func TestTheFirstDescriptionOfAServiceIsKept(t *testing.T) {
	sdt := func(streamID uint16, serviceType byte) []byte {
		service := []byte{0x04, 0x00, 0xfc, 0x80, 0x05, 0x48, 0x03, serviceType, 0x00, 0x00}
		body := binary.BigEndian.AppendUint16(nil, streamID)
		body = append(body, 0xc1, 0x00, 0x00, 0x7f, 0xe0, 0xff)
		return crcSection(0x42, append(body, service...))
	}
	info := AnalyzePSI([][]byte{
		makeSectionPacket(0x11, sdt(0x4010, 0x01), 0),
		makeSectionPacket(0x11, sdt(0x4011, 0x02), 1),
	})
	if info == nil || len(info.Services) != 1 {
		t.Fatalf("info %+v", info)
	}
	if info.TransportStreamID != 0x4010 || info.Services[0].Type != 0x01 {
		t.Errorf("stream %#x, service type %#x, want the first", info.TransportStreamID, info.Services[0].Type)
	}
}
