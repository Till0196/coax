// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package usbcard is the winscard provider for the card interfaces built into
// tuner boards.
//
// Both bindings share it: the IFD handler binds directly, since pcscd
// multiplexes above it, while the WinSCard-compatible DLL binds through
// winscard.Service alongside a provider forwarding to the platform's own
// resource manager.
//
// Over device.CardReader it adds what PC/SC needs and a board driver has no
// reason to know about: stable reader names, board lifetime across connections,
// share modes and transactions for several clients on one slot, and card
// presence as an event.
package usbcard

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/usb"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
	"github.com/Till0196/coax/internal/winscard"
)

// BoardSource is where this package gets boards from. The board a card slot is
// in is the same USB device a tuner streams from, and only one open handle may
// have it. Standing alone, this package opens and closes boards itself; inside
// the tuner host it is given a source handing out shares of boards somebody
// else holds.
type BoardSource interface {
	// Boards lists the attached boards without opening one.
	Boards() ([]Attached, error)
	// Open borrows a board.
	Open(backend, selector string) (Borrow, error)
}

// Borrow is a board lent out by a source. The borrower must not close the board
// -- it calls Release, and the owner decides when the handle actually shuts --
// because inside the tuner host a recording may still be running on that
// handle.
type Borrow struct {
	Board device.Board
	// Identity says which physical board this turned out to be.
	Identity Identity
	// Valid reports whether the board is still the one this borrow was taken from.
	// It is asked before every use and so must be cheap: a source that shares
	// boards answers from a counter it already keeps.
	Valid func() bool
	// Release gives the board back.
	Release func()
}

// Attached is one board a source can see, as reported without opening it.
type Attached struct {
	Backend  string
	Selector string
	ID       usbio.ID
	Serial   string
	Port     string
}

// Identity is what tells one attached board from another across a replug.
// Generation counts the times the board behind a name has been opened; a borrow
// taken at one generation and used at another names hardware that has gone, and
// T=1 cannot be continued across that because both ends count messages.
type Identity struct {
	Serial     string
	Port       string
	Generation uint64
}

// registrySource is the source this package uses when nothing else is given:
// it opens boards from the registry and closes them again itself.
type registrySource struct {
	firmwareDir string
}

func (r registrySource) Boards() ([]Attached, error) {
	attached, err := detect()
	if err != nil {
		return nil, err
	}
	boards := make([]Attached, 0, len(attached))
	for _, one := range attached {
		boards = append(boards, Attached{
			Backend:  one.Backend,
			Selector: one.Device,
			ID:       one.ID,
			Serial:   one.Serial,
			Port:     one.Port,
		})
	}
	return boards, nil
}

func (r registrySource) Open(backend, selector string) (Borrow, error) {
	board, err := openBoard(backend, device.Options{Selector: selector, FirmwareDir: r.firmwareDir})
	if err != nil {
		return Borrow{}, err
	}
	// Nothing else holds this board, so letting go of it is closing it, and
	// there is nothing that could swap it underneath: a board that goes away
	// shows up as a transfer failing, which ends the session anyway.
	return Borrow{
		Board:   board,
		Valid:   func() bool { return true },
		Release: func() { _ = board.Close() },
	}, nil
}

// detect and openBoard are the registry calls the default source makes, named
// here so that tests can put a board of their own in front of them.
var (
	detect    = usb.Detect
	openBoard = usb.OpenBoard
)

// slotBackends are the backends whose boards can carry a card slot, with the
// vendor and model to name a reader after.
//
// Enumeration cannot ask the hardware: finding out whether a board has a slot
// means opening it, which powers it up and may load firmware. So the list is by
// backend, and a board of a card-carrying backend that turns out to have no
// slot is answered for when it is connected to rather than when it is listed.
var slotBackends = map[string]slotNaming{
	"px4":       {"PLEX", "PX-W3U4 series"},
	"pxmlt":     {"PLEX", "PX-MLT series"},
	"isdb2056":  {"DigiBest", "DTV02A-1T1S-U series"},
	"s1ur":      {"PLEX", "PX-S1UR series"},
	"px3":       {"PLEX", "PX-S3U series"},
	"fsusb2":    {"KEIAN", "KTV-FSUSB2"},
	"fsusb2i":   {"KEIAN", "KTV-FSMINI"},
	"us3pout":   {"DigiBest", "US-3POUT"},
	"cech-zd1j": {"Sony", "CECH-ZD1 J"},
}

