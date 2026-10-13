// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import (
	"bytes"
	"math/rand"
	"testing"
)

// packet builds one transport packet on a PID with a continuity counter and a
// payload, which is what nearly every packet in a real stream is.
func packet(pid int, counter int) []byte {
	built := make([]byte, PacketSize)
	built[0] = SyncByte
	built[1] = byte(pid >> 8 & 0x1f)
	built[2] = byte(pid & 0xff)
	built[3] = 0x10 | byte(counter&0x0f)
	for index := 4; index < PacketSize; index++ {
		built[index] = 0xff
	}
	return built
}

// pad is a run of ordinary packets on a PID nothing else in these tests uses.
//
// The analyser will not believe an offset is a packet boundary until four
// sync bytes line up behind it, so a test whose interesting packets are fewer
// than that needs something after them for the stream to be recognised at all.
func pad() []byte { return stream(0x200, 4) }

// stream builds a run of packets on one PID with the counter advancing the way
// a multiplexer advances it.
func stream(pid, count int) []byte {
	var built []byte
	for index := range count {
		built = append(built, packet(pid, index)...)
	}
	return built
}

func TestACleanStreamHasNothingWrongWithIt(t *testing.T) {
	analyser := New()
	analyser.Feed(stream(0x100, 100))

	if !analyser.Synced() {
		t.Fatal("a stream of whole packets did not line up")
	}
	stats := analyser.Stats()
	if stats.Packets != 100 {
		t.Errorf("saw %d packets, wanted 100", stats.Packets)
	}
	if stats.Skipped != 0 || stats.SyncLosses != 0 || stats.Discontinuities != 0 || stats.Errors != 0 {
		t.Errorf("a clean stream reported %+v", stats)
	}
	if stats.Bytes != 100*PacketSize {
		t.Errorf("counted %d bytes, wanted %d", stats.Bytes, 100*PacketSize)
	}
}

// A read boundary is not a packet boundary on any of these backends, so the
// analyser has to carry a partial packet from one call to the next.
func TestPacketsSplitAcrossReadsAreStillPackets(t *testing.T) {
	data := stream(0x100, 50)
	analyser := New()
	for offset := 0; offset < len(data); offset += 7 {
		end := min(offset+7, len(data))
		analyser.Feed(data[offset:end])
	}
	if got := analyser.Stats().Packets; got != 50 {
		t.Errorf("saw %d packets across ragged reads, wanted 50", got)
	}
	if got := analyser.Stats().SyncLosses; got != 0 {
		t.Errorf("ragged reads reported %d sync losses", got)
	}
}

// A stream never starts on a boundary: the driver hands over whatever was in
// its ring buffer when the read arrived.
func TestAStreamThatStartsMidPacketFindsTheBoundary(t *testing.T) {
	data := append([]byte{0x00, 0x11, 0x47, 0x22}, stream(0x100, 20)...)
	analyser := New()
	analyser.Feed(data)

	if !analyser.Synced() {
		t.Fatal("a stream with rubbish in front of it did not line up")
	}
	stats := analyser.Stats()
	if stats.Packets != 20 {
		t.Errorf("saw %d packets, wanted 20", stats.Packets)
	}
	if stats.Skipped != 4 {
		t.Errorf("skipped %d bytes, wanted the 4 in front", stats.Skipped)
	}
}

// This is the case the whole package is for: a tuner on a dead line locks and
// hands back noise. Noise is full of 0x47 and none of it is 188 apart.
func TestNoiseNeverLinesUp(t *testing.T) {
	noise := make([]byte, 512*1024)
	source := rand.New(rand.NewSource(1))
	source.Read(noise)

	analyser := New()
	analyser.Feed(noise)

	if analyser.Synced() {
		t.Error("noise was taken for a transport stream")
	}
	if got := analyser.Stats().Packets; got != 0 {
		t.Errorf("found %d packets in noise", got)
	}
}

func TestABreakInTheMiddleIsOneSyncLoss(t *testing.T) {
	data := stream(0x100, 20)
	data = append(data, bytes.Repeat([]byte{0xa5}, 1000)...)
	data = append(data, stream(0x100, 20)...)

	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.SyncLosses != 1 {
		t.Errorf("a single burst of rubbish was reported as %d sync losses", stats.SyncLosses)
	}
	if stats.Packets != 40 {
		t.Errorf("saw %d packets around the break, wanted 40", stats.Packets)
	}
	if !analyser.Synced() {
		t.Error("the stream did not come back after the break")
	}
}

