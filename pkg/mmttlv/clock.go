// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mmttlv

// TimestampHz is how finely the MMTP transmission clock counts: NTP short
// format, whose lower 16 bits are a fraction of a second.
const TimestampHz = 1 << 16

// minimumSpan is the shortest stretch of clock worth dividing by. Shorter than
// this and the answer says more about where the two packets happened to fall
// than about the rate of the stream.
const minimumSpan = TimestampHz / 10

// Clock works out how fast a TLV stream runs from the transmission times its
// MMTP packets carry, the way a transport stream's rate comes from its
// programme clock: two readings far enough apart, and the bytes between them.
//
// Readings are kept per packet identifier, because two identifiers are two
// assets whose senders need not agree to the millisecond. The bytes counted
// between two readings are all the bytes -- padding and signalling included --
// since the rate wanted is the whole stream's.
type Clock struct {
	readings map[uint16]*reading
}

type reading struct {
	firstOffset, lastOffset int
	first, last             uint32
	count                   int
}

// Add records that an MMTP packet of this identifier began at this many bytes
// into the stream, saying this.
func (clock *Clock) Add(packetID uint16, offset int, timestamp uint32) {
	if clock.readings == nil {
		clock.readings = make(map[uint16]*reading)
	}
	seen := clock.readings[packetID]
	if seen == nil {
		clock.readings[packetID] = &reading{
			firstOffset: offset, lastOffset: offset,
			first: timestamp, last: timestamp, count: 1,
		}
		return
	}
	seen.lastOffset, seen.last, seen.count = offset, timestamp, seen.count+1
}

// Bitrate reports bits per second, and whether there was enough clock to say.
func (clock *Clock) Bitrate() (float64, bool) {
	// The identifier read most often, the lowest of those read equally often:
	// the answer must not depend on the order a map happens to give.
	var best *reading
	bestID := uint16(0)
	for id, seen := range clock.readings {
		if best == nil || seen.count > best.count || (seen.count == best.count && id < bestID) {
			best, bestID = seen, id
		}
	}
	if best == nil || best.count < 2 {
		return 0, false
	}
	// Unsigned subtraction carries the wrap for free: the clock runs out every
	// 65536 seconds, a little over eighteen hours, and starts again at zero.
	ticks := best.last - best.first
	if ticks < minimumSpan {
		return 0, false
	}
	bytes := float64(best.lastOffset - best.firstOffset)
	if bytes <= 0 {
		return 0, false
	}
	return bytes * 8 * TimestampHz / float64(ticks), true
}
