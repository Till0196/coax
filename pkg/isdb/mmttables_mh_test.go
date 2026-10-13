// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// mmtDescribed is one MMT-SI descriptor with a one-byte length.
func mmtDescribed(tag uint16, body []byte) []byte {
	out := binary.BigEndian.AppendUint16(nil, tag)
	out = append(out, byte(len(body)))
	return append(out, body...)
}

// ntpSecond is a second as a network time.
const ntpSecond = uint64(1) << 32

func TestTheCAMessageIsNamedByItsMessageIdentifier(t *testing.T) {
	if MessageIDCA != 0x8001 || PacketIDCA != 0x0001 {
		t.Fatalf("message %#x packet %#x", MessageIDCA, PacketIDCA)
	}
}

func TestAnMHEventMessageTableIsRead(t *testing.T) {
	private := []byte("sample")
	descriptor := []byte{0xf0, 0x03, 0x00, byte(14 + len(private))}
	descriptor = append(descriptor, 0x00, 0x1f, 0x00) // group 0x001, time mode 0
	descriptor = append(descriptor, make([]byte, 8)...)
	descriptor = append(descriptor, 0x01, 0x05, 0x1b) // type 1, id 5, version 0x1b
	descriptor = append(descriptor, private...)
	table := ParseMHEventMessageTable(long(TableIDEMT, 0xf001, descriptor))
	if table == nil || table.DataEventID != 0xf || table.GroupID != 0x001 || len(table.Descriptors) != 1 {
		t.Fatalf("read %+v", table)
	}
	if table.Descriptors[0].Tag != TagMHEventMessage {
		t.Fatalf("tag %#x", table.Descriptors[0].Tag)
	}
	message, ok := ParseEventMessageDescriptor(table.Descriptors[0].Body)
	if !ok || !message.Immediate() || message.MessageID() != 5 || message.MessageVersion() != 0x1b ||
		message.Kind != 1 || !bytes.Equal(message.Private, private) {
		t.Fatalf("message %+v", message)
	}
}

func TestTheMPUTimestampDescriptorSaysWhenEachMPUIsShown(t *testing.T) {
	base := uint64(3_000_000_000) << 32
	var body []byte
	for _, pair := range []MPUTimestamp{{5, base}, {6, base + ntpSecond}} {
		body = binary.BigEndian.AppendUint32(body, pair.Sequence)
		body = binary.BigEndian.AppendUint64(body, pair.NTP)
	}
	// A trailing part-entry is not one and is ignored.
	body = append(body, 0x00)
	one, ok := FindMMTDescriptor(ParseMMTDescriptors(mmtDescribed(TagMPUTimestamp, body)), TagMPUTimestamp)
	if !ok {
		t.Fatal("no descriptor")
	}
	times := ParseMPUTimestamps(one.Body)
	if len(times) != 2 || times[0] != (MPUTimestamp{5, base}) || times[1] != (MPUTimestamp{6, base + ntpSecond}) {
		t.Fatalf("times %+v", times)
	}
}

// defaultOffsetDescriptor is an extended timestamp descriptor whose access
// units all take one presentation offset: three frames of a 29.97 stream
// counted in the transport stream's own clock.
func defaultOffsetDescriptor() []byte {
	body := []byte{0x03}
	body = binary.BigEndian.AppendUint32(body, 90_000)
	body = binary.BigEndian.AppendUint16(body, 3003)
	body = binary.BigEndian.AppendUint32(body, 5)
	body = append(body, 0x00)
	body = binary.BigEndian.AppendUint16(body, 6006)
	body = append(body, 3)
	for _, offset := range []uint16{0, 3003, 6006} {
		body = binary.BigEndian.AppendUint16(body, offset)
	}
	return mmtDescribed(TagMPUExtendedTimestamp, body)
}

// perUnitOffsetDescriptor writes a presentation offset for every access unit,
// four bytes each rather than two.
func perUnitOffsetDescriptor() []byte {
	body := []byte{0x05}
	body = binary.BigEndian.AppendUint32(body, 1000)
	body = binary.BigEndian.AppendUint32(body, 7)
	body = append(body, 0x40)
	body = binary.BigEndian.AppendUint16(body, 20)
	body = append(body, 2)
	for _, pair := range [][2]uint16{{10, 33}, {0, 34}} {
		body = binary.BigEndian.AppendUint16(body, pair[0])
		body = binary.BigEndian.AppendUint16(body, pair[1])
	}
	return mmtDescribed(TagMPUExtendedTimestamp, body)
}

func extendedOf(t *testing.T, loop []byte) ExtendedTimestamp {
	t.Helper()
	one, ok := FindMMTDescriptor(ParseMMTDescriptors(loop), TagMPUExtendedTimestamp)
	if !ok {
		t.Fatal("no descriptor")
	}
	return ParseExtendedTimestamp(one.Body)
}

