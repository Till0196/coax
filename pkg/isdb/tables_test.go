// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"
)

// The tables in tstables.go, mmttables.go and datatables.go are read but never
// acted on, so nothing else in the program would notice them going wrong.
// These tests are what says they are read correctly.

// long is a long-form section: the header, the extension and numbering every
// such table has, and then the table's own fields.
func long(tableID byte, extension uint16, body []byte) []byte {
	head := []byte{byte(extension >> 8), byte(extension), 0xc1, 0x00, 0x00}
	return crcSection(tableID, append(head, body...))
}

func TestProgramMapIsRead(t *testing.T) {
	body := []byte{
		0xe1, 0x00, // PCR on 0x100
		0xf0, 0x05, // the program's own descriptors
		0xc1, 0x01, 0x84, // digital copy control
		0x09, 0x00, // an empty conditional access descriptor
		0x02, 0xe1, 0x11, 0xf0, 0x03, 0x52, 0x01, 0x00, // video with a stream identifier
		0x0f, 0xe1, 0x12, 0xf0, 0x00, // audio with nothing said about it
	}
	table := ParseProgramMap(long(TableIDPMT, 0x0400, body))
	if table == nil {
		t.Fatal("the map was not read")
	}
	if table.ProgramNumber != 0x0400 || table.PCRPID != 0x100 {
		t.Errorf("program %d, clock on %#x", table.ProgramNumber, table.PCRPID)
	}
	if len(table.Descriptors) != 2 || table.Descriptors[0].Tag != 0xc1 {
		t.Errorf("program descriptors %+v", table.Descriptors)
	}
	if len(table.Streams) != 2 {
		t.Fatalf("read %d streams, want 2", len(table.Streams))
	}
	if table.Streams[0].Type != 0x02 || table.Streams[0].PID != 0x111 {
		t.Errorf("first stream %+v", table.Streams[0])
	}
	if tag, ok := FindDescriptor(table.Streams[0].Descriptors, 0x52); !ok || len(tag.Body) != 1 {
		t.Errorf("the stream identifier was not read: %+v", table.Streams[0].Descriptors)
	}
	if len(table.Streams[1].Descriptors) != 0 {
		t.Errorf("the second stream was given descriptors it does not have")
	}
}

// A cable and a satellite both name the services of their neighbours. The
// table is told apart from the stream's own only by its identifier, so a
// reader that takes one for the other lists another multiplex's services as
// this one's.
func TestServiceDescriptionOfAnotherStreamIsRead(t *testing.T) {
	body := []byte{
		0x00, 0x04, 0xff, // original network 4
		0x03, 0xa1, 0xff, 0x90, 0x05, // service 929, schedule and present flags
		0x48, 0x03, 0x01, 0x00, 0x00, // an empty service descriptor
	}
	table := ParseServiceDescription(long(TableIDSDTOther, 0x1234, body))
	if table == nil {
		t.Fatal("the table was not read")
	}
	if table.Actual() {
		t.Error("a neighbour's table was taken for the stream's own")
	}
	if table.TransportStreamID != 0x1234 || table.OriginalNetworkID != 4 {
		t.Errorf("stream %d of network %d", table.TransportStreamID, table.OriginalNetworkID)
	}
	if len(table.Services) != 1 {
		t.Fatalf("read %d services, want 1", len(table.Services))
	}
	one := table.Services[0]
	if one.ServiceID != 0x03a1 || !one.EITSchedule || !one.EITPresentFollowing || !one.FreeCAMode {
		t.Errorf("service %+v", one)
	}
}

