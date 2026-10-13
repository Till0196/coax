// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// ErrWouldBlock reports that nothing had arrived yet. A transport stream is
// still being broadcast, so having nothing this instant is ordinary rather than
// an error, and the caller above treats it as such.
var ErrWouldBlock = device.ErrWouldBlock

// pollInterval is how often the card's buffer is looked at. The card has no
// interrupt, so this is the whole of how the stream is noticed. A full
// terrestrial multiplex fills a block in about fourteen milliseconds and a ring
// in about half a second.
const pollInterval = 10 * time.Millisecond

// Stream is a channel's transport stream, being read. The polling has a goroutine
// of its own so that a caller which stops asking does not leave the card's ring
// unattended and overrunning.
type Stream struct {
	ring *ring
	// blocks carries what the poller has taken out of the ring. Short on
	// purpose: the ring is the buffer, and a deep channel here would only add
	// delay to a stream that is already being drained.
	blocks chan []byte
	// pending is what a Read could not fit in the buffer it was given.
	pending []byte
	// stopping is closed when a close is asked for.
	stopping chan struct{}
	// done is closed once the poller has stopped touching the card.
	done chan struct{}

	closeOnce sync.Once
	closeErr  error
	// released is what the card should do when the last stream on it goes.
	released func()
}

// openStream starts the transfer on a channel and begins reading it.
func (c *card) openStream(channel int) (*Stream, error) {
	c.streamMutex.Lock()
	defer c.streamMutex.Unlock()
	if c.streams[channel] != nil {
		return nil, errors.New("that channel is already being read")
	}
	r, err := newRing(c, channel)
	if err != nil {
		return nil, err
	}
	if err := r.arm(); err != nil {
		_ = r.close()
		return nil, err
	}
	stream := &Stream{
		ring:     r,
		blocks:   make(chan []byte, 8),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
	stream.released = func() {
		c.streamMutex.Lock()
		defer c.streamMutex.Unlock()
		c.streams[channel] = nil
	}
	c.streams[channel] = stream
	go stream.poll()
	return stream, nil
}

// poll moves what the card has written out of the ring. Everything is copied: the
// card is still writing into the ring, and the block just taken will be written
// again a ring's worth of time from now.
func (s *Stream) poll() {
	defer close(s.done)
	defer device.HaltOnPanic()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopping:
			return
		case <-ticker.C:
		}
		s.ring.collect(func(block []byte) {
			taken := make([]byte, len(block))
			copy(taken, block)
			select {
			case s.blocks <- taken:
			case <-s.stopping:
			}
		})
	}
}

// Read takes what has arrived. It never waits: a caller with nothing to read is
// told so and asks again.
func (s *Stream) Read(ctx context.Context, into []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(s.pending) == 0 {
		select {
		case block := <-s.blocks:
			s.pending = block
		default:
			select {
			case <-s.done:
				return 0, errors.New("the stream has stopped")
			default:
			}
			return 0, ErrWouldBlock
		}
	}
	read := copy(into, s.pending)
	s.pending = s.pending[read:]
	return read, nil
}

// Overruns is how many times the card came all the way round the ring before this
// program took what was in it, each of which lost most of a ring.
func (s *Stream) Overruns() int { return s.ring.overruns }

// ErrorPackets is the card's own count of packets it could not use, which it
// keeps per channel and never resets.
func (s *Stream) ErrorPackets() (uint32, error) { return s.ring.errorPackets() }

// Close stops the transfer and gives the buffer back. A channel that cannot be
// seen to stop keeps its buffer, and the error says so.
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.stopping)
		<-s.done
		s.closeErr = s.ring.close()
		if s.released != nil {
			s.released()
		}
	})
	return s.closeErr
}
