// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/chardev"
)

func TestChardevSystemOfTakesTheSpellingsTheFilesUse(t *testing.T) {
	for _, name := range []string{"ISDBT", "ISDB-T", "isdb-t"} {
		if system, err := chardevSystemOf(name); err != nil || system != chardev.SystemISDBT {
			t.Errorf("chardevSystemOf(%q) = %v, %v", name, system, err)
		}
	}
	for _, name := range []string{"ISDBS", "ISDB-S"} {
		if system, err := chardevSystemOf(name); err != nil || system != chardev.SystemISDBS {
			t.Errorf("chardevSystemOf(%q) = %v, %v", name, system, err)
		}
	}
	// The ptx interface has no way to ask for anything else, even on a board
	// whose demodulator could receive it.
	for _, name := range []string{"DVBT", "DVB-C/ANNEX_A", "ISDB-C", ""} {
		if _, err := chardevSystemOf(name); err == nil {
			t.Errorf("chardevSystemOf(%q) should have been refused", name)
		}
	}
}

// TestLNBLevelDefaultsToNoSupply checks that a satellite channel saying nothing
// about the supply is given none on every board these drivers serve, and given
// it explicitly rather than left: the node may have been handed over with the
// line still powered by whoever had it before.
//
// See pt3Driver.SupplyForUnset for why off is the default: an unfed line
// produces a stream that does not line up, which the sync check catches, while
// a line fed twice is the failure that breaks equipment.
func TestLNBLevelDefaultsToNoSupply(t *testing.T) {
	for _, driver := range []chardevDriver{px4Driver{}, pt3Driver{}, pt1Driver{}} {
		if level, asked, err := lnbLevel(driver, nil); err != nil || !asked || level != chardev.LNBOff {
			t.Errorf("%T with nothing said = %v, %t, %v; want the supply off and said", driver, level, asked, err)
		}
	}
	// What a channel does say is taken the same way whichever board it is.
	for _, one := range []struct {
		value string
		level int
	}{
		{"0", chardev.LNBOff},
		{"OFF", chardev.LNBOff},
		{"11", chardev.LNB11V},
		{"13", chardev.LNB11V},
		{"15", chardev.LNB15V},
		{"18", chardev.LNB15V},
	} {
		for _, driver := range []chardevDriver{px4Driver{}, pt3Driver{}} {
			level, asked, err := lnbLevel(driver, map[string]string{"VOLTAGE": one.value})
			if err != nil || !asked || level != one.level {
				t.Errorf("%T with VOLTAGE=%q = %v, %t, %v; want %v", driver, one.value, level, asked, err, one.level)
			}
		}
	}
	if _, _, err := lnbLevel(px4Driver{}, map[string]string{"VOLTAGE": "24"}); err == nil {
		t.Error("a voltage the driver cannot produce should be refused")
	}
}

// A frequency outside the channel plan is the same situation as a frequency a
// tuner cannot reach, and is reported as that: a scan of a band this hardware
// covers part of should say so per channel rather than fault.
//
// 800 MHz rather than a VHF one: the VHF stretches the driver has no number
// for are reached through the offset field now, so what is left outside is
// what is outside the plan.
func TestOutOfRangeIsWhatAMissingChannelNumberBecomes(t *testing.T) {
	_, _, err := chardev.TerrestrialChannel(800_000)
	if err == nil {
		t.Fatal("expected a frequency outside the plan to be refused")
	}
	if !errors.Is(outOfRange(err), ErrOutOfRange) {
		t.Errorf("outOfRange(%v) does not report ErrOutOfRange", err)
	}
	// Anything else passes through as it was.
	other := errors.New("something else")
	if !errors.Is(outOfRange(other), other) {
		t.Error("outOfRange should leave an unrelated error alone")
	}
}