func TestEventTableIsRead(t *testing.T) {
	// 3 September 2026 at 00:00, running for thirty minutes.
	event := []byte{
		0x12, 0x34, // event 0x1234
		0xef, 0x66, 0x00, 0x00, 0x00, // the start
		0x00, 0x30, 0x00, // the duration
		0x90, 0x07, // running, under access, and seven bytes of descriptors
		0x4d, 0x05, 0x6a, 0x70, 0x6e, 0x01, 0x41, 0x00, // a short event
	}
	body := append([]byte{0x00, 0x40, 0x00, 0x04, 0x00, 0x4e}, event...)
	table := ParseEventTable(long(TableIDEITActual, 0x0065, body))
	if table == nil {
		t.Fatal("the table was not read")
	}
	if !table.Actual() || table.Schedule() {
		t.Error("the present and following table was taken for a schedule")
	}
	if table.ServiceID != 0x0065 || table.TransportStreamID != 0x0040 || table.OriginalNetworkID != 4 {
		t.Errorf("table %+v", table)
	}
	if len(table.Events) != 1 {
		t.Fatalf("read %d events, want 1", len(table.Events))
	}
	one := table.Events[0]
	if !one.StartKnown || one.Start.Format("2006-01-02 15:04:05") != "2026-09-03 00:00:00" {
		t.Errorf("the event starts at %v", one.Start)
	}
	if !one.DurationKnown || one.Duration != 30*time.Minute {
		t.Errorf("the event runs for %v", one.Duration)
	}
	if one.RunningStatus != 4 || !one.FreeCAMode {
		t.Errorf("event %+v", one)
	}
	if short, ok := FindDescriptor(one.Descriptors, 0x4d); !ok || len(short.Body) != 5 {
		t.Errorf("the short event was not read: %+v", one.Descriptors)
	}
}

// A start of every bit set is the table saying it does not know when the event
// begins, which is not the same as the epoch.
func TestAnUndefinedStartIsNotAMoment(t *testing.T) {
	event := []byte{
		0x00, 0x01,
		0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff,
		0x00, 0x00,
	}
	body := append([]byte{0x00, 0x40, 0x00, 0x04, 0x00, 0x4e}, event...)
	table := ParseEventTable(long(TableIDEITActual, 1, body))
	if table == nil || len(table.Events) != 1 {
		t.Fatalf("the table was not read: %+v", table)
	}
	if table.Events[0].StartKnown || table.Events[0].DurationKnown {
		t.Errorf("an unknown start was read as %v", table.Events[0].Start)
	}
}

func TestTimeTableIsRead(t *testing.T) {
	// The offset table carries the local time offsets after the time.
	body := []byte{0xef, 0x66, 0x12, 0x36, 0x49, 0xf0, 0x0d,
		0x58, 0x0b, 0x4a, 0x50, 0x4e, 0xff, 0x09, 0x00, 0xef, 0x66, 0x00, 0x00, 0x00}
	section := append([]byte{TableIDTOT, 0x70 | byte((len(body)+4)>>8), byte(len(body) + 4)}, body...)
	section = binary.BigEndian.AppendUint32(section, 0)
	table := ParseTimeTable(section)
	if table == nil {
		t.Fatal("the table was not read")
	}
	if table.Time.Format("2006-01-02 15:04:05") != "2026-09-03 12:36:49" {
		t.Errorf("the stream says it is %v", table.Time)
	}
	if len(table.Descriptors) != 1 || table.Descriptors[0].Tag != 0x58 {
		t.Errorf("descriptors %+v", table.Descriptors)
	}
	// The date table is the same time without them.
	short := append([]byte{TableIDTDT, 0x70, 0x05}, body[:5]...)
	if plain := ParseTimeTable(short); plain == nil || !plain.Time.Equal(table.Time) {
		t.Errorf("the date table read as %+v", plain)
	}
}

func TestBroadcasterInformationIsRead(t *testing.T) {
	body := []byte{
		0xf0, 0x00, // no descriptors for the network
		0x01, 0xf0, 0x05, 0xd8, 0x03, 0x58, 0x59, 0x5a, // broadcaster 1, named
	}
	table := ParseBroadcasterInformation(long(TableIDBIT, 0x0004, body))
	if table == nil {
		t.Fatal("the table was not read")
	}
	if table.OriginalNetworkID != 4 || len(table.Broadcasters) != 1 {
		t.Fatalf("table %+v", table)
	}
	if table.Broadcasters[0].BroadcasterID != 1 {
		t.Errorf("broadcaster %+v", table.Broadcasters[0])
	}
	name, ok := FindDescriptor(table.Broadcasters[0].Descriptors, 0xd8)
	if !ok || string(name.Body) != "XYZ" {
		t.Errorf("the broadcaster's name was not read: %+v", table.Broadcasters[0].Descriptors)
	}
}

