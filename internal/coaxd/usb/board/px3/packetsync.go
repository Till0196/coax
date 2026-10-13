// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

// The ASV5211 sends each transport packet with bit 7 of its sync byte set,
// 0xc7 where 0x47 belongs. It is cleared on every 188th byte of what arrives
// before anything else looks at the stream, since a stream left as it comes is
// refused by every reader of transport packets. Nothing else in the packet is
// touched.

const (
	packetSize = 188
	syncByte   = 0x47
	// syncRun is how many packets in a row have to line up before a position is
	// taken for the packet boundary.
	syncRun = 3
)

// packetSync tracks where the packet boundaries fall across reads, which need
// not be whole packets.
type packetSync struct {
	locked bool
	// into is how far into a packet the next byte to arrive is.
	into int
	// seen is the last bytes to arrive while the boundaries are being looked for,
	// so that reads too short to hold syncRun packets still find them.
	seen []byte
}

// restore clears bit 7 of every sync byte in data, finding the boundaries
// first when they are not known and looking for them again when one is not
// where it should be.
func (sync *packetSync) restore(data []byte) {
	for len(data) > 0 {
		if !sync.locked {
			sync.seen = append(sync.seen, data...)
			if keep := syncRun * packetSize; len(sync.seen) > keep {
				sync.seen = sync.seen[len(sync.seen)-keep:]
			}
			start := findBoundary(sync.seen)
			if start < 0 {
				return
			}
			// The first boundary at or after the start of data, counted in packets
			// from the one found.
			first := start - (len(sync.seen) - len(data))
			first = (first%packetSize + packetSize) % packetSize
			sync.locked, sync.into, sync.seen = true, (packetSize-first)%packetSize, nil
		}
		first := (packetSize - sync.into) % packetSize
		for at := first; at < len(data); at += packetSize {
			if data[at]&0x7f != syncByte {
				sync.locked = false
				data = data[at+1:]
				break
			}
			data[at] &= 0x7f
		}
		if !sync.locked {
			continue
		}
		sync.into = (sync.into + len(data)) % packetSize
		return
	}
}

// findBoundary finds the first position where syncRun packets line up, or -1.
func findBoundary(data []byte) int {
	for start := 0; start+(syncRun-1)*packetSize < len(data); start++ {
		lined := true
		for packet := 0; packet < syncRun; packet++ {
			if data[start+packet*packetSize]&0x7f != syncByte {
				lined = false
				break
			}
		}
		if lined {
			return start
		}
	}
	return -1
}