func TestOnePresentationOffsetStandsForEveryAccessUnit(t *testing.T) {
	read := extendedOf(t, defaultOffsetDescriptor())
	if read.Truncated || read.PTSOffsetType != 1 || !read.HasTimescale || read.Timescale != 90_000 || read.DefaultPTSOffset != 3003 {
		t.Fatalf("read %+v", read)
	}
	entry, ok := read.Entry(5)
	if !ok || entry.Leap != 0 || entry.DecodingTimeOffset != 6006 {
		t.Fatalf("entry %+v", entry)
	}
	want := []AccessUnitTiming{{0, 3003}, {3003, 3003}, {6006, 3003}}
	for i, unit := range want {
		if entry.AccessUnits[i] != unit {
			t.Fatalf("unit %d %+v", i, entry.AccessUnits[i])
		}
	}
	if _, ok := read.Entry(6); ok {
		t.Fatal("an MPU that is not there")
	}
}

func TestEveryAccessUnitCanWriteItsOwnPresentationOffset(t *testing.T) {
	read := extendedOf(t, perUnitOffsetDescriptor())
	if read.Truncated || read.PTSOffsetType != 2 || read.Timescale != 1000 || read.DefaultPTSOffset != 0 {
		t.Fatalf("read %+v", read)
	}
	entry, ok := read.Entry(7)
	if !ok || entry.Leap != 1 || entry.DecodingTimeOffset != 20 || len(entry.AccessUnits) != 2 ||
		entry.AccessUnits[0] != (AccessUnitTiming{10, 33}) || entry.AccessUnits[1] != (AccessUnitTiming{0, 34}) {
		t.Fatalf("entry %+v", entry)
	}
}

func TestADescriptorThatRunsOutSaysSo(t *testing.T) {
	if !ParseExtendedTimestamp(nil).Truncated {
		t.Fatal("empty")
	}
	// A timescale is promised and not written.
	if !ParseExtendedTimestamp([]byte{0x01, 0x00, 0x01}).Truncated {
		t.Fatal("promised timescale")
	}
	// A timescale of zero is no timescale.
	if !ParseExtendedTimestamp([]byte{0x01, 0x00, 0x00, 0x00, 0x00}).Truncated {
		t.Fatal("zero timescale")
	}
	// Three access units are promised and two are written.
	read := ParseExtendedTimestamp([]byte{
		0x03, 0x00, 0x00, 0x03, 0xe8, 0x00, 0x0a, 0x00, 0x00, 0x00, 0x07, 0x00, 0x00, 0x14, 0x03,
		0x00, 0x01, 0x00, 0x02,
	})
	if !read.Truncated || len(read.Entries) != 0 {
		t.Fatalf("read %+v", read)
	}
}

func TestTheTransportClockCountsFromOneBase(t *testing.T) {
	base := uint64(3_000_000_000) << 32
	var clock ClockBase
	if _, ok := clock.To90k(base); ok {
		t.Fatal("a base before one was taken")
	}
	clock.Set(base)
	clock.Set(base + ntpSecond)
	if got, _ := clock.NTP(); got != base {
		t.Fatalf("base %d", got)
	}
	for ntp, want := range map[uint64]int64{base: 0, base + ntpSecond: 90_000, base + ntpSecond/2: 45_000, base - ntpSecond: -90_000} {
		if got, _ := clock.To90k(ntp); got != want {
			t.Errorf("%d: %d, want %d", ntp, got, want)
		}
	}
}

func TestACountOfATimescaleRoundsTheSameWayOnEitherSideOfZero(t *testing.T) {
	for _, c := range []struct {
		ticks     int64
		timescale uint32
		want      int64
	}{{1, 0, 0}, {1, 90_000, 1}, {20, 1000, 1800}, {1, 7, 12_857}, {-1, 7, -12_857}, {1, 1_000_000, 0}, {6, 1_000_000, 1}} {
		if got := TicksTo90k(c.ticks, c.timescale); got != c.want {
			t.Errorf("TicksTo90k(%d, %d) = %d, want %d", c.ticks, c.timescale, got, c.want)
		}
	}
}