func TestConditionalAccessTableIsRead(t *testing.T) {
	body := []byte{0x09, 0x04, 0x00, 0x05, 0xe0, 0x01}
	table := ParseConditionalAccess(long(TableIDCAT, 0xffff, body))
	if table == nil || len(table.Descriptors) != 1 || table.Descriptors[0].Tag != 0x09 {
		t.Fatalf("the table read as %+v", table)
	}
}

// The package table is the one that says what an MMT broadcast is made of. The
// asset locations are what a demultiplexer would follow, so the packet
// identifier has to come out of the right end of them.
func TestPackageTableIsRead(t *testing.T) {
	asset := []byte{
		0x00,                   // identifier type
		0x00, 0x00, 0x00, 0x00, // scheme
		0x02, 0x00, 0x65, // a two-byte identifier
		'h', 'e', 'v', '1', // the asset type
		0x00,             // no clock relation
		0x01,             // one location
		0x00, 0xf3, 0x00, // in this stream, packet 0xf300
		0x00, 0x03, // three bytes of descriptors
		0x80, 0x01, 0x00, // a video component descriptor, empty
	}
	body := []byte{0x00, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x65}
	body = append(body, 0x00, 0x00, 0x01) // no table descriptors, one asset
	body = append(body, asset...)
	table := append([]byte{TableIDMPTComplete, 0x0d}, byte(len(body)>>8), byte(len(body)))
	table = append(table, body...)

	read := ParsePackageTable(table)
	if read == nil {
		t.Fatal("the table was not read")
	}
	if read.ServiceID() != 0x0065 {
		t.Errorf("the package is service %d", read.ServiceID())
	}
	if len(read.Assets) != 1 {
		t.Fatalf("read %d assets, want 1", len(read.Assets))
	}
	one := read.Assets[0]
	if one.Type != "hev1" {
		t.Errorf("the asset is a %q", one.Type)
	}
	id, ok := one.PacketID()
	if !ok || id != 0xf300 {
		t.Errorf("the asset is on packet %#x (%v)", id, ok)
	}
	if len(one.Descriptors) != 1 || one.Descriptors[0].Tag != 0x8001 {
		t.Errorf("asset descriptors %+v", one.Descriptors)
	}
}

func TestMHServiceDescriptionOfAnotherStreamIsRead(t *testing.T) {
	body := []byte{
		0x00, 0x0b, 0xff,
		0x00, 0x65, 0xfc, 0x80, 0x00, 0x00,
	}
	table := ParseMHServiceDescription(long(TableIDMHSDTOther, 0xb0f1, body))
	if table == nil || table.Actual() {
		t.Fatalf("the table read as %+v", table)
	}
	if table.TLVStreamID != 0xb0f1 || len(table.Services) != 1 || table.Services[0].ServiceID != 0x0065 {
		t.Errorf("table %+v", table)
	}
}

func TestMHEventTableIsRead(t *testing.T) {
	event := []byte{
		0x00, 0x01,
		0xef, 0x66, 0x18, 0x00, 0x00,
		0x00, 0x45, 0x00,
		0x80, 0x08,
		0xf0, 0x01, 0x00, 0x04, 0x6a, 0x70, 0x6e, 0x00, // an MH short event with no name
	}
	body := append([]byte{0xb0, 0xf1, 0x00, 0x0b, 0x00, 0x8b}, event...)
	table := ParseMHEventTable(long(TableIDMHEITPresent, 0x0065, body))
	if table == nil || len(table.Events) != 1 {
		t.Fatalf("the table read as %+v", table)
	}
	one := table.Events[0]
	if one.Start.Format("2006-01-02 15:04") != "2026-09-03 18:00" || one.Duration != 45*time.Minute {
		t.Errorf("the event is at %v for %v", one.Start, one.Duration)
	}
	if len(one.Descriptors) != 1 || one.Descriptors[0].Tag != 0xf001 {
		t.Errorf("event descriptors %+v", one.Descriptors)
	}
}

