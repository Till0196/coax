// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"testing"
)

// packetiseIT9175 splits an image into scatter packets the way ITE's driver
// does, so that the reader can be exercised without a driver to hand.  Blocks
// are cut into short runs and up to three runs share a packet, with their data
// stored in the reverse of the descriptor order.
func packetiseIT9175(banks []firmwareBank, runSize int) []byte {
	var segments []scatterSegment
	for _, bank := range banks {
		for _, block := range bank.blocks {
			for offset := 0; offset < len(block.data); offset += runSize {
				stop := min(offset+runSize, len(block.data))
				segments = append(segments, scatterSegment{
					bank: bank.index,
					addr: block.addr + uint16(offset),
					data: block.data[offset:stop],
				})
			}
		}
	}
	var stream []byte
	for start := 0; start < len(segments); {
		stop := min(start+3, len(segments))
		for i := start + 1; i < stop; i++ {
			if segments[i].bank != segments[start].bank {
				stop = i
				break
			}
		}
		packet := []byte{0x03, segments[start].bank, 0x00, byte(stop - start)}
		for _, segment := range segments[start:stop] {
			packet = binary.BigEndian.AppendUint16(packet, segment.addr)
			packet = append(packet, byte(len(segment.data)))
		}
		for i := stop - 1; i >= start; i-- {
			packet = append(packet, segments[i].data...)
		}
		stream = append(stream, packet...)
		start = stop
	}
	return stream
}

func sampleBanks() []firmwareBank {
	first := make([]byte, 200)
	second := make([]byte, 90)
	third := make([]byte, 61)
	for i := range first {
		first[i] = byte(i * 7)
	}
	for i := range second {
		second[i] = byte(i*13 + 1)
	}
	for i := range third {
		third[i] = byte(i * 3)
	}
	return []firmwareBank{
		{index: 0x01, blocks: []firmwareBlock{{addr: 0x4100, data: first}, {addr: 0x8000, data: second}}},
		{index: 0x02, blocks: []firmwareBlock{{addr: 0x4193, data: third}}},
	}
}

func TestIT9175ScatterRoundTrip(t *testing.T) {
	banks := sampleBanks()
	want := encodeIT9175Image(banks)
	// Surround the packets with bytes that contain the 0x03 the search keys on,
	// so that a run has to be chosen on length rather than on first match.
	decoy := []byte{0x03, 0x03, 0x00, 0x01, 0x41, 0x00, 0x02, 0xAA, 0xBB, 0xFF, 0xFF, 0xFF}
	driver := append(decoy, packetiseIT9175(banks, 17)...)
	driver = append(driver, 0x03, 0x00, 0x00, 0x00)

	segments := findLongestScatterRun(driver).segments
	if len(segments) == 0 {
		t.Fatal("no scatter packets found")
	}
	if got := encodeIT9175Image(joinScatterSegments(segments)); !bytes.Equal(got, want) {
		t.Fatalf("rebuilt image does not match\n got %x\nwant %x", got, want)
	}
}

func TestIT9175RejectsUnrelatedData(t *testing.T) {
	if _, err := ExtractIT9175Firmware(bytes.Repeat([]byte{0x03, 0x00}, 512)); err == nil {
		t.Fatal("expected unrelated data to be rejected")
	}
}

func TestInstallShieldDeobfuscation(t *testing.T) {
	const name = "Setup.ini"
	plain := make([]byte, 3000) // longer than the key period
	for i := range plain {
		plain[i] = byte(i)
	}
	// Encode with the inverse transform, then read it back.
	key := []byte(name)
	for i := range key {
		key[i] ^= installShieldKeyMagic[i%len(installShieldKeyMagic)]
	}
	encoded := make([]byte, len(plain))
	for i, b := range plain {
		mixed := key[i%installShieldKeyPeriod%len(key)] ^ ^b
		encoded[i] = mixed<<4 | mixed>>4
	}
	if got := deobfuscateInstallShield(encoded, name); !bytes.Equal(got, plain) {
		t.Fatal("deobfuscated content does not match the original")
	}
}

// buildCabinet writes a one folder cabinet holding a single file, either stored
// or MSZip compressed.
func buildCabinet(t *testing.T, name string, content []byte, compress uint16) []byte {
	t.Helper()
	var blocks []byte
	if compress == cabCompressNone {
		blocks = append(make([]byte, 0, len(content)+cabDataSize), 0, 0, 0, 0)
		blocks = binary.LittleEndian.AppendUint16(blocks, uint16(len(content)))
		blocks = binary.LittleEndian.AppendUint16(blocks, uint16(len(content)))
		blocks = append(blocks, content...)
	} else {
		var compressed bytes.Buffer
		writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		payload := append([]byte("CK"), compressed.Bytes()...)
		blocks = append(make([]byte, 0, len(payload)+cabDataSize), 0, 0, 0, 0)
		blocks = binary.LittleEndian.AppendUint16(blocks, uint16(len(payload)))
		blocks = binary.LittleEndian.AppendUint16(blocks, uint16(len(content)))
		blocks = append(blocks, payload...)
	}

	entry := make([]byte, cabFileSize)
	binary.LittleEndian.PutUint32(entry, uint32(len(content)))
	entry = append(entry, name...)
	entry = append(entry, 0)

	folderOffset := cabHeaderSize
	fileOffset := folderOffset + cabFolderSize
	dataOffset := fileOffset + len(entry)

	cabinet := make([]byte, cabHeaderSize)
	copy(cabinet, cabSignature)
	binary.LittleEndian.PutUint32(cabinet[0x10:], uint32(fileOffset))
	binary.LittleEndian.PutUint16(cabinet[0x1A:], 1)
	binary.LittleEndian.PutUint16(cabinet[0x1C:], 1)

	folder := make([]byte, cabFolderSize)
	binary.LittleEndian.PutUint32(folder, uint32(dataOffset))
	binary.LittleEndian.PutUint16(folder[4:], 1)
	binary.LittleEndian.PutUint16(folder[6:], compress)

	cabinet = append(cabinet, folder...)
	cabinet = append(cabinet, entry...)
	cabinet = append(cabinet, blocks...)
	binary.LittleEndian.PutUint32(cabinet[0x08:], uint32(len(cabinet)))
	return cabinet
}

