// Copyright (c) 2026 waya0125; 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// tsPackets builds count transport packets, numbered in their payload, with
// the transport error indicator set on those damaged says.
func tsPackets(from, count int, damaged func(int) bool) []byte {
	var out []byte
	for i := from; i < from+count; i++ {
		packet := make([]byte, tsPacketSize)
		packet[0], packet[1], packet[2], packet[3] = tsSyncByte, 0x01, 0x00, 0x10
		if damaged(i) {
			packet[1] |= 0x80
		}
		packet[4], packet[5], packet[6] = byte(i>>16), byte(i>>8), byte(i)
		out = append(out, packet...)
	}
	return out
}

func packetNumber(packet []byte) int {
	return int(packet[4])<<16 | int(packet[5])<<8 | int(packet[6])
}

func never(int) bool { return false }

// feed hands out its bytes a chunk at a time, then would block, or ends with
// end if one is set.
type feed struct {
	data  []byte
	chunk int
	end   error
}

func (f *feed) Read(_ context.Context, buffer []byte) (int, error) {
	if len(f.data) == 0 {
		if f.end != nil {
			return 0, f.end
		}
		return 0, ErrWouldBlock
	}
	n := min(len(buffer), f.chunk, len(f.data))
	copy(buffer, f.data[:n])
	f.data = f.data[n:]
	return n, nil
}
func (f *feed) Close() error { return nil }

// drain reads a settling stream the way the server does until the feed has
// nothing more, and returns what came out.
func drain(t *testing.T, stream Stream) ([]byte, error) {
	t.Helper()
	var out []byte
	buffer := make([]byte, 32*1024)
	idle := 0
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		read, err := stream.Read(context.Background(), buffer)
		out = append(out, buffer[:read]...)
		if err != nil && !errors.Is(err, ErrWouldBlock) {
			return out, err
		}
		if read == 0 {
			if idle++; idle > 200 {
				return out, nil
			}
			time.Sleep(time.Millisecond)
		} else {
			idle = 0
		}
	}
	t.Fatal("the stream did not drain")
	return nil, nil
}

func shortenSettleLimit(t *testing.T, limit time.Duration) {
	saved := settleLimit
	settleLimit = limit
	t.Cleanup(func() { settleLimit = saved })
}

func TestSettlingStreamDropsOnlyTheDamagedHead(t *testing.T) {
	// The head is flagged now and then up to packet 1500, as on a PT3 just
	// after lock, and clean from there on.
	damaged := func(i int) bool { return i <= 1500 && i%50 == 7 }
	all := tsPackets(0, 5000, damaged)
	// A partial packet before the first boundary, as a read can start mid-way.
	got, err := drain(t, newSettlingStream(&feed{data: append([]byte{1, 2, 3}, all...), chunk: 10000}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got)%tsPacketSize != 0 || got[0] != tsSyncByte {
		t.Fatalf("not aligned on a packet: %d bytes", len(got))
	}
	if first := packetNumber(got); first != 1458 {
		t.Fatalf("starts at packet %d, want 1458, just after the last flagged one", first)
	}
	if !bytes.Equal(got, all[1458*tsPacketSize:]) {
		t.Fatal("a clean packet after the damaged head was lost")
	}
}

func TestSettlingStreamPassesACleanStreamWhole(t *testing.T) {
	all := tsPackets(0, 3000, never)
	got, err := drain(t, newSettlingStream(&feed{data: all, chunk: 7000}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, all) {
		t.Fatalf("got %d bytes of %d", len(got), len(all))
	}
}

func TestSettlingStreamHandsOverAStreamThatNeverSettles(t *testing.T) {
	// A weak channel: flagged now and then for as long as it runs. Its front
	// must not be lost for want of a clean run.
	shortenSettleLimit(t, 30*time.Millisecond)
	all := tsPackets(0, 5000, func(i int) bool { return i%500 == 3 })
	stream := newSettlingStream(&feed{data: all, chunk: 200 * tsPacketSize})
	got, err := drain(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, all) {
		t.Fatalf("got %d bytes of %d: part of a weak channel was lost", len(got), len(all))
	}
}

func TestSettlingStreamDoesNotHoldWhatIsNotTransportPackets(t *testing.T) {
	// Noise, or a framing other than transport packets, goes straight through
	// for the sync check to judge, without waiting for the limit.
	shortenSettleLimit(t, time.Hour)
	noise := bytes.Repeat([]byte{0x12, 0x47, 0x00, 0x99, 0x47}, settleProbeBytes/5+100)
	stream := newSettlingStream(&feed{data: noise, chunk: 16 * 1024})
	got, err := drain(t, stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, noise) {
		t.Fatalf("got %d bytes of %d", len(got), len(noise))
	}
}

func TestSettlingStreamHandsOverWhatCameBeforeAnEnd(t *testing.T) {
	// The stream ends while it is held -- the board is unplugged. What was
	// read comes out, then the end.
	shortenSettleLimit(t, time.Hour)
	all := tsPackets(0, 500, func(i int) bool { return i < 10 })
	got, err := drain(t, newSettlingStream(&feed{data: all, chunk: 5000, end: io.ErrUnexpectedEOF}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want the end the stream reported", err)
	}
	if !bytes.Equal(got, all) {
		t.Fatalf("got %d bytes of %d", len(got), len(all))
	}
}

func TestAlignTSWantsFourPacketsInARow(t *testing.T) {
	data := make([]byte, 5*tsPacketSize)
	data[10], data[10+tsPacketSize] = tsSyncByte, tsSyncByte
	if at := alignTS(data, 0); at != -1 {
		t.Fatalf("two sync bytes in a row lined up at %d", at)
	}
	for packet := 0; packet < 4; packet++ {
		data[20+packet*tsPacketSize] = tsSyncByte
	}
	if at := alignTS(data, 0); at != 20 {
		t.Fatalf("aligned at %d, want 20", at)
	}
}
