// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import (
	"bytes"
	"testing"
)

// pesUnit builds a packet with the optional header on it, the stamps asked
// for (negative for none), and the payload given.
func pesUnit(streamID byte, pts, dts int64, payload []byte) []byte {
	var optional []byte
	flags := byte(0)
	if pts >= 0 {
		flags |= 0x80
		prefix := byte(0x2)
		if dts >= 0 {
			prefix = 0x3
		}
		stamp := WriteTimestamp(prefix, uint64(pts))
		optional = append(optional, stamp[:]...)
	}
	if dts >= 0 {
		flags |= 0x40
		stamp := WriteTimestamp(0x1, uint64(dts))
		optional = append(optional, stamp[:]...)
	}
	out := []byte{0, 0, 1, streamID, 0, 0, 0x84, flags, byte(len(optional))}
	out = append(out, optional...)
	out = append(out, payload...)
	length := len(out) - 6
	out[4], out[5] = byte(length>>8), byte(length)
	return out
}

func TestAPESHeaderSaysWhichStreamAndWhereThePayloadStarts(t *testing.T) {
	data := pesUnit(0xe0, -1, -1, []byte{1, 2, 3})
	header, ok := ParsePESHeader(data)
	if !ok || header.StreamID != 0xe0 || !IsVideoStream(header.StreamID) || !header.DataAlignment ||
		header.Scrambling != 0 || header.PayloadAt != 9 {
		t.Fatalf("header %+v %v", header, ok)
	}
	if length, ok := header.PayloadLength(); !ok || length != 3 {
		t.Errorf("payload length %d %v", length, ok)
	}
	if total, ok := header.TotalLength(); !ok || total != len(data) {
		t.Errorf("total length %d %v", total, ok)
	}
}

func TestTheStampsComeBackAsTheyWereWritten(t *testing.T) {
	header, _ := ParsePESHeader(pesUnit(0xe0, 0x1_2345_6789, 0x0_1234_5678, []byte{9}))
	if !header.HasPTS || header.PTS != 0x1_2345_6789 || !header.HasDTS || header.DTS != 0x0_1234_5678 {
		t.Errorf("stamps %+v", header)
	}
}

func TestAPESWithOneStampHasNoDecodingStamp(t *testing.T) {
	header, _ := ParsePESHeader(pesUnit(0xc0, 1000, -1, []byte{1}))
	if !IsAudioStream(header.StreamID) || !header.HasPTS || header.PTS != 1000 || header.HasDTS {
		t.Errorf("stamps %+v", header)
	}
}

// A video access unit is longer than the field can count, so the field is left
// empty and the payload runs to whatever arrived.
func TestAPESThatDeclaresNoLengthCarriesEverythingHandedIn(t *testing.T) {
	data := pesUnit(0xe0, 42, -1, bytes.Repeat([]byte{7}, 40))
	data[4], data[5] = 0, 0
	packet, ok := ParsePES(data)
	if !ok || packet.Header.PacketLength != 0 || len(packet.Payload) != 40 {
		t.Fatalf("packet %+v %v", packet.Header, ok)
	}
	if _, ok := packet.Header.PayloadLength(); ok {
		t.Error("a length was read where none was declared")
	}
}

func TestAStreamWithoutTheOptionalHeaderStartsItsPayloadAtSix(t *testing.T) {
	packet, ok := ParsePES([]byte{0, 0, 1, 0xbe, 0, 3, 1, 2, 3})
	if !ok || packet.Header.PayloadAt != 6 || !bytes.Equal(packet.Payload, []byte{1, 2, 3}) || packet.Header.HasPTS {
		t.Errorf("packet %+v", packet)
	}
}

func TestBytesThatAreNotAPESAreNotReadAsOne(t *testing.T) {
	for _, data := range [][]byte{
		{0, 0, 2, 0xe0, 0, 0},
		{0, 0, 1},
		{0, 0, 1, 0xe0, 0, 0, 0x80, 0, 0x40}, // a header longer than the bytes
	} {
		if _, ok := ParsePESHeader(data); ok {
			t.Errorf("% x was read as a header", data)
		}
	}
}

func TestAStampWhoseMarkersAreWrongIsNotAStamp(t *testing.T) {
	stamp := WriteTimestamp(0x2, 90_000)
	if value, ok := Timestamp(stamp[:]); !ok || value != 90_000 {
		t.Fatalf("stamp %d %v", value, ok)
	}
	stamp[2] &= 0xfe
	if _, ok := Timestamp(stamp[:]); ok {
		t.Error("a stamp with a broken marker was read")
	}
}
