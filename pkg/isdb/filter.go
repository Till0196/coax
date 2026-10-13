// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package isdb

import (
	"fmt"
	"io"
	"strings"

	"github.com/Till0196/coax/pkg/mmttlv"
	"github.com/Till0196/coax/pkg/mpegts"
)

// decideBudget is how many packets are held while working out whether the
// stream is multiplexed at all. Three frames' worth, so that a frame header
// lost to a bad moment on the cable does not settle the question.
const decideBudget = 3 * (SlotCount + 1)

// decideBytes is how much is held while the question is open.
const decideBytes = decideBudget * mpegts.PacketSize

// Filter passes on one relative stream of a multiplexing frame and drops the
// rest, as the packets go by.
//
// A satellite demodulator is given the transport stream identifier and hands
// back that stream alone, so there is nothing left to choose by the time the
// bytes arrive; a cable multiplex arrives whole. A stream that turns out not to
// be multiplexed is passed through untouched, which is what makes the flag safe
// to give in either case.
type Filter struct {
	writer   io.Writer
	streamID uint16
	report   func(string)

	// residue is the tail of a write that did not end on a packet boundary.
	residue []byte
	// pending holds the bytes as they arrived while the question is open, not
	// packets: a stream that turns out not to be a transport stream at all must
	// come out exactly as it went in.
	pending []byte
	decided bool
	pass    bool
	// failed records that the multiplex was read and did not carry what was
	// asked for; what is held back then belongs to streams nobody asked for
	// and must not be written out on the way down.
	failed bool

	header *Header
	slot   int
	want   int
	format string
	// started gates TLV output on the first packet that begins a TLV packet.
	// What comes before it is the tail of one whose head was never seen.
	started bool
	// aligned records that output has begun on a TLV packet boundary; until
	// then the bytes at hand are the end of the packet the slot was in the
	// middle of when the selection started.
	aligned bool
}

// NewFilter returns a filter writing the relative stream with this transport
// stream identifier to w.
func NewFilter(w io.Writer, streamID uint16, report func(string)) *Filter {
	if report == nil {
		report = func(string) {}
	}
	return &Filter{writer: w, streamID: streamID, report: report}
}

func (filter *Filter) Write(written []byte) (int, error) {
	if !filter.decided {
		filter.pending = append(filter.pending, written...)
		if err := filter.decide(); err != nil {
			return len(written), err
		}
		if !filter.decided {
			return len(written), nil
		}
	} else {
		filter.residue = append(filter.residue, written...)
	}
	if filter.pass {
		out := filter.residue
		filter.residue = nil
		if len(out) == 0 {
			return len(written), nil
		}
		_, err := filter.writer.Write(out)
		return len(written), err
	}
	return len(written), filter.drain()
}

// decide settles what is being looked at, once there is enough to say. A
// multiplexing frame is recognised by its header, which carries a sync word and
// a checksum over the whole packet, so finding one is proof and not finding one
// in three frames' worth of bytes is proof enough of the other answer. Nothing
// is thrown away meanwhile.
func (filter *Filter) decide() error {
	for offset := 0; offset+mpegts.PacketSize <= len(filter.pending); offset++ {
		if filter.pending[offset] != mpegts.SyncByte {
			continue
		}
		header := Parse(filter.pending[offset:offset+mpegts.PacketSize], 0)
		if header == nil {
			continue
		}
		// Frames begin at the header, so what came before it is the tail of
		// one whose beginning was never seen. follow settles the question,
		// and refuses when the multiplex does not carry the stream asked for.
		rest := append([]byte(nil), filter.pending[offset+mpegts.PacketSize:]...)
		if err := filter.follow(header); err != nil {
			return err
		}
		filter.residue = rest
		return nil
	}
	if len(filter.pending) < decideBytes {
		return nil
	}
	filter.decided, filter.pass = true, true
	filter.residue = filter.pending
	filter.pending = nil
	return nil
}

// drain takes whole packets off the front of what has arrived, keeping the head
// of an unfinished one because the caller's buffer is its own again as soon as
// Write returns.
func (filter *Filter) drain() error {
	buffer := filter.residue
	for {
		if len(buffer) == 0 {
			break
		}
		if buffer[0] != mpegts.SyncByte {
			next := indexSync(buffer[1:])
			if next < 0 {
				buffer = nil
				break
			}
			buffer = buffer[1+next:]
			continue
		}
		if len(buffer) < mpegts.PacketSize {
			break
		}
		if err := filter.push(buffer[:mpegts.PacketSize]); err != nil {
			return err
		}
		buffer = buffer[mpegts.PacketSize:]
	}
	filter.residue = append([]byte(nil), buffer...)
	return nil
}