// The stream hands out what the reader has taken in whatever size the caller
// asks for, keeps what did not fit, and says it would block rather than
// waiting -- the caller polls, and a stream that waited here would stop it
// noticing that its client had gone.
func TestChardevStreamHandsOverWhatTheReaderTook(t *testing.T) {
	stream := &chardevStream{
		chunks:   make(chan []byte, 2),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	buffer := make([]byte, 3)

	if _, err := stream.Read(context.Background(), buffer); !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("an empty stream should report it would block, got %v", err)
	}

	stream.chunks <- []byte{1, 2, 3, 4, 5}
	read, err := stream.Read(context.Background(), buffer)
	if err != nil || read != 3 || buffer[0] != 1 || buffer[2] != 3 {
		t.Fatalf("read %d bytes %v, err %v", read, buffer[:read], err)
	}
	// What did not fit is kept rather than dropped, and comes out next.
	read, err = stream.Read(context.Background(), buffer)
	if err != nil || read != 2 || buffer[0] != 4 || buffer[1] != 5 {
		t.Fatalf("read %d bytes %v, err %v", read, buffer[:read], err)
	}
	if _, err := stream.Read(context.Background(), buffer); !errors.Is(err, ErrWouldBlock) {
		t.Fatalf("a drained stream should report it would block, got %v", err)
	}
}

// A chunk that has been handed over in full goes back on the spare list, so
// that a live tuner is not allocating a buffer every few milliseconds. The
// reason that matters is not the allocation: it is that collecting the garbage
// stops the world, and stopping the world is what sends the preemption signals
// these drivers cannot survive.
func TestChardevStreamPutsAFinishedChunkBack(t *testing.T) {
	stream := &chardevStream{
		chunks:   make(chan []byte, 2),
		spare:    make(chan []byte, 2),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	chunk := make([]byte, chardevChunk)
	chunk[0], chunk[1] = 7, 8
	stream.chunks <- chunk[:2]

	buffer := make([]byte, 1)
	if read, err := stream.Read(context.Background(), buffer); err != nil || read != 1 {
		t.Fatalf("read %d bytes, err %v", read, err)
	}
	// Half of it is still to come, so it is not free yet.
	if len(stream.spare) != 0 {
		t.Fatalf("a chunk that is still being handed over should not be back on the spare list")
	}
	if read, err := stream.Read(context.Background(), buffer); err != nil || read != 1 {
		t.Fatalf("read %d bytes, err %v", read, err)
	}
	if len(stream.spare) != 1 {
		t.Fatalf("a chunk that has been handed over in full should be back on the spare list")
	}

	// And the reader picks that one up rather than making another, at its
	// full length rather than at the length it was handed over as.
	taken := stream.takeBuffer()
	if len(taken) != chardevChunk || &taken[0] != &chunk[0] {
		t.Fatalf("the reader took a %d byte buffer, and not the one that came back", len(taken))
	}
	if len(stream.spare) != 0 {
		t.Fatalf("a buffer in use should not still be on the spare list")
	}
}

// A spare list with no room drops what it is offered rather than blocking the
// reader, and a reader with none waiting makes its own. Neither is a failure.
func TestChardevStreamBuffersAreNeverWaitedFor(t *testing.T) {
	stream := &chardevStream{spare: make(chan []byte, 1)}
	stream.recycle(make([]byte, chardevChunk))
	stream.recycle(make([]byte, chardevChunk))
	if len(stream.spare) != 1 {
		t.Fatalf("the spare list should have kept one and let the other go, has %d", len(stream.spare))
	}
	// Too small to be a chunk, so it is not offered at all.
	stream.recycle(make([]byte, 4))
	if len(stream.spare) != 1 {
		t.Fatalf("a buffer that is not chunk sized should not go on the spare list")
	}
	<-stream.spare
	if taken := stream.takeBuffer(); len(taken) != chardevChunk {
		t.Fatalf("an empty spare list should still produce a %d byte buffer, got %d", chardevChunk, len(taken))
	}
}

// A cancelled request is answered before the stream is touched.
func TestChardevStreamAnswersACancelledContext(t *testing.T) {
	stream := &chardevStream{chunks: make(chan []byte, 1)}
	stream.chunks <- []byte{1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream.Read(ctx, make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Errorf("Read = %v, want the context's error", err)
	}
}

// Why the reader stopped is what the next read reports, so that a device that
// went away is not mistaken for one with nothing on it.
func TestChardevStreamReportsWhyTheReaderStopped(t *testing.T) {
	stream := &chardevStream{chunks: make(chan []byte, 1)}
	stream.failure = syscall.EIO
	close(stream.chunks)
	if _, err := stream.Read(context.Background(), make([]byte, 1)); !errors.Is(err, syscall.EIO) {
		t.Errorf("Read = %v, want EIO", err)
	}
}
