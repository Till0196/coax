// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/internal/coaxd/pcie/dd"
	"github.com/Till0196/coax/internal/coaxd/pcie/tbs"
	"github.com/Till0196/coax/internal/coaxd/receiver"
	"github.com/Till0196/coax/internal/coaxd/usb/board/t230"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend is the name these devices are listed under.
const Backend = "bda"

// Owns reports whether a tuner is served from here. Four backends reach
// hardware Windows knows as a BDA tuner: the plain BDA path plus the TBS,
// Digital Devices and T230 paths, which reach the same hardware but drive the
// demodulator themselves. A T230 is also a WinUSB board under the same name;
// the BDA one has no USB IDs of its own in the listing.
func Owns(one pool.Detected) bool {
	switch one.Backend {
	case Backend, tbs.Backend, dd.Backend:
		return true
	case t230.Backend.Name:
		return one.USB == (usbio.ID{})
	}
	return ownsEarthsoft(one.Backend)
}

// Recover puts back any tuner a previous run was killed while holding and
// reports how many it repaired, asking the hardware rather than remembering
// across runs. The capture files such a run left behind go too.
//
// The repair opens graphs, so it runs in the host process like every other
// graph. A machine with no TBS card has nothing to repair and starts no host.
func Recover(ctx context.Context) (int, error) {
	bda.RemoveLeftovers()
	cards, err := tbs.Devices()
	if err != nil {
		return 0, err
	}
	if len(cards) == 0 {
		return 0, nil
	}
	client, err := recoveryHostClient(ctx)
	if err != nil {
		return 0, err
	}
	var repaired int
	call := client.Go("Host.Recover", struct{}{}, &repaired, nil)
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case result := <-call.Done:
			if result.Error != nil {
				return 0, fromHost(result.Error)
			}
			return repaired, nil
		}
	}
}

// Recovering names the backends Recover touches. Nothing else waits for it:
// a repair is one backend's business, and a card in a USB enclosure has no
// part in a PCIe tuner being handed back to its vendor driver.
func Recovering() []string { return []string{tbs.Backend} }

// SetRoot does nothing here: this system has no DVB device tree.
func SetRoot(string) {}

// SetChardevRoot does nothing here: px4_drv is a Linux driver.
func SetChardevRoot(string) {}

// Claimed reports the USB devices a system driver holds: none here. A device
// bound to a vendor's BDA driver would be listed twice, but the USB listing
// already leaves out every device not bound to WinUSB (see winusb.heldBy).
func Claimed() map[string]bool { return nil }

// Detect lists the BDA network tuners Windows is offering. Nothing is opened:
// opening means building a graph, which is expensive and exclusive. BDA has no
// way to ask what a tuner receives either, so the listing says nothing rather
// than guessing and the pool treats a silent tuner as one that may serve
// anything.
func Detect() ([]pool.Detected, error) {
	devices, err := bda.Devices()
	if err != nil {
		return nil, err
	}
	// A card the TBS backend can drive is listed under that backend: same
	// hardware, but it drives the demodulator itself and so can say which
	// delivery systems the tuner serves, which BDA cannot.
	driven := make(map[string][]string)
	backends := make(map[string]string)
	// What a TBS card's supply can do depends on the card (see tbs.Found.LNB).
	supplies := make(map[string]*device.LNBControl)
	if cards, err := tbs.Devices(); err == nil {
		for _, card := range cards {
			driven[card.Selector] = card.Deliveries
			backends[card.Selector] = tbs.Backend
			supplies[card.Selector] = card.LNB
		}
	}
	names := make(map[string]string)
	// A MyGica stick on its vendor's driver is tuned over that driver's I2C
	// transfer, which also reaches the standards the driver cannot tune.
	if sticks, err := t230.BDASticks(); err == nil {
		for _, stick := range sticks {
			driven[stick.Path] = stick.Deliveries
			backends[stick.Path] = t230.Backend.Name
			names[stick.Path] = stick.Name
		}
	}
	// A Digital Devices card publishes one BDA filter per standard per
	// demodulator, and that backend folds them back into the real tuners. The
	// folded-away filters are dropped, or one demodulator would be offered once
	// per standard it receives.
	folded := make(map[string]bool)
	if cards, err := dd.Devices(); err == nil {
		for _, card := range cards {
			driven[card.Selector] = card.Deliveries
			backends[card.Selector] = dd.Backend
			names[card.Selector] = card.Name
			for _, moniker := range card.Faces {
				if moniker != card.Selector {
					folded[moniker] = true
				}
			}
		}
	}

	found := make([]pool.Detected, 0, len(devices))
	for _, tuner := range devices {
		if folded[tuner.Selector] {
			continue
		}
		backend, deliveries := Backend, []string(nil)
		if known, ok := driven[tuner.Selector]; ok {
			backend, deliveries = backends[tuner.Selector], known
		}
		description := tuner.Name
		if name, ok := names[tuner.Selector]; ok && name != "" {
			description = name
		}
		found = append(found, pool.Detected{
			Backend:     backend,
			Selector:    tuner.Selector,
			Name:        shortName(tuner.Selector),
			Description: description,
			Location:    tuner.Selector,
			Deliveries:  deliveries,
			Tuners: []device.TunerInfo{{
				Index:      0,
				Name:       description,
				Deliveries: deliveries,
				Controls:   windowsControls(backend, deliveries, supplies[tuner.Selector]),
			}},
		})
	}
	// And the cards Windows has no name for: they publish no BDA filter, so the
	// loop above can never have seen them.
	found = append(found, detectEarthsoft()...)
	return found, nil
}

