// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

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

// Backend is the name these tuners are listed under.
const Backend = "pt3"

// What the card receives, and nothing else: the demodulator cores are ISDB only
// and the tuners cover the satellite first IF and the terrestrial band. Two
// spellings of each: how a listing and a channel file write it, and what that
// folds to for comparison.
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

// systemOf is which half of the card a channel is on, as a property compares.
func systemOf(channel int) string {
	if satelliteChannel(channel) {
		return satelliteSystem
	}
	return terrestrialSystem
}

// systemNameOf is the same, as a listing shows it.
func systemNameOf(channel int) string {
	if satelliteChannel(channel) {
		return satelliteName
	}
	return terrestrialName
}

// Found is one of a card's four receivers. A card is listed as four tuners
// because the four are independent: each has its own demodulator core and its own
// transfer engine. What they share -- the I2C master, the supply on the lead --
// is shared inside this package.
type Found struct {
	// Name is what to show for it.
	Name string
	// Selector is what Open takes: "<bus>:<slot>.<function>/<channel>".
	Selector string
	// Deliveries is what this receiver can be tuned to.
	Deliveries []string
}

// Devices lists the receivers of every PT3 in the machine. No card is opened: a
// card opened here would be one something else could not have. See
// pool.Detected's Exclusive.
func Devices() ([]Found, error) {
	installed, err := library()
	if errors.Is(err, ErrNoSDK) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	session, err := installed.Open()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	slots, err := session.Scan(pciVendor, pciDevice)
	if err != nil {
		return nil, err
	}
	found := make([]Found, 0, len(slots)*channels)
	for _, slot := range slots {
		for channel := range channels {
			found = append(found, Found{
				Name:       fmt.Sprintf("Earthsoft PT3 %s tuner %d", strings.ToLower(describeHalf(channel)), tunerOf(channel)+1),
				Selector:   selectorOf(slot, channel),
				Deliveries: []string{systemNameOf(channel)},
			})
		}
	}
	return found, nil
}

func describeHalf(channel int) string {
	if satelliteChannel(channel) {
		return "satellite"
	}
	return "terrestrial"
}

// selectorOf and parseSelector are the two directions of a receiver's name.
func selectorOf(slot windriver.Slot, channel int) string {
	return fmt.Sprintf("%s/%d", slot, channel)
}