// slotNaming is the vendor and model a reader is named after.
type slotNaming struct{ vendor, model string }

// Config settles what the provider cannot work out for itself.
type Config struct {
	// FirmwareDir holds the blobs the firmware-loading boards need. Empty
	// means the platform default.
	FirmwareDir string
	// PollInterval is how often a wait looks at a slot. Card insertion is not
	// an interrupt on any of these interfaces, so a wait is a poll however it
	// is dressed up. Zero means DefaultPollInterval.
	PollInterval time.Duration
	// Boards is where boards come from. Nil means the registry, which this
	// package then opens and closes itself.
	Boards BoardSource
}

// DefaultPollInterval is slow enough not to keep a USB bridge busy and quick
// enough that inserting a card feels immediate.
const DefaultPollInterval = 400 * time.Millisecond

// Provider is the winscard.Provider over the attached boards. One serves every
// context, because there is one card slot per enclosure however many tuners,
// contexts or processes are looking at it.
type Provider struct {
	config Config

	mutex sync.Mutex
	slots map[string]*slot
	// known is the reader set as of the last enumeration, and generation
	// counts the times that set changed. The PnP pseudo-reader reports the
	// generation, which is how an application learns a tuner was plugged in.
	known      map[string]bool
	generation uint16
}

// New builds the provider. Nothing is opened or enumerated until a call needs it.
func New(config Config) *Provider {
	if config.PollInterval <= 0 {
		config.PollInterval = DefaultPollInterval
	}
	if config.Boards == nil {
		config.Boards = registrySource{firmwareDir: config.FirmwareDir}
	}
	return &Provider{config: config, slots: make(map[string]*slot), known: make(map[string]bool)}
}

// Name identifies the provider in diagnostics.
func (p *Provider) Name() string { return "usb" }

// EstablishContext opens a context. The provider holds the state that matters,
// so a context is a thin thing: what it owns is the connections made through
// it, and releasing it closes those.
func (p *Provider) EstablishContext(scope winscard.Scope) (winscard.ProviderContext, error) {
	return &providerContext{provider: p}, nil
}

// device is one attached board that may have a slot.
type cardDevice struct {
	name     string
	backend  string
	selector string
	serial   string
	port     string
	vendor   string
	model    string
}

// devices enumerates the attached boards of card-carrying backends and gives
// each the reader name it will be known by.
func (p *Provider) devices() ([]cardDevice, error) {
	attached, err := p.config.Boards.Boards()
	if err != nil {
		if errors.Is(err, usbio.ErrNoDevices) {
			// This one already says what happened and what to do about it.
			return nil, err
		}
		return nil, fmt.Errorf("list USB boards: %w", err)
	}
	var found []cardDevice
	numbered := numberedBoards(attached)
	for _, candidate := range attached {
		naming, ok := slotBackends[candidate.Backend]
		if !ok {
			continue
		}
		name := readerName(naming.vendor, naming.model, boardIdentity(candidate))
		if candidate.Serial == "" {
			name = numberedReaderName(naming.vendor, naming.model, numbered[candidate.Selector])
		}
		found = append(found, cardDevice{
			name:     name,
			backend:  candidate.Backend,
			selector: candidate.Selector,
			serial:   candidate.Serial,
			port:     candidate.Port,
			vendor:   naming.vendor,
			model:    naming.model,
		})
	}
	p.observe(found)
	return found, nil
}

// boardIdentity is the token in a reader's published name that tells one
// attached board from another. It is built from whatever survives a replug: the
// serial number, then the socket the board is in, and only failing both the bus
// address, which the kernel reassigns. A name a user wrote into a configuration
// should go on meaning the same slot.
func boardIdentity(one Attached) string {
	switch {
	case one.Serial != "" && one.Port != "":
		return identity(one.Backend, one.Serial, one.Port)
	case one.Serial != "":
		return identity(one.Backend, one.Serial)
	case one.Port != "":
		return identity(one.Backend, one.Port)
	default:
		return identity(one.Backend, one.Selector,
			fmt.Sprintf("%04X:%04X", one.ID.Vendor, one.ID.Product))
	}
}

// observe records the reader set and bumps the generation when it changes.
func (p *Provider) observe(found []cardDevice) {
	current := make(map[string]bool, len(found))
	for _, one := range found {
		current[one.name] = true
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	changed := len(current) != len(p.known)
	if !changed {
		for name := range current {
			if !p.known[name] {
				changed = true
				break
			}
		}
	}
	if changed {
		p.known = current
		p.generation++
	}
}

// readerSetGeneration is what the PnP pseudo-reader reports.
func (p *Provider) readerSetGeneration() uint16 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.generation
}

