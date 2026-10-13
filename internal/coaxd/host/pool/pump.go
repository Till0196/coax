// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/receiver"
)

// Every stream the daemon hands out is drained by a goroutine of its own, into
// the buffer below, and whoever asked for it is served from there.
//
// Without that the same goroutine reads the tuner and writes to whoever asked
// for it, so a consumer that stops taking bytes for a moment stops the tuner as
// well -- and a tuner is not a file that waits. A card streaming isochronously
// is served only while transfers are outstanding; once those are used up the
// endpoint stops. Measured, a front end starting its video decoder took a
// quarter of a second to come back for the next bytes.
//
// pumpBuffer is how far ahead of its consumer a stream may run: a little over
// two seconds of a terrestrial multiplex, half a second of a satellite carrier
// at its widest. A consumer further behind than that is not having a moment's
// trouble and the stream waits.
const pumpBuffer = 4 << 20

// pumpIdle is how long a read that came back with nothing waits before asking
// again, and how long a consumer waits for bytes that have not arrived.
const pumpIdle = 10 * time.Millisecond

// pumpRead is how much is asked of the tuner at a time.
const pumpRead = 256 * 1024

// piece is one read waiting to be handed on, and how much of it has gone.
type piece struct {
	bytes []byte
	taken int
}

// pumpSpare is how many finished pieces are kept to be filled again -- enough
// for a consumer that has fallen a moment behind, and no more.
const pumpSpare = 64

// pumped is a stream kept drained by a goroutine of its own.
type pumped struct {
	stream device.Stream
	cancel context.CancelFunc
	done   chan struct{}

	mutex   sync.Mutex
	arrived *sync.Cond
	waiting []piece
	// spare holds the pieces a consumer has finished with, to be filled again. A
	// stream that made a piece per read would make as much garbage as it carries,
	// and collecting it stops every thread the runtime has until the last of them
	// notices. Measured on a busy two-processor machine, one such stop held the
	// thread reading the card for a third of a second, long enough for the card to
	// overrun what was queued to receive it.
	spare [][]byte
	held  int
	// taken is signalled when a consumer has made room, for the goroutine
	// waiting to put more in.
	taken *sync.Cond
	// ended is set when the goroutine has stopped, with the failure that stopped
	// it if there was one. What is already in the buffer is served first: the
	// reason the stream ended is no reason to lose the bytes that arrived before
	// it did.
	ended  bool
	reason error
	// stopping ends the wait for room when the stream is being closed.
	stopping bool
	// idle wakes a consumer whose stream has gone quiet. It is kept rather than
	// made per read for the same reason the buffers are: a stream is read hundreds
	// of times a second.
	idle *time.Timer

	closeOnce sync.Once
	closeErr  error
}

// pump starts draining a stream. Closing the stream it returns stops the
// goroutine and closes the one underneath.
func pump(stream device.Stream) *pumped {
	ctx, cancel := context.WithCancel(context.Background())
	p := &pumped{stream: stream, cancel: cancel, done: make(chan struct{})}
	p.arrived = sync.NewCond(&p.mutex)
	p.taken = sync.NewCond(&p.mutex)
	go p.drain(ctx)
	return p
}

func (p *pumped) drain(ctx context.Context) {
	defer close(p.done)
	defer device.HaltOnPanic()
	buffer := make([]byte, pumpRead)
	quiet := time.NewTimer(pumpIdle)
	quiet.Stop()
	defer quiet.Stop()
	for {
		if ctx.Err() != nil {
			p.finish(nil)
			return
		}
		read, err := p.stream.Read(ctx, buffer)
		if read > 0 {
			p.put(buffer[:read])
		}
		if err != nil && !errors.Is(err, receiver.ErrWouldBlock) && !errors.Is(err, device.ErrWouldBlock) {
			p.finish(err)
			return
		}
		if read > 0 {
			// More may be waiting, and sleeping now would cap the stream at one
			// read per interval, which a multiplex outruns.
			continue
		}
		quiet.Reset(pumpIdle)
		select {
		case <-ctx.Done():
			quiet.Stop()
			p.finish(nil)
			return
		case <-quiet.C:
		}
	}
}

func (p *pumped) put(bytes []byte) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	held := p.reuse(len(bytes))
	held = append(held, bytes...)
	// A consumer this far behind is one the buffer was never going to cover, so
	// the stream waits for it as it did before there was a buffer. Throwing the
	// oldest bytes away would put holes in a recording whose consumer is merely
	// slower than the card.
	for p.held >= pumpBuffer && !p.stopping {
		p.taken.Wait()
	}
	p.waiting = append(p.waiting, piece{bytes: held})
	p.held += len(held)
	p.arrived.Signal()
}

// reuse takes back an empty piece big enough for size bytes, or makes one.
func (p *pumped) reuse(size int) []byte {
	for at := len(p.spare) - 1; at >= 0; at-- {
		if cap(p.spare[at]) >= size {
			held := p.spare[at]
			p.spare = append(p.spare[:at], p.spare[at+1:]...)
			return held[:0]
		}
	}
	return make([]byte, 0, size)
}

func (p *pumped) finish(reason error) {
	p.mutex.Lock()
	p.ended = true
	p.reason = reason
	p.mutex.Unlock()
	p.arrived.Broadcast()
}

func (p *pumped) Read(ctx context.Context, buffer []byte) (int, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if len(p.waiting) == 0 && !p.ended {
		// Woken either by bytes arriving or by the wait running out, which is
		// what turns a stream that has gone quiet into ErrWouldBlock rather
		// than a consumer left holding the lock for ever.
		if p.idle == nil {
			p.idle = time.AfterFunc(pumpIdle, func() {
				p.mutex.Lock()
				defer p.mutex.Unlock()
				p.arrived.Broadcast()
			})
		} else {
			p.idle.Reset(pumpIdle)
		}
		p.arrived.Wait()
		p.idle.Stop()
	}
	if len(p.waiting) == 0 {
		if p.ended {
			if p.reason != nil {
				return 0, p.reason
			}
			return 0, device.ErrWouldBlock
		}
		return 0, device.ErrWouldBlock
	}
	taken := 0
	for len(p.waiting) > 0 && taken < len(buffer) {
		head := &p.waiting[0]
		moved := copy(buffer[taken:], head.bytes[head.taken:])
		taken += moved
		head.taken += moved
		p.held -= moved
		if head.taken == len(head.bytes) {
			if len(p.spare) < pumpSpare {
				p.spare = append(p.spare, head.bytes)
			}
			p.waiting = p.waiting[1:]
		}
	}
	p.taken.Signal()
	return taken, nil
}

// Dropped forwards the underlying stream's count; without it the wrapper would
// hide the capability from the type assertion that looks for it. Nothing is
// thrown away here.
func (p *pumped) Dropped() uint64 {
	counter, ok := p.stream.(device.StreamStats)
	if !ok {
		return 0
	}
	return counter.Dropped()
}

// Close stops the goroutine and then closes the stream underneath, in that
// order: nothing may be reading a stream while it is closed. Closing twice is
// harmless, since the board closes a lease's stream before its tuner and the
// client closes it again when it notices.
func (p *pumped) Close() error {
	p.closeOnce.Do(func() {
		p.mutex.Lock()
		p.stopping = true
		p.mutex.Unlock()
		p.taken.Broadcast()
		p.cancel()
		<-p.done
		p.closeErr = p.stream.Close()
	})
	return p.closeErr
}
