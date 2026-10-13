// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Putting a multiplexing frame back together when it was sent over several
// carriers.
//
// A frame too wide for one carrier is split across several: each carries the
// group identifier they share, each sends its own frames with its own place in
// the group, and every carrier of a group holds the same streams. A receiver
// that tunes one of them gets a part of a stream, so all of them are tuned and
// their slots put back in order.
//
// That order is not "one carrier then the next". A superframe is a run of
// frames; within it the slots go round the carriers, and the frame a slot
// belongs to falls out of the arithmetic.

package isdb

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Till0196/coax/pkg/mmttlv"
	"github.com/Till0196/coax/pkg/mpegts"
)

// Carriage is what comes out of a slot.
type Carriage int

const (
	// CarriageTransport is the packets as they are, which is a transport
	// stream.
	CarriageTransport Carriage = iota
	// CarriageTLV is the TLV packets inside them, put back together.
	CarriageTLV
)

const (
	// slotsWithHeader is how many slots a frame holds counting the header as
	// the first of them, which is what the arithmetic below counts in.
	slotsWithHeader = SlotCount + 1
	// mostFrames is the most frames a superframe may hold.
	mostFrames = 15
	// heldBack is how many superframes to keep back before handing anything
	// on, so that a carrier arriving a little later than the others has
	// somewhere to land.
	heldBack = 2
	// mostWaiting is the most frames to hold for a carrier whose superframes
	// never complete.
	mostWaiting = 32
	// enoughToLineUp is how many superframes every carrier must have before
	// the carriers are lined up, and gatherAgain how many more to gather after
	// a try that settled nothing.
	enoughToLineUp = 10
	gatherAgain    = 60
	// scoredOver is how many superframes a candidate alignment is scored over.
	scoredOver = 30
	// mostUnaligned is the most superframes held while the carriers have not
	// been lined up.
	mostUnaligned = 600
	// mostAhead is how far one carrier may run ahead of the slowest once they
	// are lined up. Further than that and the slowest has stopped or the
	// alignment no longer holds, so it is worked out again.
	mostAhead = gatherAgain
	// mostCarriers is the most carriers a group may have.
	mostCarriers = 16
	// A wrong alignment scores near nothing, so these are not close calls.
	believedAbove    = 0.9
	enoughCompressed = 16
	aheadBy          = 0.02
	// enoughCounted is how many packets a transport stream must be scored
	// over.
	enoughCounted = 16
)

// bondCarrier is one carrier of the group, and what has arrived on it.
type bondCarrier struct {
	// waiting holds the frames not yet made into a superframe.
	waiting []Frame
	// superframes holds the superframes made from them, oldest first.
	superframes [][]Frame
}

// Bond is the carriers of one group, bonded.
type Bond struct {
	carriers byte
	carriage Carriage
	held     map[byte]*bondCarrier
	tlv      TLVJoin
	// offsets is how many superframes to drop from the front of each carrier
	// so that they line up, once that has been worked out.
	offsets    []int
	applied    bool
	tryAgainAt int
}

func NewBond(carriage Carriage) *Bond {
	return &Bond{carriage: carriage, held: make(map[byte]*bondCarrier)}
}

// Carriers is how many carriers the group turned out to have, once a frame has
// said.
func (bond *Bond) Carriers() byte { return bond.carriers }