// earthsoftEntry is one receiver of an Earthsoft card as a listing wants it.
// The two backends report the same things without sharing a type, so their
// listings meet here.
func earthsoftEntry(backend, name, selector string, deliveries []string) pool.Detected {
	return pool.Detected{
		Backend:  backend,
		Selector: selector,
		// One open, shared between the card's four receivers, so a listing must
		// not open one to look nor hold one it is not using.
		Exclusive: true,
		// The card's place on the bus names it; the driver knows no serial.
		Name:        strings.NewReplacer(":", "-", ".", "-", "/", "-").Replace(selector),
		Description: name,
		Location:    selector,
		Deliveries:  deliveries,
		Tuners: []device.TunerInfo{{
			Index:      0,
			Name:       name,
			Deliveries: deliveries,
			Controls:   controlsFor(backend, deliveries),
		}},
	}
}

// windowsControls is controlsFor with the TBS cards, whose supply differs by card.
func windowsControls(backend string, deliveries []string, supply *device.LNBControl) device.Controls {
	if backend == tbs.Backend {
		return device.Controls{LNB: supply}
	}
	return controlsFor(backend, deliveries)
}

// shortName is what a BDA tuner is called in a tuner name. The device path
// Windows knows it by is long, full of punctuation and would need escaping in a
// URL; a hash keeps the uniqueness and stability while fitting a name a person
// can type.
func shortName(selector string) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(selector))
	return fmt.Sprintf("%016x", sum.Sum64())
}

// Open builds a graph on one tuner.
//
// A BDA tuner is opened in the host process, never here: see host_windows.go.
// The Earthsoft cards are not BDA and are opened here.
func Open(one pool.Detected, _ device.Options) (device.Board, error) {
	var opened openedDevice
	var err error
	if hosted(one) {
		opened, err = openHosted(one.Backend, one.Selector)
	} else {
		opened, err = openHere(one.Backend, one.Selector)
	}
	if err != nil {
		return nil, err
	}
	return &board{
		name:   one.Name,
		tuner:  device.TunerInfo{Index: 0, Name: one.Description, Deliveries: one.Deliveries, Mode: opened.mode, Settings: opened.settings},
		device: &tuner{receiver: opened.device, deliveries: one.Deliveries},
	}, nil
}

// openedDevice is a tuner just opened, with what it said about itself then.
type openedDevice struct {
	device   receiver.Device
	mode     string
	settings []device.Setting
}

