// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/chardev"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/pcie/windriver"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Backend is the name these tuners are listed under. The Linux side uses the
// same name for the same cards, reached there through a character device; a
// tuner's full name tells the two apart, one being built from a device node
// and the other from a place on the bus.
const Backend = "pt1"

// What the cards receive, and nothing else: the demodulator cores are ISDB
// only and the tuners cover the satellite first IF and the terrestrial band.
// Two spellings of each: the first is how the rest of the program writes a
// delivery system, the second what it folds to for comparison.
const (
	satelliteName   = "ISDB-S"
	terrestrialName = "ISDB-T"

	satelliteSystem   = "ISDBS"
	terrestrialSystem = "ISDBT"
)

// Supports reports whether this backend receives a delivery system at all.
func Supports(deliverySystem string) bool {
	switch tuning.Normalize(deliverySystem) {
	case satelliteSystem, terrestrialSystem:
		return true
	}
	return false
}

func systemOf(receiver int) string {
	if satelliteReceiver(receiver) {
		return satelliteSystem
	}
	return terrestrialSystem
}

func systemNameOf(receiver int) string {
	if satelliteReceiver(receiver) {
		return satelliteName
	}
	return terrestrialName
}

func describeHalf(receiver int) string {
	if satelliteReceiver(receiver) {
		return "satellite"
	}
	return "terrestrial"
}

// tunerNumber is which of the two tuners of a kind this receiver is, counted
// from one. The standards alternate, so each pair is receivers 0 and 2, or 1
// and 3.
func tunerNumber(receiver int) int { return receiver/2 + 1 }

// Found is one of a card's four receivers.
type Found struct {
	// Name is what to show for it.
	Name string
	// Selector is what Open takes: the card's place on the bus and which of
	// its four receivers, as "<bus>:<slot>.<function>/<receiver>".
	Selector string
	// Deliveries is what this receiver can be tuned to.
	Deliveries []string
}

// Devices lists the receivers of every PT1 and PT2 in the machine. No card is
// opened: the scan only asks the driver what is on the bus, and a card opened
// here would be a card something else could not have.
func Devices() ([]Found, error) {
	var found []Found
	for _, one := range models {
		// A card is listed only through its own driver. Asking the PT1's driver
		// what PT2s are on the bus would answer, but nothing could be done with
		// the answer: the card can only be opened by the driver bound to it.
		session, err := sessionFor(one.model)
		if errors.Is(err, ErrNoSDK) {
			return nil, nil
		}
		if err != nil {
			// A machine has the driver for the card it has. The other model
			// having no driver is the ordinary case, not a fault.
			continue
		}
		slots, err := session.Scan(pciVendor, one.device)
		session.Close()
		if err != nil {
			return nil, err
		}
		for _, slot := range slots {
			for receiver := range receivers {
				found = append(found, Found{
					Name: fmt.Sprintf("Earthsoft %s %s tuner %d",
						one.model, describeHalf(receiver), tunerNumber(receiver)),
					Selector:   selectorOf(slot, receiver),
					Deliveries: []string{systemNameOf(receiver)},
				})
			}
		}
	}
	return found, nil
}

// selectorOf and parseSelector are the two directions of a receiver's name.
func selectorOf(slot windriver.Slot, receiver int) string {
	return fmt.Sprintf("%s/%d", slot, receiver)
}

func parseSelector(selector string) (windriver.Slot, int, error) {
	place, number, found := strings.Cut(selector, "/")
	if !found {
		return windriver.Slot{}, 0, fmt.Errorf("%q does not name a receiver of a PT1 or PT2", selector)
	}
	receiver, err := strconv.Atoi(number)
	if err != nil || receiver < 0 || receiver >= receivers {
		return windriver.Slot{}, 0, fmt.Errorf("%q does not name one of the card's %d receivers", selector, receivers)
	}
	bus, rest, found := strings.Cut(place, ":")
	if !found {
		return windriver.Slot{}, 0, fmt.Errorf("%q does not name a place on the bus", selector)
	}
	device, function, found := strings.Cut(rest, ".")
	if !found {
		return windriver.Slot{}, 0, fmt.Errorf("%q does not name a place on the bus", selector)
	}
	numbers := make([]uint32, 3)
	for index, text := range []string{bus, device, function} {
		value, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return windriver.Slot{}, 0, fmt.Errorf("%q does not name a place on the bus", selector)
		}
		numbers[index] = uint32(value)
	}
	return windriver.Slot{Bus: numbers[0], Slot: numbers[1], Function: numbers[2]}, receiver, nil
}

