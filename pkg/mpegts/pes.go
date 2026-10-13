// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

// A transport stream carries pictures and sound as packetised elementary
// streams: a short header saying which stream this is, when it is to be decoded
// and when shown, and then as many bytes of the coded stream as the multiplexer
// chose to send at once. Nothing here looks into the bytes after the header.
//
// The length a header declares may be nought, which is not a header that says
// nothing but one that declines to say: a coded picture is larger than sixteen
// bits can count, so video streams leave the field empty and the packet ends
// where the next one begins.

// PESPrefix is the shortest a header can be: the start code, the stream
// identifier and the declared length.
const PESPrefix = 6

// PESHasOptionalHeader reports whether packets of this stream carry the
// optional header -- the flags, the timestamps, the rest -- after the prefix.
// The handful that do not carry something other than a coded elementary stream,
// and their payload begins at the sixth byte.
func PESHasOptionalHeader(streamID byte) bool {
	switch streamID {
	// padding, private stream 2, ECM, EMM, DSM-CC, ITU-T H.222.1 type E,
	// programme stream directory.
	case 0xbe, 0xbf, 0xf0, 0xf1, 0xf2, 0xf8, 0xff:
		return false
	}
	return true
}

// IsVideoStream reports whether the stream identifier names one of the sixteen
// video streams, whose packets are the ones that leave the length unsaid.
func IsVideoStream(streamID byte) bool { return streamID&0xf0 == 0xe0 }

// IsAudioStream reports whether the stream identifier names one of the
// thirty-two audio streams.
func IsAudioStream(streamID byte) bool { return streamID&0xe0 == 0xc0 }

// PESHeader is the header of one packetised elementary stream packet.
type PESHeader struct {
	// StreamID says which stream this is: what kind, and which of that kind.
	StreamID byte
	// PacketLength is how many bytes follow the prefix, or nought where the
	// packet declines to say and ends where the next one starts.
	PacketLength uint16
	// DataAlignment says the payload starts on a boundary the decoder can
	// start at -- an access unit for video, a frame for audio.
	DataAlignment bool
	// Scrambling is the scrambling control of this layer. Nought is in the
	// clear.
	Scrambling byte
	// PTS is when the access unit is to be shown, in the 90kHz clock, as sent
	// (33 bits); HasPTS says it was there.
	PTS    uint64
	HasPTS bool
	// DTS is when it is to be decoded, which differs from when it is shown
	// wherever pictures are sent out of order; HasDTS says it was there.
	DTS    uint64
	HasDTS bool
	// PayloadAt is where the payload begins, counted from the start of the
	// packet.
	PayloadAt int
}

// ParsePESHeader reads a header from the start of a packet. It reports false
// where these bytes are not one: no start code, or too few bytes to hold the
// header the packet declares.
func ParsePESHeader(data []byte) (PESHeader, bool) {
	if len(data) < PESPrefix || data[0] != 0 || data[1] != 0 || data[2] != 1 {
		return PESHeader{}, false
	}
	header := PESHeader{
		StreamID:     data[3],
		PacketLength: uint16(data[4])<<8 | uint16(data[5]),
		PayloadAt:    PESPrefix,
	}
	if !PESHasOptionalHeader(header.StreamID) {
		return header, true
	}
	if len(data) < 9 {
		return PESHeader{}, false
	}
	header.Scrambling = (data[6] >> 4) & 0x03
	header.DataAlignment = data[6]&0x04 != 0
	flags := data[7] >> 6
	length := int(data[8])
	header.PayloadAt = 9 + length
	if len(data) < header.PayloadAt {
		return PESHeader{}, false
	}
	// The high flag says a presentation stamp is there, and both together say
	// a decoding stamp follows it. The low one on its own is forbidden and
	// read as neither.
	if flags&0x02 != 0 && length >= 5 {
		header.PTS, header.HasPTS = Timestamp(data[9:14])
	}
	if flags == 0x03 && length >= 10 {
		header.DTS, header.HasDTS = Timestamp(data[14:19])
	}
	return header, true
}

// PayloadLength is how many bytes of payload the packet declares. It reports
// false where the packet declared no length, or a length shorter than its own
// header.
func (header PESHeader) PayloadLength() (int, bool) {
	if header.PacketLength == 0 {
		return 0, false
	}
	length := PESPrefix + int(header.PacketLength) - header.PayloadAt
	if length < 0 {
		return 0, false
	}
	return length, true
}

// TotalLength is how long the whole packet is, header and payload. It reports
// false where the packet declared no length.
func (header PESHeader) TotalLength() (int, bool) {
	if header.PacketLength == 0 {
		return 0, false
	}
	return PESPrefix + int(header.PacketLength), true
}

// PESPacket is a whole packet: its header, and the bytes of the elementary
// stream it carries.
type PESPacket struct {
	Header  PESHeader
	Payload []byte
}

// ParsePES reads a packet from bytes that begin with one. Where the packet
// declares a length the payload is cut to it; where it declares none the
// payload is everything that was handed in.
func ParsePES(data []byte) (PESPacket, bool) {
	header, ok := ParsePESHeader(data)
	if !ok {
		return PESPacket{}, false
	}
	rest := data[header.PayloadAt:]
	if length, ok := header.PayloadLength(); ok && length <= len(rest) {
		rest = rest[:length]
	}
	return PESPacket{Header: header, Payload: rest}, true
}

// Timestamp reads one of the 33-bit stamps out of the five bytes it is spread
// across with markers between. It reports false where the markers are wrong,
// which is the cheapest sign that these bytes are not a timestamp at all.
func Timestamp(data []byte) (uint64, bool) {
	if len(data) < 5 || data[0]&0x01 == 0 || data[2]&0x01 == 0 || data[4]&0x01 == 0 {
		return 0, false
	}
	return uint64(data[0]&0x0e)<<29 | uint64(data[1])<<22 | uint64(data[2]&0xfe)<<14 |
		uint64(data[3])<<7 | uint64(data[4])>>1, true
}

// WriteTimestamp writes a stamp back out into the five bytes it is sent as,
// under the four-bit prefix the header gives it.
func WriteTimestamp(prefix byte, stamp uint64) [5]byte {
	stamp &= 0x1_ffff_ffff
	return [5]byte{
		prefix<<4 | (byte(stamp>>30)&0x07)<<1 | 0x01,
		byte(stamp >> 22),
		(byte(stamp>>15)&0x7f)<<1 | 0x01,
		byte(stamp >> 7),
		(byte(stamp)&0x7f)<<1 | 0x01,
	}
}
