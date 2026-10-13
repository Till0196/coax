// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package daemoncard is the winscard provider that reaches a card in another
// process, or on another machine, over the daemon's HTTP interface.
//
// It is the third of the three: usbcard drives a slot built into a board this
// process has opened, native forwards to the resource manager the system
// already runs, and this one forwards to coaxd. A caller of winscard.Service
// cannot tell them apart.
//
// A card belongs to the HTTP connection it was opened on, so each open card
// here carries a connection of its own and the daemon needs to remember
// nothing.
package daemoncard

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/winscard"
	"github.com/Till0196/coax/pkg/host"
)

// Provider is the readers one daemon has.
type Provider struct {
	client *host.Client
	// generation counts the times the reader set has changed, which is what the
	// PnP pseudo-reader carries. It is this process's own count: the daemon does
	// not publish one, and what an application compares is whether the number
	// moved.
	mutex      sync.Mutex
	generation uint16
	known      []string
}

// New builds a provider over a client already talking to a daemon.
func New(client *host.Client) *Provider { return &Provider{client: client} }

func (p *Provider) Name() string { return "daemon" }

func (p *Provider) EstablishContext(winscard.Scope) (winscard.ProviderContext, error) {
	return &providerContext{provider: p}, nil
}

// observe notes the reader set, moving the generation when it has changed.
func (p *Provider) observe(readers []string) uint16 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if len(readers) != len(p.known) {
		p.generation++
	} else {
		for index, name := range readers {
			if p.known[index] != name {
				p.generation++
				break
			}
		}
	}
	p.known = append(p.known[:0:0], readers...)
	return p.generation
}

func (p *Provider) readerSetGeneration() uint16 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.generation
}

// providerContext is one application context's half of this provider. The
// daemon has no notion of a context -- what it tracks is connections, one per
// open card -- so this holds the cards opened under it and closes them
// together.
type providerContext struct {
	provider *Provider

	mutex    sync.Mutex
	released bool
	cards    map[*card]bool
}

func (c *providerContext) Readers([]string) ([]string, error) {
	// Groups are not passed on: the daemon publishes one group, and asking for
	// a named one that it does not have would lose every reader.
	listed, err := c.provider.client.Cards(context.Background())
	if err != nil {
		return nil, translate(err)
	}
	readers := make([]string, 0, len(listed))
	for _, one := range listed {
		readers = append(readers, one.Reader)
	}
	c.provider.observe(readers)
	return readers, nil
}

func (c *providerContext) ReaderGroups() ([]string, error) {
	return []string{winscard.AllReaderGroups}, nil
}

func (c *providerContext) Connect(reader string, share winscard.ShareMode, protocols winscard.Protocol) (winscard.Card, winscard.Protocol, error) {
	switch share {
	case winscard.ShareShared, winscard.ShareExclusive:
	default:
		// ShareDirect is a reader-level channel with no card behind it, which
		// these readers do not have; the daemon has no way to open one either.
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidValue
	}
	if !protocols.Has(winscard.ProtocolT1) {
		return nil, winscard.ProtocolUndefined, winscard.ErrProtoMismatch
	}

	c.mutex.Lock()
	released := c.released
	c.mutex.Unlock()
	if released {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidHandle
	}

	opened, err := c.provider.client.OpenCard(context.Background(), reader)
	if err != nil {
		return nil, winscard.ProtocolUndefined, translate(err)
	}
	held := &card{context: c, conn: opened, reader: reader}
	c.track(held)
	return held, winscard.ProtocolT1, nil
}

func (c *providerContext) track(one *card) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.cards == nil {
		c.cards = make(map[*card]bool)
	}
	c.cards[one] = true
}

func (c *providerContext) forget(one *card) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.cards, one)
}

