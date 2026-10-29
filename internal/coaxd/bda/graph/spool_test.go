// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package graph

import (
	"bytes"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSpoolCopiesSamplesAndPreservesPartialReads(t *testing.T) {
	s := new(Spool)
	data := []byte("abcdef")
	s.Write(data)
	data[0] = 'z'
	s.Write([]byte("gh"))
	out := make([]byte, 8)
	n, _ := s.Read(out[:3])
	m, _ := s.Read(out[n:])
	if n+m != 8 || string(out) != "abcdefgh" {
		t.Fatalf("got %q", out)
	}
	if n, _ := s.Read(out); n != 0 {
		t.Fatal("empty pipe returned data")
	}
}

func TestSpoolOverflowDoesNotJoinBytesAcrossALostSample(t *testing.T) {
	s := new(Spool)
	s.Write(bytes.Repeat([]byte{1}, spoolCapacity))
	s.Write([]byte{2})
	out := make([]byte, spoolCapacity)
	n, _ := s.Read(out)
	if n != spoolCapacity || !bytes.Equal(out, bytes.Repeat([]byte{1}, spoolCapacity)) {
		t.Fatal("buffer changed on overflow")
	}
	s.Write([]byte{3})
	n, err := s.Read(out)
	if n != 0 || err != ErrCaptureOverflow {
		t.Fatalf("got %d, %v after overflow", n, err)
	}

}

func TestSpoolCloseDiscardsPendingBytesAndEndsReads(t *testing.T) {
	s := new(Spool)
	s.Write([]byte("pending"))
	s.Close()
	s.Close()
	s.Write([]byte("later"))
	if n, err := s.Read(make([]byte, 16)); n != 0 || err != io.ErrClosedPipe {
		t.Fatalf("closed read: %d, %v", n, err)
	}
}

func TestSpoolConcurrentTransfer(t *testing.T) {
	s := new(Spool)
	want := bytes.Repeat([]byte("abcdef"), 1000)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for at := 0; at < len(want); at += 6 {
			s.Write(want[at : at+6])
		}
	}()
	got := make([]byte, 0, len(want))
	buffer := make([]byte, 17)
	deadline := time.Now().Add(5 * time.Second)
	for len(got) < len(want) {
		if time.Now().After(deadline) {
			t.Fatal("concurrent transfer stalled")
		}
		n, _ := s.Read(buffer)
		got = append(got, buffer[:n]...)
		if n == 0 {
			runtime.Gosched()
		}
	}
	<-done
	if !bytes.Equal(got, want) {
		t.Fatal("concurrent transfer reordered data")
	}
}

func TestSpoolVariableLengthPacketsAcrossRingWrap(t *testing.T) {
	s := new(Spool)
	// Put the write position just before the end of the ring.
	padding := make([]byte, spoolCapacity-2)
	s.Write(padding)
	s.Read(padding)
	var want []byte
	for _, size := range []int{0, 1, 187, 188, 65535, 13} {
		want = append(want, 0x7f, 0xff, byte(size>>8), byte(size))
		want = append(want, bytes.Repeat([]byte{0x7f}, size)...)
	}
	for at := 0; at < len(want); {
		end := min(at+3, len(want))
		s.Write(want[at:end])
		at = end
	}
	var got []byte
	out := make([]byte, 197)
	for len(got) < len(want) {
		n, err := s.Read(out)
		if err != nil || n == 0 {
			t.Fatalf("read %d, %v", n, err)
		}
		got = append(got, out[:n]...)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("packet bytes changed across sample/read/ring boundaries")
	}
}

func TestSpoolStartsSmallAndGrowsAcrossWrappedData(t *testing.T) {
	s := new(Spool)
	if len(s.buffer) != 0 {
		t.Fatal("empty pipe allocated memory")
	}
	initial := bytes.Repeat([]byte{0x11}, spoolInitialCapacity-2)
	s.Write(initial)
	if len(s.buffer) != spoolInitialCapacity {
		t.Fatalf("initial capacity: %d", len(s.buffer))
	}
	s.Read(make([]byte, len(initial)-7))
	tail := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19}
	s.Write(tail)
	// The retained data now straddles the ring boundary.
	large := bytes.Repeat([]byte{0x22}, spoolInitialCapacity)
	s.Write(large)
	want := append(bytes.Repeat([]byte{0x11}, 7), tail...)
	want = append(want, large...)
	got := make([]byte, len(want))
	n, err := s.Read(got)
	if n != len(want) || err != nil || !bytes.Equal(got, want) {
		t.Fatal("growth changed wrapped data")
	}
	if len(s.buffer) != 2*spoolInitialCapacity {
		t.Fatalf("grown capacity: %d", len(s.buffer))
	}
	before := &s.buffer[0]
	for range 100 {
		s.Write(tail)
		s.Read(got[:len(tail)])
	}
	if &s.buffer[0] != before {
		t.Fatal("small transfers kept reallocating")
	}
}

func TestSpoolRejectsOversizedSampleBeforeAllocating(t *testing.T) {
	s := new(Spool)
	sample := make([]byte, spoolCapacity+1)
	n, err := s.Write(sample)
	if n != len(sample) || err != nil {
		t.Fatalf("callback write: %d, %v", n, err)
	}
	if s.buffer != nil {
		t.Fatal("oversized sample allocated capture memory")
	}
	if n, err := s.Read(make([]byte, 1)); n != 0 || err != ErrCaptureOverflow {
		t.Fatalf("reader: %d, %v", n, err)
	}
}

func TestSpoolCloseDuringConcurrentTransfer(t *testing.T) {
	s := new(Spool)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		sample := make([]byte, 64*1024)
		for range 1000 {
			s.Write(sample)
		}
	}()
	go func() {
		defer group.Done()
		out := make([]byte, 32*1024)
		for range 2000 {
			_, err := s.Read(out)
			if err != nil && err != ErrCaptureOverflow && err != io.ErrClosedPipe {
				t.Errorf("read: %v", err)
				return
			}
		}
	}()
	for range 100 {
		runtime.Gosched()
	}
	s.Close()
	group.Wait()
	if n, err := s.Read(make([]byte, 1)); n != 0 || err != io.ErrClosedPipe {
		t.Fatalf("closed reader: %d, %v", n, err)
	}
}