func TestExtractCabinet(t *testing.T) {
	content := bytes.Repeat([]byte("driver bytes "), 400)
	for _, method := range []struct {
		name     string
		compress uint16
	}{{"stored", cabCompressNone}, {"mszip", cabCompressMSZip}} {
		t.Run(method.name, func(t *testing.T) {
			cabinet := buildCabinet(t, "it9175bda.sys1", content, method.compress)
			if !IsCabinet(cabinet) {
				t.Fatal("cabinet not recognised")
			}
			members, err := ExtractCabinet(cabinet, wantedInCabinet, newExpansionBudget())
			if err != nil {
				t.Fatal(err)
			}
			if len(members) != 1 || members[0].Name != "it9175bda.sys1" {
				t.Fatalf("got %d members; want it9175bda.sys1", len(members))
			}
			if !bytes.Equal(members[0].Data, content) {
				t.Fatal("member content does not match")
			}
		})
	}
}

func TestExtractCabinetSkipsUnwantedFolders(t *testing.T) {
	cabinet := buildCabinet(t, "readme.txt", []byte("not a driver"), cabCompressMSZip)
	members, err := ExtractCabinet(cabinet, wantedInCabinet, newExpansionBudget())
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("got %d members; want none", len(members))
	}
}

func TestHasExtension(t *testing.T) {
	for _, name := range []string{"it9175bda.sys", "IT9175BDA.SYS1", "it9175bda.sys42"} {
		if !hasExtension(name, ".sys") {
			t.Errorf("%q should be taken as a driver", name)
		}
	}
	for _, name := range []string{"it9175bda.system", "it9175bda.inf", "sys"} {
		if hasExtension(name, ".sys") {
			t.Errorf("%q should not be taken as a driver", name)
		}
	}
}

// buildIT930xDriver lays out a driver the way ITE's do: the packet stream, and
// a segment table whose lengths add up to the length of that stream.
func buildIT930xDriver(stream []byte, align int, entries int) []byte {
	driver := append(bytes.Repeat([]byte{0xCC}, 0x40), stream...)
	// A compiler puts the table on a word boundary, and the reader steps
	// through the driver a word at a time, so the layout has to match.
	for len(driver)%align != 0 {
		driver = append(driver, 0x00)
	}
	table := make([]byte, 0, entries*align*2)
	remaining := len(stream)
	for i := 0; i < entries; i++ {
		part := remaining / (entries - i)
		remaining -= part
		entry := make([]byte, align*2)
		binary.LittleEndian.PutUint32(entry, it930xSegmentType)
		binary.LittleEndian.PutUint32(entry[align:], uint32(part))
		table = append(table, entry...)
	}
	return append(append(driver, table...), 0xEE, 0xEE, 0xEE, 0xEE)
}

func TestExtractIT930xFirmware(t *testing.T) {
	// A stream long enough to clear the floor that keeps stray packets out.
	banks := []firmwareBank{{index: 0x00, blocks: []firmwareBlock{{addr: 0x4100, data: make([]byte, 4000)}}}}
	stream := packetiseIT9175(banks, 41)
	for _, align := range []int{4, 8} {
		driver := buildIT930xDriver(stream, align, 5)
		image, err := ExtractIT930xFirmware(driver)
		if err != nil {
			t.Fatalf("align %d: %v", align, err)
		}
		if !bytes.Equal(image, stream) {
			t.Fatalf("align %d: image does not match the stream", align)
		}
	}
}

func TestIT930xNeedsASegmentTable(t *testing.T) {
	banks := []firmwareBank{{index: 0x00, blocks: []firmwareBlock{{addr: 0x4100, data: make([]byte, 4000)}}}}
	stream := append(bytes.Repeat([]byte{0xCC}, 0x40), packetiseIT9175(banks, 41)...)
	if _, err := ExtractIT930xFirmware(stream); err == nil {
		t.Fatal("a stream with nothing confirming its length must be refused")
	}
}

func TestIT930xIgnoresShortRuns(t *testing.T) {
	// Two packets and a segment table that happens to describe their length:
	// far too short to be a firmware, and it must not be taken for one.
	banks := []firmwareBank{{index: 0x00, blocks: []firmwareBlock{{addr: 0x4100, data: make([]byte, 60)}}}}
	driver := buildIT930xDriver(packetiseIT9175(banks, 30), 4, 2)
	if _, err := ExtractIT930xFirmware(driver); err == nil {
		t.Fatal("a run far below any real firmware size must be refused")
	}
}

func TestDecodeMSIName(t *testing.T) {
	if got := decodeMSIName("Data1.cab"); got != "Data1.cab" {
		t.Fatalf("plain names must pass through; got %q", got)
	}
}