// sequences is the carriers held, in order.
func (bond *Bond) sequences() []byte {
	keys := make([]byte, 0, len(bond.held))
	for key := range bond.held {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func (bond *Bond) ordered() []*bondCarrier {
	var carriers []*bondCarrier
	for _, key := range bond.sequences() {
		carriers = append(carriers, bond.held[key])
	}
	return carriers
}

// Feed takes frames from one carrier and gives back whatever the group is now
// able to say.
func (bond *Bond) Feed(frames []Frame) []byte {
	for _, frame := range frames {
		sequence := max(frame.Sequence, 1)
		carriers := max(frame.Carriers, 1)
		// A frame that names a carrier the group cannot have is a damaged one,
		// and taking it would start a carrier that never completes.
		if carriers > mostCarriers || sequence > carriers {
			continue
		}
		if bond.offsets != nil {
			// Once lined up, the group is what it was lined up as.
			if _, ok := bond.held[sequence]; sequence > bond.carriers || !ok {
				continue
			}
		} else {
			bond.carriers = max(bond.carriers, carriers)
		}
		carrier, ok := bond.held[sequence]
		if !ok {
			carrier = &bondCarrier{}
			bond.held[sequence] = carrier
		}
		carrier.waiting = append(carrier.waiting, frame)
		carrier.pack()
		if len(carrier.waiting) > mostWaiting {
			carrier.waiting = carrier.waiting[len(carrier.waiting)-mostWaiting:]
		}
	}
	if bond.offsets == nil {
		// Nothing may be handed on before the carriers are lined up, so what
		// has arrived is kept -- but not for ever.
		for _, carrier := range bond.held {
			if len(carrier.superframes) > mostUnaligned {
				carrier.superframes = carrier.superframes[len(carrier.superframes)-mostUnaligned:]
			}
		}
		bond.lineUp()
	}
	return bond.drain()
}

// lineUp works out how far apart the carriers are running. They are tuned one
// after another and arrive when they arrive, so every likely alignment is
// tried and the one that comes out framed as the stream is the right one --
// which is decidable without understanding a byte of the payload.
func (bond *Bond) lineUp() {
	if len(bond.held) < int(max(bond.carriers, 1)) {
		return
	}
	carriers := bond.ordered()
	gathered := fewestSuperframes(carriers)
	if gathered < max(enoughToLineUp, bond.tryAgainAt) {
		return
	}
	type scoredAlignment struct {
		offsets []int
		valid   float64
	}
	var scored []scoredAlignment
	for _, offsets := range alignmentCandidates(carriers) {
		built, ok := assembleBond(carriers, offsets, bond.carriage, scoredOver)
		if !ok {
			continue
		}
		valid, ok := scoreBond(built, bond.carriage)
		if !ok {
			continue
		}
		scored = append(scored, scoredAlignment{offsets, valid})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].valid > scored[j].valid })
	if len(scored) == 0 {
		// Nothing read as a stream, so more is gathered and it is tried again
		// rather than guessed at.
		bond.tryAgainAt = gathered + gatherAgain
		return
	}
	if bond.carriers > 1 && len(scored) > 1 && scored[0].valid-scored[1].valid < aheadBy {
		// Two alignments look alike, so neither has been shown to be the one.
		bond.tryAgainAt = gathered + gatherAgain
		return
	}
	bond.offsets = scored[0].offsets
	bond.applied = false
}

// drain gives the slots of every superframe all the carriers have reached, in
// the order they were sent.
func (bond *Bond) drain() []byte {
	if len(bond.held) < int(max(bond.carriers, 1)) || bond.offsets == nil {
		return nil
	}
	carriers := bond.ordered()
	if !bond.applied {
		for index, carrier := range carriers {
			if len(carrier.superframes) <= bond.offsets[index] {
				return nil
			}
		}
		for index, carrier := range carriers {
			carrier.superframes = carrier.superframes[bond.offsets[index]:]
		}
		bond.applied = true
	}
	ready := fewestSuperframes(carriers)
	furthest := 0
	for _, carrier := range carriers {
		furthest = max(furthest, len(carrier.superframes))
	}
	if furthest > ready+mostAhead {
		bond.offsets = nil
		bond.applied = false
		bond.tryAgainAt = 0
		return nil
	}
	if ready <= heldBack {
		return nil
	}
	taking := ready - heldBack
	var out []byte
	for which := range taking {
		superframes := make([][]Frame, len(carriers))
		for index, carrier := range carriers {
			superframes[index] = carrier.superframes[which]
		}
		for _, packet := range slotsInOrder(superframes) {
			if bond.carriage == CarriageTLV {
				out = bond.tlv.Take(packet, out)
			} else {
				out = append(out, packet...)
			}
		}
	}
	for _, carrier := range carriers {
		carrier.superframes = carrier.superframes[taking:]
	}
	return out
}

func fewestSuperframes(carriers []*bondCarrier) int {
	fewest := -1
	for _, carrier := range carriers {
		if fewest < 0 || len(carrier.superframes) < fewest {
			fewest = len(carrier.superframes)
		}
	}
	return max(fewest, 0)
}

// alignmentCandidates are the alignments worth trying: the carriers as they
// stand, and every way of nudging one or two of them by a superframe or two.
func alignmentCandidates(carriers []*bondCarrier) [][]int {
	fewest := fewestSuperframes(carriers)
	base := make([]int, len(carriers))
	for index, carrier := range carriers {
		base[index] = len(carrier.superframes) - fewest
	}
	seen := make(map[string]bool)
	var candidates [][]int
	many := len(carriers) > 1
	add := func(offsets []int) {
		for _, offset := range offsets {
			if offset < 0 {
				return
			}
		}
		// With more than one carrier only the distance between them is a
		// different guess: shifting them all alike tries the same alignment
		// against a different window.
		parts := make([]string, len(offsets))
		for index, offset := range offsets {
			if many {
				offset -= offsets[0]
			}
			parts[index] = strconv.Itoa(offset)
		}
		key := strings.Join(parts, ",")
		if seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, offsets)
	}
	nudged := func(changes map[int]int) []int {
		out := append([]int(nil), base...)
		for index, step := range changes {
			out[index] += step
		}
		return out
	}
	add(nudged(nil))
	for which := range carriers {
		for _, step := range []int{1, -1, 2, -2} {
			add(nudged(map[int]int{which: step}))
		}
	}
	for left := range carriers {
		for right := left + 1; right < len(carriers); right++ {
			for _, one := range []int{-1, 1} {
				for _, other := range []int{-1, 1} {
					add(nudged(map[int]int{left: one, right: other}))
				}
			}
		}
	}
	return candidates
}

