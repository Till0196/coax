// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pcscd

import (
	"sync"
	"time"

	"github.com/Till0196/coax/internal/winscard"
)

// Slots is the hardware the table publishes, named as an interface so that the
// protocol can be tested against something other than a tuner.
type Slots interface {
	// Readers lists the slots by the name they are published under.
	Readers() ([]string, error)
	// Open holds one slot's board open.
	Open(reader string) (Slot, error)
}

// Slot is one slot held open.
type Slot interface {
	Present() (bool, error)
	ATR() []byte
	Sharing() int
	Close() error
}

// DefaultPollInterval is how often the table looks at a slot it is watching.
// It matches what the daemon uses on the hardware, since looking oftener than
// the state can change gains nothing.
const DefaultPollInterval = 400 * time.Millisecond

// Table is the reader array the daemon publishes. A client library answers
// SCardListReaders, SCardStatus and SCardGetStatusChange from its own copy of
// this rather than asking, so it is the daemon's only way of saying anything
// about a slot.
//
// Whether a card is seated cannot be known without opening the board, and
// opening a board takes the USB device a recording streams from. So the card
// state is measured only while something has asked for it, and otherwise the
// last measurement stands. poll measures continuously instead, for a machine
// where nothing else uses the tuners.
type Table struct {
	slots    Slots
	interval time.Duration
	poll     bool

	mutex sync.Mutex
	names []string
	cards map[string]*entry
	// events counts the times the set of readers changed, which is what the
	// PnP pseudo-reader reports.
	events uint32
	// watchers counts the clients waiting for an event. The measuring
	// goroutine runs while there is at least one, or always when poll is set.
	watchers int
	held     map[string]Slot
	stop     chan struct{}
	notify   func(uint32)
}

type entry struct {
	present  bool
	measured bool
	atr      []byte
	events   uint32
	sharing  int32
}

func NewTable(slots Slots, interval time.Duration, poll bool) *Table {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	created := &Table{
		slots:    slots,
		interval: interval,
		poll:     poll,
		cards:    make(map[string]*entry),
		held:     make(map[string]Slot),
		notify:   func(uint32) {},
	}
	created.Refresh()
	if poll {
		created.Watch()
	}
	return created
}

// OnEvent sets what to call when something moves. It is separate from
// NewTable because the server and the table refer to each other.
func (t *Table) OnEvent(notify func(uint32)) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if notify != nil {
		t.notify = notify
	}
}

// Refresh re-enumerates the readers. Nothing is opened: the list comes from
// the USB identifiers alone.
func (t *Table) Refresh() {
	names, err := t.slots.Readers()
	if err != nil {
		names = nil
	}
	if len(names) > maxReaders {
		names = names[:maxReaders]
	}

	t.mutex.Lock()
	changed := len(names) != len(t.names)
	if !changed {
		for index, name := range names {
			if t.names[index] != name {
				changed = true
				break
			}
		}
	}
	t.names = names
	for _, name := range names {
		if _, ok := t.cards[name]; !ok {
			t.cards[name] = &entry{}
		}
	}
	for name := range t.cards {
		if !contains(names, name) {
			delete(t.cards, name)
		}
	}
	if changed {
		t.events++
	}
	notify := t.notify
	t.mutex.Unlock()

	if changed {
		notify(uint32(winscard.Success))
	}
}

