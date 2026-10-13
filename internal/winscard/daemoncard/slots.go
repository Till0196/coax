// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemoncard

import (
	"context"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/pcscd"
	"github.com/Till0196/coax/pkg/host"
)

// Slots is the reader table's view of a daemon's slots.
//
// The table looks at every slot it is watching on a timer, which is right next
// to hardware and wrong across a network: a card that has not moved in hours
// would still cost a request every tick. So a slot opened here starts a wait at
// the daemon instead -- one request that does not answer until something moves
// -- and the table's looks are served from that.
type Slots struct {
	client *host.Client
}

// FromClient adapts a daemon's cards to the reader table.
func FromClient(client *host.Client) Slots { return Slots{client: client} }

func (s Slots) Readers() ([]string, error) {
	listed, err := s.client.Cards(context.Background())
	if err != nil {
		return nil, err
	}
	readers := make([]string, 0, len(listed))
	for _, one := range listed {
		readers = append(readers, one.Reader)
	}
	return readers, nil
}

func (s Slots) Open(reader string) (pcscd.Slot, error) {
	// Nothing is opened at the daemon: what a slot costs there is decided by
	// the wait below, and a client that lists readers should not power a card
	// up. The first look is taken now so a table that asks straight away is
	// answered with the truth rather than with nothing.
	watched := &slot{client: s.client, reader: reader, done: make(chan struct{})}
	state, err := s.client.CardEvents(context.Background(), reader, 0, 0)
	if err != nil {
		return nil, err
	}
	watched.remember(state)
	go watched.wait()
	return watched, nil
}

// slot is one slot being watched, with the last thing the daemon said about it.
type slot struct {
	client *host.Client
	reader string

	mutex   sync.Mutex
	present bool
	atr     []byte
	sharing int
	events  uint32

	closeOnce sync.Once
	done      chan struct{}
}

func (s *slot) remember(state host.CardState) {
	atr, _ := host.ParseHex(state.ATR)
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.present, s.atr, s.sharing, s.events = state.Present, atr, state.Sharing, uint32(state.Events)
}

// wait holds a request open at the daemon until the slot moves, and asks again.
func (s *slot) wait() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		<-s.done
		stop()
	}()
	for {
		s.mutex.Lock()
		since := s.events
		s.mutex.Unlock()

		state, err := s.client.CardEvents(ctx, s.reader, since, watchWait)
		select {
		case <-s.done:
			return
		default:
		}
		if err != nil {
			// Nothing answered. Waiting a moment before asking again keeps a
			// daemon that has gone from being asked in a tight loop; the table
			// meanwhile reports the last state that was true.
			select {
			case <-s.done:
				return
			case <-time.After(retryAfter):
			}
			continue
		}
		s.remember(state)
	}
}

func (s *slot) Present() (bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.present, nil
}

func (s *slot) ATR() []byte {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]byte(nil), s.atr...)
}

func (s *slot) Sharing() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.sharing
}

func (s *slot) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	return nil
}

const (
	// watchWait is how long one request asks the daemon to hold the wait open.
	// The daemon has a ceiling of its own and answers sooner; what this decides
	// is only that this side is not the one setting the pace.
	watchWait = time.Minute
	// retryAfter is how long to leave a daemon that is not answering alone.
	retryAfter = time.Second
)
