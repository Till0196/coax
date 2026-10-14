// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"context"
	"time"

	"github.com/Till0196/coax/pkg/winscard"
)

// GetStatusChange waits for a card to be inserted or removed, or for the set
// of readers to change. No such interface raises an interrupt on insertion, so
// a wait is a poll however it is presented. The boards named in the array are
// opened for the duration of the wait and closed after: opening powers a board
// up and loads firmware into some of them, so doing it per poll would be worse
// than the wait itself.
func (c *providerContext) GetStatusChange(ctx context.Context, timeout time.Duration, states []winscard.ReaderState) ([]winscard.ReaderState, error) {
	answered := make([]winscard.ReaderState, len(states))
	copy(answered, states)
	if len(states) == 0 {
		return answered, nil
	}

	// watched pairs each entry with the slot behind it, or with nothing when
	// the entry is the PnP pseudo-reader or names a reader that is not here.
	watched := make([]*slot, len(states))
	for index, state := range states {
		if state.Reader == winscard.PnPNotification || state.Current.Has(winscard.StateIgnore) {
			continue
		}
		one, err := c.provider.find(state.Reader)
		if err != nil {
			continue
		}
		target := c.provider.slotFor(one)
		if err := target.open(); err != nil {
			continue
		}
		defer target.close()
		watched[index] = target
	}

	var expiry <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expiry = timer.C
	}
	poll := time.NewTicker(c.provider.config.PollInterval)
	defer poll.Stop()

	for {
		changed := false
		for index := range answered {
			if states[index].Current.Has(winscard.StateIgnore) {
				answered[index].Event = states[index].Current
				continue
			}
			event, atr := c.read(states[index], watched[index])
			answered[index].Event = event
			answered[index].ATR = atr
			if event.Has(winscard.StateChanged) {
				changed = true
			}
		}
		if changed || timeout == 0 {
			return answered, nil
		}
		select {
		case <-ctx.Done():
			return nil, winscard.ErrCancelled
		case <-expiry:
			return nil, winscard.ErrTimeout
		case <-poll.C:
		}
	}
}

// read works out one entry's event state.
func (c *providerContext) read(state winscard.ReaderState, target *slot) (winscard.State, []byte) {
	var event winscard.State
	var atr []byte
	switch {
	case state.Reader == winscard.PnPNotification:
		// The pseudo-reader carries the number of times the reader set has
		// changed, and nothing else. An application compares the count rather
		// than the flags.
		event = state.Current.WithCount(c.provider.readerSetGeneration())
	case target == nil:
		// Either no provider has this reader or its board could not be opened.
		// Both are things the caller is waiting to hear about.
		event = winscard.StateUnknown | winscard.StateUnavailable
	default:
		seen, err := target.observeOpen()
		if err != nil {
			event = winscard.StateUnavailable
			break
		}
		if seen.present {
			event = winscard.StatePresent
			atr = seen.atr
		} else {
			event = winscard.StateEmpty
		}
		if seen.held {
			event |= winscard.StateExclusive
		} else if seen.inUse {
			event |= winscard.StateInUse
		}
		event = event.WithCount(seen.events)
	}
	if event != state.Current {
		event |= winscard.StateChanged
	}
	return event, atr
}
