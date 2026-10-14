// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package pool owns the boards.
//
// Only one open handle may have a USB board -- every backend claims interface 0
// exclusively -- while a board is several things at once, four tuners and a
// card slot on a PX-W3U4, wanted by several clients at the same time. So one
// part of the program holds the handle and hands out shares of it.
//
// What it serialises is ownership: opening a board, handing out a tuner, taking
// the card, and deciding when the last user has gone. The traffic itself is
// deliberately not serialised -- the drivers underneath already serialise what
// has to stay atomic, and a second queue would make a status read wait behind a
// card reset.
package pool

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/tunerid"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/pkg/usbio"
)

// ErrNoDevices reports that the bus itself could not be read, which is not the
// same as nothing being plugged into it: a caller told "no tuners" when the
// truth is "cannot see the bus" goes looking for hardware that is sitting right
// there.
var ErrNoDevices = usbio.ErrNoDevices

// ErrNoTuner reports that nothing attached can tune what was asked for.
var ErrNoTuner = errors.New("no tuner can reach that delivery system")

// ErrBusy reports that every tuner that could serve the request is taken.
var ErrBusy = errors.New("every matching tuner is in use")

// ErrUnknownTuner reports a tuner name nothing attached answers to.
var ErrUnknownTuner = errors.New("no such tuner")

// ErrNoCardReader reports that a board carries no card slot.
var ErrNoCardReader = errors.New("this board has no card reader")

// ErrGone reports that the board went away while it was being used.
var ErrGone = errors.New("the board is no longer attached")

// IsGone reports whether an error means the hardware is no longer reachable.
// There are two sentinels -- the pool's and the driver's -- and they mean the
// same thing to everyone above.
func IsGone(err error) bool {
	return errors.Is(err, ErrGone) || errors.Is(err, usbio.ErrGone)
}

// sharedSerialBackends are the backends whose enclosures can enumerate as
// several USB devices carrying one serial number between them: a PX-Q3U4 is one
// box the bus sees as two four-tuner devices. So their boards are named by
// serial and socket together.
var sharedSerialBackends = map[string]bool{
	"px4":   true,
	"pxmlt": true,
}

// Detected is one tuner device the registry can see without opening it. A USB
// board, a Linux DVB adapter and a Windows BDA tuner are all boards here --
// something claimed before use, carrying one or more tuners and perhaps a card
// slot.
type Detected struct {
	// Backend is the driver, and the first part of every name built from it.
	Backend string
	// Selector tells this device from the others of its backend: a USB
	// "bus:address", a frontend path, a device path.
	Selector string
	// Description is the hardware in words, for a listing.
	Description string
	// Name is the operating system's own name for the device, for hardware not on
	// a bus this program enumerates itself. Empty for USB.
	Name string
	// Serial, Port and Location are the USB identity, empty for anything else.
	Serial   string
	Port     string
	Location string
	// USB is the vendor and product, zero for anything that is not USB.
	USB usbio.ID
	// Deliveries are the delivery systems the hardware is known to reach, before
	// it is opened and asked.
	Deliveries []string
	// Exclusive says the driver behind this device allows one open and no more, so
	// holding it open shuts everything else out. A DVB frontend or a BDA graph can
	// be opened twice and only refuses at the point of tuning; px4_drv and the
	// Earthsoft drivers allow a single open per node, so a device marked here is
	// not held a moment longer than used.
	Exclusive bool
	// Tuners is the layout as far as it can be told without opening the device.
	Tuners []device.TunerInfo
}

// Registry is where the pool finds hardware, named as an interface so that the
// pool can be tested against boards that are not there.
type Registry interface {
	// Detect lists the attached devices without opening one.
	Detect() ([]Detected, error)
	// Open claims one of them.
	Open(one Detected, options device.Options) (device.Board, error)
}

// Options settle what the pool cannot work out for itself.
type Options struct {
	// FirmwareDir holds the blobs the firmware-loading boards need.
	FirmwareDir string
	// Linger is how long a board stays open after its last user has gone. Opening
	// one is expensive -- it powers the hardware up and loads firmware into
	// several models -- and its users come and go in quick succession. Zero closes
	// the board the moment its last user goes; LingerForever keeps boards open
	// until the pool itself is closed.
	Linger time.Duration
	// Settled is closed once the hardware is in a state a board may be opened
	// from. It is what lets the daemon answer while it is still putting right what
	// a killed run left behind: the repair opens boards of its own, and a client
	// walking into the middle of that would be two openers on one board. Nil means
	// nothing to wait for.
	Settled <-chan struct{}
	// Settling names the backends that wait for it. A repair is one backend's
	// business: a card in a USB enclosure has no part in a PCIe tuner being handed
	// back. Empty means every backend waits.
	Settling []string
}

// DefaultLinger is long enough to cover the gap between one client leaving and
// the next arriving, and short enough that a board is not held against a
// program that wants it after the daemon has finished with it.
const DefaultLinger = 10 * time.Second

// LingerForever keeps a board open until the pool is closed.
const LingerForever = time.Duration(-1)

