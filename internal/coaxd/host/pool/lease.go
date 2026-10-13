// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"context"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/tunerid"
)

// Lease is one tuner, held while whoever asked for it is still there -- the
// whole of what the daemon remembers about a client. Releasing it twice is
// harmless, since a lease is given back both when its own work finishes and
// when its owner goes away.
type Lease struct {
	board      *board
	owner      Owner
	index      int
	generation uint64
	device     device.Tuner

	// priority is how much the work holding this tuner said it mattered, and so
	// what it takes to take the tuner away. Guarded by the board's lock.
	priority int
	// preempted records that this lease was ended to give its tuner to work that
	// outranked it, rather than by its own client going away. The difference is
	// the whole of what its client can be told.
	preempted bool

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once

	// stream is kept while open so the packets it dropped can be reported by
	// something that is not reading it. open is every stream not yet closed,
	// so the board can close them before it closes the tuner under them.
	streamMutex sync.Mutex
	stream      device.Stream
	open        map[device.Stream]bool

	// tuned is the channel last set, so asking for the same one again need not
	// retune.
	tuneMutex sync.Mutex
	tuned     map[string]string
}

// TunedTo reports whether this tuner is already set to exactly this channel. A
// scan tunes to measure and then opens the stream, naming the same channel
// twice; retuning would cost the lock time twice and give the demodulator a
// second chance to miss.
func (l *Lease) TunedTo(properties map[string]string) bool {
	l.tuneMutex.Lock()
	defer l.tuneMutex.Unlock()
	if len(l.tuned) == 0 || len(l.tuned) != len(properties) {
		return false
	}
	for name, value := range properties {
		if l.tuned[name] != value {
			return false
		}
	}
	return true
}

// Tuned is the channel the tuner is set to, as the properties a tune takes. A
// copy, so that nothing outside can write into what the lease compares
// against; empty for a lease that has not tuned yet.
func (l *Lease) Tuned() map[string]string {
	l.tuneMutex.Lock()
	defer l.tuneMutex.Unlock()
	if len(l.tuned) == 0 {
		return nil
	}
	tuned := make(map[string]string, len(l.tuned))
	for name, value := range l.tuned {
		tuned[name] = value
	}
	return tuned
}

// deliverySystem tells a stream what shape of multiplex to expect. The one
// property is read under the lock rather than the map handed out, so nothing
// outside this file need know Remember replaces the map rather than writing
// into it.
func (l *Lease) deliverySystem() string {
	l.tuneMutex.Lock()
	defer l.tuneMutex.Unlock()
	return l.tuned["DELIVERY_SYSTEM"]
}

// Remember records the channel a successful tune set.
func (l *Lease) Remember(properties map[string]string) {
	l.tuneMutex.Lock()
	defer l.tuneMutex.Unlock()
	l.tuned = make(map[string]string, len(properties))
	for name, value := range properties {
		l.tuned[name] = value
	}
}

// Forget drops the remembered channel, so the next request tunes even for the
// same one.
func (l *Lease) Forget() {
	l.tuneMutex.Lock()
	defer l.tuneMutex.Unlock()
	l.tuned = nil
}

// Device is the tuner. Tuning, status and stream reads run on the caller's
// goroutine: the board driver serialises what has to be atomic, and queueing
// here would make a status read wait behind whatever else the board was doing.
//
// The pool adds a look at every error on the way back. A board is usually found
// gone by trying to use it, long before anything re-reads the bus.
func (l *Lease) Device() device.Tuner { return &leaseDevice{Tuner: l.device, lease: l} }

// Index is which tuner of the board this is.
func (l *Lease) Index() int { return l.index }

// Priority is how much the work holding this tuner said it mattered.
func (l *Lease) Priority() int {
	l.board.mutex.Lock()
	defer l.board.mutex.Unlock()
	return l.priority
}

// Reprioritise records what the latest request on this lease said it was worth.
// A client that keeps its tuner across channels says so on every one, and the
// last word counts: work whose importance changed part-way would otherwise be
// weighed by what it was.
func (l *Lease) Reprioritise(priority int) {
	l.board.mutex.Lock()
	defer l.board.mutex.Unlock()
	l.priority = priority
}

// Preempted reports whether this lease ended because higher-priority work took
// its tuner. It is what lets a handler say so rather than let the stream stop
// unexplained.
func (l *Lease) Preempted() bool {
	l.board.mutex.Lock()
	defer l.board.mutex.Unlock()
	return l.preempted
}

// Backend is the driver the tuner belongs to.
func (l *Lease) Backend() string { return l.board.entry.detected.Backend }

// ID is the name the tuner is known by outside the program.
func (l *Lease) ID() string {
	name, _ := tunerid.Tuner(deviceOf(l.board.entry.detected), l.index)
	return name
}

// Done is closed when the board goes away underneath the lease, so a stream
// copying to a client ends the response instead of hanging on a read that will
// never return.
func (l *Lease) Done() <-chan struct{} { return l.ctx.Done() }

// Context carries the same signal for a caller that has one to pass on.
func (l *Lease) Context() context.Context { return l.ctx }

// watched is this tuner's open stream, or nil when it is not streaming.
func (l *Lease) watched() *monitored {
	l.streamMutex.Lock()
	defer l.streamMutex.Unlock()
	stream, _ := l.stream.(*monitored)
	return stream
}

// dropped is what the stream threw away for want of a fast enough reader, where
// the board can count. Not streaming and cannot say are different answers.
func (l *Lease) dropped() *uint64 {
	stream := l.watched()
	if stream == nil || !stream.counts() {
		return nil
	}
	count := stream.Dropped()
	return &count
}

