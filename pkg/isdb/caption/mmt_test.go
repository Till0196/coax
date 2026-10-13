// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package caption

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"
)

func TestSubtitleInfoIsRead(t *testing.T) {
	// A caption: timing mode 0b0010, so the base time (NTP) follows.
	body := []byte{0x00, 0x20, 0x30, 0x07, 'j', 'p', 'n', 0x01, 0x2a, 0x10}
	body = binary.BigEndian.AppendUint64(body, 0xe000_0000_8000_0000)
	info, ok := ParseSubtitleInfo(body)
	if !ok {
		t.Fatal("not read")
	}
	if info.Tag != 0x30 || info.Language != "jpn" || info.Kind != 0 || info.OPM != 1 ||
		info.TMD != 0b0010 || info.DMF != 0b1010 || info.Resolution != 1 || info.Compression != 0 ||
		info.HasStartMPUSequence || !info.HasReferenceStart || info.ReferenceStart != 0xe000_0000_8000_0000 {
		t.Errorf("info %+v", info)
	}
	// Superimposed text: no timing, so no base time.
	info, ok = ParseSubtitleInfo([]byte{0x00, 0x20, 0x38, 0x07, 0x6a, 0x70, 0x6e, 0x41, 0xf2, 0x10})
	if !ok || info.Kind != 1 || info.TMD != 0b1111 || info.HasReferenceStart {
		t.Errorf("superimposed %+v %v", info, ok)
	}
	if _, ok := ParseSubtitleInfo([]byte{0x00, 0x21, 0x30}); ok {
		t.Error("another component read as a caption")
	}
}

func TestATTMLMFUIsRead(t *testing.T) {
	b := append([]byte{0x30, 0xa9, 0x00, 0x00, 0x03, 0x05, 0x63}, "<tt/>"...)
	m, ok := ParseMFU(b)
	if !ok || m.Tag != 0x30 || m.Sequence != 0xa9 || m.DataType != DataTTML || m.Size != 0x0563 || string(m.Data) != "<tt/>" {
		t.Fatalf("mfu %+v", m)
	}
	// A long size, and a first MFU listing the ones that follow.
	b = []byte{0x30, 0x01, 0x00, 0x02, 0x0c, 0x00, 0x00, 0x00, 0x03, 0x60, 0x00, 0x00, 0x00, 0x10,
		0x10, 0x00, 0x00, 0x00, 0x20, 'a', 'b', 'c'}
	m, ok = ParseMFU(b)
	if !ok || m.Size != 3 || m.LastSubsample != 2 || string(m.Data) != "abc" {
		t.Fatalf("mfu %+v", m)
	}
}

func TestACompressedDocumentIsInflated(t *testing.T) {
	var packed bytes.Buffer
	w := zlib.NewWriter(&packed)
	w.Write([]byte("<tt/>"))
	w.Close()
	text, ok := TTMLText(MFU{DataType: DataTTML, Data: packed.Bytes()}, true)
	if !ok || text != "<tt/>" {
		t.Fatalf("got %q %v", text, ok)
	}
	if _, ok := TTMLText(MFU{DataType: DataPNG, Data: []byte("x")}, false); ok {
		t.Error("a picture read as a document")
	}
}
