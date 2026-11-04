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
	"github.com/Till0196/coax/pkg/i2c"
	"github.com/Till0196/coax/pkg/usbio"
)

// Each W3U2/W3U3 enclosure has two streaming bridges. The original generation
// controls each RF pair through its own bridge; V2 controls both through the
// even-position bridge. Opening either pair initializes and retains the whole
// enclosure so a second open cannot reset a pair already in use.
// USB handles are shared because WinUSB and usbfs cannot claim a device twice.

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

// enclosure owns the initialized RF pairs and the USB references backing them.
type enclosure struct {
	key   string
	holds []*sharedBridge
	refs  int
	pairs [2]enclosurePair
}

// enclosurePair keeps each RF pair with its control bus and shared resources.
type enclosurePair struct {
	frontEnds
	*controller
}

// controller owns the state reached through one control bridge. V2 shares it
// between both RF pairs; the FC0012 boards have one per bridge.
type controller struct {
	control  *asv5211.Bridge
	lnb      *device.Latch
	chips    *tsChips
	card     card
	slowdown struct {
		sync.Mutex
		probed, present bool
	}
	amplifiers struct {
		sync.Mutex
		on [2]bool
	}
}

// frontEnds is one bridge's worth of demodulators and tuners.
type frontEnds struct {
	wired          wiring
	demodT, demodS *tc90522.Demod
	tunerT         terrestrialTuner
	tunerS         satelliteTuner
}

func (ends *frontEnds) configureDemods(control boardControl, setups []demodSetup) error {
	demods := [2]*tc90522.Demod{satellite: ends.demodS, terrestrial: ends.demodT}
	for _, setup := range setups {
		demod := demods[setup.index]
		if setup.address != 0 {
			demod = tc90522.New(control.I2C(setup.address), setup.address, false)
		}
		var err error
		if setup.tuner == 0 {
			err = demod.WritePairs(setup.pairs)
		} else if len(setup.pairs)%2 != 0 {
			err = fmt.Errorf("tuner register pair list has an odd length %d", len(setup.pairs))
		} else {
			bus := demod.TunerBus()
			for at := 0; at < len(setup.pairs); at += 2 {
				if err = bus.Write(setup.tuner, setup.pairs[at:at+2]); err != nil {
					break
				}
			}
		}
		if err != nil {
			return fmt.Errorf("configure TC90522 for tuner %d: %w", setup.index, err)
		}
	}
	return nil
}

// openEnclosure initializes a whole enclosure once, publishing it only after
// every RF pair is ready. Until then, acquired USB references belong to this call.
func openEnclosure(own *sharedBridge) (*enclosure, int, error) {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()
	product := own.bridge.ProductID()
	locations, at, err := locateEnclosure(own)
	if err != nil {
		return nil, 0, err
	}
	key := locations[0]
	if found := enclosures[key]; found != nil {
		found.refs++
		return found, at, nil
	}
	var holds []*sharedBridge
	ready := false
	defer func() {
		if !ready {
			_ = releaseBridgesLocked(holds)
		}
	}()
	for _, location := range locations {
		held := own
		if location == own.location {
			held.refs++
		} else {
			held, err = acquireLocked(location, 0)
			if err != nil {
				return nil, 0, fmt.Errorf("open enclosure bridge %s: %w", location, err)
			}
		}
		holds = append(holds, held)
	}
	if product == v2Product && at%2 == 1 {
		first, err := position(holds[0].bridge)
		if err != nil {
			return nil, 0, err
		}
		if first != at-1 {
			return nil, 0, fmt.Errorf("enclosure controller %s is at position %d, want %d", key, first, at-1)
		}
	}
	built, err := initializeEnclosure(holds, product)
	if err != nil {
		return nil, 0, err
	}
	enclosures[key] = built
	ready = true
	return built, at, nil
}

