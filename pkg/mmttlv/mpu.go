// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// MPU payloads, and putting access units back together out of them.
//
// A media fragment unit is one access unit's worth of bytes, but it need not be
// one packet's worth. A small one shares a payload with its neighbours, behind
// a length each; a large one is split across as many payloads as it takes, and
// only the last piece says the unit is finished.
//
// The sample number a timed fragment unit carries is not a count: broadcast
// leaves it at nought, and so does the offset and the movie fragment sequence
// number. What is counted instead is the processing unit boundary: units are
// numbered from one within each MPU sequence number.
package mmttlv

import (
	"encoding/binary"
	"sort"
)

// PayloadTypeMPU is the payload type of a packet carrying media in media
// processing units.
const PayloadTypeMPU = 0x00

// What part of a processing unit a payload carries. Only FragmentMFU is media;
// the other two are the boxes that describe it.
const (
	FragmentMPUMetadata           = 0
	FragmentMovieFragmentMetadata = 1
	FragmentMFU                   = 2
)

// Which piece of a data unit a payload is, where the unit did not fit in one
// packet. A unit that did fit says FragmentationComplete.
const (
	FragmentationComplete = 0
	FragmentationFirst    = 1
	FragmentationMiddle   = 2
	FragmentationLast     = 3
)

const (
	// mpuHeader is the fixed part of an MPU payload: length, flags, counter and
	// sequence number.
	mpuHeader = 8
	// timedHeader is the header a timed media fragment unit carries in front of
	// its bytes.
	timedHeader = 14
)

// DataUnit is one data unit out of an MPU payload. What a fragmented payload
// holds is a piece of the unit's Data, not a unit of its own.
type DataUnit struct {
	MovieFragmentSequenceNumber uint32
	// SampleNumber is which sample of the movie fragment this is. Broadcasters
	// leave it at nought, so nothing may be counted from it.
	SampleNumber uint32
	// Offset is where the unit sits in its sample.
	Offset            uint32
	Priority          byte
	DependencyCounter byte
	// ItemID is which item of an untimed asset this is. Nought for a timed one.
	ItemID uint32
	Data   []byte
}

// MPUPayload is what an MMTP packet of type PayloadTypeMPU carries.
type MPUPayload struct {
	// FragmentType is FragmentMPUMetadata, FragmentMovieFragmentMetadata or
	// FragmentMFU.
	FragmentType byte
	// Timed says the asset has a timeline. Media does; a file sent alongside it
	// does not.
	Timed bool
	// FragmentationIndicator is one of the Fragmentation constants.
	FragmentationIndicator byte
	// Aggregation says several units share the payload, each behind its own
	// length. A payload cannot both aggregate and be a fragment.
	Aggregation bool
	// FragmentCounter counts down the fragments left of the unit being split.
	FragmentCounter   byte
	MPUSequenceNumber uint32
	Units             []DataUnit
}

// ParseMPU reads an MPU payload, or reports false where it is not one.
func ParseMPU(payload []byte) (MPUPayload, bool) {
	if len(payload) < mpuHeader {
		return MPUPayload{}, false
	}
	length := int(binary.BigEndian.Uint16(payload))
	if length+2 > len(payload) || length+2 < mpuHeader {
		return MPUPayload{}, false
	}
	payload = payload[:2+length]
	flags := payload[2]
	read := MPUPayload{
		FragmentType:           flags >> 4,
		Timed:                  flags&0x08 != 0,
		FragmentationIndicator: (flags >> 1) & 0x03,
		Aggregation:            flags&0x01 != 0,
		FragmentCounter:        payload[3],
		MPUSequenceNumber:      binary.BigEndian.Uint32(payload[4:]),
	}
	body := payload[mpuHeader:]
	if !read.Aggregation {
		unit, ok := parseDataUnit(body, read.Timed, read.FragmentType)
		if !ok {
			return MPUPayload{}, false
		}
		read.Units = append(read.Units, unit)
		return read, true
	}
	// Aggregated units are each behind two bytes of their own length. An
	// aggregating payload is never fragmented, so every unit in it is whole.
	for len(body) > 0 {
		if len(body) < 2 {
			return MPUPayload{}, false
		}
		length := int(binary.BigEndian.Uint16(body))
		body = body[2:]
		if length > len(body) {
			return MPUPayload{}, false
		}
		unit, ok := parseDataUnit(body[:length], read.Timed, read.FragmentType)
		if !ok {
			return MPUPayload{}, false
		}
		read.Units = append(read.Units, unit)
		body = body[length:]
	}
	return read, true
}

