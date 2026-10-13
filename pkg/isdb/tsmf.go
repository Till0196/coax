// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// TSMF, the multiplexing frame that carries several relative streams -- TS or
// TLV -- down one carrier.
package isdb

import "github.com/Till0196/coax/pkg/mpegts"

const (
	HeaderPID = 0x2f
	TLVPID    = 0x2d
	SlotCount = 52
)

type RelativeStream struct {
	Number            int
	StreamID          uint16
	OriginalNetworkID uint16
	Format            string
	SlotCount         int
	// ReceiveStatus is how the sender says the stream is being received:
	// nought normal, one poor, two not received.
	ReceiveStatus byte
}

// Header is everything a frame header says, whether or not the
// demultiplexing uses it.
type Header struct {
	// Sync is the sync word as sent: one of two, alternating frame by frame.
	Sync    uint16
	Version byte
	// RelativeMode says whether the relative stream numbers are fixed or
	// reassigned.
	RelativeMode byte
	// FrameType is one for a frame of one carrier, two for a frame split
	// across carriers.
	FrameType byte
	// Emergency is the emergency warning start signal.
	Emergency bool
	// Grouped says the frame is split across carriers and the group it names
	// is one that can be. A split frame whose group is out of range keeps its
	// header; only the group is left unsaid, and the frame reads as one
	// carrier's.
	Grouped          bool
	GroupID          byte
	NumberOfCarriers byte
	CarrierSequence  byte
	FramePosition    byte
	NumberOfFrames   byte
	RelativeStreams  []RelativeStream
	PacketIndex      int
	SlotMap          [SlotCount]byte
	CRC              uint32
}

type Streams struct {
	TS  map[int][][]byte
	TLV map[int][]byte
	// Frames is how many frame headers were decoded. Comparing it with the
	// number of packets in the sample says whether the frame structure is
	// being followed or only picked up occasionally.
	Frames int
}

func Find(packets [][]byte) *Header {
	lastCC := -1
	synced := false
	for index, packet := range packets {
		pid, _, _, ok := mpegts.Payload(packet)
		if !ok || pid != HeaderPID {
			continue
		}
		cc := int(packet[3] & 0x0f)
		if !synced {
			if lastCC >= 0 && cc == (lastCC+1)&0x0f {
				synced = true
			} else {
				lastCC = cc
				continue
			}
		}
		lastCC = cc
		if header := Parse(packet, index); header != nil {
			return header
		}
	}
	return nil
}

func Parse(packet []byte, packetIndex int) *Header {
	pid, payload, _, ok := mpegts.Payload(packet)
	if !ok || pid != HeaderPID || len(payload) != 184 {
		return nil
	}
	sync := (uint16(payload[0])<<8 | uint16(payload[1])) & 0x1fff
	if (sync != 0x1a86 && sync != 0x0579) || mpegts.CRC32(payload) != 0 {
		return nil
	}
	frameType := payload[2] & 0x0f
	header := &Header{
		Sync:             sync,
		Version:          payload[2] >> 5,
		RelativeMode:     (payload[2] >> 4) & 0x01,
		FrameType:        frameType,
		Emergency:        payload[68]&0x01 != 0,
		NumberOfCarriers: 1,
		CarrierSequence:  1,
		FramePosition:    payload[126] & 0x0f,
		NumberOfFrames:   payload[126] >> 4,
		PacketIndex:      packetIndex,
		CRC:              uint32(payload[180])<<24 | uint32(payload[181])<<16 | uint32(payload[182])<<8 | uint32(payload[183]),
	}
	if frameType == 2 {
		carriers, sequence := payload[124], payload[125]
		if carriers >= 1 && carriers <= 16 && sequence >= 1 && sequence <= carriers {
			header.Grouped = true
			header.GroupID, header.NumberOfCarriers, header.CarrierSequence = payload[123], carriers, sequence
		}
	}
	streamTypeBits := uint16(payload[121])<<7 | uint16(payload[122]>>1)
	counts := [16]int{}
	for slot := 0; slot < SlotCount; slot++ {
		value := payload[69+slot/2]
		if slot%2 == 0 {
			value >>= 4
		} else {
			value &= 0x0f
		}
		header.SlotMap[slot] = value
		if value >= 1 && value <= 15 {
			counts[value]++
		}
	}
	for relative := 1; relative <= 15; relative++ {
		if counts[relative] == 0 {
			continue
		}
		offset := 5 + (relative-1)*4
		format := "ts"
		if (streamTypeBits>>(15-relative))&1 == 0 {
			format = "tlv"
		}
		index := relative - 1
		header.RelativeStreams = append(header.RelativeStreams, RelativeStream{
			Number:            relative,
			StreamID:          uint16(payload[offset])<<8 | uint16(payload[offset+1]),
			OriginalNetworkID: uint16(payload[offset+2])<<8 | uint16(payload[offset+3]),
			Format:            format,
			SlotCount:         counts[relative],
			ReceiveStatus:     (payload[65+index/4] >> ((3 - index%4) * 2)) & 0x03,
		})
	}
	return header
}