func TestMHTimeAndBroadcasterAndApplicationAreRead(t *testing.T) {
	moment := []byte{0xef, 0x66, 0x15, 0x01, 0x23, 0xf0, 0x00}
	section := append([]byte{TableIDMHTOT, 0x70 | byte((len(moment)+4)>>8), byte(len(moment) + 4)}, moment...)
	section = binary.BigEndian.AppendUint32(section, 0)
	if table := ParseMHTimeTable(section); table == nil ||
		table.Time.Format("2006-01-02 15:04:05") != "2026-09-03 15:01:23" {
		t.Errorf("the offset table read as %+v", table)
	}

	broadcasters := []byte{0xf0, 0x00, 0x01, 0xf0, 0x00}
	if table := ParseMHBroadcasterInformation(long(TableIDMHBIT, 0x000b, broadcasters)); table == nil ||
		len(table.Broadcasters) != 1 || table.Broadcasters[0].BroadcasterID != 1 {
		t.Errorf("the broadcaster table read as %+v", table)
	}

	applications := []byte{
		0xf0, 0x00, // no common descriptors
		0xf0, 0x09, // one application
		0x00, 0x00, 0x00, 0x07, 0x40, 0x01, 0x01, 0xf0, 0x00,
	}
	table := ParseMHApplicationInformation(long(TableIDMHAIT, 0x0010, applications))
	if table == nil || len(table.Applications) != 1 {
		t.Fatalf("the application table read as %+v", table)
	}
	if table.Applications[0].OrganizationID != 7 || table.Applications[0].ApplicationID != 0x4001 {
		t.Errorf("application %+v", table.Applications[0])
	}
}

// The address map is what says which datagrams a service is carried in. These
// are the bytes one satellite feed sends: service 101 on an IPv6 group.
func TestAddressMapIsRead(t *testing.T) {
	section, err := hex.DecodeString(
		"fef0310000df0000007f0065fc222401dbc010000000000000000000000280ff3e" +
			"00000000000000000000a000100080ba2034ff")
	if err != nil {
		t.Fatal(err)
	}
	table := ParseAddressMap(section)
	if table == nil {
		t.Fatal("the table was not read")
	}
	if len(table.Services) != 1 {
		t.Fatalf("read %d services, want 1", len(table.Services))
	}
	one := table.Services[0]
	if one.ServiceID != 101 || !one.IPv6 {
		t.Errorf("service %+v", one)
	}
	if one.Source.String() != "2401:dbc0:1000::2" || one.SourceMask != 128 {
		t.Errorf("the datagrams come from %v/%d", one.Source, one.SourceMask)
	}
	if one.Destination.String() != "ff3e::a000:1000" || one.DestinationMask != 128 {
		t.Errorf("the datagrams go to %v/%d", one.Destination, one.DestinationMask)
	}
}

