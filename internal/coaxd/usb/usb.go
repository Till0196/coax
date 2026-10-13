// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package usb is the registry of USB tuner boards.
//
// The subpackages are layered so that a new tuner reuses whatever it shares with an
// existing one:
//
//	usbio        raw USB transfers: control, bulk and isochronous endpoints
//	i2c          the bus contract between bridges and the ICs behind them
//	tuning       delivery-system and channel-property parsing
//	usbdev       the Board/Device/Stream/Backend contract a board fulfils
//	boardkit     fulfils that contract for a board, given only its tuning hardware
//	tsdemux      splits one endpoint carrying several tuners' streams at once
//	bridge/*     USB bridge chips (dvbsky, em287x, ...)
//	demod/*      demodulator ICs (si2168, tc90522, ...)
//	tuner/*      tuner ICs (si2157, mxl136, tda20142, ...)
//	smartcard/*  on-board smart card reader ICs (asie5606) and T=1
//	board/*      one package per product, wiring a bridge, demod and tuners together
//
// Supporting a new board means adding a package under board/ and listing its Backend
// below; only genuinely new silicon needs a new bridge, demod or tuner package. A board
// package implements only boardkit.Tuner.
//
// The model is two-level: a Board is the USB device and hands out one Device per tuner,
// and every tuner can be tuned and streamed at the same time. OpenAll takes them all at
// once; Open takes just one.
package usb

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/board/cechzd1j"
	"github.com/Till0196/coax/internal/coaxd/usb/board/fsusb2"
	"github.com/Till0196/coax/internal/coaxd/usb/board/fsusb2i"
	"github.com/Till0196/coax/internal/coaxd/usb/board/isdb2056"
	"github.com/Till0196/coax/internal/coaxd/usb/board/px3"
	"github.com/Till0196/coax/internal/coaxd/usb/board/px4"
	"github.com/Till0196/coax/internal/coaxd/usb/board/pxmlt"
	"github.com/Till0196/coax/internal/coaxd/usb/board/s1ur"
	"github.com/Till0196/coax/internal/coaxd/usb/board/s270"
	"github.com/Till0196/coax/internal/coaxd/usb/board/t230"
	"github.com/Till0196/coax/internal/coaxd/usb/board/tbs5520"
	"github.com/Till0196/coax/internal/coaxd/usb/board/tbs5520se"
	"github.com/Till0196/coax/internal/coaxd/usb/board/tbs5530"
	"github.com/Till0196/coax/internal/coaxd/usb/board/us3pout"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// backends lists every supported board. Add one line per new board package.
var backends = []usbdev.Backend{
	fsusb2.Backend,
	fsusb2i.Backend,
	isdb2056.Backend,
	px4.Backend,
	pxmlt.Backend,
	px3.Backend,
	s1ur.Backend,
	s270.Backend,
	t230.Backend,
	tbs5520.Backend,
	tbs5520se.Backend,
	tbs5530.Backend,
	cechzd1j.Backend,
	us3pout.Backend,
}

// Contracts re-exported so callers need only import this package.
type (
	Backend = usbdev.Backend
	Board   = device.Board
	Device  = device.Tuner
	Options = device.Options
	Status  = device.Status
	Stream  = device.Stream
	Tuner   = device.TunerInfo
)

// ErrWouldBlock reports that a stream read found no data before its timeout.
var ErrWouldBlock = device.ErrWouldBlock

// ErrTunerBusy reports that the requested tuner of a board is already open.
var ErrTunerBusy = device.ErrTunerBusy

// Detected is an attached USB device together with the board driver that
// claims it.
type Detected struct {
	// Backend is the -backend value that selects this driver.
	Backend string
	// Description names the hardware.
	Description string
	// Device is the USB bus:address, which is what --device takes when more
	// than one alike board is attached.
	Device string
	// ID is the vendor and product pair the device enumerated with.
	ID usbio.ID
	// Serial is the device's serial number, when the operating system could
	// report it without the device being opened. Empty where the device
	// carries none worth naming it by.
	Serial string
	// Port is the physical path from the root hub, "1-2.3". Unlike Device it
	// survives a re-enumeration, so a caller naming a board across a replug
	// uses this rather than the bus and address.
	Port string
	// Deliveries lists every delivery system some tuner of the board reaches.
	Deliveries []string
}

// Detect lists the attached USB devices a board driver recognises, so a caller
// can find out what --backend and --device to scan with. Nothing is opened: the
// answer comes from matching USB identifiers against what each driver claims.
func Detect() ([]Detected, error) {
	attached, err := usbio.Devices()
	if err != nil {
		return nil, err
	}
	var detected []Detected
	for _, candidate := range attached {
		for _, backend := range backends {
			if !candidate.ID.In(backend.IDs) {
				continue
			}
			detected = append(detected, Detected{
				Backend:     backend.Name,
				Description: backend.Description,
				Device:      candidate.Location,
				ID:          candidate.ID,
				Serial:      candidate.Serial,
				Port:        candidate.Port,
				Deliveries:  append([]string(nil), backend.Deliveries...),
			})
		}
	}
	return detected, nil
}

// coldBootTimeout bounds the wait for booted boards to come back under their
// own identifiers. They take well under a second; the rest is slack for a hub.
const (
	coldBootTimeout = 5 * time.Second
	coldBootPoll    = 100 * time.Millisecond
)

// coldBoot keeps two callers from uploading to the same board at once.
var coldBoot sync.Mutex