// Close gives up on a stream that ended before it said what it was. A capture
// too short to carry a frame header is not a multiplexed one as far as anybody
// can tell, so what was held back is written out; a capture whose multiplex did
// not carry the stream keeps nothing.
func (filter *Filter) Close() error {
	if filter.decided || filter.failed || len(filter.pending) == 0 {
		return nil
	}
	filter.decided, filter.pass = true, true
	pending := filter.pending
	filter.pending = nil
	_, err := filter.writer.Write(pending)
	return err
}

func (filter *Filter) push(packet []byte) error {
	if header := Parse(packet, 0); header != nil {
		if err := filter.follow(header); err != nil {
			return err
		}
		return nil
	}
	// Slots are taken by position: a frame is a header and then exactly SlotCount
	// packets. Once the count runs out with no new header the frame is lost, and
	// nothing more is emitted until one arrives -- guessing would splice bytes
	// that were never adjacent.
	if filter.header == nil || filter.slot >= SlotCount {
		return nil
	}
	relative := int(filter.header.SlotMap[filter.slot])
	filter.slot++
	if relative != filter.want {
		return nil
	}
	if filter.format != "tlv" {
		_, err := filter.writer.Write(packet)
		return err
	}
	payload, start := TLVPayload(packet)
	if payload == nil {
		return nil
	}
	if start {
		filter.started = true
	}
	if !filter.started {
		return nil
	}
	if !filter.aligned {
		// The file written out should begin where a TLV packet begins.
		// Analysis here would find its way in regardless, but a file handed to
		// something else should not need to.
		payload = alignTLV(payload)
		if payload == nil {
			return nil
		}
		filter.aligned = true
	}
	_, err := filter.writer.Write(payload)
	return err
}

// follow takes up a frame header, and settles the question of what is being
// looked at the first time one arrives.
func (filter *Filter) follow(header *Header) error {
	filter.header, filter.slot = header, 0
	for _, relative := range header.RelativeStreams {
		if relative.StreamID != filter.streamID {
			continue
		}
		if !filter.decided {
			filter.decided, filter.pass, filter.pending = true, false, nil
			filter.report(fmt.Sprintf("selecting relative stream #%d (%s, TSID %d, ONID %d) of %d in the multiplex",
				relative.Number, relative.Format, relative.StreamID, relative.OriginalNetworkID,
				len(header.RelativeStreams)))
		}
		if filter.want != relative.Number {
			// The multiplex was rearranged under us, which the slot map is entitled to do
			// between frames. Following it means starting the TLV reassembly again.
			filter.started, filter.aligned = false, false
		}
		filter.want, filter.format = relative.Number, relative.Format
		return nil
	}
	if filter.decided && !filter.pass {
		// It was there and now is not. The frame is still followed, in case it
		// comes back, but nothing is written meanwhile.
		filter.want = 0
		return nil
	}
	filter.failed, filter.pending = true, nil
	return fmt.Errorf("the multiplex carries no stream with TSID %d; it carries %s",
		filter.streamID, describeStreams(header.RelativeStreams))
}

func describeStreams(streams []RelativeStream) string {
	if len(streams) == 0 {
		return "none"
	}
	described := make([]string, 0, len(streams))
	for _, relative := range streams {
		described = append(described, fmt.Sprintf("#%d %s TSID=%d ONID=%d",
			relative.Number, relative.Format, relative.StreamID, relative.OriginalNetworkID))
	}
	return strings.Join(described, ", ")
}

// alignTLV cuts a payload back to the first TLV packet in it, and reports
// nothing when there is none to be sure of. A sync byte alone is not enough --
// it is one byte value out of 256 -- so the type that must follow it is checked
// too, exactly as a TLV reader does.
func alignTLV(payload []byte) []byte {
	for index := 0; index+1 < len(payload); index++ {
		if payload[index] != mmttlv.SyncByte {
			continue
		}
		switch payload[index+1] {
		case mmttlv.TypeIPv4, mmttlv.TypeIPv6, mmttlv.TypeCompressedIP, mmttlv.TypeControl, mmttlv.TypeNull:
			return payload[index:]
		}
	}
	return nil
}

func indexSync(buffer []byte) int {
	for index, value := range buffer {
		if value == mpegts.SyncByte {
			return index
		}
	}
	return -1
}