func TestDataTransmissionTablesAreRead(t *testing.T) {
	directory := []byte{
		0x01, '/', // the base path
		0x01,                   // one node
		0x00, 0x02, 0x00, 0x01, // node 2, version 0, a one-byte path
		'a',
		0x00, 0x01, // one file
		0x00, 0x03, 0x05, 's', 't', 'a', 'r', 't',
	}
	tree := ParseDataDirectory(long(TableIDDDMT, 0x0100, directory))
	if tree == nil || len(tree.Nodes) != 1 || len(tree.Nodes[0].Files) != 1 {
		t.Fatalf("the directory read as %+v", tree)
	}
	if tree.BasePath != "/" || tree.Nodes[0].Path != "a" || tree.Nodes[0].Files[0].Name != "start" {
		t.Errorf("the tree is %+v", tree.Nodes[0])
	}

	assets := []byte{
		0x00, 0x00, 0x00, 0x01, // transaction
		0x00, 0x40, // component
		0x00, 0x00, 0x00, 0x02, // download
		0x01,                   // one unit
		0x00, 0x00, 0x00, 0x05, // sequence
		0x00, 0x00, 0x10, 0x00, // size
		0x00,       // no index item
		0x00, 0x01, // one item
		0x00, 0x03, // node tag
		0x00, 0x00, 0x00, 0x09, // item id
		0x00, 0x00, 0x00, 0x20, // item size
		0x01, // version
		0x00, // no checksum
		0x00, // no item info
		0x00, // no unit info
		0x00, // no component info
	}
	managed := ParseDataAssetManagement(long(TableIDDAMT, 0x0100, assets))
	if managed == nil || len(managed.Units) != 1 || len(managed.Units[0].Items) != 1 {
		t.Fatalf("the asset table read as %+v", managed)
	}
	if managed.ComponentTag != 0x0040 || managed.Units[0].Items[0].Size != 0x20 {
		t.Errorf("the unit is %+v", managed.Units[0])
	}

	content := []byte{
		0x00, 0x07, // content
		0x01,                   // version
		0x00, 0x00, 0x08, 0x00, // size
		0x80, // presentation units follow
		0x01,
		0x09,                   // tag
		0x00, 0x00, 0x04, 0x00, // size
		0x01, 0x00, 0x03, // one member node
		0x00, // no descriptors
	}
	configured := ParseDataContentConfiguration(long(TableIDDCCT, 0x0100, content))
	if configured == nil || len(configured.Units) != 1 || len(configured.Units[0].Nodes) != 1 {
		t.Fatalf("the content table read as %+v", configured)
	}
	if configured.ContentID != 7 || configured.Units[0].Tag != 9 {
		t.Errorf("the content is %+v", configured)
	}

	// The EMT's descriptors are MMT-SI ones: a 16-bit tag (STD-B60 Table 11-1).
	messages := ParseMHEventMessageTable(long(TableIDEMT, 0x1002, []byte{0xf0, 0x03, 0x00, 0x01, 0x00}))
	if messages == nil || messages.DataEventID != 1 || messages.GroupID != 2 {
		t.Fatalf("the message table read as %+v", messages)
	}
	if len(messages.Descriptors) != 1 || messages.Descriptors[0].Tag != 0xf003 {
		t.Errorf("message descriptors %+v", messages.Descriptors)
	}
}

func TestEntitlementMessagesAndTheirTableAreRead(t *testing.T) {
	control := ParseEntitlementMessage(long(TableIDMHECMLow, 0x0001, []byte{0x01, 0x02, 0x03}))
	if control == nil || !control.Control() || len(control.Data) != 3 {
		t.Fatalf("the control message read as %+v", control)
	}
	management := ParseEntitlementMessage(long(TableIDMHEMMHigh, 0x0001, []byte{0x04}))
	if management == nil || management.Control() {
		t.Fatalf("the management message read as %+v", management)
	}

	table := []byte{TableIDMHCAT, 0x01, 0x00, 0x06, 0x80, 0x04, 0x00, 0x05, 0x00, 0x01}
	access := ParseMHConditionalAccess(table)
	if access == nil || len(access.Descriptors) != 1 || access.Descriptors[0].Tag != 0x8004 {
		t.Fatalf("the access table read as %+v", access)
	}
}