func TestAMissingPacketIsADiscontinuity(t *testing.T) {
	var data []byte
	for counter := range 10 {
		if counter == 4 {
			continue // the packet that went missing
		}
		data = append(data, packet(0x100, counter)...)
	}
	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 1 {
		t.Errorf("a missing packet gave %d discontinuities, wanted 1", got)
	}
}

func TestADuplicatedPacketIsNotADiscontinuity(t *testing.T) {
	data := append(packet(0x100, 0), packet(0x100, 1)...)
	data = append(data, packet(0x100, 1)...) // the standard allows one repeat
	data = append(data, packet(0x100, 2)...)

	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("a duplicated packet gave %d discontinuities", got)
	}
}

func TestStuffingIsCountedButNotCheckedForContinuity(t *testing.T) {
	var data []byte
	for range 8 {
		// Null packets whose counters go nowhere, which is allowed.
		data = append(data, packet(NullPID, 0)...)
	}
	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.Nulls != 8 {
		t.Errorf("counted %d null packets, wanted 8", stats.Nulls)
	}
	if stats.Discontinuities != 0 {
		t.Errorf("stuffing was checked for continuity: %d discontinuities", stats.Discontinuities)
	}
}

func TestTheErrorIndicatorIsCountedAndTheHeaderIsNotTrusted(t *testing.T) {
	data := packet(0x100, 0)
	broken := packet(0x100, 9) // a counter that would be a break if believed
	broken[1] |= 0x80
	data = append(data, broken...)
	data = append(data, packet(0x100, 1)...)
	data = append(data, pad()...)

	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.Errors != 1 {
		t.Errorf("counted %d error packets, wanted 1", stats.Errors)
	}
	if stats.Discontinuities != 0 {
		t.Errorf("a packet marked uncorrectable was believed: %d discontinuities", stats.Discontinuities)
	}
}

func TestADeliberateDiscontinuityIsNotCounted(t *testing.T) {
	data := packet(0x100, 0)
	spliced := packet(0x100, 9)
	spliced[3] = 0x30 | 0x09 // adaptation field and payload
	spliced[4] = 1           // adaptation field length
	spliced[5] = 0x80        // discontinuity indicator
	data = append(data, spliced...)
	data = append(data, pad()...)

	analyser := New()
	analyser.Feed(data)

	if !analyser.Synced() {
		t.Fatal("the test stream did not line up, so nothing was checked")
	}
	if got := analyser.Stats().Discontinuities; got != 0 {
		t.Errorf("a flagged discontinuity was counted %d times", got)
	}
}

// The same packets without the indicator must be counted, or the test above
// would pass on an analyser that never counted anything.
func TestAnUnflaggedJumpInTheCounterIsCounted(t *testing.T) {
	data := packet(0x100, 0)
	data = append(data, packet(0x100, 9)...)
	data = append(data, pad()...)

	analyser := New()
	analyser.Feed(data)

	if got := analyser.Stats().Discontinuities; got != 1 {
		t.Errorf("a jump in the counter gave %d discontinuities, wanted 1", got)
	}
}

func TestScramblingIsCountedRatherThanTreatedAsDamage(t *testing.T) {
	scrambled := packet(0x100, 0)
	scrambled[3] |= 0x80 // transport_scrambling_control = 10
	data := append(scrambled, packet(0x100, 1)...)
	data = append(data, pad()...)

	analyser := New()
	analyser.Feed(data)

	stats := analyser.Stats()
	if stats.Scrambled != 1 {
		t.Errorf("counted %d scrambled packets, wanted 1", stats.Scrambled)
	}
	if stats.Errors != 0 || stats.Discontinuities != 0 {
		t.Errorf("a pay channel looked like a fault: %+v", stats)
	}
}

// The hunt must not grow without bound on a stream that never lines up.
func TestHuntingDoesNotHoardBytes(t *testing.T) {
	analyser := New()
	for range 100 {
		analyser.Feed(bytes.Repeat([]byte{0x47}, 4096))
	}
	if len(analyser.buffer) > huntLimit+PacketSize {
		t.Errorf("held %d bytes while hunting", len(analyser.buffer))
	}
}
