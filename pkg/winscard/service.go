// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package winscard is the WinSCard API, shaped so that the smart card
// interfaces built into tuner boards can be reached through it.
//
// Those interfaces are not PC/SC devices: they hang off a USB bridge as a UART
// or a vendor-specific block interface, and the T=1 conversation is assembled
// on the host. The protocol is in internal/coaxd/usb/card; what is missing is
// the shape applications expect it in, WinSCard on Windows and pcsc-lite
// elsewhere.
//
// This package is deliberately a proxy rather than a driver: a Service holds an
// ordered list of providers and an application sees a single merged set.
// Loading this in place of the system implementation must not cost a user the
// external reader they already had.
//
// What is here is the part that is the same whichever provider answers: handles
// that hide which provider a caller reached, enumeration order, routing by
// reader name, cancellation, and the status codes. The contract follows
// px4_drv's WinSCard_PX4.
package winscard

import (
	"context"
	"sync"
	"time"
)

// ContextHandle is the handle EstablishContext returns, SCARDCONTEXT in
// winscard.h. It is this package's own, not a provider's: the value does not
// reveal which provider was reached, and the handle tables are the only
// mapping. Zero is never valid.
type ContextHandle uint64

// CardHandle is the handle Connect returns, SCARDHANDLE in winscard.h. The
// same rules as ContextHandle apply to it.
type CardHandle uint64

// Service is one instance of the API, holding the providers and the handles
// issued against them. Safe for concurrent use: the API it presents is called
// from several threads of one application at once.
type Service struct {
	providers []Provider

	mutex      sync.Mutex
	nextHandle uint64
	contexts   map[ContextHandle]*serviceContext
	cards      map[CardHandle]*serviceCard
}

// serviceContext is one application context: a set of provider contexts, the
// cards opened under them, and the cancellation state of its waits.
type serviceContext struct {
	service *Service
	handle  ContextHandle
	scope   Scope

	mutex    sync.Mutex
	released bool
	// bound holds the provider contexts established so far, indexed by the
	// provider's position in the service.
	bound map[int]ProviderContext
	cards map[CardHandle]*serviceCard
	// cancel is closed by Cancel and replaced afterwards, so that a cancel
	// unblocks the waits already running and does not condemn later ones.
	cancel chan struct{}
}

// serviceCard is one card connection and the provider context it belongs to.
type serviceCard struct {
	owner    *serviceContext
	handle   CardHandle
	provider int
	reader   string
	card     Card
}

// NewService builds the API over the given providers, in enumeration order:
// built-in readers first, the provider forwarding to the platform's own
// resource manager after it. The order is fixed because clients routinely take
// the first reader with a card in it.
func NewService(providers ...Provider) *Service {
	return &Service{
		providers: providers,
		contexts:  make(map[ContextHandle]*serviceContext),
		cards:     make(map[CardHandle]*serviceCard),
	}
}