// analysis is what the multiplex looked like. The counts are of what went past,
// so a tuner not streaming reports nothing.
func (l *Lease) analysis() *device.StreamCounts {
	stream := l.watched()
	if stream == nil {
		return nil
	}
	counts := stream.StreamCounts()
	return &counts
}

// settings is what the hardware says about how it is tuned. Read now rather
// than remembered from the open: a board just opened has been pointed at
// nothing, and the answer would be whatever the graph was built against.
func (l *Lease) settings() []device.Setting {
	reporter, ok := l.device.(interface{ Settings() []device.Setting })
	if !ok {
		return nil
	}
	return reporter.Settings()
}

// Stream opens the tuner's stream and remembers it, so the packets it threw
// away can be reported while it runs.
func (l *Lease) Stream() (device.Stream, error) {
	opened, err := l.device.OpenStream()
	if err != nil {
		return nil, err
	}
	// The analyser goes on before anything reads, since the sync check is about
	// exactly the first bytes; which analyser follows the channel, ISDB-S3 not
	// being a transport stream. The pump goes on first, so the analyser sees the
	// stream as its consumer will: what the pump threw away for a consumer that
	// fell behind is a hole like any other.
	stream := watch(pump(opened), l.deliverySystem())
	l.streamMutex.Lock()
	l.stream = stream
	if l.open == nil {
		l.open = make(map[device.Stream]bool)
	}
	l.open[stream] = true
	l.streamMutex.Unlock()
	return &leaseStream{lease: l, Stream: stream}, nil
}

// closeStreams closes every stream the lease has open. The board calls it
// before closing the tuner or the board: a stream is read by a goroutine of its
// own, and closing the hardware under a read is what the drivers are not built
// for. Closing the stream stops that goroutine first.
func (l *Lease) closeStreams() {
	l.streamMutex.Lock()
	open := make([]device.Stream, 0, len(l.open))
	for stream := range l.open {
		open = append(open, stream)
	}
	l.open = nil
	l.stream = nil
	l.streamMutex.Unlock()
	for _, stream := range open {
		_ = stream.Close()
	}
}

// leaseDevice reports a device that has gone to the board that owns it.
type leaseDevice struct {
	device.Tuner
	lease *Lease
}

func (d *leaseDevice) Tune(properties map[string]string) error {
	return d.lease.check(d.Tuner.Tune(properties))
}

func (d *leaseDevice) Status() (device.Status, error) {
	status, err := d.Tuner.Status()
	return status, d.lease.check(err)
}

func (d *leaseDevice) OpenStream() (device.Stream, error) {
	stream, err := d.Tuner.OpenStream()
	if err != nil {
		return nil, d.lease.check(err)
	}
	return stream, nil
}

// check passes an error through, tearing the board down first if it says the
// hardware is no longer reachable.
func (l *Lease) check(err error) error {
	if err != nil && IsGone(err) {
		l.board.markGone()
	}
	return err
}

// leaseStream wraps the stream in the lease's bookkeeping, so closing it stops
// it being reported on.
type leaseStream struct {
	device.Stream
	lease *Lease
}

func (s *leaseStream) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := s.Stream.Read(ctx, buffer)
	return read, s.lease.check(err)
}

// Synced and StreamCounts forward the analyser's view. Written out rather than
// promoted because the embedded device.Stream names only two methods.
func (s *leaseStream) Synced() bool {
	watcher, ok := s.Stream.(*monitored)
	return ok && watcher.Synced()
}

func (s *leaseStream) StreamCounts() device.StreamCounts {
	watcher, ok := s.Stream.(*monitored)
	if !ok {
		return device.StreamCounts{}
	}
	return watcher.StreamCounts()
}

func (s *leaseStream) Close() error {
	s.lease.streamMutex.Lock()
	if s.lease.stream == s.Stream {
		s.lease.stream = nil
	}
	delete(s.lease.open, s.Stream)
	s.lease.streamMutex.Unlock()
	return s.Stream.Close()
}

// Release gives the tuner back.
func (l *Lease) Release() {
	l.once.Do(func() {
		l.cancel()
		l.board.release(l)
		l.board.pool.untrack(l.owner, l)
	})
}

// expire is Release for a lease whose board has gone: nothing can be handed
// back to absent hardware, so only the waiters are woken.
//
// The board's map of leases also holds the placeholder that claims a tuner's
// slot while its handle is being opened, and a board unplugged during that open
// is handed one here. It has neither a handle to close nor a context to cancel.
func (l *Lease) expire() {
	l.once.Do(func() {
		if l.cancel == nil {
			return
		}
		l.cancel()
		l.closeStreams()
		_ = l.device.Close()
		l.board.pool.untrack(l.owner, l)
	})
}

// CardHold keeps a board open for something using its card slot. It carries the
// generation it was taken at, so a board unplugged and put back is caught
// rather than talked to as if the conversation had continued: the card's
// protocol counts messages at both ends.
type CardHold struct {
	board      *board
	owner      Owner
	reader     device.CardReader
	generation uint64
	once       sync.Once
}

// Reader is the card interface.
func (h *CardHold) Reader() device.CardReader { return h.reader }

// Generation is the board's generation when this hold was taken.
func (h *CardHold) Generation() uint64 { return h.generation }

// Stale reports whether the board was reopened since this hold was taken, which
// means its reader belongs to hardware that has gone.
func (h *CardHold) Stale() bool {
	return h.board.Gone() || h.board.Generation() != h.generation
}

// Release lets the board go.
func (h *CardHold) Release() {
	h.once.Do(func() {
		h.board.drop(&h.board.cards)
		h.board.pool.untrack(h.owner, h)
	})
}

// leaseStream answers the stream-analysis contract, letting the stream endpoint
// ask what came out before it commits to a response.
var _ device.StreamAnalysis = (*leaseStream)(nil)