// openHere opens a tuner in this process. All COM runs on a thread of the
// tuner's own, for its whole life; see comThread.
func openHere(backend, selector string) (openedDevice, error) {
	thread := startCOMThread()
	var claimed receiver.Device
	err := thread.do(func() error {
		opened, err := receiver.Open(receiver.Options{Backend: backend, Device: selector})
		if err != nil {
			return err
		}
		claimed = opened
		return nil
	})
	if err != nil {
		thread.stop()
		return openedDevice{}, err
	}
	// The delivery systems come from the listing; opening the tuner does not make
	// them better known. Leaving them out here once left `coax list --tuners`,
	// which opens the board, with an empty column where `coax list` named them.
	mode := ""
	if reporter, ok := claimed.(interface{ Mode() string }); ok {
		_ = thread.do(func() error {
			mode = reporter.Mode()
			return nil
		})
	}
	// The pair form keeps the receiver package from having to know device.
	settings := []device.Setting(nil)
	if reporter, ok := claimed.(interface{ Settings() [][2]string }); ok {
		_ = thread.do(func() error {
			for _, pair := range reporter.Settings() {
				settings = append(settings, device.Setting{Name: pair[0], Value: pair[1]})
			}
			return nil
		})
	}
	return openedDevice{device: &onThread{device: claimed, thread: thread}, mode: mode, settings: settings}, nil
}

// onThread is a receiver device whose calls all run on the board's COM thread.
type onThread struct {
	device receiver.Device
	thread *comThread

	// streams are the streams opened and not yet closed. Closing the device
	// closes them first, on the thread: once the thread has stopped, a stream's
	// own Close has nowhere to run and its file would be left behind.
	streamsMutex sync.Mutex
	streams      map[*onThreadStream]bool
}

// Name and Backend read fields settled at open, so they skip the thread.
func (o *onThread) Name() string    { return o.device.Name() }
func (o *onThread) Backend() string { return o.device.Backend() }

func (o *onThread) Supports(deliverySystem string) (bool, error) {
	var supported bool
	err := o.thread.do(func() error {
		answer, err := o.device.Supports(deliverySystem)
		supported = answer
		return err
	})
	return supported, err
}

func (o *onThread) Tune(properties map[string]string) error {
	return o.thread.do(func() error { return o.device.Tune(properties) })
}

func (o *onThread) Status() (receiver.Status, error) {
	var status receiver.Status
	err := o.thread.do(func() error {
		answer, err := o.device.Status()
		status = answer
		return err
	})
	return status, err
}

func (o *onThread) OpenStream() (receiver.Stream, error) {
	var stream receiver.Stream
	err := o.thread.do(func() error {
		opened, err := o.device.OpenStream()
		stream = opened
		return err
	})
	if err != nil {
		return nil, err
	}
	opened := &onThreadStream{stream: stream, thread: o.thread, owner: o}
	o.streamsMutex.Lock()
	if o.streams == nil {
		o.streams = make(map[*onThreadStream]bool)
	}
	o.streams[opened] = true
	o.streamsMutex.Unlock()
	return opened, nil
}

// Close shuts the device down and then ends the thread, in that order: the graph
// must be torn down by the thread that built it. Any stream still open is closed
// before the device.
func (o *onThread) Close() error {
	o.streamsMutex.Lock()
	open := make([]*onThreadStream, 0, len(o.streams))
	for stream := range o.streams {
		open = append(open, stream)
	}
	o.streamsMutex.Unlock()
	for _, stream := range open {
		_ = stream.Close()
	}
	err := o.thread.do(func() error { return o.device.Close() })
	o.thread.stop()
	return err
}

// onThreadStream sends the calls that touch the graph to the thread and leaves
// reads where they are: a BDA graph writes into a file this program reads back,
// so a read is ordinary file I/O and must not queue behind a tuning call.
type onThreadStream struct {
	stream receiver.Stream
	thread *comThread
	owner  *onThread

	once     sync.Once
	closeErr error
}

func (s *onThreadStream) Read(ctx context.Context, buffer []byte) (int, error) {
	return s.stream.Read(ctx, buffer)
}

func (s *onThreadStream) Close() error {
	s.once.Do(func() {
		s.closeErr = s.thread.do(func() error { return s.stream.Close() })
		s.owner.streamsMutex.Lock()
		delete(s.owner.streams, s)
		s.owner.streamsMutex.Unlock()
	})
	return s.closeErr
}