// parseDataUnit reads one data unit. Only a media fragment unit carries a
// header in front of its bytes; the metadata fragments are boxes all the way
// down. The header is on every piece of a split unit, not only the first.
func parseDataUnit(body []byte, timed bool, fragmentType byte) (DataUnit, bool) {
	if fragmentType != FragmentMFU {
		return DataUnit{Data: body}, true
	}
	if !timed {
		if len(body) < 4 {
			return DataUnit{}, false
		}
		return DataUnit{ItemID: binary.BigEndian.Uint32(body), Data: body[4:]}, true
	}
	if len(body) < timedHeader {
		return DataUnit{}, false
	}
	return DataUnit{
		MovieFragmentSequenceNumber: binary.BigEndian.Uint32(body),
		SampleNumber:                binary.BigEndian.Uint32(body[4:]),
		Offset:                      binary.BigEndian.Uint32(body[8:]),
		Priority:                    body[12],
		DependencyCounter:           body[13],
		Data:                        body[timedHeader:],
	}, true
}

// MostHeld is how much of one split unit is held while the rest of it
// arrives. A unit this long is a stream that has gone wrong rather than a
// picture.
const MostHeld = 64 << 20

// AccessUnit is one access unit, put back together.
type AccessUnit struct {
	PacketID          uint16
	MPUSequenceNumber uint32
	// Number is which unit of its processing unit this is, counted from one
	// here rather than taken from the stream.
	Number uint32
	Data   []byte
	// RAP says the packet that opened the unit was a random access point.
	RAP bool
	// Timed says the asset has a timeline. An untimed one is a file sent
	// alongside the media, and arrives whole.
	Timed bool
	// AfterLoss says something was thrown away since the last unit given back
	// for this identifier: a gap in the packets, a payload that would not
	// parse, or a split unit left unfinished.
	AfterLoss bool
	// SampleNumber and Offset are what the stream said, unmodified; nothing
	// here counts from them.
	SampleNumber uint32
	Offset       uint32
	// ItemID is which item of an untimed asset this is. Nought for a timed one.
	ItemID uint32
	// Fragmentation is, for an untimed unit, the payload's fragmentation
	// indicator: a file of a data broadcast is split over many packets and is
	// put together by whoever keeps the files. Nought for a timed one, which
	// is put together here.
	Fragmentation byte
}

// MPUStats is what went past, and what did not survive the trip.
type MPUStats struct {
	Payloads    uint64
	AccessUnits uint64
	// SequenceGaps counts the times the packet counter skipped, and
	// LostPackets how many packets those skips add up to.
	SequenceGaps uint64
	LostPackets  uint64
	// OutOfOrderPackets counts packets whose counter had gone back a little:
	// the same packet sent again, dropped. Not a fault of the stream.
	OutOfOrderPackets uint64
	// RestartedCounters counts the times a counter went back a long way and the
	// count started over from it. Not a fault either.
	RestartedCounters uint64
	// MetadataFragments counts payloads carrying the boxes that describe the
	// media, which are not reassembled here.
	MetadataFragments uint64
	UntimedUnits      uint64
	ParseErrors       uint64
	// FragmentErrors counts split units abandoned: a piece arrived without its
	// opening, or an opening arrived while one was still being held.
	FragmentErrors   uint64
	ScrambledPackets uint64
	// Payloads whose fields the transmission actually filled in.
	NonZeroSamples        uint64
	NonZeroOffsets        uint64
	NonZeroMovieFragments uint64
}

// MPUTally is what one packet identifier saw go past.
type MPUTally struct {
	Arrived    uint64
	Lost       uint64
	OutOfOrder uint64
	Restarted  uint64
	Scrambled  uint64
	Errors     uint64
}

// mpuAsset is what is being held for one packet identifier, which is to say
// one asset.
type mpuAsset struct {
	expect     uint32
	haveExpect bool
	mpu        uint32
	haveMPU    bool
	number     uint32
	held       []byte
	holding    bool
	heldMPU    uint32
	heldRAP    bool
	heldUnit   DataUnit
	// orphan says a piece without its opening has already been complained
	// about. It starts true: a receiver joins a stream in the middle of a
	// unit, and the pieces up to the first opening are the rest of a unit
	// already on its way, not a unit that lost its opening.
	orphan    bool
	afterLoss bool
	tally     MPUTally
}

