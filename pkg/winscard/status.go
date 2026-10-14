// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winscard

import (
	"time"
)

// GetStatusChange waits until one of the given readers is no longer in the
// state the caller believes it is in, SCardGetStatusChange. Every entry comes
// back with Event filled and the entries that moved carry StateChanged; a
// timeout of zero polls and returns at once.
//
// The array is split by owning provider and the pieces waited on concurrently,
// since one array routinely mixes a built-in slot with an external reader. The
// first provider to report a change ends the wait, and entries whose provider
// had not answered come back unchanged.
//
// PnPNotification goes to every provider, as any of them may be the one whose
// reader set changed. A reader no provider claims is answered StateUnknown |
// StateChanged.
func (s *Service) GetStatusChange(handle ContextHandle, timeout time.Duration, states []ReaderState) ([]ReaderState, error) {
	entry, err := s.lookupContext(handle)
	if err != nil {
		return nil, err
	}

	result := make([]ReaderState, len(states))
	copy(result, states)
	for index := range result {
		result[index].Event = result[index].Current
	}
	if len(states) == 0 {
		return result, nil
	}

	// assignment[p] lists the positions in the caller's array that provider p
	// is asked about, so its answers can be put back in place.
	assignment := make(map[int][]int)
	immediate := false
	for index, state := range states {
		if state.Current.Has(StateIgnore) {
			continue
		}
		if state.Reader == PnPNotification {
			for provider := range s.providers {
				assignment[provider] = append(assignment[provider], index)
			}
			continue
		}
		provider, _, locateErr := entry.locate(state.Reader)
		if locateErr != nil {
			result[index].Event = StateUnknown | StateChanged
			result[index].ATR = nil
			immediate = true
			continue
		}
		assignment[provider] = append(assignment[provider], index)
	}
	if immediate {
		return result, nil
	}
	if len(assignment) == 0 {
		if timeout == 0 {
			return result, nil
		}
		return nil, ErrTimeout
	}

	waitCtx, stop, err := entry.waitContext()
	if err != nil {
		return nil, err
	}
	defer stop()

	type answer struct {
		provider int
		states   []ReaderState
		err      error
	}
	answers := make(chan answer, len(assignment))
	for provider, positions := range assignment {
		providerContext, bindErr := entry.bind(provider)
		if bindErr != nil {
			answers <- answer{provider: provider, err: bindErr}
			continue
		}
		request := make([]ReaderState, len(positions))
		for offset, position := range positions {
			request[offset] = states[position]
			request[offset].Event = 0
		}
		go func(provider int, providerContext ProviderContext, request []ReaderState) {
			answered, waitErr := providerContext.GetStatusChange(waitCtx, timeout, request)
			answers <- answer{provider: provider, states: answered, err: waitErr}
		}(provider, providerContext, request)
	}

	expiry, cancelTimer := deadlineFor(timeout)
	defer cancelTimer()

	var (
		firstErr  error
		succeeded bool
		pending   = len(assignment)
	)
	for pending > 0 {
		select {
		case <-waitCtx.Done():
			return nil, ErrCancelled
		case <-expiry:
			return nil, ErrTimeout
		case received := <-answers:
			pending--
			if received.err != nil {
				if firstErr == nil {
					firstErr = received.err
				}
				continue
			}
			succeeded = true
			changed := false
			positions := assignment[received.provider]
			for offset, state := range received.states {
				if offset >= len(positions) {
					break
				}
				position := positions[offset]
				// A position shared between providers -- only
				// PnPNotification -- keeps the first change reported.
				if result[position].Event.Has(StateChanged) {
					continue
				}
				result[position].Event = state.Event
				result[position].ATR = state.ATR
				if state.Event.Has(StateChanged) {
					changed = true
				}
			}
			if changed {
				return result, nil
			}
		}
	}
	if !succeeded && firstErr != nil {
		return nil, firstErr
	}
	// Every provider answered without a change: the expected end of a poll,
	// and for a wait it means they finished early, so report a timeout.
	if timeout == 0 {
		return result, nil
	}
	return nil, ErrTimeout
}
