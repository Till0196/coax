// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/usbio"
)

// board is one physical board: the shared device.Board value and the
// bookkeeping that decides when it is open. One exists per attached board
// whether or not anything uses it.
//
// The lock covers bookkeeping only. Opening is slow -- several models take
// firmware -- so the opener drops the lock and leaves opening set; anything
// arriving meanwhile waits on the condition rather than starting a second open.
type board struct {
	pool  *Supervisor
	entry entry

	mutex sync.Mutex
	ready *sync.Cond

	opening bool
	opened  device.Board
	reader  device.CardReader
	tuners  []device.TunerInfo
	probed  bool

	// leases, cards and watchers are the three reasons a board is kept open,
	// counted apart so a change in one cannot be mistaken for another. They
	// mirror the counts px4_drv's kernel driver keeps for the same purposes.
	leases   map[int]*Lease
	cards    int
	watchers int

	// handover holds, per tuner, a channel closed when a release under way has
	// finished. A tuner stays in leases until its handle is really shut, so
	// nothing is handed a tuner the driver still has.
	handover map[int]chan struct{}

	// generation counts opens; anything from an earlier generation is held
	// against hardware that has since gone away.
	generation uint64
	gone       bool

	linger *time.Timer
}

func newBoard(pool *Supervisor, one entry) *board {
	created := &board{
		pool:     pool,
		entry:    one,
		leases:   make(map[int]*Lease),
		handover: make(map[int]chan struct{}),
	}
	created.ready = sync.NewCond(&created.mutex)
	return created
}

// users is how many reasons there are to keep the board open.
func (b *board) users() int { return len(b.leases) + b.cards + b.watchers }

// acquire opens the board if it is not open. Called, and returning, with the
// lock held, by a caller already counted among the users. The lock is dropped
// while the open runs, so a second caller finds opening set and waits rather
// than asking the hardware twice.
func (b *board) acquire() error {
	for {
		if b.gone {
			return ErrGone
		}
		if b.opened != nil {
			return nil
		}
		if b.opening {
			b.ready.Wait()
			continue
		}
		b.opening = true
		b.mutex.Unlock()

		// Whatever the daemon had to put right on this backend. Waited for
		// here rather than before serving, so that everything which does not
		// touch the hardware being repaired is answered while it runs.
		if b.pool.waitsToSettle(b.entry.key.Backend) {
			<-b.pool.options.Settled
		}
		opened, err := b.pool.registry.Open(b.entry.detected, device.Options{
			Selector:    b.entry.key.Selector,
			FirmwareDir: b.pool.options.FirmwareDir,
		})

		b.mutex.Lock()
		b.opening = false
		if err != nil {
			b.ready.Broadcast()
			if errors.Is(err, usbio.ErrGone) {
				b.gone = true
				return ErrGone
			}
			return fmt.Errorf("open %s on %s: %w", b.entry.key.Backend, b.entry.key.Selector, err)
		}
		// Closing while this open was in flight leaves nothing to hand back.
		if b.gone {
			b.mutex.Unlock()
			_ = opened.Close()
			b.mutex.Lock()
			b.ready.Broadcast()
			return ErrGone
		}
		b.opened = opened
		b.tuners = opened.Tuners()
		b.probed = true
		b.reader, _ = opened.(device.CardReader)
		b.generation++
		b.ready.Broadcast()
		return nil
	}
}

// hold counts one more user and opens the board; a caller that gets an error is
// not counted.
func (b *board) hold(count *int) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	*count++
	b.stopLinger()
	if err := b.acquire(); err != nil {
		*count--
		b.startLinger()
		return err
	}
	return nil
}

// drop gives one user's hold back, starting the board out if it was the last.
func (b *board) drop(count *int) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if *count > 0 {
		*count--
	}
	b.startLinger()
}

// startLinger closes the board once nothing has wanted it for a while. Called
// with the lock held.
func (b *board) startLinger() {
	if b.users() != 0 || b.opened == nil || b.gone {
		return
	}
	if b.entry.detected.Exclusive {
		// Lingering on an exclusive board is a lock held against every other
		// program on the machine, not a head start for the next caller.
		b.closeLocked()
		return
	}
	if b.pool.options.Linger < 0 {
		return
	}
	if b.pool.options.Linger == 0 {
		b.closeLocked()
		return
	}
	if b.linger != nil {
		return
	}
	b.linger = time.AfterFunc(b.pool.options.Linger, b.closeIfIdle)
}

// stopLinger calls off a pending close. Called with the lock held.
func (b *board) stopLinger() {
	if b.linger != nil {
		b.linger.Stop()
		b.linger = nil
	}
}