// MPUReassembler puts access units back together out of MMTP packets. State is
// kept per packet identifier: two identifiers are two assets, their packets
// interleave, and a split unit of one says nothing about the other.
type MPUReassembler struct {
	assets   map[uint16]*mpuAsset
	stats    MPUStats
	mostHeld int
}

// NewMPUReassembler holds at most MostHeld bytes of one split unit.
func NewMPUReassembler() *MPUReassembler { return NewMPUReassemblerLimit(MostHeld) }

// NewMPUReassemblerLimit holds at most this many bytes of one split unit.
func NewMPUReassemblerLimit(mostHeld int) *MPUReassembler {
	return &MPUReassembler{assets: make(map[uint16]*mpuAsset), mostHeld: mostHeld}
}

func (reassembler *MPUReassembler) Stats() MPUStats { return reassembler.stats }

// PacketIDs lists every packet identifier a packet has arrived under, in order.
func (reassembler *MPUReassembler) PacketIDs() []uint16 {
	ids := make([]uint16, 0, len(reassembler.assets))
	for id := range reassembler.assets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Tally reports what one packet identifier saw, if anything arrived under it.
func (reassembler *MPUReassembler) Tally(packetID uint16) (MPUTally, bool) {
	asset, ok := reassembler.assets[packetID]
	if !ok {
		return MPUTally{}, false
	}
	return asset.tally, true
}

// Push takes one MMTP packet and gives back whatever it completed.
func (reassembler *MPUReassembler) Push(packet MMTPPacket) []AccessUnit {
	var out []AccessUnit
	stats := &reassembler.stats
	asset, ok := reassembler.assets[packet.PacketID]
	if !ok {
		asset = &mpuAsset{orphan: true}
		reassembler.assets[packet.PacketID] = asset
	}
	asset.tally.Arrived++
	if !asset.track(packet, stats) {
		return out
	}
	if packet.Scrambled {
		stats.ScrambledPackets++
		asset.tally.Scrambled++
		asset.dropHeld(stats)
		asset.afterLoss = true
		return out
	}
	if packet.PayloadType != PayloadTypeMPU {
		return out
	}
	read, ok := ParseMPU(packet.Payload)
	if !ok {
		stats.ParseErrors++
		asset.tally.Errors++
		asset.dropHeld(stats)
		asset.afterLoss = true
		return out
	}
	stats.Payloads++
	// The boxes that describe the media are not the media.
	if read.FragmentType != FragmentMFU {
		stats.MetadataFragments++
		return out
	}
	if !read.Timed {
		// An untimed asset is a file, and arrives whole.
		stats.UntimedUnits += uint64(len(read.Units))
		for _, unit := range read.Units {
			afterLoss := asset.afterLoss
			asset.afterLoss = false
			out = append(out, AccessUnit{
				PacketID:          packet.PacketID,
				MPUSequenceNumber: read.MPUSequenceNumber,
				Number:            asset.next(read.MPUSequenceNumber),
				Data:              append([]byte(nil), unit.Data...),
				RAP:               packet.RAP,
				AfterLoss:         afterLoss,
				SampleNumber:      unit.SampleNumber,
				Offset:            unit.Offset,
				ItemID:            unit.ItemID,
				Fragmentation:     read.FragmentationIndicator,
			})
			stats.AccessUnits++
		}
		return out
	}
	if read.Aggregation {
		// A payload cannot both aggregate and be a piece of something, so
		// anything being held is a unit that will never be finished.
		asset.dropHeld(stats)
		asset.orphan = false
		for _, unit := range read.Units {
			out = asset.emit(out, stats, packet.PacketID, read.MPUSequenceNumber, unit, append([]byte(nil), unit.Data...), packet.RAP)
		}
		return out
	}
	if len(read.Units) == 0 {
		return out
	}
	unit := read.Units[0]
	switch read.FragmentationIndicator {
	case FragmentationComplete:
		asset.dropHeld(stats)
		asset.orphan = false
		out = asset.emit(out, stats, packet.PacketID, read.MPUSequenceNumber, unit, append([]byte(nil), unit.Data...), packet.RAP)
	case FragmentationFirst:
		asset.dropHeld(stats)
		asset.orphan = false
		if len(unit.Data) > reassembler.mostHeld {
			asset.overflow(stats)
			return out
		}
		asset.holding = true
		asset.heldMPU = read.MPUSequenceNumber
		asset.heldRAP = packet.RAP
		asset.heldUnit = unit
		asset.heldUnit.Data = nil
		asset.held = append(asset.held[:0], unit.Data...)
	case FragmentationMiddle:
		if !asset.holding {
			if !asset.orphan {
				stats.FragmentErrors++
				asset.tally.Errors++
				asset.afterLoss = true
				asset.orphan = true
			}
			return out
		}
		if len(asset.held)+len(unit.Data) > reassembler.mostHeld {
			asset.overflow(stats)
			return out
		}
		asset.held = append(asset.held, unit.Data...)
	case FragmentationLast:
		if !asset.holding {
			if !asset.orphan {
				stats.FragmentErrors++
				asset.tally.Errors++
				asset.afterLoss = true
			}
			// The unit is over either way, so the next opening is not the one
			// being complained about.
			asset.orphan = false
			return out
		}
		if len(asset.held)+len(unit.Data) > reassembler.mostHeld {
			asset.overflow(stats)
			asset.orphan = false
			return out
		}
		data := append(asset.held, unit.Data...)
		asset.held = nil
		asset.holding = false
		out = asset.emit(out, stats, packet.PacketID, asset.heldMPU, asset.heldUnit, data, asset.heldRAP)
	}
	return out
}

const (
	// replayedWithin is how far back a counter may go and still be the same
	// packet sent again rather than a counter that started over.
	replayedWithin = 16
	// lostAtMost is how far forward a counter may jump and still be packets
	// that were lost rather than another sender's counter.
	lostAtMost = 4096
)

// track says whether the packet is in order, and counts what its counter says
// was lost. The subtraction carries the wrap: a difference in the top half of
// the range is a packet that has gone backwards, not four billion lost.
func (asset *mpuAsset) track(packet MMTPPacket, stats *MPUStats) bool {
	if !asset.haveExpect {
		asset.expect = packet.SequenceNumber + 1
		asset.haveExpect = true
		return true
	}
	gap := packet.SequenceNumber - asset.expect
	if gap >= 1<<31 {
		if asset.expect-packet.SequenceNumber <= replayedWithin {
			stats.OutOfOrderPackets++
			asset.tally.OutOfOrder++
			return false
		}
		stats.RestartedCounters++
		asset.tally.Restarted++
		asset.dropHeld(stats)
		asset.afterLoss = true
		asset.expect = packet.SequenceNumber + 1
		return true
	}
	if gap > 0 {
		// A leap too long to be a loss is another counter. What was held
		// belongs to the old numbering either way.
		if gap <= lostAtMost {
			stats.SequenceGaps++
			stats.LostPackets += uint64(gap)
			asset.tally.Lost += uint64(gap)
		}
		asset.dropHeld(stats)
		asset.afterLoss = true
	}
	asset.expect = packet.SequenceNumber + 1
	return true
}

// next numbers the next access unit, counting afresh whenever the processing
// unit changes.
func (asset *mpuAsset) next(mpu uint32) uint32 {
	if !asset.haveMPU || asset.mpu != mpu {
		asset.mpu = mpu
		asset.haveMPU = true
		asset.number = 0
	}
	asset.number++
	return asset.number
}

func (asset *mpuAsset) emit(out []AccessUnit, stats *MPUStats, packetID uint16, mpu uint32, unit DataUnit, data []byte, rap bool) []AccessUnit {
	if unit.SampleNumber != 0 {
		stats.NonZeroSamples++
	}
	if unit.Offset != 0 {
		stats.NonZeroOffsets++
	}
	if unit.MovieFragmentSequenceNumber != 0 {
		stats.NonZeroMovieFragments++
	}
	stats.AccessUnits++
	afterLoss := asset.afterLoss
	asset.afterLoss = false
	return append(out, AccessUnit{
		PacketID:          packetID,
		MPUSequenceNumber: mpu,
		Number:            asset.next(mpu),
		Data:              data,
		RAP:               rap,
		Timed:             true,
		AfterLoss:         afterLoss,
		SampleNumber:      unit.SampleNumber,
		Offset:            unit.Offset,
		ItemID:            unit.ItemID,
	})
}

// dropHeld throws away a split unit that will never be finished.
func (asset *mpuAsset) dropHeld(stats *MPUStats) {
	if asset.holding {
		stats.FragmentErrors++
		asset.tally.Errors++
		asset.holding = false
		asset.held = asset.held[:0]
		asset.afterLoss = true
	}
}

// overflow gives up on a split unit longer than anything a picture is, and
// ignores the rest of it until something opens a new one.
func (asset *mpuAsset) overflow(stats *MPUStats) {
	stats.FragmentErrors++
	asset.tally.Errors++
	asset.holding = false
	asset.held = nil
	asset.orphan = true
	asset.afterLoss = true
}
