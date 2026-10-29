// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import "context"

// Settlement is the result of startup repair. Cancellation or failure never
// grants permission to open hardware still held by the host.
type Settlement struct {
	done chan struct{}
	err  error
}

// NewSettlement follows completion or the daemon ending. A call still inside
// a live driver remains pending; elapsed time is not evidence that it failed.
func NewSettlement(ctx context.Context, repaired <-chan error) *Settlement {
	s := &Settlement{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		select {
		case s.err = <-repaired:
		case <-ctx.Done():
			s.err = ctx.Err()
		}
	}()
	return s
}

// Wait may be cancelled independently of other requests waiting for repair.
func (s *Settlement) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return s.err
	}
}

func (s *Supervisor) waitForSettlement(ctx context.Context, backend string) error {
	if !s.waitsToSettle(backend) {
		return ctx.Err()
	}
	return s.options.Settled.Wait(ctx)
}
