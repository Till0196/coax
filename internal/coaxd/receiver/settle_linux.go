// Copyright (c) 2026 waya0125; 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"context"
	"errors"
	"time"
)

// settleFrontendName is the frontend whose stream is held back at the start:
// the ISDB-T side of a TC90522, which the PT3 and boards like it carry.
//
// The kernel's tc90522 driver reports a terrestrial lock as soon as any of
// the top three bits of register 0x96 is set, without looking at the lock bit
// in 0x80. The demodulator has not settled by then, and for about the next
// 100 to 160 ms it hands over packets with the transport error indicator set,
// which a recording counts as drops. Waiting for the lock bit instead fixes
// the packets but reports the lock more than a second later, so the damaged
// head is dropped here rather than waited out. The ISDB-S side and other
// demodulators do not do this and are left alone.
const settleFrontendName = "Toshiba TC90522 ISDB-T module"

const (
	// settleCleanPackets is how long the stream must go without a flagged
	// packet before it is taken to have settled: about 180 ms of an ISDB-T
	// multiplex, against a damaged head measured at up to 1,805 packets.
	settleCleanPackets = 2000
	// settleProbeBytes is how far into the stream transport packets must have
	// lined up. Anything else -- noise from a lead nothing feeds, or bytes that
	// are not transport packets at all -- is handed over at once, as it would
	// have been, and left for the sync check to judge.
	settleProbeBytes = 64 * 1024
	tsPacketSize     = 188
	tsSyncByte       = 0x47
	// tsAlignPackets is how many sync bytes in a row mark a packet boundary,
	// as the sync check asks for: two can turn up in noise, four do not.
	tsAlignPackets = 4
)

// settleLimit is how long a stream that does not settle is held, after which
// all of it is handed over: a channel that is weak from the start flags
// packets for as long as it runs, and losing its front would be worse than the
// drops. It is a variable only so a test can shorten it.
var settleLimit = time.Second

// settlingStream holds the front of a stream back until the demodulator has
// gone a while without flagging a packet, then starts just after the last
// flagged one. Nothing clean is thrown away; only the damaged head is.
type settlingStream struct {
	Stream
	buffer []byte
	// data is everything read while settling. Once settled it is what is
	// still to be handed out, and the stream is read directly after it.
	data     []byte
	settled  bool
	deadline time.Time
	// err is what ended the stream while it was being held, returned once
	// what was read before it has been handed out.
	err error
	// scanned is where the next packet to look at begins, or -1 before the
	// packets have lined up. keepFrom is where the stream would start if it
	// settled now, and firstAligned where it starts if it does not.
	scanned, keepFrom, firstAligned, clean int
}

func newSettlingStream(stream Stream) *settlingStream {
	return &settlingStream{Stream: stream, buffer: make([]byte, 188*1024), scanned: -1}
}

func (stream *settlingStream) Read(ctx context.Context, buffer []byte) (int, error) {
	if !stream.settled {
		if err := stream.settle(ctx); err != nil {
			return 0, err
		}
		if !stream.settled {
			return 0, ErrWouldBlock
		}
	}
	if len(stream.data) > 0 {
		read := copy(buffer, stream.data)
		stream.data = stream.data[read:]
		if len(stream.data) == 0 {
			stream.data = nil
		}
		return read, nil
	}
	if stream.err != nil {
		return 0, stream.err
	}
	return stream.Stream.Read(ctx, buffer)
}

// settle reads once and decides, if it can, where the stream starts.
func (stream *settlingStream) settle(ctx context.Context) error {
	if stream.deadline.IsZero() {
		stream.deadline = time.Now().Add(settleLimit)
	}
	read, err := stream.Stream.Read(ctx, stream.buffer)
	stream.data = append(stream.data, stream.buffer[:read]...)
	if err != nil && !errors.Is(err, ErrWouldBlock) {
		if ctx.Err() != nil {
			return err
		}
		stream.err = err
		stream.handOverWhole()
		return nil
	}
	if stream.scan() {
		return nil
	}
	if stream.scanned < 0 && len(stream.data) >= settleProbeBytes {
		stream.handOverWhole()
		return nil
	}
	if !time.Now().Before(stream.deadline) {
		stream.handOverWhole()
	}
	return nil
}

// scan looks at the packets read so far, and reports whether the stream has
// settled.
func (stream *settlingStream) scan() bool {
	data := stream.data
	if stream.scanned < 0 {
		at := alignTS(data, 0)
		if at < 0 {
			return false
		}
		stream.scanned, stream.keepFrom, stream.firstAligned = at, at, at
	}
	for stream.scanned+tsPacketSize <= len(data) {
		if data[stream.scanned] != tsSyncByte {
			// Lost the boundary: start again from where it lines up next,
			// counting the gap as damage.
			at := alignTS(data, stream.scanned+1)
			if at < 0 {
				return false
			}
			stream.scanned, stream.keepFrom, stream.clean = at, at, 0
			continue
		}
		if data[stream.scanned+1]&0x80 != 0 {
			stream.clean = 0
			stream.keepFrom = stream.scanned + tsPacketSize
		} else {
			stream.clean++
		}
		stream.scanned += tsPacketSize
		if stream.clean >= settleCleanPackets {
			stream.data = data[stream.keepFrom:]
			stream.settled = true
			return true
		}
	}
	return false
}

// handOverWhole gives up waiting and hands over everything read, from the
// first packet boundary if one was found.
func (stream *settlingStream) handOverWhole() {
	if stream.scanned >= 0 {
		stream.data = stream.data[stream.firstAligned:]
	}
	stream.settled = true
}

// alignTS finds the first offset at or after from where tsAlignPackets
// packets in a row begin with the sync byte, or -1 when there is not enough to
// say.
func alignTS(data []byte, from int) int {
	for at := from; at+(tsAlignPackets-1)*tsPacketSize < len(data); at++ {
		lined := true
		for packet := 0; packet < tsAlignPackets; packet++ {
			if data[at+packet*tsPacketSize] != tsSyncByte {
				lined = false
				break
			}
		}
		if lined {
			return at
		}
	}
	return -1
}
