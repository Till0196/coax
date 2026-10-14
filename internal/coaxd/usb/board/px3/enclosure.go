// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/trace"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
	"github.com/Till0196/coax/pkg/usbio"
)

// The PX-W3U3 V2 is two bridges behind a hub, each streaming one satellite and
// one terrestrial tuner, but only one of them drives anything. The board is
// powered up, and all four demodulators and tuners are reached, through the
// bridge at position zero; the one at position one has nothing on its GPIO or
// I2C and only carries the stream of the second pair. So a V2
// board opened on its second bridge has to borrow the first, and both boards
// share what is behind it.
//
// Every open in this process goes through the shared table below, because
// neither WinUSB nor usbfs lets a device be claimed twice: the first bridge
// has to be the same handle whether its own board or its sibling holds it.

// sharedBridge is an opened bridge and how many holders it has.
type sharedBridge struct {
	bridge   *asv5211.Bridge
	location string
	refs     int
}

var (
	sharedMutex sync.Mutex
	shared      = map[string]*sharedBridge{}
	enclosures  = map[string]*enclosure{}
)

// acquireBridge opens the bridge a selector picks, or takes another hold on it
// when this process has it open already. wait is how long to give a board that
// has just been booted to come back.
func acquireBridge(selector string, wait time.Duration) (*sharedBridge, error) {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()
	return acquireLocked(selector, wait)
}

func acquireLocked(selector string, wait time.Duration) (*sharedBridge, error) {
	if attached, err := usbio.Resolve(selector, supportedIDs); err == nil {
		if held := shared[attached.Location]; held != nil {
			held.refs++
			return held, nil
		}
	}
	transport, err := openWarm(selector, wait)
	if err != nil {
		return nil, err
	}
	held := &sharedBridge{bridge: asv5211.New(transport), location: transport.Name(), refs: 1}
	shared[held.location] = held
	return held, nil
}

// release drops one hold, closing the bridge with the last.
func (held *sharedBridge) release() error {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()
	return held.releaseLocked()
}

func (held *sharedBridge) releaseLocked() error {
	held.refs--
	if held.refs > 0 {
		return nil
	}
	delete(shared, held.location)
	return held.bridge.Close()
}

// position is where a V2 bridge sits in its enclosure, from the top two bits
// of the last byte of its customer information: 0, 2, 1 and 3 there stand for
// positions 0, 1, 2 and 3.
func position(bridge *asv5211.Bridge) (int, error) {
	info, err := bridge.CustomerInfo()
	if err != nil {
		return 0, fmt.Errorf("read the board's customer information: %w", err)
	}
	return [4]int{0, 2, 1, 3}[info[len(info)-1]>>6], nil
}

// enclosure is the part of a V2 both of its bridges share: the controlling
// bridge, and the demodulators and tuners of both pairs behind it.
type enclosure struct {
	controller *sharedBridge
	refs       int
	// pairs is each position's front ends, by position within the pair.
	pairs [2]frontEnds
	// lnb is the aerial supply, switched on the controlling bridge for both.
	lnb *device.Latch
	// chips is the card interface ICs in the four tuners' stream paths, reached
	// through the controlling bridge for both.
	chips tsChips
}

// frontEnds is one bridge's worth of demodulators and tuners.
type frontEnds struct {
	wired          wiring
	demodT, demodS *tc90522.Demod
	tunerT         terrestrialTuner
	tunerS         satelliteTuner
}

// openV2Enclosure finds or brings up the enclosure a V2 bridge belongs to. own is
// the bridge being opened and at is its position; the controlling bridge is the
// even position below it.
func openV2Enclosure(own *sharedBridge, at int) (*enclosure, error) {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()
	controller := own
	if at%2 == 1 {
		location, err := siblingOf(own.location)
		if err != nil {
			return nil, err
		}
		if found := enclosures[location]; found != nil {
			found.refs++
			return found, nil
		}
		if controller, err = acquireLocked(location, 0); err != nil {
			return nil, fmt.Errorf("open the bridge that drives this one: %w", err)
		}
		if first, err := position(controller.bridge); err != nil || first != at-1 {
			_ = controller.releaseLocked()
			if err == nil {
				err = fmt.Errorf("its sibling %s is at position %d, not %d", location, first, at-1)
			}
			return nil, fmt.Errorf("find the bridge that drives this one: %w", err)
		}
	} else {
		if found := enclosures[own.location]; found != nil {
			found.refs++
			return found, nil
		}
		controller.refs++
	}
	built, err := bringUpV2(controller)
	if err != nil {
		_ = controller.releaseLocked()
		return nil, err
	}
	enclosures[controller.location] = built
	return built, nil
}