// EstablishContext opens a context on the resource manager,
// SCardEstablishContext. No provider is contacted here; each is established on
// the first call that needs it, so an unavailable provider does not fail a call
// meant for the other one.
func (s *Service) EstablishContext(scope Scope) (ContextHandle, error) {
	switch scope {
	case ScopeUser, ScopeTerminal, ScopeSystem, ScopeGlobal:
	default:
		return 0, ErrInvalidValue
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.nextHandle++
	handle := ContextHandle(s.nextHandle)
	s.contexts[handle] = &serviceContext{
		service: s,
		handle:  handle,
		scope:   scope,
		bound:   make(map[int]ProviderContext),
		cards:   make(map[CardHandle]*serviceCard),
		cancel:  make(chan struct{}),
	}
	return handle, nil
}

// IsValidContext reports whether the handle is still one this service issued,
// SCardIsValidContext.
func (s *Service) IsValidContext(handle ContextHandle) error {
	_, err := s.lookupContext(handle)
	return err
}

// ReleaseContext closes a context, SCardReleaseContext. Everything opened under
// it goes too: connected cards are disconnected with LeaveCard, running waits
// are unblocked, provider contexts are released. The order matters -- cards,
// then waits, then providers -- because a provider released while one of its
// cards is still open would have to invent an answer for the disconnect that
// follows.
func (s *Service) ReleaseContext(handle ContextHandle) error {
	s.mutex.Lock()
	entry, ok := s.contexts[handle]
	if !ok {
		s.mutex.Unlock()
		return ErrInvalidHandle
	}
	delete(s.contexts, handle)
	s.mutex.Unlock()

	entry.mutex.Lock()
	if entry.released {
		entry.mutex.Unlock()
		return ErrInvalidHandle
	}
	entry.released = true
	cards := make([]*serviceCard, 0, len(entry.cards))
	for _, card := range entry.cards {
		cards = append(cards, card)
	}
	entry.cards = make(map[CardHandle]*serviceCard)
	bound := entry.bound
	entry.bound = make(map[int]ProviderContext)
	close(entry.cancel)
	entry.mutex.Unlock()

	var first error
	for _, card := range cards {
		s.mutex.Lock()
		delete(s.cards, card.handle)
		s.mutex.Unlock()
		if err := card.card.Disconnect(LeaveCard); err != nil && first == nil {
			first = err
		}
	}
	for _, providerContext := range bound {
		if err := providerContext.Release(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Cancel unblocks the waits running on a context, SCardCancel. Only waits
// already in progress are cancelled; a cancel arriving while nothing waits is
// not remembered. A pending cancel inherited by a later wait is a fault of the
// Windows implementation that WinSCard_PX4 had to work around.
func (s *Service) Cancel(handle ContextHandle) error {
	entry, err := s.lookupContext(handle)
	if err != nil {
		return err
	}
	entry.mutex.Lock()
	defer entry.mutex.Unlock()
	if entry.released {
		return ErrInvalidHandle
	}
	close(entry.cancel)
	entry.cancel = make(chan struct{})
	return nil
}

// ListReaderGroups lists the reader groups, SCardListReaderGroups. The groups
// of every provider are merged, in provider order, with duplicates dropped.
func (s *Service) ListReaderGroups(handle ContextHandle) ([]string, error) {
	entry, err := s.lookupContext(handle)
	if err != nil {
		return nil, err
	}
	groups, err := entry.collect(func(providerContext ProviderContext) ([]string, error) {
		return providerContext.ReaderGroups()
	})
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		groups = []string{DefaultReaderGroup}
	}
	return groups, nil
}

// ListReaders lists the readers of every provider, SCardListReaders. An empty
// group list, or one naming AllReaderGroups, asks for all of them; the result
// is the providers' lists concatenated in provider order without duplicates.
//
// A provider that fails is skipped as long as another answered. This is the
// case the proxy exists for: an unplugged board must not take the other one's
// readers away.
func (s *Service) ListReaders(handle ContextHandle, groups []string) ([]string, error) {
	entry, err := s.lookupContext(handle)
	if err != nil {
		return nil, err
	}
	readers, err := entry.collect(func(providerContext ProviderContext) ([]string, error) {
		return providerContext.Readers(groups)
	})
	if err != nil {
		return nil, err
	}
	if len(readers) == 0 {
		return nil, ErrNoReadersAvailable
	}
	return readers, nil
}

func (s *Service) lookupContext(handle ContextHandle) (*serviceContext, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	entry, ok := s.contexts[handle]
	if !ok {
		return nil, ErrInvalidHandle
	}
	return entry, nil
}

func (s *Service) lookupCard(handle CardHandle) (*serviceCard, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	entry, ok := s.cards[handle]
	if !ok {
		return nil, ErrInvalidHandle
	}
	return entry, nil
}

// bind establishes the given provider's context if it has not been already. The
// lock is held across EstablishContext so two concurrent calls cannot leave two
// provider contexts behind with only one recorded.
func (c *serviceContext) bind(index int) (ProviderContext, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.released {
		return nil, ErrInvalidHandle
	}
	if providerContext, ok := c.bound[index]; ok {
		return providerContext, nil
	}
	providerContext, err := c.service.providers[index].EstablishContext(c.scope)
	if err != nil {
		return nil, err
	}
	c.bound[index] = providerContext
	return providerContext, nil
}

// collect runs list over every provider and concatenates what they answer, in
// provider order and without duplicates. A provider that fails is skipped; the
// error it gave is only returned when no provider at all answered.
func (c *serviceContext) collect(list func(ProviderContext) ([]string, error)) ([]string, error) {
	var (
		merged   []string
		seen     = make(map[string]bool)
		first    error
		answered bool
	)
	for index := range c.service.providers {
		providerContext, err := c.bind(index)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		names, err := list(providerContext)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		answered = true
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, name)
		}
	}
	if !answered && first != nil {
		return nil, first
	}
	return merged, nil
}

// locate finds the provider that owns a reader name. The first provider to
// claim the name owns it, which is the same precedence enumeration reports.
func (c *serviceContext) locate(reader string) (int, ProviderContext, error) {
	var first error
	for index := range c.service.providers {
		providerContext, err := c.bind(index)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		names, err := providerContext.Readers(nil)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		for _, name := range names {
			if name == reader {
				return index, providerContext, nil
			}
		}
	}
	if first != nil {
		return 0, nil, first
	}
	return 0, nil, ErrUnknownReader
}

// waitContext derives a cancellable context from the service context's current
// cancellation generation, and returns the stop function for it.
func (c *serviceContext) waitContext() (context.Context, context.CancelFunc, error) {
	c.mutex.Lock()
	if c.released {
		c.mutex.Unlock()
		return nil, nil, ErrInvalidHandle
	}
	cancel := c.cancel
	c.mutex.Unlock()

	waitCtx, stop := context.WithCancel(context.Background())
	go func() {
		select {
		case <-cancel:
			stop()
		case <-waitCtx.Done():
		}
	}()
	return waitCtx, stop, nil
}

// deadlineFor turns a WinSCard timeout into a timer channel. A zero timeout
// polls, which the providers implement themselves, so only a positive timeout
// arms a timer here.
func deadlineFor(timeout time.Duration) (<-chan time.Time, func()) {
	if timeout <= 0 {
		return nil, func() {}
	}
	timer := time.NewTimer(timeout)
	return timer.C, func() { timer.Stop() }
}
