// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tsdemux

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// packet builds a tagged 188-byte packet whose payload is filled with marker.
func packet(tuner int, marker byte) []byte {
	data := make([]byte, 188)
	data[0] = byte((tuner+1)<<4) | 0x07
	for index := 1; index < len(data); index++ {
		data[index] = marker
	}
	return data
}

// source replays a fixed byte stream, then reports timeouts forever.
func source(data []byte) func([]byte, uint32) (int, error) {
	var mu sync.Mutex
	offset := 0
	return func(buffer []byte, _ uint32) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if offset >= len(data) {
			time.Sleep(time.Millisecond)
			return 0, errors.New("timeout")
		}
		read := copy(buffer, data[offset:])
		offset += read
		return read, nil
	}
}

func readPacket(t *testing.T, reader *Reader) []byte {
	t.Helper()
	buffer := make([]byte, 188)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		read, err := reader.Read(context.Background(), buffer)
		if errors.Is(err, device.ErrWouldBlock) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if read != 188 {
			t.Fatalf("short read: %d", read)
		}
		return buffer
	}
	t.Fatal("timed out waiting for a packet")
	return nil
}

func TestTaggedSync(t *testing.T) {
	t.Parallel()
	tag := TaggedSync(5)
	for sync, want := range map[byte]int{0x17: 0, 0x27: 1, 0x37: 2, 0x47: 3, 0x57: 4} {
		if index, ok := tag(sync); !ok || index != want {
			t.Errorf("tag(%#02x) = %d, %v; want %d, true", sync, index, ok, want)
		}
	}
	for _, sync := range []byte{0x00, 0x67, 0x48, 0x87} {
		if _, ok := tag(sync); ok {
			t.Errorf("tag(%#02x) accepted a packet outside the board's tuners", sync)
		}
	}
}

func TestSplitsPacketsPerTuner(t *testing.T) {
	t.Parallel()
	var stream []byte
	for round := 0; round < 4; round++ {
		for tuner := 0; tuner < 3; tuner++ {
			stream = append(stream, packet(tuner, byte(0xa0+tuner))...)
		}
	}
	demux, err := New(Config{Read: source(stream), Tag: TaggedSync(3), Tuners: 3, SyncRun: 1})
	if err != nil {
		t.Fatal(err)
	}
	readers := make([]*Reader, 3)
	for index := range readers {
		if readers[index], err = demux.Open(index); err != nil {
			t.Fatal(err)
		}
	}
	// Every tuner is read concurrently, which is the point of the demultiplexer.
	var wait sync.WaitGroup
	for index, reader := range readers {
		wait.Add(1)
		go func(index int, reader *Reader) {
			defer wait.Done()
			for count := 0; count < 4; count++ {
				got := readPacket(t, reader)
				if got[0] != 0x47 {
					t.Errorf("tuner %d: sync byte = %#02x; want 0x47", index, got[0])
					return
				}
				if got[1] != byte(0xa0+index) {
					t.Errorf("tuner %d: payload = %#02x; want %#02x", index, got[1], 0xa0+index)
					return
				}
			}
		}(index, reader)
	}
	wait.Wait()
	for _, reader := range readers {
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResynchronisesOnPartialPacket(t *testing.T) {
	t.Parallel()
	// Start mid-packet, as a device that was already streaming would.
	stream := packet(0, 0x11)[100:]
	for round := 0; round < 6; round++ {
		stream = append(stream, packet(0, 0x22)...)
	}
	demux, err := New(Config{Read: source(stream), Tag: TaggedSync(1), Tuners: 1, ReadSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := demux.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got := readPacket(t, reader)
	if got[0] != 0x47 || got[1] != 0x22 {
		t.Fatalf("packet = %#02x %#02x; want 0x47 0x22", got[0], got[1])
	}
}

func TestStartsAndStopsWithTheFirstAndLastReader(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	starts, stops := 0, 0
	demux, err := New(Config{
		Read:   source(nil),
		Tag:    TaggedSync(2),
		Tuners: 2,
		Start:  func() error { mu.Lock(); defer mu.Unlock(); starts++; return nil },
		Stop:   func() error { mu.Lock(); defer mu.Unlock(); stops++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := demux.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := demux.Open(1)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if starts != 1 || stops != 0 {
		t.Fatalf("after two Opens: starts=%d stops=%d; want 1, 0", starts, stops)
	}
	mu.Unlock()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if stops != 0 {
		t.Fatalf("stopped while a reader was still open: stops=%d", stops)
	}
	mu.Unlock()
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if starts != 1 || stops != 1 {
		t.Fatalf("after both Closes: starts=%d stops=%d; want 1, 1", starts, stops)
	}
}

func TestOpenRejectsBusyAndOutOfRangeTuners(t *testing.T) {
	t.Parallel()
	demux, err := New(Config{Read: source(nil), Tag: TaggedSync(2), Tuners: 2})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := demux.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := demux.Open(0); !errors.Is(err, device.ErrTunerBusy) {
		t.Fatalf("second Open of tuner 0 = %v; want ErrTunerBusy", err)
	}
	if _, err := demux.Open(2); err == nil {
		t.Fatal("Open accepted a tuner outside the board's range")
	}
	// A closed reader frees its tuner again.
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := demux.Open(0)
	if err != nil {
		t.Fatalf("reopening a closed tuner failed: %v", err)
	}
	again.Close()
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{Tag: TaggedSync(1), Tuners: 1}); err == nil {
		t.Error("New accepted a config without Read")
	}
	if _, err := New(Config{Read: source(nil), Tuners: 1}); err == nil {
		t.Error("New accepted a config without Tag")
	}
	if _, err := New(Config{Read: source(nil), Tag: PlainSync(), Tuners: 0}); err == nil {
		t.Error("New accepted a config without tuners")
	}
}
