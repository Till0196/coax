// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package bml

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/mpegts"
)

// The entry component of a terrestrial service: the values as operated.
func TestTheEntryComponentSaysHowTheDocumentsAreLaidOut(t *testing.T) {
	// Format 0, entry point, auto start, 960x540; no XML, version given,
	// independent, style for TV; BML 3.0; carousel info.
	info, ok := ParseAdditionalInfo([]byte{0x33, 0x30, 0x00, 0x03, 0x00, 0x00, 0xF8, 0xA0})
	if !ok {
		t.Fatal("not read")
	}
	if !info.EntryPoint || !info.AutoStart {
		t.Errorf("entry %v auto %v", info.EntryPoint, info.AutoStart)
	}
	if width, height, wide, ok := info.Resolution(); !ok || width != 960 || height != 540 || !wide {
		t.Errorf("resolution %d %d %v %v", width, height, wide, ok)
	}
	if info.BMLVersion != [2]uint16{3, 0} {
		t.Errorf("version %v", info.BMLVersion)
	}
	if info.DataEventID != 0xf || !info.EventSection || !info.OndemandRetrieval || info.StartPriority != 1 {
		t.Errorf("carousel %+v", info)
	}
}

func TestAComponentThatIsNotTheEntryHasOnlyTheCarouselInfo(t *testing.T) {
	info, ok := ParseAdditionalInfo([]byte{0x1f, 0xff, 0xbf})
	if !ok {
		t.Fatal("not read")
	}
	if info.EntryPoint || info.DataEventID != 0xf || !info.EventSection || !info.OndemandRetrieval || info.StartPriority != 1 {
		t.Errorf("info %+v", info)
	}
	if _, ok := ParseAdditionalInfo([]byte{0x00}); ok {
		t.Error("a short carousel info read")
	}
}

func TestAModuleSaysItsTypeAndItsCompression(t *testing.T) {
	described := Describe([]byte{0x01, 4, 't', 'e', 'x', 't', 0xc2, 5, 0, 0, 0, 1, 0})
	if !described.HasType || described.ContentType != "text" {
		t.Errorf("type %+v", described)
	}
	if !described.Compressed || described.CompressionType != 0 || described.OriginalSize != 256 {
		t.Errorf("compression %+v", described)
	}
	if flag, ok := ReturnToEntry([]byte{0xf0, 1, 0x80}); !ok || !flag {
		t.Errorf("return to entry %v %v", flag, ok)
	}
	if _, ok := ReturnToEntry(nil); ok {
		t.Error("return to entry from nothing")
	}
}

func TestACompressedModuleUnpacksToTheLengthItSaid(t *testing.T) {
	original := []byte("<bml>hello hello hello hello</bml>")
	var packed bytes.Buffer
	writer := zlib.NewWriter(&packed)
	writer.Write(original)
	writer.Close()
	described := Described{Compressed: true, OriginalSize: uint32(len(original))}
	if out, ok := Unpack(described, packed.Bytes()); !ok || !bytes.Equal(out, original) {
		t.Errorf("unpacked %q %v", out, ok)
	}
	described.OriginalSize = 3
	if _, ok := Unpack(described, packed.Bytes()); ok {
		t.Error("unpacked to another length than it said")
	}
	if _, ok := Unpack(Described{Compressed: true, CompressionType: 1}, packed.Bytes()); ok {
		t.Error("unpacked a method that is not zlib")
	}
}

func TestAMultipartModuleIsItsFiles(t *testing.T) {
	body := []byte("Content-Type: multipart/mixed; boundary=\"BOUNDARY\"\r\n\r\n--BOUNDARY\r\nContent-Type: text/X-arib-bml; charset=\"euc-jp\"\r\nContent-Location: startup.bml\r\n\r\n<bml/>\r\n--BOUNDARY\r\nContent-Location: a.png\r\nContent-Type: image/png\r\n\r\n\x89PNG\r\n--BOUNDARY--\r\n")
	files := Files("", body)
	if len(files) != 2 {
		t.Fatalf("files %d", len(files))
	}
	if files[0].Location != "startup.bml" || files[0].ContentType != "text/x-arib-bml" || string(files[0].Body) != "<bml/>" {
		t.Errorf("first %+v", files[0])
	}
	if files[1].Location != "a.png" || string(files[1].Body) != "\x89PNG" {
		t.Errorf("second %+v", files[1])
	}
}

func TestAModuleOfOneFileIsThatFile(t *testing.T) {
	files := Files("image/jpeg", []byte{0xff, 0xd8})
	if len(files) != 1 || files[0].ContentType != "image/jpeg" || files[0].Location != "" {
		t.Errorf("files %+v", files)
	}
}

func TestAGeneralEventSentNowIsReadWithItsMessage(t *testing.T) {
	section := mpegts.StreamDescriptors{
		Extension:   0xf001,
		Descriptors: []byte{0x40, 13, 0x00, 0x10, 0x00, 0, 0, 0, 0, 0, 0x01, 0x00, 0x2a, 'h', 'i'},
	}
	dataEvent, group, events := Events(section)
	if dataEvent != 0xf || group != 1 {
		t.Errorf("data event %x group %x", dataEvent, group)
	}
	if len(events) != 1 || events[0].General == nil {
		t.Fatalf("events %+v", events)
	}
	general := events[0].General
	if general.GroupID != 1 || general.Time.Kind != TimingImmediate || general.Kind != 1 || general.ID != 0x2a || string(general.PrivateData) != "hi" {
		t.Errorf("event %+v", general)
	}
}

