// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// hostState publishes transitions, never elapsed time or a heartbeat.
type hostState struct {
	mutex   sync.Mutex
	state   string
	changed chan struct{}
}

func newHostState() *hostState { return &hostState{state: "starting", changed: make(chan struct{})} }

func (s *hostState) set(state string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.state == state {
		return
	}
	s.state = state
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *hostState) current() (string, <-chan struct{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.state, s.changed
}

func (s *hostState) send(ctx context.Context, out io.WriteCloser) {
	defer out.Close()
	stop := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer stop()
	for {
		state, changed := s.current()
		if _, err := fmt.Fprintln(out, state); err != nil {
			return
		}
		if state == "closed" {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
	}
}