// scoreBond says how well a run of bytes reads as what it is supposed to be.
// A stream put together the wrong way round is not merely worse, it is not
// framed at all.
func scoreBond(built []byte, carriage Carriage) (float64, bool) {
	if carriage == CarriageTLV {
		at, ok := mmttlv.FindSync(built)
		if !ok {
			return 0, false
		}
		seen := mmttlv.MeasureIntegrity(built, at)
		if seen.Valid < believedAbove || seen.Compressed < enoughCompressed || seen.CompressedOK != seen.Compressed {
			return 0, false
		}
		return seen.Valid, true
	}
	// A transport stream has no framing to lose -- every slot is a whole packet
	// whichever order they go in -- so what says whether the order is right is
	// the counter each stream carries.
	valid, counted := continuityOf(built)
	if counted < enoughCounted || valid < believedAbove {
		return 0, false
	}
	return valid, true
}

// continuityOf says how much of a transport stream runs in sequence: how many
// packets follow the one before them on their own identifier, out of how many
// were looked at.
func continuityOf(built []byte) (float64, int) {
	last := make(map[uint16]byte)
	counted, inOrder := 0, 0
	for at := 0; at+mpegts.PacketSize <= len(built); at += mpegts.PacketSize {
		packet := built[at : at+mpegts.PacketSize]
		if packet[0] != 0x47 {
			continue
		}
		pid := uint16(packet[1]&0x1f)<<8 | uint16(packet[2])
		counter := packet[3] & 0x0f
		// A packet with no payload does not move the counter on.
		moves := packet[3]&0x10 != 0
		before, seen := last[pid]
		last[pid] = counter
		if !seen {
			continue
		}
		counted++
		expected := before
		if moves {
			expected = (before + 1) & 0x0f
		}
		if counter == expected {
			inOrder++
		}
	}
	if counted == 0 {
		return 0, 0
	}
	return float64(inOrder) / float64(counted), counted
}

// assembleBond puts a run of superframes together under one alignment for
// scoring. Nothing is consumed: this is a question, not an answer.
func assembleBond(carriers []*bondCarrier, offsets []int, carriage Carriage, most int) ([]byte, bool) {
	available := -1
	for index, carrier := range carriers {
		left := max(len(carrier.superframes)-offsets[index], 0)
		if available < 0 || left < available {
			available = left
		}
	}
	taking := min(max(available, 0), most)
	if taking == 0 {
		return nil, false
	}
	var join TLVJoin
	var built []byte
	for which := range taking {
		superframes := make([][]Frame, len(carriers))
		for index, carrier := range carriers {
			superframes[index] = carrier.superframes[offsets[index]+which]
		}
		for _, packet := range slotsInOrder(superframes) {
			if carriage == CarriageTLV {
				built = join.Take(packet, built)
			} else {
				built = append(built, packet...)
			}
		}
	}
	return built, true
}

// pack makes superframes out of whatever runs of frames are complete: a run
// starts at frame nought and holds as many frames as that frame says the run
// has.
func (carrier *bondCarrier) pack() {
	at := 0
	for at < len(carrier.waiting) {
		of := int(carrier.waiting[at].Of)
		if carrier.waiting[at].Position != 0 || of == 0 || of > mostFrames {
			at++
			continue
		}
		if len(carrier.waiting) < at+of {
			return
		}
		whole := true
		for place, frame := range carrier.waiting[at : at+of] {
			if int(frame.Of) != of || int(frame.Position) != place {
				whole = false
				break
			}
		}
		if !whole {
			at++
			continue
		}
		run := append([]Frame(nil), carrier.waiting[at:at+of]...)
		carrier.waiting = append(carrier.waiting[:at], carrier.waiting[at+of:]...)
		carrier.superframes = append(carrier.superframes, run)
	}
}

// slotsInOrder gives the slots of one superframe from every carrier, in the
// order they were sent. The slots go round the carriers rather than down them,
// and which frame of the superframe a slot belongs to falls out of the count:
// a superframe of n frames holds n slots at each position of the round, and
// the header slot of every frame is not data.
func slotsInOrder(superframes [][]Frame) [][]byte {
	var packets [][]byte
	for step := range slotsWithHeader {
		for within := range 4 {
			for _, superframe := range superframes {
				of := len(superframe)
				if within >= of {
					continue
				}
				slot := step*of + within
				frame, inside := slot/slotsWithHeader, slot%slotsWithHeader
				if frame >= of || inside == 0 {
					continue
				}
				if packet := superframe[frame].Slots[inside-1]; packet != nil {
					packets = append(packets, packet)
				}
			}
		}
	}
	return packets
}
