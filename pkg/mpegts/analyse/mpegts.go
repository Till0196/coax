// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package analyse reads a transport stream as it goes past and says what was
// wrong with it.
//
// It answers two questions no demodulator can. A tuner producing nothing looks
// like one producing rubbish -- a lock is the state of the chip's own loops,
// and a line with no signal can lock and hand back noise for ever -- and a
// recording that lost data looks like one fed a poor signal, which calls for
// the opposite remedy. Both come down to whether packets arrive with their sync
// byte where it belongs and in order.
package analyse

// PacketSize is the length of a transport packet, and SyncByte is the value
// every one of them begins with. Neither has ever been anything else.
const (
	PacketSize = 188
	SyncByte   = 0x47
)

// NullPID carries the stuffing a multiplexer pads a constant-rate stream with.
// Its packets have no continuity to check and are not worth counting as
// content.
const NullPID = 0x1fff

// syncConfirm is how many sync bytes have to line up, one packet apart, before
// an offset is believed to be the packet boundary. One proves nothing -- 0x47
// is an ordinary byte -- while four at exactly 188-byte spacing is 2^-24 by
// luck. A false one costs nothing: the packets after it do not line up either.
const syncConfirm = 4

// syncSpan is how many bytes an offset needs after it before it can be judged.
const syncSpan = PacketSize*(syncConfirm-1) + 1

// huntLimit caps what is held while looking for the boundary. Beyond it the
// oldest bytes cannot be part of any alignment that has not already been
// tested, so they are counted as skipped and let go.
const huntLimit = syncSpan + PacketSize

// Stats is what an Analyser saw. Every field counts since the stream was
// opened and none is reset: a caller that wants a rate takes two readings and
// subtracts.
type Stats struct {
	// Bytes is everything fed in, whether it turned out to be a packet or not.
	Bytes uint64
	// Packets is the transport packets recognised, null packets included.
	Packets uint64
	// Skipped is bytes thrown away because they were not part of a packet:
	// what came before the stream lined up, and what was passed over on the
	// way back after a break.
	Skipped uint64
	// SyncLosses is how many times the stream stopped lining up. It counts
	// breaks, not packets: a burst of rubbish is one loss, not thousands.
	SyncLosses uint64
	// Errors is packets whose transport error indicator was set, meaning the
	// demodulator handed over a packet it knew it had failed to correct. On a
	// satellite or terrestrial front end this is the signal being poor.
	Errors uint64
	// Discontinuities is packets whose continuity counter did not follow the
	// last one on the same PID, which is data that went missing between the
	// tuner and here rather than data that arrived damaged.
	Discontinuities uint64
	// Scrambled is packets still carrying their scrambling control bits, which
	// is normal for a pay channel and is here so that a stream nothing can
	// decode is distinguishable from one nothing is arriving on.
	Scrambled uint64
	// Nulls is stuffing. A multiplex that is mostly stuffing is a multiplex
	// whose services are not being received.
	Nulls uint64
}

// unknownCounter marks a PID nothing has been seen on, so that the first
// packet of a stream is not a discontinuity.
const unknownCounter = -1

// Analyser reads bytes and keeps the counts. It is not safe for concurrent
// use; a stream has one reader, which is where this belongs.
type Analyser struct {
	// synced says whether buffer is known to start on a packet boundary.
	synced bool
	// buffer holds what has not been accounted for yet: a partial packet when
	// synced, and whatever is still being searched when not.
	buffer []byte
	// counters is the last continuity counter seen on each PID.
	counters [NullPID + 1]int8
	stats    Stats
}

// New returns an Analyser that has seen nothing.
func New() *Analyser {
	analyser := &Analyser{buffer: make([]byte, 0, huntLimit+PacketSize)}
	analyser.reset()
	return analyser
}

// reset forgets every PID's continuity, which is what a break in the stream
// means: the packet after it does not follow the packet before it, and saying
// so once is the sync loss that was already counted.
func (a *Analyser) reset() {
	for pid := range a.counters {
		a.counters[pid] = unknownCounter
	}
}

// Synced reports whether the stream is currently lining up on packet
// boundaries. A stream that has never lined up has never carried a packet.
func (a *Analyser) Synced() bool { return a.synced }

// Stats is what has been seen so far.
func (a *Analyser) Stats() Stats { return a.stats }