func (b *board) closeIfIdle() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.linger = nil
	if b.users() != 0 {
		return
	}
	b.closeLocked()
}

// closeLocked drops the board handle, with the lock held. Tuners a caller still
// holds keep the USB device alive through the driver's own reference count, so
// this is only the pool letting go of its share.
func (b *board) closeLocked() {
	opened := b.opened
	if opened == nil {
		return
	}
	b.opened = nil
	b.reader = nil
	b.probed = false
	b.mutex.Unlock()
	_ = opened.Close()
	b.mutex.Lock()
}

// markGone reports that the hardware has been unplugged. Every holder is told
// at once and nothing is retried, so a recording sees its data stop rather than
// hang. A board that comes back is a new board with a new generation.
func (b *board) markGone() {
	b.mutex.Lock()
	if b.gone {
		b.mutex.Unlock()
		return
	}
	b.gone = true
	b.generation++
	b.stopLinger()
	leases := make([]*Lease, 0, len(b.leases))
	for _, lease := range b.leases {
		leases = append(leases, lease)
	}
	b.leases = make(map[int]*Lease)
	// Anything waiting for a tuner is waiting for hardware that has gone.
	for index, done := range b.handover {
		delete(b.handover, index)
		close(done)
	}
	b.cards = 0
	b.watchers = 0
	opened := b.opened
	b.opened = nil
	b.reader = nil
	b.probed = false
	b.ready.Broadcast()
	b.mutex.Unlock()

	// The streams first, then the board, then the tuners: nothing may be
	// reading when the hardware under it is closed.
	for _, lease := range leases {
		lease.closeStreams()
	}
	if opened != nil {
		_ = opened.Close()
	}
	for _, lease := range leases {
		lease.expire()
	}
}

// shutdown closes the board because the pool is going away, not because the
// hardware did.
func (b *board) shutdown() { b.markGone() }

// fail decides what a hardware failure means for the board as a whole, and
// reports the error the caller should see.
func (b *board) fail(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, usbio.ErrGone) {
		b.markGone()
		return ErrGone
	}
	return err
}

// Generation is how many times this board has been opened; anything from an
// earlier generation is stale.
func (b *board) Generation() uint64 {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.generation
}

func (b *board) Gone() bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.gone
}

// Open reports whether the pool currently holds the board open.
func (b *board) Open() bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.opened != nil
}

// watch keeps the board open while a caller looks at it, and returns the stop.
func (b *board) watch() (func(), error) {
	if err := b.hold(&b.watchers); err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { b.drop(&b.watchers) }) }, nil
}

// layout is the board's tuners: what the backend could say from the
// identifiers, or what the board itself reported once it was open.
func (b *board) layout() []device.TunerInfo {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.probed && len(b.tuners) != 0 {
		probed := expandTuners(b.tuners)
		// What a channel may set is known from the model, which is what the layout
		// before opening was worked out from. A board that says nothing of it when
		// opened keeps what the model said.
		for index := range probed {
			if probed[index].Controls != (device.Controls{}) {
				continue
			}
			for _, known := range b.entry.tuners {
				if known.Index == probed[index].Index {
					probed[index].Controls = known.Controls
				}
			}
		}
		return probed
	}
	return append([]device.TunerInfo(nil), b.entry.tuners...)
}

