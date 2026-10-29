// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package graph

import (
	"errors"
	"io"
	"sync"
)

// Spool is a bounded byte pipe. Its ring accepts arbitrary sample sizes and
// copies bytes in and out independent of TS or TLV framing.
// Writers never wait for a reader. Overflow ends the stream instead of joining
// bytes across a gap, which could corrupt a variable-length packet.
type Spool struct {
	mutex    sync.Mutex
	buffer   []byte
	head     int
	buffered int
	closed   bool
	failure  error
}

var ErrCaptureOverflow = errors.New("BDA capture buffer overflow: reader cannot keep up")

const (
	spoolInitialCapacity = 256 << 10
	spoolCapacity        = 32 << 20
)

func (s *Spool) Write(data []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	size := len(data)
	// The reader receives failures. Returning one to DirectShow would stop the
	// graph from its callback rather than through the owner's teardown path.
	if s.closed || s.failure != nil {
		return size, nil
	}
	if len(data) > spoolCapacity-s.buffered {
		s.failure = ErrCaptureOverflow
		return size, nil
	}
	if len(data) == 0 {
		return size, nil
	}
	if needed := s.buffered + len(data); needed > len(s.buffer) {
		capacity := max(spoolInitialCapacity, len(s.buffer))
		for capacity < needed {
			capacity *= 2
		}
		next := make([]byte, capacity)
		if s.buffered > 0 {
			first := copy(next[:s.buffered], s.buffer[s.head:])
			copy(next[first:s.buffered], s.buffer[:s.buffered-first])
		}
		s.buffer, s.head = next, 0
	}
	tail := (s.head + s.buffered) % len(s.buffer)
	n := copy(s.buffer[tail:], data)
	copy(s.buffer, data[n:])
	s.buffered += len(data)
	return size, nil
}

// Read returns immediately. Zero bytes with no error means the reader should
// ask again later. A failure follows the retained prefix before the gap.
func (s *Spool) Read(out []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	n := min(len(out), s.buffered)
	if n == 0 {
		if len(out) > 0 {
			if s.closed {
				return 0, io.ErrClosedPipe
			}
			return 0, s.failure
		}
		return 0, nil
	}
	first := copy(out[:n], s.buffer[s.head:])
	copy(out[first:n], s.buffer[:n-first])
	s.head = (s.head + n) % len(s.buffer)
	s.buffered -= n
	return n, nil
}

func (s *Spool) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.closed = true
	s.buffer = nil
	s.head = 0
	s.buffered = 0
	return nil
}
