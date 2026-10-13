// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

// held is one thing an owner has taken and can give back.
type held interface{ Release() }

// track records that an owner has taken something. A client goes away without
// saying so -- a recording stops by its connection closing -- and a tuner left
// held would be lost until the daemon restarted. The owner is the connection, so
// everything it took goes back when the connection does.
func (s *Supervisor) track(owner Owner, thing held) {
	if owner.id == 0 {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.holdings == nil {
		s.holdings = make(map[uint64][]held)
	}
	s.holdings[owner.id] = append(s.holdings[owner.id], thing)
}

// untrack forgets one thing an owner gave back of its own accord.
func (s *Supervisor) untrack(owner Owner, thing held) {
	if owner.id == 0 {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	things := s.holdings[owner.id]
	for index, candidate := range things {
		if candidate != thing {
			continue
		}
		s.holdings[owner.id] = append(things[:index], things[index+1:]...)
		break
	}
	if len(s.holdings[owner.id]) == 0 {
		delete(s.holdings, owner.id)
	}
}

// ReleaseOwner gives back everything one owner took. It is what a closed
// connection triggers, and is safe for an owner that holds nothing.
func (s *Supervisor) ReleaseOwner(owner Owner) {
	if owner.id == 0 {
		return
	}
	s.mutex.Lock()
	things := s.holdings[owner.id]
	delete(s.holdings, owner.id)
	s.mutex.Unlock()

	// Released outside the pool's lock: giving a tuner back reaches into the
	// board, which takes its own.
	for _, thing := range things {
		thing.Release()
	}
}

// Held reports how many things an owner holds, for a caller deciding whether
// anything is still using the daemon.
func (s *Supervisor) Held(owner Owner) int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return len(s.holdings[owner.id])
}