// find resolves a reader name to the board behind it, re-enumerating so that a
// board unplugged since the last listing is reported as gone rather than
// opened.
func (p *Provider) find(reader string) (cardDevice, error) {
	found, err := p.devices()
	if err != nil {
		return cardDevice{}, err
	}
	for _, one := range found {
		if one.name == reader {
			return one, nil
		}
	}
	return cardDevice{}, winscard.ErrUnknownReader
}

// slotFor returns the shared state for a reader, creating it on first use.
func (p *Provider) slotFor(one cardDevice) *slot {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if existing, ok := p.slots[one.name]; ok {
		return existing
	}
	created := newSlot(p, one)
	p.slots[one.name] = created
	return created
}

// readerName builds the name applications will see and remember, for a board
// with a serial number. The identifier is a hash so the name is one token wide,
// and it follows the board from socket to socket.
func readerName(vendor, model, id string) string {
	return fmt.Sprintf("%s %s Smart Card Reader (ID: %s)", vendor, model, id)
}

// numberedReaderName names the reader of a board with no serial number by its
// place among the boards of its model, the way PC/SC readers are commonly
// numbered. A name built from the socket would change every time the board was
// moved, and an application remembers the name; a number does not change while
// one board of the model is attached, wherever it is plugged in.
func numberedReaderName(vendor, model string, number int) string {
	return fmt.Sprintf("%s %s Smart Card Reader %d", vendor, model, number)
}

// numberedBoards numbers the card-carrying boards that have no serial number,
// from 0 within each model, in the order of the sockets they are in. Keyed by
// selector.
func numberedBoards(attached []Attached) map[string]int {
	byModel := map[slotNaming][]Attached{}
	for _, one := range attached {
		naming, ok := slotBackends[one.Backend]
		if !ok || one.Serial != "" {
			continue
		}
		byModel[naming] = append(byModel[naming], one)
	}
	numbers := map[string]int{}
	for _, boards := range byModel {
		sort.Slice(boards, func(i, j int) bool {
			if boards[i].Port != boards[j].Port {
				return boards[i].Port < boards[j].Port
			}
			return boards[i].Selector < boards[j].Selector
		})
		for number, one := range boards {
			numbers[one.Selector] = number
		}
	}
	return numbers
}

// identity hashes what tells one attached board from another. FNV-1a is used
// for the same reason the Windows implementation uses it: it is short, it is
// stable across runs, and nothing here depends on it being hard to invert.
func identity(parts ...string) string {
	sum := fnv.New64a()
	for _, part := range parts {
		_, _ = sum.Write([]byte(part))
		_, _ = sum.Write([]byte{0})
	}
	return fmt.Sprintf("%016X", sum.Sum64())
}

type providerContext struct {
	provider *Provider

	mutex       sync.Mutex
	released    bool
	connections []*connection
}

// Readers lists the attached boards of card-carrying backends.
func (c *providerContext) Readers(groups []string) ([]string, error) {
	if !wantsAllReaders(groups) {
		return nil, nil
	}
	found, err := c.provider.devices()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(found))
	for _, one := range found {
		names = append(names, one.name)
	}
	return names, nil
}

// ReaderGroups reports the one group these readers are in. There is no reason
// to invent groups for hardware that is not user-arranged.
func (c *providerContext) ReaderGroups() ([]string, error) {
	return []string{winscard.DefaultReaderGroup}, nil
}

// wantsAllReaders reports whether a group list covers the default group. An
// empty list and the all-readers group both do.
func wantsAllReaders(groups []string) bool {
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		if group == winscard.AllReaderGroups || group == winscard.DefaultReaderGroup {
			return true
		}
	}
	return false
}

// Release closes every connection made through the context.
func (c *providerContext) Release() error {
	c.mutex.Lock()
	if c.released {
		c.mutex.Unlock()
		return nil
	}
	c.released = true
	open := c.connections
	c.connections = nil
	c.mutex.Unlock()

	var first error
	for _, one := range open {
		if err := one.Disconnect(winscard.LeaveCard); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (c *providerContext) track(one *connection) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.connections = append(c.connections, one)
}

func (c *providerContext) forget(one *connection) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	for index, candidate := range c.connections {
		if candidate == one {
			c.connections = append(c.connections[:index], c.connections[index+1:]...)
			return
		}
	}
}