// Owner names whoever is holding tuners and cards, so everything one holder
// took can be given back at once. The daemon issues one per connection: a
// client owns what it took for as long as its connection is open.
type Owner struct{ id uint64 }

// Supervisor is the pool.
type Supervisor struct {
	registry Registry
	options  Options

	mutex    sync.Mutex
	boards   map[Key]*board
	nextID   uint64
	closed   bool
	catalog  []entry
	holdings map[uint64][]held
}

// Key names one physical board, before and after it is opened.
type Key struct {
	Backend  string
	Selector string
}

// entry is one attached board as the last listing saw it.
type entry struct {
	key      Key
	detected Detected
	name     string
	kind     tunerid.Kind
	tuners   []device.TunerInfo
}

// waitsToSettle reports whether a board of this backend is one the repair could
// hold.
func (s *Supervisor) waitsToSettle(backend string) bool {
	if s.options.Settled == nil {
		return false
	}
	if len(s.options.Settling) == 0 {
		return true
	}
	return slices.Contains(s.options.Settling, backend)
}

// New builds a pool over a registry. Nothing is opened until something is asked
// for, and options are taken as given: a zero Linger means what it says.
func New(registry Registry, options Options) *Supervisor {
	return &Supervisor{
		registry: registry,
		options:  options,
		boards:   make(map[Key]*board),
	}
}

// NewOwner issues a holder identity.
func (s *Supervisor) NewOwner() Owner {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.nextID++
	return Owner{id: s.nextID}
}

// device turns a listing entry into what the naming rules need.
func deviceOf(detected Detected) tunerid.Device {
	return tunerid.Device{
		Backend:      detected.Backend,
		Name:         detected.Name,
		Serial:       detected.Serial,
		Port:         detected.Port,
		Location:     detected.Location,
		SharedSerial: sharedSerialBackends[detected.Backend],
	}
}

// refresh re-reads the registry and rebuilds the catalogue of attached boards.
// A board that has gone is dropped with its actor, so the next request fails
// rather than reaching a dead handle; one that has come back is a new entry
// with a new generation, since the same name may lead to a different device.
func (s *Supervisor) refresh() ([]entry, error) {
	detected, err := s.registry.Detect()
	if err != nil {
		if errors.Is(err, ErrNoDevices) {
			// This one already says what happened and what to do about it.
			return nil, err
		}
		return nil, fmt.Errorf("list boards: %w", err)
	}
	catalog := make([]entry, 0, len(detected))
	attached := make(map[Key]bool, len(detected))
	for _, one := range detected {
		one.Deliveries = tuning.ExpandDeliveries(one.Deliveries)
		one.Tuners = expandTuners(one.Tuners)
		key := Key{Backend: one.Backend, Selector: one.Selector}
		attached[key] = true
		name, kind := tunerid.Board(deviceOf(one))
		catalog = append(catalog, entry{
			key:      key,
			detected: one,
			name:     name,
			kind:     kind,
			tuners:   one.Tuners,
		})
	}

	s.mutex.Lock()
	s.catalog = catalog
	var orphaned []*board
	for key, held := range s.boards {
		if !attached[key] {
			delete(s.boards, key)
			orphaned = append(orphaned, held)
		}
	}
	s.mutex.Unlock()

	// Shutting an orphan down happens outside the pool's own lock, because it
	// waits on the board's.
	for _, held := range orphaned {
		held.markGone()
	}
	return catalog, nil
}

// expandTuners fills in the other spellings of each tuner's delivery systems. A
// board driver names them as its documentation does and a channel file as its
// author did, and the difference must not decide whether a tuner is offered a
// channel it can receive: a T230C2 that says "DVB-C" receives a file that says
// "DVBC/ANNEX_A".
func expandTuners(tuners []device.TunerInfo) []device.TunerInfo {
	expanded := make([]device.TunerInfo, len(tuners))
	for index, tuner := range tuners {
		tuner.Deliveries = tuning.ExpandDeliveries(tuner.Deliveries)
		expanded[index] = tuner
	}
	return expanded
}

// board finds or creates the actor for a key. It opens nothing.
//
// An actor told its board is gone is never handed out again, even while the
// same key is still on the bus: that happens on a replug into the same socket,
// and the old actor's handle is dead.
func (s *Supervisor) board(one entry) (*board, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return nil, errors.New("the board pool is closed")
	}
	if held, ok := s.boards[one.key]; ok {
		if !held.Gone() {
			return held, nil
		}
		delete(s.boards, one.key)
	}
	created := newBoard(s, one)
	s.boards[one.key] = created
	return created, nil
}

// find locates one attached board by the name it is listed under.
func (s *Supervisor) find(name string) (entry, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, one := range s.catalog {
		if one.name == name {
			return one, true
		}
	}
	return entry{}, false
}

// Close shuts every board down, whether or not anything still holds one.
func (s *Supervisor) Close() error {
	s.mutex.Lock()
	s.closed = true
	held := make([]*board, 0, len(s.boards))
	for key, one := range s.boards {
		held = append(held, one)
		delete(s.boards, key)
	}
	s.mutex.Unlock()

	for _, one := range held {
		one.shutdown()
	}
	return nil
}