// locateEnclosure returns control bridge locations in pair order and the
// streaming bridge's position. V2 needs only its shared controller retained.
func locateEnclosure(own *sharedBridge) ([]string, int, error) {
	product := own.bridge.ProductID()
	if product == v2Product {
		at, err := position(own.bridge)
		if err != nil {
			return nil, 0, err
		}
		if at > 1 {
			return nil, 0, fmt.Errorf("PX-W3U3 V2 bridge %s reports unsupported position %d", own.location, at)
		}
		location := own.location
		if at%2 == 1 {
			location, err = siblingOf(own.location, product)
			if err != nil {
				return nil, 0, err
			}
		}
		return []string{location}, at, nil
	}
	if product != 0x0004 && product != 0x0005 {
		return []string{own.location}, 0, nil
	}
	sibling, err := siblingOf(own.location, product)
	if err != nil {
		return nil, 0, err
	}
	locations := enclosureLocations(own.location, sibling)
	at := 0
	if locations[0] != own.location {
		at = 1
	}
	return locations, at, nil
}

// Both USB opens must pick the same cache key without interpreting the V2-only
// customer position field on the original hardware.
func enclosureLocations(own, sibling string) []string {
	if own < sibling {
		return []string{own, sibling}
	}
	return []string{sibling, own}
}

func releaseBridgesLocked(holds []*sharedBridge) error {
	var first error
	for at := len(holds) - 1; at >= 0; at-- {
		if err := holds[at].releaseLocked(); first == nil {
			first = err
		}
	}
	return first
}

// initializeEnclosure preserves each product's control topology: W3U2/W3U3
// power each pair through its own bridge, while V2 powers both through one.
func initializeEnclosure(holds []*sharedBridge, product uint16) (*enclosure, error) {
	built := &enclosure{key: holds[0].location, holds: holds, refs: 1}
	sharedControl := product == v2Product
	pairCount := len(holds)
	if sharedControl {
		pairCount = len(built.pairs)
	}
	for at := 0; at < pairCount; at++ {
		controlAt := at
		wired := wiringFor(product)
		if sharedControl {
			controlAt = 0
			wired = v2WiringAt(at)
		}
		bridge := holds[controlAt].bridge
		pair := enclosurePair{}
		if sharedControl && at > 0 {
			pair.controller = built.pairs[0].controller
		} else {
			if err := powerUpBoard(bridge, product); err != nil {
				return nil, fmt.Errorf("power enclosure bridge %s up: %w", bridge.Name(), err)
			}
			on := wired.lnbSupplyOn
			pair.controller = &controller{control: bridge, chips: &tsChips{}}
			pair.amplifiers.on = [2]bool{true, true}
			pair.lnb = device.NewLatch(
				func() error { return bridge.SetGPIO(on, lnbSupplyMask) },
				func() error { return bridge.SetGPIO(^on&lnbSupplyMask, lnbSupplyMask) },
			)
			if wired.noLNBControl {
				pair.lnb = device.NewLatch(nil, nil)
			}
		}
		ends, err := newFrontEnds(bridge, wired)
		if err != nil {
			return nil, fmt.Errorf("initialize enclosure pair %d through %s: %w", at, bridge.Name(), err)
		}
		pair.frontEnds = ends
		built.pairs[at] = pair
	}
	// The W3U2 and W3U3 feed one lead from both bridges' supply lines, so the
	// supply is one for the enclosure and goes on or off on both bridges.
	if !sharedControl && len(holds) == 2 {
		supply := enclosureSupply([]boardControl{holds[0].bridge, holds[1].bridge}, wiringFor(product).lnbSupplyOn)
		built.pairs[0].lnb = supply
		built.pairs[1].lnb = supply
	}
	return built, nil
}

// enclosureSupply is one LNB supply switched on every bridge of an enclosure.
func enclosureSupply(bridges []boardControl, on byte) *device.Latch {
	write := func(value byte) error {
		for _, bridge := range bridges {
			if err := bridge.SetGPIO(value, lnbSupplyMask); err != nil {
				return err
			}
		}
		return nil
	}
	return device.NewLatch(
		func() error { return write(on) },
		func() error { return write(^on & lnbSupplyMask) },
	)
}

// release drops one board's hold on the enclosure.
func (enclosure *enclosure) release() error {
	sharedMutex.Lock()
	defer sharedMutex.Unlock()
	enclosure.refs--
	if enclosure.refs > 0 {
		return nil
	}
	delete(enclosures, enclosure.key)
	return releaseBridgesLocked(enclosure.holds)
}

