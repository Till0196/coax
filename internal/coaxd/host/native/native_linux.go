// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package native

import (
	"fmt"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/chardev"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/dvb"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/receiver"
)

// Backend is the name these devices are listed under.
const Backend = "dvb"

// ChardevBackend is the name for tuners published as character devices: not DVB
// adapters, since none of these drivers is a DVB driver, but nodes claimed
// exclusively by whoever opens them.
//
// One name covers the three drivers, which share the ioctl interface and most
// of a backend. Where they differ, the caller is told what the tuner will take
// instead. The product is in the listing's description and the driver is read
// off the node; see chardev.DriverOfNode.
const ChardevBackend = chardev.Backend

// Owns reports whether a tuner is served from here.
func Owns(one pool.Detected) bool {
	switch one.Backend {
	case Backend, ChardevBackend:
		return true
	default:
		return false
	}
}

// Recover has nothing to put back: no backend here takes hardware out from
// under another driver.
func Recover() int { return 0 }

// Recovering names the backends Recover touches, which is none of them.
func Recovering() []string { return nil }

// Root is where the DVB device nodes are looked for.
var Root = dvb.DefaultRoot

// ChardevRoot and ChardevClassRoot are where px4_drv's character devices and
// their descriptions in sysfs are looked for.
var (
	ChardevRoot      = chardev.DefaultRoot
	ChardevClassRoot = chardev.DefaultClassRoot
)

// SetChardevRoot points detection at a device tree other than /dev, for a
// container that mounts the nodes elsewhere. An empty path changes nothing.
func SetChardevRoot(path string) {
	if path != "" {
		ChardevRoot = path
	}
}

// SetRoot points detection at a device tree other than /dev/dvb, for a
// container that mounts it elsewhere. An empty path changes nothing.
func SetRoot(path string) {
	if path != "" {
		Root = path
	}
}

// Detect lists the DVB frontends and character devices the kernel is offering.
// A DVB frontend is opened briefly to ask what it is, its name and delivery
// systems not being in the device tree. One that is busy or will not answer is
// still listed from what it said last time: a recording tuner must not vanish
// from the listing.
func Detect() ([]pool.Detected, error) {
	found, err := detectDVB()
	nodes, nodeErr := detectChardev()
	if err == nil {
		err = nodeErr
	}
	return append(found, nodes...), err
}

func detectDVB() ([]pool.Detected, error) {
	adapters, err := dvb.Adapters(Root)
	if err != nil {
		return nil, err
	}
	found := make([]pool.Detected, 0, len(adapters))
	for _, adapter := range adapters {
		one := pool.Detected{
			Backend:  Backend,
			Selector: adapter.Path,
			Name:     adapter.Name(),
			Location: adapter.Path,
			Tuners:   []device.TunerInfo{{Index: 0}},
		}
		if info, ok := describe(adapter.Path); ok {
			one.Description = info.Name
			one.Deliveries = info.Deliveries
			// The frontend is the board, so naming the tuner too would print the
			// same name twice in one line.
			one.Tuners = []device.TunerInfo{{
				Index:      0,
				Deliveries: info.Deliveries,
				Controls:   controlsFor(Backend, info.Deliveries),
			}}
		}
		found = append(found, one)
	}
	return found, nil
}

// remembered holds what each frontend last said about itself.
var remembered sync.Map // path -> dvb.Info

// describe asks a frontend what it is and lets go at once. It opens it only to
// read, so a tuner being opened to tune at the same moment is not turned away.
// Should it not open, what it said last time serves: it describes hardware,
// which does not change while the device stays where it is.
func describe(path string) (dvb.Info, bool) {
	frontend, err := dvb.OpenFrontendReadOnly(path)
	if err != nil {
		known, ok := remembered.Load(path)
		if !ok {
			return dvb.Info{}, false
		}
		return known.(dvb.Info), true
	}
	defer frontend.Close()
	info, err := frontend.Info()
	if err != nil {
		return dvb.Info{}, false
	}
	remembered.Store(path, info)
	return info, true
}

// detectChardev lists the tuners the character device drivers are offering.
//
// A PT3 node says what it receives in its name and is listed untouched. A
// px4_drv node does not: the interface has no ioctl describing a node, and a
// PX-W3U4's four nodes are two satellite and two terrestrial tuners with
// nothing outside the driver saying which. Those are opened briefly and offered
// each standard in turn.
func detectChardev() ([]pool.Detected, error) {
	nodes, err := chardev.Nodes(ChardevRoot, ChardevClassRoot)
	if err != nil {
		return nil, err
	}
	found := make([]pool.Detected, 0, len(nodes))
	for _, node := range nodes {
		one := pool.Detected{
			Backend:  ChardevBackend,
			Selector: node.Path,
			// One open per node, so the pool must not hold one longer than it is
			// in use, nor open one to look.
			Exclusive:   true,
			Name:        node.Name,
			Description: node.Description,
			Location:    node.Path,
			Tuners:      []device.TunerInfo{{Index: 0}},
		}
		if systems, ok := describeChardev(node); ok {
			one.Deliveries = systems
			one.Tuners = []device.TunerInfo{{
				Index:      0,
				Deliveries: systems,
				Controls:   controlsFor(ChardevBackend, systems),
			}}
		}
		found = append(found, one)
	}
	return found, nil
}