// BootCold starts every attached board that is still waiting for its firmware,
// and waits for it to come back as the board Detect lists.
//
// Detect cannot do this itself, since it opens nothing, and a board under its
// cold identifiers is not one it can name a backend for; left alone, such a board
// is never listed and so never opened. Finding one opens nothing either, so a
// machine without one pays for a listing and no more.
func BootCold(options Options) error {
	coldBoot.Lock()
	defer coldBoot.Unlock()
	attached, err := usbio.Devices()
	if err != nil {
		return err
	}
	var failed []error
	for _, backend := range backends {
		if backend.Boot == nil {
			continue
		}
		cold, warm := count(attached, backend.ColdIDs), count(attached, backend.IDs)
		if cold == 0 {
			continue
		}
		if err := backend.Boot(options); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", backend.Name, err))
			continue
		}
		if err := awaitBooted(backend, warm+cold); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", backend.Name, err))
		}
	}
	return errors.Join(failed...)
}

// awaitBooted waits for the backend's cold boards to be gone and for want of its
// boards to be attached under their own identifiers.
func awaitBooted(backend Backend, want int) error {
	deadline := time.Now().Add(coldBootTimeout)
	for {
		attached, err := usbio.Devices()
		if err == nil && count(attached, backend.ColdIDs) == 0 && count(attached, backend.IDs) >= want {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("the firmware was uploaded but the board did not come back")
		}
		time.Sleep(coldBootPoll)
	}
}

func count(attached []usbio.Attached, ids []usbio.ID) int {
	matched := 0
	for _, candidate := range attached {
		if candidate.ID.In(ids) {
			matched++
		}
	}
	return matched
}

// Layout describes the tuners a board of the named backend carries, worked out
// from the identifiers it enumerated with. It answers with nothing when the
// backend cannot tell from the identifiers alone; getting the list anyway means
// opening the board.
func Layout(name string, id usbio.ID) []device.TunerInfo {
	backend := Lookup(name)
	if backend == nil || backend.Layout == nil {
		return nil
	}
	return backend.Layout(id)
}

// Backends returns every supported board.
func Backends() []Backend { return append([]Backend(nil), backends...) }

// Names returns the canonical backend names, sorted, for help output.
func Names() []string {
	names := make([]string, 0, len(backends))
	for _, backend := range backends {
		names = append(names, backend.Name)
	}
	sort.Strings(names)
	return names
}

// Lookup finds the backend a -backend value selects, or nil when none matches.
// Both "t230" and "usb-t230" spellings are accepted.
func Lookup(name string) *Backend {
	name = strings.ToLower(strings.TrimSpace(name))
	for index := range backends {
		if backends[index].Matches(name) {
			return &backends[index]
		}
	}
	return nil
}

// OpenBoard claims the named USB device, giving access to every tuner on it.
// A bring-up that failed is tried once more with the device reset in between;
// see recoverAndOpen.
func OpenBoard(name string, options Options) (Board, error) {
	backend := Lookup(name)
	if backend == nil {
		return nil, fmt.Errorf("USB backend %q is not supported; try one of %s", name, strings.Join(Names(), ", "))
	}
	board, err := backend.Open(options)
	if err == nil {
		return board, nil
	}
	return recoverAndOpen(backend, options, err)
}

// recoverAndOpen resets a board that would not open, and opens it once more.
//
// Closing a handle undoes none of the bring-up, so the next open walks into
// whatever the last one left behind -- on the boards with the most silicon that
// is sometimes a chip that no longer acknowledges its own I2C address. Opening
// a US-3POUT in a loop hits it every dozen or so opens, and a bus reset clears
// it at the cost of a few hundred milliseconds.
//
// It is tried for any failure rather than a chosen few, because the ways a
// board says it has stopped answering are not a closed set. It cannot unstick a
// device that has stopped answering the bus altogether: that needs the cable.
// See docs/setup/os.md.
func recoverAndOpen(backend *Backend, options Options, first error) (Board, error) {
	if err := usbio.Reset(options.Selector, backend.IDs); err != nil {
		return nil, first
	}
	board, err := backend.Open(options)
	if err != nil {
		return nil, err
	}
	return board, nil
}

// Open claims a single tuner, the one named by Options.Tuner. The board is
// released as soon as the tuner is taken, so closing the returned Device closes
// the USB device too.
func Open(name string, options Options) (Device, error) {
	board, err := OpenBoard(name, options)
	if err != nil {
		return nil, err
	}
	opened, err := board.OpenTuner(options.Tuner)
	if err != nil {
		_ = board.Close()
		return nil, err
	}
	// The board handle is no longer needed: the tuner holds the device open.
	_ = board.Close()
	return opened, nil
}

// OpenAll claims every tuner of a board that can reach the given delivery
// system, for scanning a channel list across tuners in parallel. An empty
// delivery system takes them all. The returned Devices are independent; closing
// all of them closes the USB device.
func OpenAll(name string, options Options, deliverySystem string) ([]Device, error) {
	board, err := OpenBoard(name, options)
	if err != nil {
		return nil, err
	}
	defer board.Close()
	var devices []Device
	for _, tuner := range board.Tuners() {
		if deliverySystem != "" && !tuner.Supports(deliverySystem, tuning.Normalize) {
			continue
		}
		opened, err := board.OpenTuner(tuner.Index)
		if errors.Is(err, device.ErrTunerBusy) {
			continue
		}
		if err != nil {
			for _, open := range devices {
				_ = open.Close()
			}
			return nil, fmt.Errorf("open tuner %d: %w", tuner.Index, err)
		}
		devices = append(devices, opened)
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("board %q has no tuner for %q", name, deliverySystem)
	}
	return devices, nil
}
