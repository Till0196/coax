// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"sync"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/winscard"
)

// slot is one card slot: the board it is in, the connections to it, and the T=1
// session they share. There is one per reader name for the life of the process,
// connected or not, because it is what remembers that a card was removed while
// nobody was looking.
type slot struct {
	provider   *Provider
	cardDevice cardDevice

	// state guards the bookkeeping below. ready is signalled when a
	// transaction ends, which is what a connection waiting for its turn is
	// waiting for.
	state sync.Mutex
	ready *sync.Cond
	// hardware serialises the calls that actually reach the board. It is kept
	// apart from state so that a connection blocked on another's transaction
	// is not also holding the lock that every status poll needs.
	hardware sync.Mutex

	board  device.Board
	reader device.CardReader
	// borrow is the board this slot holds, and how to tell whether it is still the
	// board the name refers to. Checked before every use: a name outliving its
	// hardware is exactly what a card conversation must not continue across.
	borrow Borrow
	// opens counts the connections keeping the board open.
	opens int
	// shared and exclusive count the connections by share mode, which is what
	// decides whether another connection is allowed.
	shared    int
	exclusive int
	// holder owns the card until it drops it: a connection between
	// BeginTransaction and EndTransaction, or whoever is mid-exchange.
	holder any
	depth  int

	// initialized is set once a reset produced an ATR and cleared whenever
	// anything goes wrong, taking the whole session with it: a T=1 conversation
	// whose sequence numbers may have drifted can only be restarted.
	initialized bool
	atr         []byte
	present     bool
	// events counts insertions and removals, which PC/SC carries in the high
	// half of a reader's state word.
	events uint16
}

func newSlot(provider *Provider, one cardDevice) *slot {
	created := &slot{provider: provider, cardDevice: one}
	created.ready = sync.NewCond(&created.state)
	return created
}

// open takes a reference on the board, opening it if this is the first. Opening
// powers the board up and loads firmware into the ones that need it, so it
// happens here rather than at enumeration: a caller connecting to a reader has
// asked for that board in particular.
func (s *slot) open() error {
	s.state.Lock()
	defer s.state.Unlock()
	return s.openLocked()
}

func (s *slot) openLocked() error {
	if s.opens > 0 {
		if s.staleLocked() {
			// The board this slot was holding has gone and another has come
			// back under the same name. Nothing about the old conversation
			// carries over, so it is torn down before the count grows.
			s.discardLocked()
		} else {
			s.opens++
			return nil
		}
	}
	borrow, err := s.provider.config.Boards.Open(s.cardDevice.backend, s.cardDevice.selector)
	if err != nil {
		return winscard.ErrReaderUnavailable
	}
	reader, ok := borrow.Board.(device.CardReader)
	if !ok {
		borrow.Release()
		// The backend can carry a slot but this board does not: the second half of a
		// Q3, the three-tuner side of an MLT8PE. A reader that exists in the listing
		// and cannot be used, which is what SCARD_E_READER_UNSUPPORTED says.
		return winscard.ErrReaderUnsupported
	}
	s.board = borrow.Board
	s.reader = reader
	s.borrow = borrow
	s.opens = 1
	return nil
}

// staleLocked reports whether the board this slot holds is no longer the board
// its name refers to. DriverHost_PX4 makes the same check for the same reason:
// a device unplugged and put back is a different device.
func (s *slot) staleLocked() bool {
	if s.board == nil {
		return true
	}
	return s.borrow.Valid != nil && !s.borrow.Valid()
}

// discardLocked throws away everything about the board this slot was holding,
// leaving the connections counted against it in place: they are still
// connections, they simply have no session any more.
func (s *slot) discardLocked() {
	release := s.borrow.Release
	s.board = nil
	s.reader = nil
	s.borrow = Borrow{}
	s.opens = 0
	s.initialized = false
	s.atr = nil
	if s.present {
		s.present = false
		s.events++
	}
	if release != nil {
		s.state.Unlock()
		release()
		s.state.Lock()
	}
}

// close drops a reference, closing the board when the last one goes.
func (s *slot) close() {
	s.state.Lock()
	defer s.state.Unlock()
	s.closeLocked()
}

func (s *slot) closeLocked() {
	if s.opens == 0 {
		return
	}
	s.opens--
	if s.opens > 0 {
		return
	}
	release := s.borrow.Release
	s.board = nil
	s.reader = nil
	s.borrow = Borrow{}
	s.initialized = false
	s.atr = nil
	if release != nil {
		release()
	}
}

// admit applies the share rules, which are the whole of what one connection
// can say about another's.
func (s *slot) admit(share winscard.ShareMode) error {
	s.state.Lock()
	defer s.state.Unlock()
	switch share {
	case winscard.ShareExclusive:
		if s.shared > 0 || s.exclusive > 0 {
			return winscard.ErrSharingViolation
		}
	case winscard.ShareShared:
		if s.exclusive > 0 {
			return winscard.ErrSharingViolation
		}
	default:
		return winscard.ErrInvalidValue
	}
	if err := s.openLocked(); err != nil {
		return err
	}
	if share == winscard.ShareExclusive {
		s.exclusive++
	} else {
		s.shared++
	}
	return nil
}

func (s *slot) discharge(share winscard.ShareMode) {
	s.state.Lock()
	defer s.state.Unlock()
	if share == winscard.ShareExclusive {
		if s.exclusive > 0 {
			s.exclusive--
		}
	} else if s.shared > 0 {
		s.shared--
	}
	s.closeLocked()
}