// rememberedSystems holds what each character device last said about itself.
var rememberedSystems sync.Map // path -> []string

// describeChardev asks a node what it is, once. Unlike a DVB frontend it is
// asked only when nothing is remembered: px4_drv powers the board up on the
// first open and down on the last, so probing every node on every listing would
// power-cycle the board each time anybody asked what is attached.
func describeChardev(node chardev.Node) ([]string, bool) {
	// A node that says what it receives in its name has already answered.
	if name := node.System.Name(); name != "" {
		return []string{name}, true
	}
	if known, ok := rememberedSystems.Load(node.Path); ok {
		return known.([]string), true
	}
	systems, err := receiver.ChardevSystems(node.Path)
	if err != nil || len(systems) == 0 {
		return nil, false
	}
	rememberedSystems.Store(node.Path, systems)
	return systems, true
}

// Claimed reports the USB devices a kernel driver holds, keyed by the
// "bus:address" the USB backends use, so one board is not offered twice.
// px4_drv holds the USB interface of every board it bound to, so the direct USB
// backend can see the board but not open it.
//
// A node whose USB device could not be told is left out rather than guessed at.
func Claimed() map[string]bool {
	nodes, err := chardev.Nodes(ChardevRoot, ChardevClassRoot)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	claimed := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.USB != "" {
			claimed[node.USB] = true
		}
	}
	return claimed
}

// Open claims one of the devices Detect listed.
func Open(one pool.Detected, options device.Options) (device.Board, error) {
	if one.Backend != Backend {
		return openChardev(one)
	}
	return openDVB(one)
}

func openChardev(one pool.Detected) (device.Board, error) {
	opened, err := receiver.Open(receiver.Options{
		Backend: one.Backend,
		Device:  one.Selector,
	})
	if err != nil {
		return nil, err
	}
	// First-hand word of what the device accepts beats what a listing guessed.
	if reporter, ok := opened.(interface{ Systems() []string }); ok {
		if systems := reporter.Systems(); len(systems) != 0 {
			rememberedSystems.Store(one.Selector, systems)
			one.Deliveries = systems
		}
	}
	return wrap(one, opened), nil
}

func openDVB(one pool.Detected) (device.Board, error) {
	adapter, frontend, ok := numbersIn(one.Name)
	if !ok {
		return nil, fmt.Errorf("%q is not a DVB adapter name", one.Name)
	}
	opened, err := receiver.Open(receiver.Options{
		Backend:      Backend,
		Device:       one.Selector,
		LinuxDVBRoot: Root,
		Adapter:      adapter,
		Frontend:     frontend,
		// Demux and DVR nodes are numbered to match the frontend on every adapter.
		Demux: frontend,
		DVR:   frontend,
	})
	if err != nil {
		return nil, err
	}
	return wrap(one, opened), nil
}

// wrap presents an opened receiver as a board of one tuner.
func wrap(one pool.Detected, opened receiver.Device) device.Board {
	// A backend with a hardware setting beside the channel says so; the same is
	// asked on the other systems, see native_windows.go.
	mode := ""
	if reporter, ok := opened.(interface{ Mode() string }); ok {
		mode = reporter.Mode()
	}
	// The pair form keeps the receiver package from having to know device.
	settings := []device.Setting(nil)
	if reporter, ok := opened.(interface{ Settings() [][2]string }); ok {
		for _, pair := range reporter.Settings() {
			settings = append(settings, device.Setting{Name: pair[0], Value: pair[1]})
		}
	}
	return &board{
		name:   one.Name,
		tuner:  device.TunerInfo{Index: 0, Name: one.Description, Deliveries: one.Deliveries, Mode: mode, Settings: settings},
		device: &tuner{receiver: opened, deliveries: one.Deliveries},
	}
}

// numbersIn takes the adapter and frontend numbers back out of a name built by
// dvb.Adapter.Name.
func numbersIn(name string) (adapter, frontend int, ok bool) {
	if _, err := fmt.Sscanf(name, "adapter%d.frontend%d", &adapter, &frontend); err != nil {
		return 0, 0, false
	}
	return adapter, frontend, true
}
