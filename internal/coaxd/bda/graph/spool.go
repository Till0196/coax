// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package graph

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Spool is what a graph writes its stream into: a run of files rather than
// one. Each is closed once it has taken spoolSegment bytes and the next begun,
// so a reader that has finished with one can delete it and the disk holds a
// few segments however long the graph runs. One file would take 17 GB an hour
// of a 39 Mbps carrier, and could not be cut down while the graph held it.
//
// The files are made with names nobody can guess and that nothing else may
// already hold, in the temporary directory. A service's temporary directory is
// the system's, which every user can write to.
type Spool struct {
	// limit bounds the whole spool, for the stretches when nothing is reading
	// it; past it, writes are swallowed. Zero is unbounded.
	limit int64
	// segment is how much one file takes; spoolSegment but in a test.
	segment int64

	mutex   sync.Mutex
	file    *os.File
	written int64 // into file
	total   int64
	// paths are the files not yet deleted, oldest first. While the spool is
	// being written, the last is file's.
	paths  []string
	closed bool
}

// spoolSegment is how much one file takes before the next is begun.
const spoolSegment = 64 << 20

// spoolUnread is how many files a reader may fall behind by. A reader further
// behind than that -- half a gigabyte, a couple of minutes of the widest
// carrier -- is not coming back for them soon, and the stream is dropped rather
// than the disk filled.
const spoolUnread = 8

// spoolPattern names the files, so a run that was killed can have what it left
// behind found again. See RemoveLeftovers.
const spoolPattern = "coax-bda-*.ts"

// NewSpool begins a spool, with its first file.
func NewSpool(limit int64) (*Spool, error) {
	file, err := os.CreateTemp("", spoolPattern)
	if err != nil {
		return nil, err
	}
	return &Spool{limit: limit, segment: spoolSegment, file: file, paths: []string{file.Name()}}, nil
}

// Write takes what the graph delivered. It reports everything as written when
// it drops some or all of it: the graph is not being told the disk failed, it
// is being told nothing at all, which is what a stream nobody reads is.
func (s *Spool) Write(data []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed || s.file == nil {
		return len(data), nil
	}
	wanted := data
	if s.limit > 0 {
		left := s.limit - s.total
		if left <= 0 {
			return len(data), nil
		}
		if int64(len(wanted)) > left {
			wanted = wanted[:left]
		}
	}
	if s.written >= s.segment {
		if len(s.paths) >= spoolUnread {
			return len(data), nil
		}
		if err := s.begin(); err != nil {
			return 0, err
		}
	}
	written, err := s.file.Write(wanted)
	s.written += int64(written)
	s.total += int64(written)
	if err != nil {
		return written, err
	}
	return len(data), nil
}

// begin closes the file being written and starts the next. The caller holds
// the lock.
func (s *Spool) begin() error {
	next, err := os.CreateTemp("", spoolPattern)
	if err != nil {
		return err
	}
	_ = s.file.Close()
	s.file, s.written = next, 0
	s.paths = append(s.paths, next.Name())
	return nil
}

// First is the oldest file still there, which is where a reader starts.
func (s *Spool) First() string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if len(s.paths) == 0 {
		return ""
	}
	return s.paths[0]
}

// Next is the file after this one, once there is one. A file with one after
// it is finished: a reader that has read it to the end has all of it.
func (s *Spool) Next(path string) (string, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index, one := range s.paths {
		if one == path && index+1 < len(s.paths) {
			return s.paths[index+1], true
		}
	}
	return "", false
}

// Consumed deletes a file a reader has finished with.
func (s *Spool) Consumed(path string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for index, one := range s.paths {
		if one != path {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		s.paths = append(s.paths[:index], s.paths[index+1:]...)
		return nil
	}
	return nil
}

// Remove stops the spool and deletes every file it still has. A file a reader
// still has open cannot be deleted on Windows; it is kept, and a later Remove,
// once the reader has let go, deletes it.
func (s *Spool) Remove() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.closed = true
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	var first error
	kept := s.paths[:0]
	for _, path := range s.paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			kept = append(kept, path)
			if first == nil {
				first = err
			}
		}
	}
	s.paths = kept
	return first
}

// RemoveLeftovers deletes what a run that was killed left in the temporary
// directory. A spool still being written is not touched: its file is open, and
// Windows refuses to delete an open file.
func RemoveLeftovers() int {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), spoolPattern))
	if err != nil {
		return 0
	}
	removed := 0
	for _, path := range matches {
		if !strings.HasPrefix(filepath.Base(path), "coax-bda-") {
			continue
		}
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if os.Remove(path) == nil {
			removed++
		}
	}
	return removed
}