// Observed records what a card connection just saw, so that a client asking
// about a reader it is using gets an answer measured rather than remembered.
func (t *Table) Observed(status winscard.Status) {
	if len(status.Readers) == 0 {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	known, ok := t.cards[status.Readers[0]]
	if !ok {
		return
	}
	present := status.State != winscard.CardAbsent
	if known.measured && known.present != present {
		known.events++
	}
	known.measured = true
	known.present = present
	known.atr = append([]byte(nil), status.ATR...)
}

func (t *Table) Events() uint32 {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.events
}

// States renders the array a client reads.
func (t *Table) States() []readerState {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	rendered := make([]readerState, 0, len(t.names))
	for _, name := range t.names {
		known := t.cards[name]
		state := uint32(stateAbsent)
		var atr []byte
		if known.present {
			// Powered and specific follow presence here because a card this
			// daemon can see at all has been through its answer to reset:
			// there is no state in between on these interfaces.
			state = statePresent | statePowered | stateNegotiable | stateSpecific
			atr = known.atr
		} else if !known.measured {
			// Nothing has looked yet, and looking means opening the board.
			state = stateUnknown
		}
		rendered = append(rendered, readerState{
			name:     name,
			events:   known.events,
			state:    state,
			sharing:  known.sharing,
			atr:      atr,
			protocol: protocolT1,
		})
	}
	return rendered
}

// Watch says a client is waiting, which is what makes the daemon measure.
func (t *Table) Watch() {
	t.mutex.Lock()
	t.watchers++
	start := t.watchers == 1 && t.stop == nil
	if start {
		t.stop = make(chan struct{})
	}
	stop := t.stop
	t.mutex.Unlock()
	if start {
		go t.measure(stop)
	}
}

// Unwatch says one client stopped waiting.
func (t *Table) Unwatch() {
	t.mutex.Lock()
	if t.watchers > 0 {
		t.watchers--
	}
	stop := t.stop
	finished := t.watchers == 0 && !t.poll && stop != nil
	if finished {
		t.stop = nil
	}
	t.mutex.Unlock()
	if finished {
		close(stop)
	}
}

// Close releases everything the table holds open.
func (t *Table) Close() error {
	t.mutex.Lock()
	stop := t.stop
	t.stop = nil
	t.watchers = 0
	t.poll = false
	t.mutex.Unlock()
	if stop != nil {
		close(stop)
	}
	return nil
}

// measure polls the slots while anything is waiting, holding each board open
// for the whole time rather than opening it per poll: opening is what powers a
// board up and loads firmware into it.
func (t *Table) measure(stop chan struct{}) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	defer t.release()
	for {
		if t.sweep() {
			t.mutex.Lock()
			notify := t.notify
			t.mutex.Unlock()
			notify(uint32(winscard.Success))
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

// sweep measures every reader once and reports whether anything moved.
func (t *Table) sweep() bool {
	t.mutex.Lock()
	names := append([]string(nil), t.names...)
	t.mutex.Unlock()

	moved := false
	for _, name := range names {
		handle, err := t.handleFor(name)
		if err != nil {
			continue
		}
		present, presenceErr := handle.Present()
		if presenceErr != nil {
			continue
		}
		atr := handle.ATR()
		sharing := int32(handle.Sharing())

		t.mutex.Lock()
		known, ok := t.cards[name]
		if ok {
			if !known.measured || known.present != present {
				known.events++
				moved = true
			}
			if known.sharing != sharing {
				moved = true
			}
			known.measured = true
			known.present = present
			known.sharing = sharing
			if present {
				known.atr = atr
			} else {
				known.atr = nil
			}
		}
		t.mutex.Unlock()
	}
	return moved
}

// handleFor holds a board open for as long as the measuring goroutine runs.
func (t *Table) handleFor(name string) (Slot, error) {
	t.mutex.Lock()
	held, ok := t.held[name]
	t.mutex.Unlock()
	if ok {
		return held, nil
	}
	opened, err := t.slots.Open(name)
	if err != nil {
		return nil, err
	}
	t.mutex.Lock()
	if existing, raced := t.held[name]; raced {
		t.mutex.Unlock()
		_ = opened.Close()
		return existing, nil
	}
	t.held[name] = opened
	t.mutex.Unlock()
	return opened, nil
}

func (t *Table) release() {
	t.mutex.Lock()
	held := t.held
	t.held = make(map[string]Slot)
	t.mutex.Unlock()
	for _, handle := range held {
		_ = handle.Close()
	}
}

func contains(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

// Names lists the readers the table publishes, for a caller that wants to
// report them rather than serve them.
func (t *Table) Names() []string {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return append([]string(nil), t.names...)
}
