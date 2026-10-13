// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"errors"
	"sync"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
	"github.com/Till0196/coax/internal/winscard"
)

// lentSource stands in for a board source that shares boards with something
// else, the way the tuner host's pool does: it never closes a board, it counts
// how many times it was borrowed and given back, and it can declare the board
// it lent out to be a different one.
type lentSource struct {
	mutex sync.Mutex

	board    *fakeBoard
	backend  string
	selector string
	serial   string
	port     string

	borrows  int
	releases int
	// generation stands for the board being unplugged and put back: the
	// borrows taken before it moved are no longer of this board.
	generation uint64
	openErr    error
}

func (s *lentSource) Boards() ([]Attached, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return []Attached{{
		Backend:  s.backend,
		Selector: s.selector,
		ID:       usbio.ID{Vendor: 0x0511, Product: 0x083f},
		Serial:   s.serial,
		Port:     s.port,
	}}, nil
}

func (s *lentSource) Open(backend, selector string) (Borrow, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.openErr != nil {
		return Borrow{}, s.openErr
	}
	s.borrows++
	taken := s.generation
	return Borrow{
		Board:    s.board,
		Identity: Identity{Serial: s.serial, Port: s.port, Generation: taken},
		Valid: func() bool {
			s.mutex.Lock()
			defer s.mutex.Unlock()
			return s.generation == taken
		},
		Release: func() {
			s.mutex.Lock()
			defer s.mutex.Unlock()
			s.releases++
		},
	}, nil
}

func (s *lentSource) replug() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.generation++
}

func (s *lentSource) counts() (borrows, releases int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.borrows, s.releases
}

func newLentSource() *lentSource {
	return &lentSource{
		board:    &fakeBoard{present: true, atr: []byte{0x3B, 0xF0, 0x12}},
		backend:  "px4",
		selector: "001:007",
		serial:   "ABC123",
		port:     "1-2",
	}
}

// providerOver wires a provider onto a source and returns the one reader it
// publishes.
func providerOver(t *testing.T, source BoardSource) (*Provider, string) {
	t.Helper()
	provider := New(Config{Boards: source})
	context, err := provider.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish a context: %v", err)
	}
	t.Cleanup(func() { _ = context.Release() })
	readers, err := context.Readers(nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 1 {
		t.Fatalf("listed %d readers, want 1: %v", len(readers), readers)
	}
	return provider, readers[0]
}

// TestBorrowedBoardIsGivenBackNotClosed is the point of the whole seam: inside
// the tuner host a recording may be running on the same handle, so the card
// provider must never close it.
func TestBorrowedBoardIsGivenBackNotClosed(t *testing.T) {
	source := newLentSource()
	provider, reader := providerOver(t, source)

	handle, err := provider.OpenReader(reader)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	if _, err := handle.Present(); err != nil {
		t.Fatalf("look at the slot: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close the handle: %v", err)
	}

	borrows, releases := source.counts()
	if borrows != 1 || releases != 1 {
		t.Errorf("borrowed %d times and gave back %d, want one of each", borrows, releases)
	}
	if source.board.closes != 0 {
		t.Errorf("closed a board it had only borrowed (%d times)", source.board.closes)
	}
}

// TestOneBorrowServesEveryConnection checks that several PC/SC connections to
// one slot share the borrow rather than taking one each.
func TestOneBorrowServesEveryConnection(t *testing.T) {
	source := newLentSource()
	provider, reader := providerOver(t, source)

	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()
	first, _, err := context.Connect(reader, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	second, _, err := context.Connect(reader, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	if borrows, _ := source.counts(); borrows != 1 {
		t.Errorf("took %d borrows for two connections, want 1", borrows)
	}
	_ = first.Disconnect(winscard.LeaveCard)
	if _, releases := source.counts(); releases != 0 {
		t.Error("gave the board back while a connection still had it")
	}
	_ = second.Disconnect(winscard.LeaveCard)
	if _, releases := source.counts(); releases != 1 {
		t.Error("did not give the board back once the last connection went")
	}
}

// TestReplugEndsTheConversation is the case DriverHost_PX4 makes the same check
// for: a board unplugged and put back is a different board, and the card
// protocol counts its messages from a reset that the new card never saw.
func TestReplugEndsTheConversation(t *testing.T) {
	source := newLentSource()
	provider, reader := providerOver(t, source)

	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()
	card, _, err := context.Connect(reader, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, _, err := card.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00}, winscard.ProtocolT1); err != nil {
		t.Fatalf("transmit before the replug: %v", err)
	}

	source.replug()

	if _, _, err := card.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00}, winscard.ProtocolT1); !errors.Is(err, winscard.ErrRemovedCard) {
		t.Fatalf("transmitting across a replug gave %v, want %v", err, winscard.ErrRemovedCard)
	}
	// The slot no longer claims to have a card on the old board.
	status, err := card.Status()
	if err == nil && status.State != winscard.CardAbsent {
		t.Errorf("the slot still reports %v after its board was swapped", status.State)
	}
}

// TestSessionKeepsTheResetAndItsCommandsTogether checks the unit the HTTP card
// endpoint hands out.
func TestSessionKeepsTheResetAndItsCommandsTogether(t *testing.T) {
	source := newLentSource()
	provider, reader := providerOver(t, source)

	handle, err := provider.OpenReader(reader)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	defer handle.Close()

	atr, responses, err := handle.Session([][]byte{
		{0x90, 0x30, 0x00, 0x00, 0x00},
		{0x90, 0x32, 0x00, 0x00, 0x00},
	})
	if err != nil {
		t.Fatalf("run a session: %v", err)
	}
	if string(atr) != string(source.board.atr) {
		t.Errorf("returned the ATR %X, want %X", atr, source.board.atr)
	}
	if len(responses) != 2 {
		t.Fatalf("returned %d responses, want 2", len(responses))
	}
	if source.board.resets != 1 {
		t.Errorf("reset the card %d times, want 1 for one session", source.board.resets)
	}
	if len(source.board.sent) != 2 {
		t.Errorf("sent %d commands, want 2", len(source.board.sent))
	}

	if _, _, err := handle.Session([][]byte{{}}); !errors.Is(err, winscard.ErrInvalidParameter) {
		t.Errorf("an empty command was accepted: %v", err)
	}
}

// TestDefaultSourceStillOpensForItself checks that a provider given no source
// behaves as it did before there was one, since that is how the standalone
// PC/SC daemon uses it.
func TestDefaultSourceStillOpensForItself(t *testing.T) {
	_, board, reader := oneBoard(t)
	provider := New(Config{})
	context, err := provider.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish a context: %v", err)
	}
	defer context.Release()

	handle, err := provider.OpenReader(reader)
	if err != nil {
		t.Fatalf("open the reader: %v", err)
	}
	if _, err := handle.Present(); err != nil {
		t.Fatalf("look at the slot: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if board.closes != 1 {
		t.Errorf("closed the board %d times, want 1: a provider that opens for itself closes for itself", board.closes)
	}
}

var _ device.Board = (*fakeBoard)(nil)
