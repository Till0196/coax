// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package tunerid builds the names a tuner is known by outside this program.
//
// The name goes into a front end's configuration and into URLs, so it has to
// survive a reboot, a re-enumeration and another device being plugged in beside
// it. It must not be built from the bus address, which the kernel hands out
// afresh every time.
package tunerid

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind says what a name was built from, so a listing can admit when a name will
// not survive the board being moved or replugged.
type Kind int

const (
	// FromDeviceName is the OS's own name for the device: a DVB adapter's
	// position in /dev/dvb, a BDA tuner's device path. Not a USB identity, and
	// as stable as the OS's naming of the device.
	FromDeviceName Kind = iota - 1
	// FromSerial is a serial the OS reported without the device being opened. It
	// survives anything short of the board being replaced.
	FromSerial
	// FromSerialAndPort is a serial qualified by the socket, for hardware that
	// presents one enclosure as several USB devices sharing a serial. It
	// survives a reboot but not a move.
	FromSerialAndPort
	// FromPort is the physical path from the root hub: survives a reboot and a
	// replug into the same socket, which is the best there is for a board with
	// no serial worth the name.
	FromPort
	// FromLocation is the bus and address, reassigned freely by the kernel, so
	// a name built from it lasts only until the next unplug.
	FromLocation
)

// Stable reports whether a name of this kind survives an unplug and replug into
// the same socket.
func (kind Kind) Stable() bool { return kind != FromLocation }

// String names the kind for a listing.
func (kind Kind) String() string {
	switch kind {
	case FromDeviceName:
		return "device"
	case FromSerial:
		return "serial"
	case FromSerialAndPort:
		return "serial+port"
	case FromPort:
		return "port"
	case FromLocation:
		return "location"
	}
	return "unknown"
}

// Device is what a board was recognised by before anything was opened.
type Device struct {
	// Backend is the driver that claims the board, and the first part of every
	// name built from it.
	Backend string
	// Name is the OS's own name, for hardware not reached over USB and having no
	// serial or port path. When set, nothing else is consulted: the system has
	// already answered the question this package exists to answer.
	Name string
	// Serial is the serial number, empty where the system could not say.
	Serial string
	// Port is the physical path from the root hub ("1-2.3"), empty where the
	// system could not say.
	Port string
	// Location is the "bus:address" the device was enumerated at.
	Location string
	// SharedSerial says this backend can present one enclosure as several USB
	// devices with the same serial, so a serial alone does not name one.
	SharedSerial bool
}

// Board builds a board's name: a tuner's name without the tuner number.
//
// Serial, then port, then bus address -- the order of how long each survives. A
// backend whose enclosures share a serial never uses the serial alone even when
// only one of the pair is attached, so a name written down keeps meaning the
// same device.
func Board(device Device) (name string, kind Kind) {
	switch {
	case device.Name != "":
		return device.Backend + ":" + clean(device.Name), FromDeviceName
	case device.Serial != "" && !device.SharedSerial:
		return device.Backend + ":" + clean(device.Serial), FromSerial
	case device.Serial != "" && device.Port != "":
		return device.Backend + ":" + clean(device.Serial) + "@" + clean(device.Port), FromSerialAndPort
	case device.Port != "":
		return device.Backend + ":p" + clean(device.Port), FromPort
	default:
		return device.Backend + ":u" + clean(device.Location), FromLocation
	}
}

// Tuner builds the full name of one tuner on a board.
func Tuner(device Device, index int) (name string, kind Kind) {
	board, kind := Board(device)
	return fmt.Sprintf("%s:%d", board, index), kind
}

// Split takes a tuner name apart into the board's name and the tuner number.
//
// A tuner name is exactly three parts, a board name exactly two, which is what
// tells them apart. Counting parts rather than looking for a trailing number
// matters: a board with an all-digit serial -- "t230:424242" -- ends in a
// number too, and reading that as tuner 424242 of "t230" finds nothing while
// looking well formed.
func Split(name string) (board string, index int, ok bool) {
	parts := strings.Split(name, ":")
	if len(parts) != 3 {
		return "", 0, false
	}
	number, err := strconv.Atoi(parts[2])
	if err != nil || number < 0 {
		return "", 0, false
	}
	return parts[0] + ":" + parts[1], number, true
}

// clean removes the characters a name is taken apart on, so a manufacturer's
// serial cannot change how a name parses: a colon separates parts and an at-sign
// joins a serial to a port, so neither may appear inside one. Whitespace goes
// too -- it survives a descriptor read but has no business in a URL.
func clean(value string) string {
	return strings.Map(func(letter rune) rune {
		switch letter {
		case ':', '@', '/', '?', '&', '#', ' ', '\t':
			return '-'
		}
		return letter
	}, strings.TrimSpace(value))
}