func (c *providerContext) Release() error {
	c.mutex.Lock()
	if c.released {
		c.mutex.Unlock()
		return nil
	}
	c.released = true
	open := make([]*card, 0, len(c.cards))
	for one := range c.cards {
		open = append(open, one)
	}
	c.cards = nil
	c.mutex.Unlock()

	for _, one := range open {
		_ = one.close(winscard.LeaveCard)
	}
	return nil
}

// GetStatusChange waits for one of the named readers to move.
//
// The waiting happens at the daemon, which is next to the hardware: this asks
// for the event count to leave what the caller believes and the answer comes
// back when it does. Reading the state here on a timer would put a poll on the
// wire for every tick of an idle card.
func (c *providerContext) GetStatusChange(ctx context.Context, timeout time.Duration, states []winscard.ReaderState) ([]winscard.ReaderState, error) {
	answered := make([]winscard.ReaderState, len(states))
	copy(answered, states)
	if len(states) == 0 {
		return answered, nil
	}

	deadline := ctx
	if timeout > 0 {
		var stop context.CancelFunc
		deadline, stop = context.WithTimeout(ctx, timeout)
		defer stop()
	}

	// Each reader is waited on at once, since the first to move ends the wait
	// for all of them.
	type moved struct {
		index int
		state winscard.State
		atr   []byte
	}
	for {
		results := make([]moved, len(states))
		var group sync.WaitGroup
		for index, state := range states {
			if state.Current.Has(winscard.StateIgnore) {
				results[index] = moved{index: index, state: state.Current}
				continue
			}
			group.Add(1)
			go func(index int, state winscard.ReaderState) {
				defer group.Done()
				event, atr := c.look(deadline, state, timeout == 0)
				results[index] = moved{index: index, state: event, atr: atr}
			}(index, state)
		}
		group.Wait()

		changed := false
		for index, one := range results {
			answered[index].Event = one.state
			answered[index].ATR = one.atr
			if one.state.Has(winscard.StateChanged) {
				changed = true
			}
		}
		if changed || timeout == 0 {
			return answered, nil
		}
		select {
		case <-ctx.Done():
			return nil, winscard.ErrCancelled
		case <-deadline.Done():
			return nil, winscard.ErrTimeout
		default:
		}
	}
}

// look works out one entry's event state, waiting at the daemon unless the
// caller asked only for what the state is now.
func (c *providerContext) look(ctx context.Context, state winscard.ReaderState, now bool) (winscard.State, []byte) {
	var event winscard.State
	var atr []byte
	switch {
	case state.Reader == winscard.PnPNotification:
		// The pseudo-reader carries the number of times the reader set has
		// changed and nothing else. Re-listing is what notices that, and it is
		// cheap: the daemon answers it without opening a board.
		if readers, err := c.Readers(nil); err == nil {
			event = state.Current.WithCount(c.provider.observe(readers))
		} else {
			event = state.Current.WithCount(c.provider.readerSetGeneration())
		}
	default:
		wait := time.Duration(0)
		if !now {
			wait = waitAtTheDaemon
		}
		seen, err := c.provider.client.CardEvents(ctx, state.Reader, uint32(state.Current.Count()), wait)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return state.Current, state.ATR
			}
			event = winscard.StateUnknown | winscard.StateUnavailable
			break
		}
		if seen.Present {
			event = winscard.StatePresent
			atr, _ = host.ParseHex(seen.ATR)
		} else {
			event = winscard.StateEmpty
		}
		switch {
		case seen.Sharing < 0:
			event |= winscard.StateExclusive
		case seen.Sharing > 0:
			event |= winscard.StateInUse
		}
		event = event.WithCount(uint16(seen.Events))
	}
	if event != state.Current {
		event |= winscard.StateChanged
	}
	return event, atr
}

// waitAtTheDaemon is how long one request asks the daemon to hold a wait open
// for. The daemon has a ceiling of its own and will answer sooner; asking for
// longer costs nothing and means this side is not the one deciding how often
// the wire carries a question.
const waitAtTheDaemon = time.Minute
