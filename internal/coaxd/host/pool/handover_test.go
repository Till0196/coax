// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/usbio"
)

// slowBoard is a board whose tuner takes a moment to let go, as real hardware
// does: stopping an endpoint and sleeping a demodulator are not instant.
type slowBoard struct {
	tuners []device.TunerInfo
	delay  time.Duration

	mutex sync.Mutex
	taken map[int]bool
	opens int
}

func (b *slowBoard) Name() string               { return "slow" }
func (b *slowBoard) Tuners() []device.TunerInfo { return b.tuners }
func (b *slowBoard) Close() error               { return nil }

func (b *slowBoard) OpenTuner(index int) (device.Tuner, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.taken == nil {
		b.taken = map[int]bool{}
	}
	if b.taken[index] {
		return nil, device.ErrTunerBusy
	}
	b.taken[index] = true
	b.opens++
	return &slowDevice{board: b, index: index}, nil
}

type slowDevice struct {
	board *slowBoard
	index int
}

func (d *slowDevice) Name() string                       { return "slow tuner" }
func (d *slowDevice) Supports(string) bool               { return true }
func (d *slowDevice) Tune(map[string]string) error       { return nil }
func (d *slowDevice) Status() (device.Status, error)     { return device.Status{Locked: true}, nil }
func (d *slowDevice) OpenStream() (device.Stream, error) { return nil, errors.New("no stream") }

func (d *slowDevice) Close() error {
	time.Sleep(d.board.delay)
	d.board.mutex.Lock()
	defer d.board.mutex.Unlock()
	delete(d.board.taken, d.index)
	return nil
}

type slowRegistry struct {
	detected []Detected
	board    *slowBoard
}

func (r *slowRegistry) Detect() ([]Detected, error) { return r.detected, nil }
func (r *slowRegistry) Open(Detected, device.Options) (device.Board, error) {
	return r.board, nil
}

// newSlowPool is one tuner that takes its time going away.
func newSlowPool(t *testing.T, delay time.Duration) (*Supervisor, *slowBoard) {
	t.Helper()
	tuners := []device.TunerInfo{{Index: 0, Name: "R850", Deliveries: []string{"ISDB-T"}}}
	board := &slowBoard{tuners: tuners, delay: delay}
	registry := &slowRegistry{board: board, detected: []Detected{{
		Backend: "px4", Description: "one tuner", Selector: "001:007",
		Location: "001:007", Serial: "ABC123", Port: "1-2",
		USB:        usbio.ID{Vendor: 0x0511, Product: 0x083f},
		Deliveries: []string{"ISDB-T"},
		Tuners:     tuners,
	}}}
	pool := New(registry, Options{Linger: LingerForever})
	t.Cleanup(func() { _ = pool.Close() })
	return pool, board
}

// TestATunerBeingGivenBackIsWaitedFor is the gap a scan falls into: stopping a
// stream closes the connection and gives the tuner back, and the next channel
// arrives on a new connection while the hardware is still shutting. Nobody else
// wants it, so the answer is to wait rather than refuse.
func TestATunerBeingGivenBackIsWaitedFor(t *testing.T) {
	pool, board := newSlowPool(t, 300*time.Millisecond)

	first := pool.NewOwner()
	lease, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:0", Owner: first})
	if err != nil {
		t.Fatalf("take the tuner: %v", err)
	}

	// The connection goes, giving the lease back, and the next channel asks for
	// the same tuner straight away.
	go pool.ReleaseOwner(first)
	time.Sleep(20 * time.Millisecond)

	second := pool.NewOwner()
	started := time.Now()
	again, err := pool.Lease(Want{TunerID: lease.ID(), Owner: second})
	if err != nil {
		t.Fatalf("take the tuner again: %v", err)
	}
	defer again.Release()
	if waited := time.Since(started); waited < 100*time.Millisecond {
		t.Errorf("the second request was answered in %v, before the first had finished letting go", waited)
	}
	if opens := board.opens; opens != 2 {
		t.Errorf("the tuner was opened %d times, want 2", opens)
	}
}

// TestATunerSomeoneElseIsUsingIsRefusedAtOnce is the other half: waiting is for
// a tuner on its way back, not one in use -- a caller that would wait out a
// whole recording would rather be told now.
func TestATunerSomeoneElseIsUsingIsRefusedAtOnce(t *testing.T) {
	pool, _ := newSlowPool(t, time.Second)

	first := pool.NewOwner()
	lease, err := pool.Lease(Want{TunerID: "px4:ABC123@1-2:0", Owner: first})
	if err != nil {
		t.Fatalf("take the tuner: %v", err)
	}
	defer lease.Release()

	started := time.Now()
	if _, err := pool.Lease(Want{TunerID: lease.ID(), Owner: pool.NewOwner()}); !errors.Is(err, device.ErrTunerBusy) {
		t.Fatalf("asking for a tuner in use answered %v, want it to be refused", err)
	}
	if waited := time.Since(started); waited > 100*time.Millisecond {
		t.Errorf("being refused took %v; a tuner in use is an answer, not a wait", waited)
	}
}