// lease takes one tuner of the board for an owner, at a priority.
//
// preempt allows the tuner to be taken from work that asked for it at a lower
// priority. It is never set on the first look over the boards: only once
// nothing is free is a holder worth taking one from.
func (b *board) lease(index int, owner Owner, priority int, preempt bool) (*Lease, error) {
	b.mutex.Lock()
	b.waitForHandover(index)
	if held, taken := b.leases[index]; taken {
		victim := b.evictable(held, priority, preempt)
		if victim == nil {
			b.mutex.Unlock()
			return nil, device.ErrTunerBusy
		}
		victim.preempted = true
		b.mutex.Unlock()
		// Outside the lock: giving a tuner back closes its handle, which is
		// the driver's own pace, and Release takes this lock itself.
		victim.Release()
		b.mutex.Lock()
		// The tuner went through a handover on its way out, and something else may
		// have been waiting for it. Losing the race is being told busy: whoever took
		// it is either the same rank or higher.
		b.waitForHandover(index)
		if _, taken := b.leases[index]; taken {
			b.mutex.Unlock()
			return nil, device.ErrTunerBusy
		}
	}
	// The slot is claimed before the open so two callers racing for the same
	// tuner cannot both be told they have it.
	placeholder := &Lease{board: b, owner: owner, index: index, priority: priority}
	b.leases[index] = placeholder
	b.stopLinger()
	if err := b.acquire(); err != nil {
		delete(b.leases, index)
		b.startLinger()
		b.mutex.Unlock()
		return nil, err
	}
	opened := b.opened
	generation := b.generation
	b.mutex.Unlock()

	device, err := opened.OpenTuner(index)
	if err != nil {
		b.mutex.Lock()
		delete(b.leases, index)
		b.startLinger()
		b.mutex.Unlock()
		return nil, b.fail(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	lease := &Lease{
		board:      b,
		owner:      owner,
		index:      index,
		generation: generation,
		device:     device,
		priority:   priority,
		ctx:        ctx,
		cancel:     cancel,
	}

	b.mutex.Lock()
	if b.gone {
		delete(b.leases, index)
		b.mutex.Unlock()
		_ = device.Close()
		cancel()
		return nil, ErrGone
	}
	b.leases[index] = lease
	b.mutex.Unlock()
	b.pool.track(owner, lease)
	return lease, nil
}

// evictable reports which lease, if any, a request at this priority may take
// this tuner from, and nil when it may not.
//
// Called with the lock held. Equal priorities do not evict: whoever asked first
// keeps it.
//
// A lease still being opened is never taken. Its handle does not exist yet, so
// there is nothing to close and nothing to tell; the request that claimed the
// slot is told busy and may ask again a moment later.
func (b *board) evictable(held *Lease, priority int, preempt bool) *Lease {
	if !preempt || held == nil || held.cancel == nil {
		return nil
	}
	if priority <= held.priority {
		return nil
	}
	return held
}

// leasedPriority is what the work holding a tuner said it was worth, and
// whether anything holds it at all. A tuner mid-open counts as held at a
// priority nothing can outrank, so that the search passes it over rather than
// settling on one it cannot have.
func (b *board) leasedPriority(index int) (int, bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	held, taken := b.leases[index]
	if !taken {
		return 0, false
	}
	if held.cancel == nil {
		return math.MaxInt, true
	}
	return held.priority, true
}

// handoverGrace is how long a request waits for the previous release to finish.
// It is not a queue for a tuner someone else is using, but the gap between a
// client letting a tuner go and the hardware having let go: the lease returns
// when the connection closes, while closing a tuner stops an endpoint and puts
// a demodulator to sleep. A scan makes the gap matter -- the next channel
// arrives milliseconds later.
const handoverGrace = 5 * time.Second

// waitForHandover waits for a release of this tuner already under way. Called
// and returning with the lock held, whether the release finished or the grace
// ran out.
func (b *board) waitForHandover(index int) {
	done, releasing := b.handover[index]
	if !releasing {
		return
	}
	b.mutex.Unlock()
	timer := time.NewTimer(handoverGrace)
	select {
	case <-done:
	case <-timer.C:
	}
	timer.Stop()
	b.mutex.Lock()
}

// release gives a tuner back. It stays in leases while its handle is closing:
// removing it first would say it was free while the driver still had it, and
// the next request would be refused by the hardware instead of waiting.
func (b *board) release(lease *Lease) {
	b.mutex.Lock()
	if held, ok := b.leases[lease.index]; !ok || held != lease {
		b.mutex.Unlock()
		return
	}
	done := make(chan struct{})
	b.handover[lease.index] = done
	b.mutex.Unlock()

	lease.closeStreams()
	_ = lease.device.Close()

	b.mutex.Lock()
	// Something may have given up waiting and been told busy, but nothing can
	// have taken it: it was in leases throughout.
	if held, ok := b.leases[lease.index]; ok && held == lease {
		delete(b.leases, lease.index)
	}
	// markGone ends every handover itself, so this channel is ours to close only
	// while it is still the one recorded.
	ours := b.handover[lease.index] == done
	if ours {
		delete(b.handover, lease.index)
	}
	b.startLinger()
	b.mutex.Unlock()
	if ours {
		close(done)
	}
}

func (b *board) card(owner Owner) (*CardHold, error) {
	if err := b.hold(&b.cards); err != nil {
		return nil, err
	}
	b.mutex.Lock()
	reader := b.reader
	generation := b.generation
	b.mutex.Unlock()
	if reader == nil {
		b.drop(&b.cards)
		return nil, ErrNoCardReader
	}
	hold := &CardHold{board: b, owner: owner, reader: reader, generation: generation}
	b.pool.track(owner, hold)
	return hold, nil
}
