// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package injected holds the USB devices this process was handed from outside.
//
// Android forbids reading /dev/bus/usb: the only way to a tuner there is the
// platform's USB service, which hands back an open descriptor for the one device
// the person granted. That descriptor is an ordinary usbfs node -- usbfs.Adopt
// takes it -- so only the listing differs, and this package is the listing. It
// is not tied to Android; a sandbox that opens devices for a daemon needs the
// same thing.
package injected

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// Device is one USB device somebody else opened and handed over.
type Device struct {
	// ID is the vendor and product pair a board driver matches against. The
	// handing-over side reports it, because reading the descriptor would mean
	// opening what is already open.
	ID backend.ID
	// Location is the name a selector matches, in the "bus:address" form the other
	// backends use. Attach fills in a placeholder when it is empty.
	Location string
	// Serial is the device's serial number, empty when the handing-over side did
	// not know it. A stable tuner name is built from it.
	Serial string
	// Port is the physical path from the root hub, "1-2.3", empty where unknown.
	Port string
	// File is the open usbfs descriptor. The registry owns it once attached and
	// closes it on detach; opening the device duplicates it, so the same device
	// may be opened again after being closed.
	File *os.File
}

// Adopter turns a handed-over descriptor into a device that can be driven.
// usbfs.Adopt is the one implementation; the indirection keeps this package
// free of any operating system.
type Adopter func(*os.File) (backend.Device, error)

// Registry is the set of devices this process has been handed, keyed by
// location. Handing over one whose location is present replaces it, which is
// what a re-plug looks like from here.
type Registry struct {
	mu      sync.Mutex
	devices map[string]Device
	order   []string
	nextID  int
}

// Default is the registry the USB enumeration reads.
var Default = New()

func New() *Registry {
	return &Registry{devices: make(map[string]Device)}
}

// maxAttached bounds how many devices may be attached at once: far more tuners
// than one machine has, and few enough that a handing-over side gone wrong
// cannot pile up descriptors.
const maxAttached = 64

// ErrNotAttached reports a location no device is attached at.
var ErrNotAttached = errors.New("no USB device is attached at that location")

// Attach takes ownership of a device's descriptor and lists it, reporting the
// location it was listed at (the given one, when there was one).
func (registry *Registry) Attach(device Device) (string, error) {
	if device.File == nil {
		return "", errors.New("attach a USB device without a descriptor")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if device.Location == "" {
		registry.nextID++
		device.Location = fmt.Sprintf("injected:%d", registry.nextID)
	}
	if previous, ok := registry.devices[device.Location]; ok {
		// This registry's to close: the handing-over side gave it up, and an open
		// device holding a duplicate is unaffected.
		_ = previous.File.Close()
	} else {
		if len(registry.devices) >= maxAttached {
			return "", fmt.Errorf("%d USB devices are already attached; detach one first", maxAttached)
		}
		registry.order = append(registry.order, device.Location)
	}
	registry.devices[device.Location] = device
	return device.Location, nil
}

// Detach closes a device's descriptor and drops it from the listing. A device
// already opened keeps working on its duplicate; what ends is its being found.
func (registry *Registry) Detach(location string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	device, ok := registry.devices[location]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotAttached, location)
	}
	delete(registry.devices, location)
	for i, name := range registry.order {
		if name == location {
			registry.order = append(registry.order[:i], registry.order[i+1:]...)
			break
		}
	}
	return device.File.Close()
}

// Attached is a device as it is reported back, without the descriptor.
type Attached struct {
	ID       backend.ID
	Location string
	Serial   string
	Port     string
}

// List reports what is attached, in the order it was handed over.
func (registry *Registry) List() []Attached {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	listed := make([]Attached, 0, len(registry.order))
	for _, location := range registry.order {
		device := registry.devices[location]
		listed = append(listed, Attached{
			ID:       device.ID,
			Location: device.Location,
			Serial:   device.Serial,
			Port:     device.Port,
		})
	}
	return listed
}

// Len reports how many devices are attached.
func (registry *Registry) Len() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return len(registry.devices)
}

// Found presents what is attached as enumeration results. The descriptor is not
// touched here: opening happens in the returned closure, which duplicates it, so
// a candidate nobody opens costs nothing -- the contract the other backends give.
func (registry *Registry) Found(adopt Adopter) []backend.Found {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	found := make([]backend.Found, 0, len(registry.order))
	for _, location := range registry.order {
		device := registry.devices[location]
		found = append(found, backend.Found{
			ID:       device.ID,
			Location: device.Location,
			Serial:   device.Serial,
			Port:     device.Port,
			Open:     func() (backend.Device, error) { return adopt(device.File) },
		})
	}
	return found
}

// Close detaches everything.
func (registry *Registry) Close() error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var failure error
	for _, location := range registry.order {
		if err := registry.devices[location].File.Close(); err != nil && failure == nil {
			failure = err
		}
	}
	registry.devices = make(map[string]Device)
	registry.order = nil
	return failure
}