func Demultiplex(packets [][]byte, initial *Header) (*Header, Streams) {
	streams := Streams{TS: make(map[int][][]byte), TLV: make(map[int][]byte)}
	// A sample begins wherever the tuner happened to be, almost never on a TLV
	// packet boundary. Bytes before the first packet that starts one are the
	// tail of a packet whose beginning was never seen; keeping them leaves the
	// reader finding sync bytes in the middle of a payload.
	started := make(map[int]bool)
	header := initial
	// Slots are taken by position: a frame is a header followed by exactly
	// SlotCount packets, and counting positions keeps a frame's payload
	// contiguous. Gathering TLV packets by their identifier instead runs past
	// the end of a frame whose header was lost, splicing together bytes that
	// were never adjacent and desynchronising the reassembled stream.
	//
	// A header arriving before the slots run out starts the frame again from
	// it, and a frame that runs out without the next header places nothing
	// more until one comes: there is no telling whose slots those are.
	position := SlotCount
	for packetIndex := initial.PacketIndex; packetIndex < len(packets); packetIndex++ {
		if candidate := Parse(packets[packetIndex], packetIndex); candidate != nil {
			header = candidate
			streams.Frames++
			position = 0
			continue
		}
		if position >= SlotCount {
			continue
		}
		relative := int(header.SlotMap[position])
		position++
		if relative < 1 || relative > 15 {
			continue
		}
		streams.TS[relative] = append(streams.TS[relative], packets[packetIndex])
		if payload, start := TLVPayload(packets[packetIndex]); payload != nil {
			if start {
				started[relative] = true
			}
			if started[relative] {
				streams.TLV[relative] = append(streams.TLV[relative], payload...)
			}
		}
	}
	return header, streams
}

// TLVPayload reports the TLV bytes a TSMF slot packet carries, and whether the
// packet starts a new TLV packet.
//
// The byte layout is Mirakurun's TLVAssembler: a packet that starts a unit
// keeps one byte less than one that continues it.
func TLVPayload(packet []byte) (payload []byte, start bool) {
	if len(packet) != mpegts.PacketSize || packet[0] != 0x47 {
		return nil, false
	}
	pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
	if pid != TLVPID {
		return nil, false
	}
	if packet[1]&0x40 != 0 {
		return packet[4:], true
	}
	return packet[3:], false
}

// Frame is one frame of a multiplexing frame's carrier, as it arrived. The
// slots are the ones the caller asked for, in the places they sat: what puts a
// split frame back together is where a slot was, not just what was in it.
type Frame struct {
	// Grouped says the frame is one of several carrying one frame, and GroupID
	// names the group.
	Grouped bool
	GroupID byte
	// Position is where this frame sits in its superframe, and Of how many
	// frames that run holds.
	Position byte
	Of       byte
	// Sequence is which carrier of the group this is, and Carriers how many
	// there are. A frame of one carrier says one of one.
	Sequence byte
	Carriers byte
	// Slots holds the wanted stream's packets, by slot. Slots belonging to
	// another stream are nil.
	Slots [SlotCount][]byte
}

// FrameReader reads a carrier as whole frames, for putting a split frame back
// together.
type FrameReader struct {
	wanted   byte
	held     []byte
	building *Frame
	slotMap  [SlotCount]byte
	place    int
}

// NewFrameReader keeps the slots of one relative stream, numbered 1 to 15 as
// the frame header numbers them.
func NewFrameReader(wanted byte) *FrameReader { return &FrameReader{wanted: wanted} }

// Feed takes what has arrived and gives back the frames it completed. A frame
// is complete when the next header arrives.
func (reader *FrameReader) Feed(arrived []byte) []Frame {
	reader.held = append(reader.held, arrived...)
	var whole []Frame
	at := 0
	for at+mpegts.PacketSize <= len(reader.held) {
		if reader.held[at] != 0x47 {
			at++
			continue
		}
		packet := reader.held[at : at+mpegts.PacketSize]
		if header := Parse(packet, 0); header != nil {
			if reader.building != nil {
				whole = append(whole, *reader.building)
			}
			reader.building = &Frame{
				Grouped:  header.Grouped,
				GroupID:  header.GroupID,
				Position: header.FramePosition,
				Of:       header.NumberOfFrames,
				Sequence: header.CarrierSequence,
				Carriers: header.NumberOfCarriers,
			}
			reader.slotMap = header.SlotMap
			reader.place = 0
		} else if reader.building != nil && reader.place < SlotCount {
			if reader.slotMap[reader.place] == reader.wanted {
				reader.building.Slots[reader.place] = append([]byte(nil), packet...)
			}
			reader.place++
		}
		at += mpegts.PacketSize
	}
	reader.held = append(reader.held[:0], reader.held[at:]...)
	return whole
}

// TLVJoin joins the TLV inside a run of slot packets. The bytes before the
// first packet that starts a TLV packet are the tail of one whose beginning was
// never seen, and are left out.
type TLVJoin struct{ started bool }

// Take adds one packet's TLV bytes to what has been gathered.
func (join *TLVJoin) Take(packet, into []byte) []byte {
	payload, start := TLVPayload(packet)
	if payload == nil {
		return into
	}
	if start {
		join.started = true
	}
	if !join.started {
		return into
	}
	return append(into, payload...)
}