// release drops one board's hold on the enclosure.
func (enclosure *enclosure) release() error {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()
	enclosure.refs--
	if enclosure.refs > 0 {
		return nil
	}
	delete(enclosures, enclosure.controller.location)
	return enclosure.controller.releaseLocked()
}

// siblingOf finds the other V2 bridge behind the same hub as the one at location.
func siblingOf(location string) (string, error) {
	attached, err := usbio.Devices()
	if err != nil {
		return "", err
	}
	var own usbio.Attached
	for _, candidate := range attached {
		if candidate.Location == location {
			own = candidate
		}
	}
	var found []string
	for _, candidate := range attached {
		if candidate.Location == location || candidate.ID != (usbio.ID{Vendor: 0x0b06, Product: v2Product}) {
			continue
		}
		if own.Port != "" && parentPort(candidate.Port) != parentPort(own.Port) {
			continue
		}
		found = append(found, candidate.Location)
	}
	trace.Printf("px3 %s (port %q): candidates for the first bridge %v", location, own.Port, found)
	if len(found) != 1 {
		return "", fmt.Errorf("the PX-W3U3 V2 bridge at %s is the second of its enclosure, and %d bridges could be the first", location, len(found))
	}
	return found[0], nil
}

// parentPort is the hub a port path hangs off: "1-2" for "1-2.3".
func parentPort(port string) string {
	if cut := strings.LastIndexByte(port, '.'); cut >= 0 {
		return port[:cut]
	}
	return port
}

// bringUpV2 powers a V2 up through its controlling bridge and brings up both
// pairs of front ends, on the first tuner opened.
func bringUpV2(controller *sharedBridge) (*enclosure, error) {
	bridge := controller.bridge
	trace.Printf("px3 %s: powering the PX-W3U3 V2 up", controller.location)
	for _, step := range v2PowerUp {
		if err := bridge.SetGPIO(step.value, step.mask); err != nil {
			return nil, fmt.Errorf("power the PX-W3U3 V2 up: %w", err)
		}
		time.Sleep(step.wait)
	}
	built := &enclosure{controller: controller, refs: 1}
	built.lnb = device.NewLatch(
		func() error { return bridge.SetGPIO(lnbSupplyMask, lnbSupplyMask) },
		func() error { return bridge.SetGPIO(0, lnbSupplyMask) },
	)
	for at := range built.pairs {
		ends, err := newFrontEnds(bridge, v2WiringAt(at), v2SatelliteInit)
		if err != nil {
			return nil, fmt.Errorf("bring up the PX-W3U3 V2's front ends at position %d: %w", at, err)
		}
		built.pairs[at] = ends
		trace.Printf("px3 %s: pair %d up, demodulators %#02x/%#02x", controller.location, at,
			ends.wired.demodTerrestrial, ends.wired.demodSatellite)
	}
	return built, nil
}

// v2PowerUp is the power-up of a V2, all of it on the bridge at position zero.
// The EEPROM at 0xa8 is read partway through; nothing here reads it, but
// the pause that read takes is kept.
var v2PowerUp = []gpioStep{
	{value: 0x08, mask: 0x08, wait: 50 * time.Millisecond},
	{value: 0, mask: 0x08, wait: 50 * time.Millisecond},
	{value: 0x04, mask: 0x04, wait: 10 * time.Millisecond},
	{value: 0, mask: 0x05, wait: 10 * time.Millisecond},
	{value: 0x04, mask: 0x04, wait: 10 * time.Millisecond},
	{value: 0x10, mask: 0x10, wait: 10 * time.Millisecond},
	{value: 0, mask: 0x10, wait: 10 * time.Millisecond},
	{value: 0x10, mask: 0x10, wait: 110 * time.Millisecond},
	{value: 0x40, mask: 0x40, wait: 10 * time.Millisecond},
	{value: 0, mask: 0x40, wait: 110 * time.Millisecond},
}

// v2SatelliteInit is what each satellite demodulator of a V2 takes before its
// tuner is brought up.
var v2SatelliteInit = []byte{0x13, 0, 0x15, 0, 0x17, 0, 0x1c, 0, 0x1d, 0, 0x1f, 0, 0x07, 0x31, 0x08, 0x77, 0x04, 0x02}