func TestTheAccessUnitsOfAnMPUAreDecodedBeforeTheMPUIsShown(t *testing.T) {
	read := extendedOf(t, defaultOffsetDescriptor())
	entry, _ := read.Entry(5)
	base := uint64(3_000_000_000) << 32
	var clock ClockBase
	times := func(ntp uint64, sample uint32) (AccessUnitTimes, bool) {
		return AccessUnitTimesOf(&clock, entry, read.Timescale, read.PTSOffsetType, read.DefaultPTSOffset, ntp, sample)
	}
	for _, c := range []struct {
		ntp    uint64
		sample uint32
		want   AccessUnitTimes
	}{
		// The MPU's own time is the base: the first access unit is decoded
		// two frames early and shown at once.
		{base, 1, AccessUnitTimes{-6006, -6006}},
		{base, 2, AccessUnitTimes{-3003, 0}},
		{base, 3, AccessUnitTimes{0, 6006}},
		// A later MPU is counted from the same base.
		{base + ntpSecond, 1, AccessUnitTimes{90_000 - 6006, 90_000 - 6006}},
	} {
		if got, ok := times(c.ntp, c.sample); !ok || got != c.want {
			t.Errorf("sample %d at %d: %+v %v, want %+v", c.sample, c.ntp, got, ok, c.want)
		}
	}
	if got, _ := clock.NTP(); got != base {
		t.Fatalf("base %d", got)
	}
	if _, ok := times(base, 0); ok {
		t.Fatal("sample zero")
	}
	if _, ok := times(base, 4); ok {
		t.Fatal("a fourth sample of three")
	}
}

func TestThePresentationOffsetsWrittenPerAccessUnitAreTheOnesAddedUp(t *testing.T) {
	read := extendedOf(t, perUnitOffsetDescriptor())
	entry, _ := read.Entry(7)
	base := uint64(3_000_000_000) << 32
	var clock ClockBase
	if got, ok := AccessUnitTimesOf(&clock, entry, read.Timescale, read.PTSOffsetType, read.DefaultPTSOffset, base, 1); !ok || got != (AccessUnitTimes{-1800, -900}) {
		t.Fatalf("first %+v", got)
	}
	if got, ok := AccessUnitTimesOf(&clock, entry, read.Timescale, read.PTSOffsetType, read.DefaultPTSOffset, base, 2); !ok || got != (AccessUnitTimes{1170, 1170}) {
		t.Fatalf("second %+v", got)
	}
	if _, ok := AccessUnitTimesOf(&ClockBase{}, entry, 0, read.PTSOffsetType, read.DefaultPTSOffset, base, 1); ok {
		t.Fatal("a timescale of zero")
	}
}

func TestParseMHSoftwareDownloadReadsTheMHTable(t *testing.T) {
	body := []byte{0x00, 0x01, 0x7f, 0xe1, 0x04, 0x00, 0x01}
	// 13 bytes described: an 8-byte schedule and a 5-byte descriptor (tag 0x8000, length 2).
	body = append(body, 0x11, 0x00, 0x10, 0x14, 0x00, 0xd0, 0x00, 0x80)
	body = append(body, 0xef, 0x66, 0x12, 0x00, 0x00, 0x01, 0x00, 0x00)
	body = append(body, 0x80, 0x00, 0x02, 0xab, 0xcd)
	table := ParseMHSoftwareDownload(long(TableIDMHSDTT, 0x1234, body))
	if table == nil || len(table.Contents) != 1 {
		t.Fatalf("read %+v", table)
	}
	content := table.Contents[0]
	if len(content.Descriptors) != 0 || len(content.MHDescriptors) != 1 || content.MHDescriptors[0].Tag != 0x8000 ||
		!bytes.Equal(content.MHDescriptors[0].Body, []byte{0xab, 0xcd}) || len(content.Schedules) != 1 {
		t.Fatalf("content %+v", content)
	}
	if ParseSoftwareDownload(long(TableIDMHSDTT, 0x1234, body)) != nil {
		t.Fatal("an MH-SDTT read as an SDTT")
	}
}

func TestAnMHCDTCarriesItsDataAsSent(t *testing.T) {
	descriptor := mmtDescribed(0x8000, []byte{0x01})
	body := []byte{0x7f, 0xe1, 0x01, 0xf0 | byte(len(descriptor)>>8), byte(len(descriptor))}
	body = append(body, descriptor...)
	logo := []byte{0x05, 0xff, 0x23, 0xf0, 0x07, 0x00, 0x04, 0x89, 'P', 'N', 'G'}
	body = append(body, logo...)
	table := ParseMHCommonData(long(TableIDMHCDT, 0x0001, body))
	if table == nil || table.OriginalNetworkID != 0x7fe1 || table.DataType != 0x01 || len(table.Descriptors) != 1 ||
		table.Descriptors[0].Tag != 0x8000 || !bytes.Equal(table.Data, logo) {
		t.Fatalf("read %+v", table)
	}
}