// Feed accounts for the next bytes of the stream, in whatever sizes the caller
// read them: nothing here assumes a read boundary is a packet boundary,
// because on none of these backends is it.
func (a *Analyser) Feed(data []byte) {
	a.stats.Bytes += uint64(len(data))
	a.buffer = append(a.buffer, data...)
	for {
		if !a.synced {
			if !a.hunt() {
				return
			}
		}
		if !a.consume() {
			return
		}
	}
}

// hunt looks for the packet boundary: an offset carrying a sync byte with more
// sync bytes exactly one packet apart after it. Everything before it was not
// part of a packet and is counted as skipped.
func (a *Analyser) hunt() bool {
	for offset := 0; offset+syncSpan <= len(a.buffer); offset++ {
		if a.aligned(offset) {
			a.discard(offset)
			a.synced = true
			return true
		}
	}
	// Nothing lined up. Everything except the tail that has not been fully
	// tested yet can go: an offset needs a whole span after it to be judged,
	// so an offset without one has not been ruled out.
	if len(a.buffer) > huntLimit {
		a.discard(len(a.buffer) - huntLimit)
	}
	return false
}

// aligned reports whether a packet boundary at this offset is confirmed by the
// packets that would follow it.
func (a *Analyser) aligned(offset int) bool {
	for packet := range syncConfirm {
		at := offset + packet*PacketSize
		if at >= len(a.buffer) {
			return false
		}
		if a.buffer[at] != SyncByte {
			return false
		}
	}
	return true
}

// discard drops bytes off the front and counts them as skipped, which is what
// bytes that were not part of a packet are.
func (a *Analyser) discard(count int) {
	if count <= 0 {
		return
	}
	a.stats.Skipped += uint64(count)
	a.advance(count)
}

// advance drops bytes off the front that have already been accounted for.
func (a *Analyser) advance(count int) {
	if count <= 0 {
		return
	}
	a.buffer = append(a.buffer[:0], a.buffer[count:]...)
}

// consume takes whole packets off the front while the stream still lines up. It
// returns false when it runs out of bytes and true when it lost sync, since the
// caller then has to hunt again and there may be good packets after the break.
func (a *Analyser) consume() bool {
	for len(a.buffer) >= PacketSize {
		if a.buffer[0] != SyncByte {
			// One byte goes, so the search starts somewhere new rather than
			// testing this offset for ever, and the continuity counters go
			// with it: what follows a break does not follow what came before.
			a.stats.SyncLosses++
			a.synced = false
			a.reset()
			a.discard(1)
			return true
		}
		a.packet(a.buffer[:PacketSize])
		a.advance(PacketSize)
	}
	return false
}

// packet accounts for one whole transport packet, whose first byte is already
// known to be the sync byte.
func (a *Analyser) packet(packet []byte) {
	a.stats.Packets++

	pid := int(packet[1]&0x1f)<<8 | int(packet[2])
	if packet[1]&0x80 != 0 {
		// The demodulator is telling us it could not correct this packet. Its
		// header past the error indicator is not to be trusted, so nothing
		// else here reads it.
		a.stats.Errors++
		return
	}
	if packet[3]&0xc0 != 0 {
		a.stats.Scrambled++
	}
	if pid == NullPID {
		// Stuffing carries no continuity: a multiplexer emits null packets
		// with whatever counter it likes, and the standard says not to check.
		a.stats.Nulls++
		return
	}

	control := (packet[3] >> 4) & 0x03
	// Control 0 is reserved and 2 is adaptation field only; neither carries
	// payload, and the counter only advances for a packet that does.
	payload := control == 1 || control == 3
	adaptation := control == 2 || control == 3

	counter := int8(packet[3] & 0x0f)
	last := a.counters[pid]
	a.counters[pid] = counter

	if last == unknownCounter {
		return
	}
	if adaptation && discontinuous(packet) {
		// The multiplexer said the break was deliberate, which is what a
		// splice or a restarted encoder looks like. Counting it would report
		// the stream working as the stream failing.
		return
	}
	if !payload {
		// Without payload the counter must not have moved.
		if counter != last {
			a.stats.Discontinuities++
		}
		return
	}
	// A repeat of the last counter is a duplicated packet, which is allowed
	// and is not a break.
	if counter != (last+1)&0x0f && counter != last {
		a.stats.Discontinuities++
	}
}

// discontinuous reads the adaptation field's discontinuity indicator, which is
// the multiplexer saying that the next counter was not meant to follow the
// last one.
func discontinuous(packet []byte) bool {
	length := int(packet[4])
	if length == 0 || 5+length > PacketSize {
		return false
	}
	return packet[5]&0x80 != 0
}