// Device is one receiver of one card.
type Device struct {
	held     *held
	receiver int
	name     string

	tuned     bool
	frequency int64

	stream *Stream
	closed bool
}

// Open claims one receiver. The card behind it is not opened again if it is
// already open: WinDriver allows a single open of a card, and all four
// receivers have to be usable at once.
func Open(selector string) (*Device, error) {
	slot, receiver, err := parseSelector(selector)
	if err != nil {
		return nil, err
	}
	card, err := acquire(slot)
	if err != nil {
		return nil, err
	}
	if err := card.card.claim(receiver); err != nil {
		release(slot)
		return nil, err
	}
	return &Device{
		held:     card,
		receiver: receiver,
		name: fmt.Sprintf("Earthsoft %s %s tuner %d at %s",
			card.model, describeHalf(receiver), tunerNumber(receiver), slot),
	}, nil
}

// Name is the receiver in words.
func (device *Device) Name() string { return device.name }

// Supports reports whether this receiver takes a delivery system. Half the
// card takes each and neither takes the other's.
func (device *Device) Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == systemOf(device.receiver)
}

// Tune points the receiver at a channel.
func (device *Device) Tune(properties map[string]string) error {
	if device.closed {
		return errors.New("this receiver has been closed")
	}
	system := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	if !device.Supports(system) {
		return fmt.Errorf("%s does not receive %s", device.name, properties["DELIVERY_SYSTEM"])
	}
	if err := tuning.RefuseGain(properties, device.name); err != nil {
		return err
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	kilohertz := chardev.KilohertzOf(frequency)

	// The satellite tuner's filter follows the symbol rate, so it is passed
	// down when the channel says one.
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", false)
	if err != nil {
		return err
	}

	stream := uint16(0)
	if system == satelliteSystem {
		if stream, err = tuning.StreamID(properties); err != nil {
			return err
		}
	}

	// The supply comes up before the tune: the tune waits for a lock, and no
	// lock comes while the low-noise block is unpowered.
	if system == satelliteSystem {
		supply, err := tuning.SatelliteSupply(properties)
		if err != nil {
			return err
		}
		raised, err := device.held.card.requestSupply(device.receiver, supply)
		if err != nil {
			return err
		}
		if raised {
			time.Sleep(supplySettle)
		}
	}

	fe := device.held.card.frontends[device.receiver]
	if err := fe.wakeDemod(); err != nil {
		return err
	}
	if err := fe.wakeTuner(); err != nil {
		return err
	}
	device.tuned, device.frequency = true, kilohertz
	if err := fe.Tune(kilohertz, uint32(symbolRate/1000)); err != nil {
		return err
	}
	if system == satelliteSystem {
		// Selecting a stream needs the demodulator to have acquired: the table
		// a relative slot resolves through is one the transponder sends.
		if _, err := fe.WaitForLock(lockBudget); err != nil {
			return err
		}
		if err := fe.SelectStream(stream); err != nil {
			return err
		}
	}
	return nil
}

// supplySettle is how long the lead is left to come up before the signal on
// it is used.
const supplySettle = 100 * time.Millisecond

// lockBudget is how long acquisition is waited for when a later step needs it.
const lockBudget = 2 * time.Second

// Measurement is a reading and whether the hardware had one.
type Measurement struct {
	Value float64
	Known bool
}

// Status is what the demodulator says about acquisition.
type Status struct {
	Locked   bool
	Names    []string
	CNR      Measurement
	Strength Measurement
}

// Status asks the demodulator how it is getting on.
func (device *Device) Status() (Status, error) {
	if !device.tuned {
		return Status{}, nil
	}
	fe := device.held.card.frontends[device.receiver]
	locked, err := fe.Locked()
	if err != nil {
		return Status{}, err
	}
	status := Status{Locked: locked}
	if locked {
		status.Names = []string{"SIGNAL", "CARRIER", "VITERBI", "SYNC", "LOCK"}
	}
	// Optional: a demodulator that will not answer must not fail the status.
	if cnr, measured, err := fe.CNR(); err == nil && measured {
		status.CNR = Measurement{Value: cnr, Known: true}
	}
	// No signal level on either half of this card: neither tuner has a
	// register for one (the terrestrial tuner is a bare PLL with nothing to
	// read back), and the demodulator's gain is a gain rather than a level.
	return status, nil
}

// OpenStream starts this receiver's share of the card's transfer.
func (device *Device) OpenStream() (*Stream, error) {
	if device.closed {
		return nil, errors.New("this receiver has been closed")
	}
	stream, err := device.held.card.openStream(device.receiver)
	if err != nil {
		return nil, err
	}
	device.stream = stream
	return stream, nil
}

// Close puts the receiver back to sleep and lets the card go.
func (device *Device) Close() error {
	if device.closed {
		return nil
	}
	device.closed = true
	if device.stream != nil {
		_ = device.stream.Close()
		device.stream = nil
	}
	fe := device.held.card.frontends[device.receiver]
	// Whatever this receiver was asking of the lead, it asks no more.
	_, _ = device.held.card.requestSupply(device.receiver, tuning.Supply{Off: true})
	_ = fe.sleepDemod()
	device.held.card.unclaim(device.receiver)
	release(device.held.slot)
	return nil
}

// held is one open card and the count of receivers using it.
type held struct {
	slot    windriver.Slot
	model   Model
	session *windriver.Session
	card    *card
	users   int
	// forgetHalt takes back the card's registration with device.OnHalt.
	forgetHalt func()
}

var (
	openMutex sync.Mutex
	openCards = map[windriver.Slot]*held{}
)

// acquire opens a card, or joins whoever has it open already.
func acquire(slot windriver.Slot) (*held, error) {
	openMutex.Lock()
	defer openMutex.Unlock()
	if already, ok := openCards[slot]; ok {
		already.users++
		return already, nil
	}
	model, err := modelAt(slot)
	if err != nil {
		return nil, err
	}
	// The session has to be the one for this card's own model: a driver bound
	// to the other card refuses to open this one.
	session, err := sessionFor(model)
	if err != nil {
		return nil, err
	}
	opened, err := open(session, slot, model)
	if err != nil {
		session.Close()
		return nil, err
	}
	one := &held{slot: slot, model: model, session: session, card: opened, users: 1}
	one.forgetHalt = device.OnHalt(opened.halt)
	openCards[slot] = one
	return one, nil
}

// models are the two cards this backend drives, each with the identifier the
// bus knows it by.
var models = []struct {
	model  Model
	device uint16
}{{ModelPT1, pciDevicePT1}, {ModelPT2, pciDevicePT2}}

// modelAt asks the bus which of the two cards is at a place. The card says
// nothing about itself once open -- there is no version register -- so each
// identifier is scanned for in turn.
func modelAt(slot windriver.Slot) (Model, error) {
	for _, one := range models {
		session, err := sessionFor(one.model)
		if err != nil {
			continue
		}
		slots, err := session.Scan(pciVendor, one.device)
		session.Close()
		if err != nil {
			return 0, err
		}
		for _, found := range slots {
			if found == slot {
				return one.model, nil
			}
		}
	}
	return 0, fmt.Errorf("there is no PT1 or PT2 at %s", slot)
}

// release lets a receiver go, and the card with it when it was the last.
func release(slot windriver.Slot) {
	openMutex.Lock()
	defer openMutex.Unlock()
	one, ok := openCards[slot]
	if !ok {
		return
	}
	one.users--
	if one.users > 0 {
		return
	}
	delete(openCards, slot)
	one.forgetHalt()
	_ = one.card.Close()
	_ = one.session.Close()
}