func parseSelector(selector string) (windriver.Slot, int, error) {
	place, number, found := strings.Cut(selector, "/")
	if !found {
		return windriver.Slot{}, 0, fmt.Errorf("%q does not name a receiver of a PT3", selector)
	}
	channel, err := strconv.Atoi(number)
	if err != nil || channel < 0 || channel >= channels {
		return windriver.Slot{}, 0, fmt.Errorf("%q does not name one of the card's %d receivers", selector, channels)
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
	return windriver.Slot{Bus: numbers[0], Slot: numbers[1], Function: numbers[2]}, channel, nil
}

// Device is one receiver of one card.
type Device struct {
	held    *held
	channel int
	name    string

	// tuned is what the receiver was last pointed at, for Status to know what
	// it is looking at.
	tuned     bool
	system    string
	frequency int64

	stream *Stream
	closed bool
}

// Open claims one receiver, joining an already open card: WinDriver allows a
// single open of a card. See held.
func Open(selector string) (*Device, error) {
	slot, channel, err := parseSelector(selector)
	if err != nil {
		return nil, err
	}
	card, err := acquire(slot)
	if err != nil {
		return nil, err
	}
	if err := card.claim(channel); err != nil {
		release(slot)
		return nil, err
	}
	return &Device{
		held:    card,
		channel: channel,
		name:    fmt.Sprintf("Earthsoft PT3 %s tuner %d at %s", describeHalf(channel), tunerOf(channel)+1, slot),
	}, nil
}

func (device *Device) Name() string { return device.name }

// Supports reports whether this receiver takes a delivery system. Half the card
// takes each, and neither takes the other's.
func (device *Device) Supports(deliverySystem string) bool {
	return tuning.Normalize(deliverySystem) == systemOf(device.channel)
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

	// A satellite transponder carries several streams. A channel that does not
	// say takes relative slot zero rather than whichever the demodulator was
	// last left on.
	stream := uint16(0)
	if system == satelliteSystem {
		if stream, err = tuning.StreamID(properties); err != nil {
			return err
		}
	}

	// Before the tune, not after: the tune waits for a lock, which will not come
	// while the low-noise block is unpowered.
	if system == satelliteSystem {
		supply, err := tuning.SatelliteSupply(properties)
		if err != nil {
			return err
		}
		raised, err := device.held.card.requestSupply(device.channel, supply)
		if err != nil {
			return err
		}
		if raised {
			time.Sleep(supplySettle)
		}
	}

	fe := device.held.card.frontends[device.channel]
	if err := fe.wakeDemod(); err != nil {
		return err
	}
	if err := fe.wakeTuner(); err != nil {
		return err
	}
	device.tuned, device.system, device.frequency = true, system, kilohertz
	if err := fe.Tune(kilohertz); err != nil {
		return err
	}
	if system == satelliteSystem {
		// Selecting a stream needs the demodulator to have acquired: the table a
		// relative slot resolves through is sent by the transponder.
		if _, err := fe.WaitForLock(lockBudget); err != nil {
			return err
		}
		if err := fe.SelectStream(stream); err != nil {
			return err
		}
	}
	return nil
}

// supplySettle is how long the lead is left to come up before anything is asked
// of the signal on it.
const supplySettle = 100 * time.Millisecond

// lockBudget is how long acquisition is waited for where something after it
// depends on it. Status does not use it; it answers whatever the demodulator says
// when asked.
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
	fe := device.held.card.frontends[device.channel]
	locked, err := fe.Locked()
	if err != nil {
		return Status{}, err
	}
	status := Status{Locked: locked}
	if locked {
		status.Names = []string{"SIGNAL", "CARRIER", "VITERBI", "SYNC", "LOCK"}
	}
	// A demodulator that will not answer must not turn a good lock into an error.
	if cnr, measured, err := fe.CNR(); err == nil && measured {
		status.CNR = Measurement{Value: cnr, Known: true}
	}
	// Only the terrestrial tuner measures what is arriving. The satellite one has
	// no such register, and the demodulator's gain is a gain rather than a level:
	// converting it to dBm would need a curve nobody has published.
	if fe.mxl != nil {
		if strength, err := fe.mxl.SignalStrength(); err == nil {
			status.Strength = Measurement{Value: float64(strength) / 1000, Known: true}
		}
	}
	return status, nil
}

// OpenStream starts the card's transfer for this receiver.
func (device *Device) OpenStream() (*Stream, error) {
	if device.closed {
		return nil, errors.New("this receiver has been closed")
	}
	stream, err := device.held.card.openStream(device.channel)
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
	var stopped error
	if device.stream != nil {
		stopped = device.stream.Close()
		device.stream = nil
	}
	fe := device.held.card.frontends[device.channel]
	// This receiver wants nothing of the lead any more. Before the tuner sleeps,
	// because the supply is the card's and another receiver may still want it.
	_, _ = device.held.card.requestSupply(device.channel, tuning.Supply{Off: true})
	_ = fe.sleepTuner()
	_ = fe.sleepDemod()
	device.held.card.unclaim(device.channel)
	release(device.held.slot)
	return stopped
}

// held is one open card and the count of receivers using it. WinDriver allows a
// card to be opened once and this card has four receivers working at the same
// time, so it is opened at most once and let go when the last receiver does.
type held struct {
	slot    windriver.Slot
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
	installed, err := library()
	if err != nil {
		return nil, err
	}
	session, err := installed.Open()
	if err != nil {
		return nil, err
	}
	opened, err := open(session, slot)
	if err != nil {
		session.Close()
		return nil, err
	}
	one := &held{slot: slot, session: session, card: opened, users: 1}
	one.forgetHalt = device.OnHalt(opened.halt)
	openCards[slot] = one
	return one, nil
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