func TestADataComponentIsFoundAmongAStreamsDescriptors(t *testing.T) {
	described := isdb.ParseDescriptors([]byte{0x52, 1, 0x40, 0xfd, 3, 0x00, 0x0c, 0x33})
	component, ok := FindDataComponent(described)
	if !ok || component.ID != 0x000c || !bytes.Equal(component.Additional, []byte{0x33}) || !IsBML(component.ID) {
		t.Errorf("component %+v %v", component, ok)
	}
}

// longSection makes a long-form section with its CRC.
func longSection(table byte, extension uint16, version, number, last byte, body []byte) []byte {
	section := []byte{table, 0, 0, byte(extension >> 8), byte(extension), 0xc1 | version<<1, number, last}
	section = append(section, body...)
	length := len(section) + 4 - 3
	section[1] = 0xb0 | byte(length>>8)&0x0f
	section[2] = byte(length)
	return binary.BigEndian.AppendUint32(section, mpegts.CRC32(section))
}

func counted(data []byte) []byte { return append([]byte{byte(len(data))}, data...) }

func TestTheAssetTableTiesUnitsToNodes(t *testing.T) {
	node := func(tag uint16) []byte {
		info := binary.BigEndian.AppendUint16(nil, DescriptorMPUNode)
		info = append(info, 2)
		info = binary.BigEndian.AppendUint16(info, tag)
		return counted(info)
	}
	var body []byte
	body = binary.BigEndian.AppendUint32(body, 1)      // transaction
	body = binary.BigEndian.AppendUint16(body, 0x0040) // component_tag
	body = binary.BigEndian.AppendUint32(body, 0x1234) // download_id
	body = append(body, 2)
	// A unit with an index item: its items are not detailed here.
	body = binary.BigEndian.AppendUint32(body, 0x0101_0001)
	body = binary.BigEndian.AppendUint32(body, 0x100)
	body = append(body, 0xbf)
	body = binary.BigEndian.AppendUint16(body, 0)
	body = append(body, node(0x0101)...)
	// A unit without one: each item is detailed, here with a checksum
	// (STD-B60 Table 10-10: node_tag, item_id, item_size, item_version,
	// checksum_flag).
	body = binary.BigEndian.AppendUint32(body, 0x0102_0001)
	body = binary.BigEndian.AppendUint32(body, 0x200)
	body = append(body, 0x3f)
	body = binary.BigEndian.AppendUint16(body, 1)
	body = append(body, 0x00, 0x01)             // node_tag
	body = append(body, 0, 0, 0, 0x20)          // item_id
	body = append(body, 0, 0, 0, 0x40)          // item_size
	body = append(body, 0x02)                   // item_version
	body = append(body, 0x80)                   // checksum present
	body = append(body, 0xde, 0xad, 0xbe, 0xef) // checksum
	body = append(body, counted(nil)...)        // item_info
	body = append(body, node(0x0102)...)
	body = append(body, counted(nil)...) // component_info

	table := isdb.ParseDataAssetManagement(longSection(isdb.TableIDDAMT, 0x0700, 1, 0, 0, body))
	if table == nil || len(table.Units) != 2 {
		t.Fatalf("table %+v", table)
	}
	if table.ComponentTag != 0x0040 || table.DownloadID != 0x1234 {
		t.Errorf("table %+v", table)
	}
	if !table.Units[0].IndexItem || table.Units[1].IndexItem {
		t.Errorf("index items %v %v", table.Units[0].IndexItem, table.Units[1].IndexItem)
	}
	for at, want := range []uint16{0x0101, 0x0102} {
		if got, ok := MPUNode(table.Units[at]); !ok || got != want {
			t.Errorf("unit %d node %x %v", at, got, ok)
		}
	}
}

func TestTheIndexItemNamesTheFiles(t *testing.T) {
	data := binary.BigEndian.AppendUint16(nil, 2)
	// Compressed with zlib, no checksum: the original size follows.
	data = binary.BigEndian.AppendUint32(data, 1)
	data = binary.BigEndian.AppendUint32(data, 0x80)
	data = append(data, 1)
	data = append(data, counted([]byte("index.html"))...)
	data = append(data, 0x7f)
	data = append(data, counted([]byte("text/html"))...)
	data = append(data, 0x00)
	data = binary.BigEndian.AppendUint32(data, 0x200)
	// Carried as it is, with a checksum: no original size.
	data = binary.BigEndian.AppendUint32(data, 2)
	data = binary.BigEndian.AppendUint32(data, 0x40)
	data = append(data, 1)
	data = append(data, counted([]byte("a.png"))...)
	data = append(data, 0xff)
	data = binary.BigEndian.AppendUint32(data, 0x0102_0304)
	data = append(data, counted([]byte("image/png"))...)
	data = append(data, 0xff)

	items, ok := ParseIndexItem(data)
	if !ok || len(items) != 2 {
		t.Fatalf("items %+v %v", items, ok)
	}
	first := items[0]
	if first.ID != 1 || first.Name != "index.html" || first.Kind != "text/html" || !first.Compressed() || first.HasChecksum || first.Size != 0x80 || first.OriginalSize != 0x200 {
		t.Errorf("first %+v", first)
	}
	second := items[1]
	if second.Name != "a.png" || second.Compressed() || !second.HasChecksum || second.Checksum != 0x0102_0304 || second.OriginalSize != 0x40 {
		t.Errorf("second %+v", second)
	}
	if _, ok := ParseIndexItem(data[:len(data)-1]); ok {
		t.Error("a short index item read")
	}
}