// A satellite sends a service table for every multiplex of its network, not
// just the one being received. Reading those is what lets one lock name the
// whole network rather than only the transponder in front of the tuner.
func TestTheNetworkListingIsNamedByAnotherStreamsServiceTable(t *testing.T) {
	// The network table: one other multiplex, with two services listed and no
	// names anywhere in it.
	transport := []byte{
		0x40, 0x10, 0x00, 0x04, // stream 16400 of network 4
		0xf0, 0x08,
		0x41, 0x06, 0x00, 0x97, 0x01, 0x00, 0x98, 0x01, // services 151 and 152
	}
	network := []byte{0xf0, 0x00, 0xf0, byte(len(transport))}
	network = append(network, transport...)
	nit := crcSection(0x40, append([]byte{0x00, 0x04, 0xc1, 0x00, 0x00}, network...))

	// The service table for that multiplex, which this stream is not.
	// Written in the broadcast character encoding, which starts in kanji: the
	// escape designates the alphanumeric set before the name.
	name := append([]byte{0x1b, 0x28, 0x4a}, "Test One"...)
	service := []byte{0x48, byte(3 + len(name)), 0x01, 0x00, byte(len(name))}
	service = append(service, name...)
	entry := []byte{0x00, 0x97, 0xff, 0x80, byte(len(service))}
	entry = append(entry, service...)
	body := append([]byte{0x40, 0x10, 0xc1, 0x00, 0x00, 0x00, 0x04, 0xff}, entry...)
	sdt := crcSection(TableIDSDTOther, body)

	info := AnalyzePSI([][]byte{sectionPacket(0x10, nit), sectionPacket(0x11, sdt)})
	if info == nil || len(info.Networks) != 1 || len(info.Networks[0].Transports) != 1 {
		t.Fatalf("the tables read as %+v", info)
	}
	listed := info.Networks[0].Transports[0].Services
	if len(listed) != 2 {
		t.Fatalf("the network listed %d services, want 2", len(listed))
	}
	if listed[0].Name != "Ｔｅｓｔ\u3000Ｏｎｅ" {
		t.Errorf("service %d is called %q", listed[0].ServiceID, listed[0].Name)
	}
	// The other service was listed but never named, and saying nothing is
	// right where no table has said anything.
	if listed[1].Name != "" {
		t.Errorf("service %d was given the name %q", listed[1].ServiceID, listed[1].Name)
	}
	// The stream still holds no services of its own: a neighbour's table
	// describes a multiplex this one is not.
	if len(info.Services) != 0 {
		t.Errorf("the neighbour's services were taken as this stream's: %+v", info.Services)
	}
}

// The same on the advanced services, where the table is the MH-SDT and the
// listing is the TLV-NIT.
func TestTheTLVListingIsNamedByAnotherStreamsServiceTable(t *testing.T) {
	stream := []byte{
		0xb0, 0x50, 0x00, 0x0b, // stream 45136 of network 11
		0xf0, 0x05,
		0x41, 0x03, 0x00, 0x97, 0x01, // service 151
	}
	body := []byte{0x00, 0x0b, 0xc1, 0x00, 0x00, 0xf0, 0x00, 0xf0, byte(len(stream))}
	body = append(body, stream...)
	nit := crcSection(TableIDTLVNITActual, body)

	name := []byte("テスト　放送")
	descriptor := []byte{0x80, 0x19, byte(3 + len(name)), 0x01, 0x00, byte(len(name))}
	descriptor = append(descriptor, name...)
	entry := []byte{0x00, 0x97, 0xfc, 0x80, byte(len(descriptor))}
	entry = append(entry, descriptor...)
	sdtBody := append([]byte{0xb0, 0x50, 0xc1, 0x00, 0x00, 0x00, 0x0b, 0xff}, entry...)
	sdt := crcSection(TableIDMHSDTOther, sdtBody)

	// Either order: the tables arrive in none in particular.
	for _, order := range [][][]byte{{nit, sdt}, {sdt, nit}} {
		state := NewState()
		for _, section := range order {
			if section[0] == TableIDTLVNITActual {
				state.PushTLVSection(section)
			} else {
				state.PushMessage(MessageIDM2Section, section)
			}
		}
		network, ok := state.Networks[11]
		if !ok || len(network.Streams) != 1 || len(network.Streams[0].Services) != 1 {
			t.Fatalf("the tables read as %+v", state.Networks)
		}
		if got := network.Streams[0].Services[0].Name; got != "テスト　放送" {
			t.Errorf("the service is called %q", got)
		}
		// The stream is not the one the neighbour's table described.
		if state.TLVStreamID == 0xb050 {
			t.Error("the neighbour's identifier was taken as this stream's")
		}
	}
}