// claim takes the card for one owner, waiting for whoever has it. It nests: a
// connection holding a transaction claims again per APDU and gives the card up
// only when the transaction ends. That is what makes a transaction mean
// anything, since an exchange is several USB transfers.
func (s *slot) claim(owner any) {
	s.state.Lock()
	defer s.state.Unlock()
	for s.holder != nil && s.holder != owner {
		s.ready.Wait()
	}
	s.holder = owner
	s.depth++
}

func (s *slot) drop() {
	s.state.Lock()
	defer s.state.Unlock()
	if s.depth == 0 {
		return
	}
	s.depth--
	if s.depth == 0 {
		s.holder = nil
		s.ready.Broadcast()
	}
}

// held reports whether an owner holds the card, so that EndTransaction can
// refuse one that was never begun.
func (s *slot) held(owner any) bool {
	s.state.Lock()
	defer s.state.Unlock()
	return s.holder == owner && s.depth > 0
}

// look reads the slot without disturbing a session, and keeps the presence and
// event count up to date.
func (s *slot) look() (present bool, name string, err error) {
	s.state.Lock()
	if s.board != nil && s.staleLocked() {
		s.discardLocked()
	}
	reader := s.reader
	s.state.Unlock()
	if reader == nil {
		return false, "", winscard.ErrReaderUnavailable
	}
	s.hardware.Lock()
	status, err := reader.CardStatus()
	s.hardware.Unlock()
	if err != nil {
		return false, "", winscard.ErrReaderUnavailable
	}
	s.state.Lock()
	if status.Present != s.present {
		s.present = status.Present
		s.events++
		if !status.Present {
			// Whatever session there was belongs to a card that is gone.
			s.initialized = false
			s.atr = nil
		}
	}
	s.state.Unlock()
	return status.Present, status.Reader, nil
}

// session brings the card up if it is not already, and returns its ATR.
func (s *slot) session() ([]byte, error) {
	present, _, err := s.look()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, winscard.ErrNoSmartcard
	}
	s.state.Lock()
	if s.initialized {
		atr := append([]byte(nil), s.atr...)
		s.state.Unlock()
		return atr, nil
	}
	reader := s.reader
	s.state.Unlock()
	if reader == nil {
		return nil, winscard.ErrReaderUnavailable
	}

	s.hardware.Lock()
	atr, err := reader.ResetCard()
	s.hardware.Unlock()
	if err != nil {
		return nil, s.fail(err)
	}
	s.state.Lock()
	s.initialized = true
	s.atr = append([]byte(nil), atr...)
	s.state.Unlock()
	return atr, nil
}

// exchange sends one APDU on an established session.
func (s *slot) exchange(apdu []byte) ([]byte, error) {
	s.state.Lock()
	stale := s.board != nil && s.staleLocked()
	if stale {
		s.discardLocked()
	}
	ready := s.initialized
	reader := s.reader
	s.state.Unlock()
	if stale {
		// The board was swapped underneath this conversation. The card that answered
		// the last message is not the card that would answer this one.
		return nil, winscard.ErrRemovedCard
	}
	if reader == nil {
		return nil, winscard.ErrReaderUnavailable
	}
	if !ready {
		// The session was given up since the caller connected. PC/SC has a
		// code for exactly this, and an application that knows it will
		// reconnect rather than treat the card as broken.
		return nil, winscard.ErrResetCard
	}
	s.hardware.Lock()
	response, err := reader.TransmitAPDU(apdu)
	s.hardware.Unlock()
	if err != nil {
		return nil, s.fail(err)
	}
	return response, nil
}

// invalidate gives up the session without touching the card, so that the next
// use starts from a reset.
func (s *slot) invalidate() {
	s.state.Lock()
	defer s.state.Unlock()
	s.initialized = false
	s.atr = nil
}

// fail gives up the session and says what the failure was in PC/SC's terms.
// What matters to a caller is whether the card is still there: a removed card
// is a reason to wait for a new one, a failed exchange with the card still
// seated a reason to reset and try again.
func (s *slot) fail(cause error) error {
	s.invalidate()
	s.state.Lock()
	reader := s.reader
	s.state.Unlock()
	if reader == nil {
		return winscard.ErrReaderUnavailable
	}
	s.hardware.Lock()
	status, err := reader.CardStatus()
	s.hardware.Unlock()
	if err != nil {
		return winscard.ErrReaderUnavailable
	}
	if !status.Present {
		s.state.Lock()
		if s.present {
			s.present = false
			s.events++
		}
		s.state.Unlock()
		return winscard.ErrRemovedCard
	}
	return winscard.ErrNotTransacted
}

// snapshot is what a waiter compares against to decide whether anything moved.
type snapshot struct {
	present bool
	events  uint16
	atr     []byte
	inUse   bool
	held    bool
}

// observe reports the slot's state, opening the board if it is not already
// open. A caller naming a reader has asked for that board in particular, which
// is what makes opening it here reasonable.
func (s *slot) observe() (snapshot, error) {
	if err := s.open(); err != nil {
		return snapshot{}, err
	}
	defer s.close()
	return s.observeOpen()
}

// observeOpen is observe for a caller that is already holding the board open,
// which is every poll of a wait after the first.
func (s *slot) observeOpen() (snapshot, error) {
	present, _, err := s.look()
	if err != nil {
		return snapshot{}, err
	}
	s.state.Lock()
	defer s.state.Unlock()
	return snapshot{
		present: present,
		events:  s.events,
		atr:     append([]byte(nil), s.atr...),
		inUse:   s.shared > 0,
		held:    s.exclusive > 0,
	}, nil
}
