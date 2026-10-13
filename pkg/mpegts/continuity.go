// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

// Every packet of a transport stream carries a counter that steps by one for
// each packet of its stream, so what the counter skips is what the receiver did
// not get. It is the only measure of a signal that every tuner gives.

// NullPID carries nothing: the stuffing a multiplex is padded to its rate with.
// Its counter does not have to step, and a run of it is not a loss.
const NullPID = 0x1fff

// continuityConfirm is how many sync bytes have to line up, one packet apart,
// before an offset is believed to be the packet boundary. One proves nothing --
// 0x47 is an ordinary byte.
const continuityConfirm = 4

// continuityEnough is how many packets have to go past before the share lost
// says anything: a second of a television multiplex is about twenty thousand
// packets, and this is a small fraction of a second.
const continuityEnough = 2000

// Continuity counts what arrived and what did not, over bytes handed in as they
// come. The bytes need not be whole packets or start at one: a receiver's reads
// are as long as its buffer and begin wherever the last one ended.
//
// The packet boundary is believed only once several packets line up, and once
// it is lost every stream's counter is forgotten: what follows a break does not
// follow what came before it, and the break is counted once.
type Continuity struct {
	held   []byte
	synced bool
	// broken says the current search for the boundary has already been
	// counted as a break.
	broken bool
	// last is the counter last seen on each stream, or -1 for none yet.
	last    [NullPID + 1]int8
	primed  bool
	packets uint64
	errors  uint64
	breaks  uint64
}

func (continuity *Continuity) forget() {
	for pid := range continuity.last {
		continuity.last[pid] = -1
	}
	continuity.primed = true
}

// Packets is how many packets went past, stuffing aside.
func (continuity *Continuity) Packets() uint64 { return continuity.packets }

// Errors is how many packets were missed: what the counters skipped, what the
// transmitter marked as received in error, and each place the reader lost the
// packet boundary.
func (continuity *Continuity) Errors() uint64 { return continuity.errors + continuity.breaks }

// Losses is the share of the carrier that did not arrive. It reports false
// where too few packets went past to say: a handful of packets says nothing
// about a signal.
func (continuity *Continuity) Losses() (float32, bool) {
	if continuity.packets < continuityEnough {
		return 0, false
	}
	return float32(continuity.Errors()) / float32(continuity.packets), true
}

// Feed takes the next bytes of the stream.
func (continuity *Continuity) Feed(data []byte) {
	if !continuity.primed {
		continuity.forget()
	}
	continuity.held = append(continuity.held, data...)
	at := 0
	for {
		if !continuity.synced {
			found := -1
			for offset := at; offset+PacketSize*(continuityConfirm-1) < len(continuity.held); offset++ {
				aligned := true
				for index := range continuityConfirm {
					if continuity.held[offset+index*PacketSize] != SyncByte {
						aligned = false
						break
					}
				}
				if aligned {
					found = offset
					break
				}
			}
			if found < 0 {
				// Keep only the tail that has not been fully tested.
				keep := PacketSize*(continuityConfirm-1) + 1
				if len(continuity.held)-at > keep {
					continuity.lostBoundary()
					at = len(continuity.held) - keep
				}
				break
			}
			if found > at {
				continuity.lostBoundary()
			}
			at = found
			continuity.synced, continuity.broken = true, false
		}
		for at+PacketSize <= len(continuity.held) && continuity.held[at] == SyncByte {
			continuity.packet(continuity.held[at : at+PacketSize])
			at += PacketSize
		}
		if at+PacketSize > len(continuity.held) {
			break
		}
		// The boundary is lost: the counters go with it.
		continuity.synced = false
		continuity.lostBoundary()
		continuity.forget()
		at++
	}
	continuity.held = append(continuity.held[:0], continuity.held[at:]...)
}

// lostBoundary counts one break per search for the boundary.
func (continuity *Continuity) lostBoundary() {
	if !continuity.broken {
		continuity.breaks++
		continuity.broken = true
	}
}

// packet counts one whole packet, which begins with a sync byte.
func (continuity *Continuity) packet(packet []byte) {
	pid := int(packet[1]&0x1f)<<8 | int(packet[2])
	if pid == NullPID {
		return
	}
	continuity.packets++
	// The transmitter saying the packet is known to be damaged. Its counter
	// is not to be trusted either, so it is taken to have filled the next
	// slot: the packet after it is not counted as missing the same one again.
	if packet[1]&0x80 != 0 {
		continuity.errors++
		if last := continuity.last[pid]; last >= 0 {
			continuity.last[pid] = (last + 1) & 0x0f
		}
		return
	}
	control := packet[3]
	counter := int8(control & 0x0f)
	// A transmitter that means to break the count says so in the adaptation
	// field, and that is not a loss.
	deliberate := control&0x20 != 0 && packet[4] > 0 && packet[5]&0x80 != 0
	last := continuity.last[pid]
	continuity.last[pid] = counter
	if last < 0 || deliberate || counter == last {
		// The same packet sent twice repeats the counter, and a packet with
		// no payload does not step it. Neither is a loss.
		return
	}
	expected := (last + 1) & 0x0f
	if counter == expected {
		return
	}
	// How far it jumped is how many packets of this stream were missed -- as
	// far as four bits can say.
	continuity.errors += uint64((counter - expected) & 0x0f)
}
