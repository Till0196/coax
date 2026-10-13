// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pool

import (
	"errors"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/tunerid"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// BoardInfo is one attached board as a listing reports it.
type BoardInfo struct {
	// Name is what the board is known by, and the stem of its tuners' names.
	Name string
	// Kind says what the name was built from, and Stable whether it survives
	// the board being unplugged and put back.
	Kind   tunerid.Kind
	Stable bool

	Backend     string
	Description string
	// Location is the bus and address, what --device takes. Not part of the name
	// because it changes.
	Location string
	Serial   string
	Port     string
	// Deliveries is every delivery system some tuner of the board reaches.
	Deliveries []string
	// Open reports whether the pool is holding the board open right now.
	Open bool
	// Probed reports whether the tuners below were read from the hardware rather
	// than worked out from the board's identifiers.
	Probed bool
	// Exclusive says the driver allows one open and no more, which is why a
	// listing does not open it to look.
	Exclusive bool
	Tuners    []TunerInfo
}

// TunerInfo is one tuner as a listing reports it.
type TunerInfo struct {
	ID         string
	Index      int
	Name       string
	Deliveries []string
	// Mode and Settings are filled in only where the board was opened to ask.
	// See device.TunerInfo.
	Mode     string
	Settings []device.Setting
	// Controls is what a channel may set on the tuner. See device.Controls.
	Controls device.Controls
	Leased   bool
	// Priority is what the work holding this tuner said it was worth, and
	// means nothing unless Leased: zero is a real priority, so it cannot also
	// stand for nobody holding it.
	Priority int
}

// Boards lists what is attached. Nothing is opened -- that would power up
// hardware nobody asked for and load firmware into it -- so the answer comes
// from the identifiers the devices enumerated with plus whatever the pool
// already had open.
func (s *Supervisor) Boards() ([]BoardInfo, error) {
	catalog, err := s.refresh()
	if err != nil {
		return nil, err
	}
	listing := make([]BoardInfo, 0, len(catalog))
	for _, one := range catalog {
		held, err := s.board(one)
		if err != nil {
			return nil, err
		}
		listing = append(listing, s.describe(one, held))
	}
	return listing, nil
}

func (s *Supervisor) describe(one entry, held *board) BoardInfo {
	info := BoardInfo{
		Name:        one.name,
		Kind:        one.kind,
		Stable:      one.kind.Stable(),
		Backend:     one.detected.Backend,
		Description: one.detected.Description,
		Location:    one.detected.Selector,
		Serial:      one.detected.Serial,
		Port:        one.detected.Port,
		Deliveries:  append([]string(nil), one.detected.Deliveries...),
		Open:        held.Open(),
		Exclusive:   one.detected.Exclusive,
	}
	held.mutex.Lock()
	info.Probed = held.probed
	leased := make(map[int]int, len(held.leases))
	for index, lease := range held.leases {
		leased[index] = lease.priority
	}
	held.mutex.Unlock()

	for _, tuner := range held.layout() {
		name, _ := tunerid.Tuner(deviceOf(one.detected), tuner.Index)
		priority, taken := leased[tuner.Index]
		info.Tuners = append(info.Tuners, TunerInfo{
			ID:         name,
			Index:      tuner.Index,
			Name:       tuner.Name,
			Deliveries: append([]string(nil), tuner.Deliveries...),
			Mode:       tuner.Mode,
			Settings:   append([]device.Setting(nil), tuner.Settings...),
			Controls:   tuner.Controls,
			Leased:     taken,
			Priority:   priority,
		})
	}
	return info
}

// Want is a request for a tuner: a named one, or any that can do the job.
type Want struct {
	// TunerID names one tuner exactly. Empty means any that will serve.
	TunerID string
	// DeliverySystem is what the tuner has to be able to reach. Empty matches
	// anything.
	DeliverySystem string
	// Owner is who to give the tuner back to when their connection goes.
	Owner Owner
	// Priority is how much this request's work matters. A tuner may be taken from
	// work that asked at a lower one, and never from work that asked at the same:
	// equal ranks keep the rule that whoever got there first keeps it. Zero is
	// what a request that says nothing is worth, so a client that has never heard
	// of priorities behaves exactly as it did.
	Priority int
}

// Lease takes a tuner. A named tuner is either free or not; there is no queue,
// since a caller that would have to wait is better told at once. An unnamed
// request walks the boards in listing order and takes the first free tuner that
// reaches the delivery system asked for, preferring an already-open board.
//
// Only when nothing is free does the priority come into it: the tuner is then
// taken from whichever holder asked at the lowest priority, provided that is
// below this request's.
func (s *Supervisor) Lease(want Want) (*Lease, error) {
	if existing := s.reuse(want); existing != nil {
		// The work on this tuner has said again what it is worth, which is the
		// word that now defends it.
		existing.Reprioritise(want.Priority)
		return existing, nil
	}
	catalog, err := s.refresh()
	if err != nil {
		return nil, err
	}

	if want.TunerID != "" {
		boardName, index, ok := tunerid.Split(want.TunerID)
		if !ok {
			return nil, ErrUnknownTuner
		}
		for _, one := range catalog {
			if one.name != boardName {
				continue
			}
			held, err := s.board(one)
			if err != nil {
				return nil, err
			}
			if !hasTuner(held.layout(), index) {
				return nil, ErrUnknownTuner
			}
			// One tuner was asked for and there is no second choice, so the
			// only question is whether this request outranks whoever has it.
			return held.lease(index, want.Owner, want.Priority, true)
		}
		return nil, ErrUnknownTuner
	}

	// Open boards first, so a request that can be served without waking more
	// hardware is.
	var candidates []entry
	var cold []entry
	for _, one := range catalog {
		held, err := s.board(one)
		if err != nil {
			return nil, err
		}
		if held.Open() {
			candidates = append(candidates, one)
			continue
		}
		cold = append(cold, one)
	}
	candidates = append(candidates, cold...)

	matched := false
	for _, one := range candidates {
		held, err := s.board(one)
		if err != nil {
			return nil, err
		}
		for _, tuner := range held.layout() {
			if !reaches(tuner, want.DeliverySystem) {
				continue
			}
			matched = true
			lease, err := held.lease(tuner.Index, want.Owner, want.Priority, false)
			if err == nil {
				return lease, nil
			}
			// A tuner someone else took, or a board that just went, means try
			// the next rather than give up.
			if errorIsBusy(err) {
				continue
			}
			return nil, err
		}
	}
	if !matched {
		return nil, ErrNoTuner
	}
	// Every tuner that would serve is in use. If this request outranks one of
	// the holders, it may have that tuner.
	if lease, err := s.preempt(candidates, want); lease != nil || err != nil {
		return lease, err
	}
	return nil, ErrBusy
}

// preempt takes a matching tuner from the lowest-ranked holder below this
// request, and answers nil when there is nobody it outranks.
//
// The lowest rather than the first found: what stops should be the least
// important work on the machine, not whichever board happened to enumerate
// first. Equal ranks are left alone throughout.
//
// Losing the race to the tuner it chose is not retried against the next
// candidate: the picture of who holds what is already a moment old, and going
// round again on a busy machine would stop one recording after another. Being
// told busy is the honest answer.
func (s *Supervisor) preempt(candidates []entry, want Want) (*Lease, error) {
	var chosen *board
	chosenIndex := 0
	chosenPriority := 0
	for _, one := range candidates {
		held, err := s.board(one)
		if err != nil {
			return nil, err
		}
		for _, tuner := range held.layout() {
			if !reaches(tuner, want.DeliverySystem) {
				continue
			}
			priority, taken := held.leasedPriority(tuner.Index)
			if !taken || priority >= want.Priority {
				continue
			}
			if chosen == nil || priority < chosenPriority {
				chosen, chosenIndex, chosenPriority = held, tuner.Index, priority
			}
		}
	}
	if chosen == nil {
		return nil, nil
	}
	lease, err := chosen.lease(chosenIndex, want.Owner, want.Priority, true)
	if err == nil {
		return lease, nil
	}
	if errorIsBusy(err) {
		return nil, nil
	}
	return nil, err
}

// reuse hands back a tuner this holder already has, when it asked for that one
// by name: a client working down a list retunes the tuner it holds, and making
// it win its own tuner back between channels would let somebody take it
// mid-list. A request that names no tuner is never served this way, or a client
// recording two programmes would get one twice.
func (s *Supervisor) reuse(want Want) *Lease {
	if want.Owner.id == 0 || want.TunerID == "" {
		return nil
	}
	s.mutex.Lock()
	things := append([]held(nil), s.holdings[want.Owner.id]...)
	s.mutex.Unlock()

	for _, thing := range things {
		lease, ok := thing.(*Lease)
		if !ok || lease.board.Gone() {
			continue
		}
		if lease.ID() == want.TunerID {
			return lease
		}
	}
	return nil
}

// Reading is everything that can be said about a tuner that is being used.
type Reading struct {
	// Status is what the demodulator reports.
	Status device.Status
	// Settings is how the hardware says it is tuned.
	Settings []device.Setting
	// Tuned is the channel the tuner was last set to, as the properties the
	// tune took. What was asked for rather than what the hardware measured,
	// and the only account of which channel a tuner is on.
	Tuned map[string]string
	// Dropped is packets thrown away for a consumer that fell behind, nil for a
	// board that cannot say. Nil and zero are different answers.
	Dropped *uint64
	// Stream is what the multiplex looked like, nil when nothing is streaming
	// and so nothing has gone past to look at.
	Stream *device.StreamCounts
}

// Measure reads what the hardware can say about a tuner in use. It reports
// nothing for a tuner nobody has taken and never opens a board to answer. It
// does not take the tuner either, which is what lets it be asked during a
// recording.
func (s *Supervisor) Measure(tunerID string) (Reading, bool) {
	boardName, index, split := tunerid.Split(tunerID)
	if !split {
		return Reading{}, false
	}
	one, found := s.find(boardName)
	if !found {
		return Reading{}, false
	}
	held, err := s.board(one)
	if err != nil {
		return Reading{}, false
	}
	held.mutex.Lock()
	lease := held.leases[index]
	held.mutex.Unlock()
	if lease == nil || lease.device == nil {
		return Reading{}, false
	}

	status, err := lease.device.Status()
	if err != nil {
		return Reading{}, false
	}
	return Reading{
		Status:   status,
		Settings: lease.settings(),
		Tuned:    lease.Tuned(),
		Dropped:  lease.dropped(),
		Stream:   lease.analysis(),
	}, true
}

// Card takes the card reader of the board a tuner or board name belongs to.
func (s *Supervisor) Card(name string, owner Owner) (*CardHold, error) {
	if _, err := s.refresh(); err != nil {
		return nil, err
	}
	if boardName, _, ok := tunerid.Split(name); ok {
		if _, found := s.find(boardName); found {
			name = boardName
		}
	}
	one, ok := s.find(name)
	if !ok {
		return nil, ErrUnknownTuner
	}
	held, err := s.board(one)
	if err != nil {
		return nil, err
	}
	return held.card(owner)
}

// Watch keeps a board open while a caller reads a tuner's state, and returns
// the stop.
func (s *Supervisor) Watch(name string) (func(), error) {
	boardName := name
	if stem, _, ok := tunerid.Split(name); ok {
		boardName = stem
	}
	one, ok := s.find(boardName)
	if !ok {
		if _, err := s.refresh(); err != nil {
			return nil, err
		}
		if one, ok = s.find(boardName); !ok {
			return nil, ErrUnknownTuner
		}
	}
	held, err := s.board(one)
	if err != nil {
		return nil, err
	}
	return held.watch()
}

func hasTuner(tuners []device.TunerInfo, index int) bool {
	for _, tuner := range tuners {
		if tuner.Index == index {
			return true
		}
	}
	return false
}

// reaches reports whether a tuner can serve a delivery system, matching the
// way channel files spell them.
func reaches(tuner device.TunerInfo, deliverySystem string) bool {
	if strings.TrimSpace(deliverySystem) == "" {
		return true
	}
	if len(tuner.Deliveries) == 0 {
		// Nothing is known about this tuner's reach: several Linux frontend drivers
		// will not enumerate their delivery systems and BDA has no way to ask.
		// Passing it over would make it unreachable for ever, so it is offered and
		// allowed to refuse the tune.
		return true
	}
	// Compared by system, not spelling: "DVB-C" and "DVBC/ANNEX_A" are the same
	// thing, and a tuner must not be passed over for a channel it receives.
	for _, delivery := range tuner.Deliveries {
		if tuning.SameDelivery(delivery, deliverySystem) {
			return true
		}
	}
	return false
}

// errorIsBusy reports whether a lease failed for a reason another tuner might
// not, so it is worth trying the next rather than giving up.
func errorIsBusy(err error) bool {
	return errors.Is(err, device.ErrTunerBusy) || errors.Is(err, ErrGone)
}
