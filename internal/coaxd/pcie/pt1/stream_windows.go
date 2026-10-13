// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
)

// ErrWouldBlock reports that nothing had arrived yet, which for a stream still
// being broadcast is ordinary rather than an error.
var ErrWouldBlock = device.ErrWouldBlock

// pollDelay is how often the card's memory is looked at. There is no interrupt
// anywhere on this card, so this is the whole of how the stream is noticed. A
// 4 KiB page holds all four streams together and at their fastest fills in
// about a third of a millisecond, so each round finds about thirty of them.
const pollDelay = 10 * time.Millisecond

// Stream is one receiver's transport stream, being read.
//
// Unlike the PT3's these are not independent: one engine interleaves all four
// receivers, so one goroutine reads the card and hands each its share. A slow
// receiver must not stall the other three, so a block that will not fit is
// dropped and counted rather than waited on.
type Stream struct {
	card     *card
	receiver int

	blocks chan []byte
	// pending is what a Read could not fit in the buffer it was given.
	pending []byte

	// dropped counts what was thrown away because this reader was not keeping
	// up. Written by the poller, read by whoever asks, under streamMutex.
	dropped uint64

	closeOnce sync.Once
	closed    chan struct{}
}

// openStream starts a receiver's share of the transfer and begins reading it.
func (c *card) openStream(receiver int) (*Stream, error) {
	c.streamMutex.Lock()
	defer c.streamMutex.Unlock()
	if c.streams[receiver] != nil {
		return nil, errors.New("that receiver is already being read")
	}
	// The first reader brings the transfer up: the engine is the card's, and
	// runs while anything is reading.
	if c.ring == nil {
		ring, err := newRing(c)
		if err != nil {
			return nil, err
		}
		c.demux = newDemultiplexer()
		if err := ring.arm(); err != nil {
			_ = ring.close()
			return nil, err
		}
		c.ring = ring
		c.stopping = make(chan struct{})
		c.polled = make(chan struct{})
		go c.poll()
	}
	stream := &Stream{
		card:     c,
		receiver: receiver,
		blocks:   make(chan []byte, queuedBlocks),
		closed:   make(chan struct{}),
	}
	c.streams[receiver] = stream
	if err := c.setStream(receiver, true); err != nil {
		c.streams[receiver] = nil
		_ = c.stopPolling()
		return nil, err
	}
	return stream, nil
}

// queuedBlocks is how far a reader may fall behind before anything is thrown
// away. A block is one polling round for that receiver, about 150 packets at a
// full multiplex, so sixteen is something over a second: longer than any
// reader here pauses, short enough that a stopped reader is noticed rather
// than accumulated.
const queuedBlocks = 16

// poll reads the card and hands each receiver its share.
func (c *card) poll() {
	defer close(c.polled)
	defer device.HaltOnPanic()
	ticker := time.NewTicker(pollDelay)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopping:
			return
		case <-ticker.C:
		}
		c.drain()
	}
}

// drain takes every page the card has finished with and hands on what came out
// of them.
func (c *card) drain() {
	c.streamMutex.Lock()
	defer c.streamMutex.Unlock()
	if c.ring == nil {
		return
	}
	for pages := 0; pages < pagesPerRound && c.ring.written(); pages++ {
		c.demux.feed(c.ring.current())
		if err := c.ring.advance(); err != nil {
			// The card stopped answering; readers find out when their streams
			// dry up.
			return
		}
	}
	for receiver, stream := range c.streams {
		if stream == nil {
			continue
		}
		block := c.demux.take(receiver)
		if len(block) == 0 {
			continue
		}
		select {
		case stream.blocks <- block:
		default:
			// Dropping this reader's block keeps the other three moving, which
			// is why this is one goroutine rather than four.
			stream.dropped++
		}
	}
}

// pagesPerRound bounds one polling round, so a reader that has been away
// cannot hold the lock while a whole ring is worked through.
const pagesPerRound = 2 * pagesPerTable

// stopPolling ends the poller and gives the transfer back, once nothing is
// reading. The caller holds the stream lock.
func (c *card) stopPolling() error {
	for _, stream := range c.streams {
		if stream != nil {
			return nil
		}
	}
	if c.ring == nil {
		return nil
	}
	close(c.stopping)
	// The poller takes the same lock, so it must be released while waiting.
	c.streamMutex.Unlock()
	<-c.polled
	c.streamMutex.Lock()

	err := c.ring.close()
	c.ring, c.demux = nil, nil
	return err
}

// Read takes what has arrived. It never waits: a caller with nothing to read
// is told so and asks again.
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
			case <-s.closed:
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

// Counts is what this receiver's share of the flow has come to.
func (s *Stream) Counts() Counts {
	s.card.streamMutex.Lock()
	defer s.card.streamMutex.Unlock()
	if s.card.demux == nil {
		return Counts{}
	}
	return s.card.demux.counts(s.receiver)
}

// Dropped is how much this reader was too slow to take.
func (s *Stream) Dropped() uint64 {
	s.card.streamMutex.Lock()
	defer s.card.streamMutex.Unlock()
	return s.dropped
}

// Close stops this receiver's share, and the transfer with it when nothing
// else is reading.
func (s *Stream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		s.card.streamMutex.Lock()
		defer s.card.streamMutex.Unlock()
		_ = s.card.setStream(s.receiver, false)
		s.card.streams[s.receiver] = nil
		if s.card.demux != nil {
			// Whatever was rebuilt and not handed over goes with it, so nothing
			// is held for a reader that has gone.
			s.card.demux.take(s.receiver)
		}
		err = s.card.stopPolling()
	})
	return err
}
