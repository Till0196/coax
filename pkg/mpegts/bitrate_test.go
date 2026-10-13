// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mpegts

import (
	"math"
	"testing"
)

// clocked builds a packet carrying a programme clock reference.
func clocked(pid uint16, value int64) []byte {
	packet := make([]byte, PacketSize)
	packet[0] = 0x47
	packet[1] = byte(pid >> 8)
	packet[2] = byte(pid)
	packet[3] = 0x30 // adaptation field and payload
	packet[4] = 7    // the adaptation field is the flags byte and six of clock
	packet[5] = 0x10 // PCR present
	base, extension := value/300, value%300
	packet[6] = byte(base >> 25)
	packet[7] = byte(base >> 17)
	packet[8] = byte(base >> 9)
	packet[9] = byte(base >> 1)
	packet[10] = byte(base<<7) | 0x7e | byte(extension>>8)
	packet[11] = byte(extension)
	return packet
}

// plain is a packet with no clock in it.
func plain(pid uint16) []byte {
	packet := make([]byte, PacketSize)
	packet[0] = 0x47
	packet[1] = byte(pid >> 8)
	packet[2] = byte(pid)
	packet[3] = 0x10
	return packet
}

// TestBitrateReadsTheStreamsOwnClock pins that the rate comes from the
// programme clock and not from how many packets there happen to be.
func TestBitrateReadsTheStreamsOwnClock(t *testing.T) {
	// One second of clock across a thousand packets is 188 * 1000 * 8 bits in
	// a second, which is 1.504 Mbps.
	packets := [][]byte{clocked(0x100, 0)}
	for i := 0; i < 999; i++ {
		packets = append(packets, plain(0x200))
	}
	packets = append(packets, clocked(0x100, pcrClockHz))

	rate, ok := Bitrate(packets)
	if !ok {
		t.Fatal("a stream with two references an entire second apart was not measured")
	}
	if want := 188.0 * 1000 * 8; math.Abs(rate-want) > 1 {
		t.Errorf("rate is %.0f bit/s, want %.0f", rate, want)
	}
}

// TestBitrateSurvivesTheClockRunningOut covers the reference wrapping, which a
// long capture of a stream that has been running for a day will meet.
func TestBitrateSurvivesTheClockRunningOut(t *testing.T) {
	packets := [][]byte{clocked(0x100, pcrWrap-pcrClockHz/2)}
	for i := 0; i < 499; i++ {
		packets = append(packets, plain(0x200))
	}
	packets = append(packets, clocked(0x100, pcrClockHz/2))

	rate, ok := Bitrate(packets)
	if !ok {
		t.Fatal("a stream whose clock wrapped was not measured")
	}
	if want := 188.0 * 500 * 8; math.Abs(rate-want) > 1 {
		t.Errorf("rate is %.0f bit/s, want %.0f: the wrap was read as time going backwards", rate, want)
	}
}

// TestBitrateRefusesTooLittleToDivideBy covers what a scan of a channel that
// barely came in has: a handful of packets, and no honest answer in them.
func TestBitrateRefusesTooLittleToDivideBy(t *testing.T) {
	for _, want := range []struct {
		name    string
		packets [][]byte
	}{
		{"nothing at all", nil},
		{"no clock anywhere", [][]byte{plain(0x100), plain(0x100)}},
		{"a single reference", [][]byte{clocked(0x100, 0), plain(0x200)}},
		{"two references too close together", [][]byte{clocked(0x100, 0), clocked(0x100, pcrClockHz/100)}},
	} {
		if _, ok := Bitrate(want.packets); ok {
			t.Errorf("%s was measured anyway", want.name)
		}
	}
}

// TestBitrateCountsEveryPacketBetweenTheReferences pins that what is measured
// is the multiplex and not the one programme the clock belongs to.
func TestBitrateCountsEveryPacketBetweenTheReferences(t *testing.T) {
	// The clock PID contributes two packets; the other PID contributes the
	// rest, and all of them are the multiplex.
	sparse := [][]byte{clocked(0x100, 0), clocked(0x100, pcrClockHz)}
	dense := [][]byte{clocked(0x100, 0)}
	for i := 0; i < 99; i++ {
		dense = append(dense, plain(0x200))
	}
	dense = append(dense, clocked(0x100, pcrClockHz))

	thin, ok := Bitrate(sparse)
	if !ok {
		t.Fatal("the sparse stream was not measured")
	}
	fat, ok := Bitrate(dense)
	if !ok {
		t.Fatal("the dense stream was not measured")
	}
	if fat <= thin {
		t.Errorf("the same clock over more packets measured %.0f, not more than %.0f", fat, thin)
	}
}