func TestAPLTAndAnLCTAreRead(t *testing.T) {
	plt := []byte{TableIDPLT, 0x00, 0x00, 0x00, 0x02, 0x01, 0xaa, 0x00, 0x01, 0x23, 0x01, 0xbb, 0x05, 0x03, 'a', 'b', 'c', 0x00}
	read := ParsePackageList(plt)
	if read == nil || len(read.Packages) != 2 || !bytes.Equal(read.Packages[0].PackageID, []byte{0xaa}) {
		t.Fatalf("read %+v", read)
	}
	if read.Packages[0].Location != (PackageLocation{Kind: LocationHere, PacketID: 0x0123}) {
		t.Fatalf("first %+v", read.Packages[0].Location)
	}
	if read.Packages[1].Location != (PackageLocation{Kind: LocationURL, URL: "abc"}) {
		t.Fatalf("second %+v", read.Packages[1].Location)
	}
	lct := []byte{TableIDLCT, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x02, 0x00, 0, 0, 50, 100, 1, 0x01, 50, 0, 100, 100, 2}
	layouts := ParseLayoutConfiguration(lct)
	if len(layouts) != 1 || len(layouts[0].Regions) != 2 || layouts[0].Regions[1].Left != 50 || layouts[0].Regions[1].Layer != 2 {
		t.Fatalf("layouts %+v", layouts)
	}
}

func TestEveryKindOfLocationIsRead(t *testing.T) {
	ipv4 := append([]byte{LocationIPv4}, make([]byte, 8)...)
	ipv4 = append(ipv4, 0x12, 0x34, 0x01, 0x00)
	if location, used, ok := ParseGeneralLocation(ipv4); !ok || used != 13 || location.Port != 0x1234 || location.PacketID != 0x0100 {
		t.Fatalf("ipv4 %+v %d", location, used)
	}
	ts := []byte{LocationTS, 0x00, 0x04, 0x40, 0x10, 0xe1, 0x00}
	if location, used, ok := ParseGeneralLocation(ts); !ok || used != 7 || location.NetworkID != 4 || location.TransportStreamID != 0x4010 || location.PID != 0x0100 {
		t.Fatalf("ts %+v %d", location, used)
	}
	if _, _, ok := ParseGeneralLocation([]byte{LocationIPv6, 0x00}); ok {
		t.Fatal("a short IPv6 location")
	}
	if location, used, ok := ParseGeneralLocation([]byte{0x7f}); !ok || used != 1 || location.Kind != 0x7f {
		t.Fatalf("unknown %+v %d", location, used)
	}
}

func TestTheSectionsOfAMessageAreCheckedAndThePaddingEndsThem(t *testing.T) {
	good := long(TableIDMHSDTActual, 1, []byte{0x00})
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xff
	payload := append(append(append([]byte(nil), good...), bad...), good...)
	payload = append(payload, 0xff, 0xff, 0xff, 0xff)
	if sections := MHSections(payload); len(sections) != 2 {
		t.Fatalf("%d sections", len(sections))
	}
	padded := append(append([]byte(nil), good...), 0xff, 0xff)
	if section, ok := MHControlSection(padded); !ok || !bytes.Equal(section, good) {
		t.Fatal("the control section with padding after it")
	}
	if _, ok := MHControlSection(bad); ok {
		t.Fatal("a section that does not check out")
	}
}

func TestThePackageMessageTablesFollowTheirIndex(t *testing.T) {
	plt := []byte{TableIDPLT, 0x00, 0x00, 0x01, 0x00}
	lct := []byte{TableIDLCT, 0x00, 0x00, 0x01, 0x00}
	payload := []byte{0x02, TableIDPLT, 0x00, 0x00, 0x01, TableIDLCT, 0x00, 0x00, 0x01}
	payload = append(append(payload, plt...), lct...)
	tables := PackageMessageTables(payload)
	if len(tables) != 2 || tables[0].TableID != TableIDPLT || !bytes.Equal(tables[1].Bytes, lct) {
		t.Fatalf("tables %+v", tables)
	}
}

func TestTheStreamSaysWhatItCarries(t *testing.T) {
	state := NewState()
	if _, _, ok := state.Identity(); ok {
		t.Fatal("an identity before any table")
	}
	state.TLVStreamID, state.OriginalNetworkID = 0x0001, 0x000b
	if stream, network, ok := state.Identity(); !ok || stream != 1 || network != 0x0b {
		t.Fatal("identity")
	}
	state.Services[101] = Service{ServiceID: 101}
	state.Services[102] = Service{ServiceID: 102}
	if len(state.CarriedServices()) != 2 {
		t.Fatal("every service without a package list")
	}
	state.Carried = []uint16{102}
	if carried := state.CarriedServices(); len(carried) != 1 || carried[0].ServiceID != 102 {
		t.Fatalf("carried %+v", carried)
	}
}