// enclosureName is the location of an enclosure's first bridge, the same for
// both of its bridges, worked out from the attached devices without opening
// either. Empty for a product with one bridge, and for a bridge whose sibling
// cannot be told.
func enclosureName(attached []usbio.Attached, own usbio.Attached) string {
	if own.ID.Vendor != 0x0b06 || !twoBridges(own.ID.Product) {
		return ""
	}
	sibling, err := enclosureSibling(attached, own.Location, own.ID.Product)
	if err != nil {
		return ""
	}
	return enclosureLocations(own.Location, sibling)[0]
}

// twoBridges reports whether a product is an enclosure of two bridges.
func twoBridges(product uint16) bool {
	return product == 0x0004 || product == 0x0005 || product == v2Product
}

// siblingOf finds the other bridge of the same product behind the same hub.
func siblingOf(location string, product uint16) (string, error) {
	attached, err := usbio.Devices()
	if err != nil {
		return "", err
	}
	return enclosureSibling(attached, location, product)
}

func enclosureSibling(attached []usbio.Attached, location string, product uint16) (string, error) {
	var own usbio.Attached
	for _, candidate := range attached {
		if candidate.Location == location {
			own = candidate
		}
	}
	if own.Location == "" {
		return "", fmt.Errorf("the enclosure bridge %s is no longer attached", location)
	}
	var found []string
	for _, candidate := range attached {
		if candidate.Location == location || candidate.ID != (usbio.ID{Vendor: 0x0b06, Product: product}) {
			continue
		}
		if own.Port != "" {
			if !strings.Contains(own.Port, ".") || !strings.Contains(candidate.Port, ".") ||
				parentPort(candidate.Port) != parentPort(own.Port) {
				continue
			}
		}
		found = append(found, candidate.Location)
	}
	trace.Printf("px3 %s (port %q): enclosure sibling candidates %v", location, own.Port, found)
	if len(found) != 1 {
		return "", fmt.Errorf("the bridge at %s has %d possible enclosure siblings", location, len(found))
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

// v2PowerUp powers both RF pairs through the controlling bridge.
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

// newFrontEnds brings up one pair of demodulators and tuners, reached through
// control.
func newFrontEnds(control boardControl, wired wiring) (frontEnds, error) {
	ends := frontEnds{wired: wired}
	terrestrialBus := control.I2C(wired.demodTerrestrial)
	satelliteBus := control.I2C(wired.demodSatellite)
	ends.demodT = tc90522.New(terrestrialBus, wired.demodTerrestrial, false)
	ends.demodS = tc90522.New(satelliteBus, wired.demodSatellite, false)
	if err := ends.configureDemods(control, wired.beforeRF); err != nil {
		return frontEnds{}, err
	}
	var err error
	if ends.tunerT, err = wired.newTerrestrial(terrestrialBus, wired.demodTerrestrial); err != nil {
		return frontEnds{}, fmt.Errorf("initialize the terrestrial tuner behind %#02x: %w", wired.demodTerrestrial, err)
	}
	if ends.tunerS, err = wired.newSatellite(ends.demodS.TunerBus()); err != nil {
		return frontEnds{}, fmt.Errorf("initialize the satellite tuner behind %#02x: %w", wired.demodSatellite, err)
	}
	if err := ends.configureDemods(control, wired.afterRF); err != nil {
		return frontEnds{}, err
	}
	if tuner, ok := ends.tunerT.(*fc0012Tuner); ok && tuner.w3 {
		if err := control.SetGPIO(0, 0x01); err != nil {
			return frontEnds{}, err
		}
		if err := ends.demodT.Tune(tc90522.TunerFC0012, func() error {
			return tuner.acquire(ends.demodT, 773_143_000)
		}, func() error {
			return ends.configureDemods(control, wired.afterTune[terrestrial])
		}); err != nil {
			return frontEnds{}, fmt.Errorf("initialize W3U2/W3U3 RF acquisition: %w", err)
		}
	}
	return ends, nil
}

// gpioStep is one masked GPIO write of a power-up sequence and the settle after
// it.
type gpioStep struct {
	extended    bool
	value, mask byte
	wait        time.Duration
}

// powerUpSequences selects the GPIO sequence for each product.
var powerUpSequences = map[uint16][]gpioStep{
	// PX-S3U
	0x0001: {
		{value: 0, mask: 0x08, wait: 50 * time.Millisecond},
		{value: 0x44, mask: 0x44, wait: 50 * time.Millisecond},
		{value: 0, mask: 0x44, wait: 50 * time.Millisecond},
		{value: 0x44, mask: 0x44, wait: 50 * time.Millisecond},
		{value: 0, mask: 0x20, wait: 100 * time.Millisecond},
		{value: 0x20, mask: 0x20, wait: 100 * time.Millisecond},
	},
	// PX-S3U2
	0x0003: {
		{value: 0x05, mask: 0x05, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x04, wait: 10 * time.Millisecond},
		{value: 0x05, mask: 0x05, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x20, wait: 10 * time.Millisecond},
		{value: 0x20, mask: 0x20, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x20, wait: 10 * time.Millisecond},
		{value: 0x10, mask: 0x10, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x10, wait: 10 * time.Millisecond},
		{value: 0x10, mask: 0x10, wait: 10 * time.Millisecond},
		{extended: true, value: 0, mask: 0x01, wait: 10 * time.Millisecond},
		{value: 0x40, mask: 0x40, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x40, wait: 10 * time.Millisecond},
		{value: 0x40, mask: 0x40, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x80, wait: 10 * time.Millisecond},
		{value: 0x80, mask: 0x80, wait: 10 * time.Millisecond},
		{value: 0, mask: 0x80, wait: 10 * time.Millisecond},
		{extended: true, value: 0, mask: 0x02, wait: 100 * time.Millisecond},
	},
	// PX-W3U2 and PX-W3U3
	0x0004: w3PowerUp,
	0x0005: w3PowerUp,
	// PX-W3U3 V2
	v2Product: v2PowerUp,
}

var w3PowerUp = []gpioStep{
	// Initialize standby and LNA lines while preserving sibling GPIO 0x40.
	{value: 0x27, mask: 0xbb},
	{value: 0x05, mask: 0x05, wait: 10 * time.Millisecond},
	{value: 0, mask: 0x04, wait: 10 * time.Millisecond},
	{value: 0x05, mask: 0x05, wait: 10 * time.Millisecond},
	{value: 0x10, mask: 0x10, wait: 10 * time.Millisecond},
	{value: 0, mask: 0x10, wait: 10 * time.Millisecond},
	{value: 0x10, mask: 0x10, wait: 10 * time.Millisecond},
	{value: 0x20, mask: 0x20, wait: 110 * time.Millisecond},
}

type boardControl interface {
	SetGPIO(value, mask byte) error
	SetGPIOEx(value, mask byte) error
	I2C(target byte) i2c.Bus
}

// powerUpBoard applies GPIO initialization before preparing the RF pair.
func powerUpBoard(control boardControl, product uint16) error {
	steps, known := powerUpSequences[product]
	if !known {
		return fmt.Errorf("no power-up sequence for product ID %#04x", product)
	}
	for _, step := range steps {
		write := control.SetGPIO
		if step.extended {
			write = control.SetGPIOEx
		}
		if err := write(step.value, step.mask); err != nil {
			return err
		}
		time.Sleep(step.wait)
	}
	if product == 0x0004 || product == 0x0005 {
		// Pulse control bit 0x10 while keeping bit 0x20 set.
		demod := tc90522.New(control.I2C(0x30), 0x30, false)
		if err := demod.WriteRegisterMask(0x1c, 0x30, 0x30); err != nil {
			return fmt.Errorf("set W3U2/W3U3 demodulator control bits: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := demod.WriteRegisterMask(0x1c, 0, 0x10); err != nil {
			return fmt.Errorf("finish W3U2/W3U3 demodulator control pulse: %w", err)
		}
	}
	return nil
}
