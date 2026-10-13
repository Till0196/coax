// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/Till0196/coax/internal/coaxd/chardev"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// px4Driver is px4_drv, which publishes USB tuners.
type px4Driver struct{}

func (px4Driver) Driver() string { return chardev.DriverPx4 }

// RelativeStreams is zero: px4_drv reads a slot of twelve or more as a stream
// identifier and looks the stream up by it, so nothing needs translating.
func (px4Driver) RelativeStreams() int { return 0 }

// SupplyForUnset drops the line, and says so rather than leaving it. See
// pt3Driver.SupplyForUnset for why. px4_drv agrees: PTX_ENABLE_LNB_POWER is
// never issued unless asked, and the supply drops again when the node closes.
func (px4Driver) SupplyForUnset() (int, bool) { return chardev.LNBOff, true }

// Systems asks a node which standards it will accept.
//
// PTX_SET_SYSTEM_MODE refuses a standard the tuner cannot receive, and that
// refusal is the only capability report this driver has: no ioctl describes a
// node, and the number says nothing either. The device is left in whichever
// standard answered last; every tune sets the standard again before selecting a
// channel.
func (px4Driver) Systems(device *chardev.Device, _ string) []chardev.System {
	var accepted []chardev.System
	for _, system := range []chardev.System{chardev.SystemISDBT, chardev.SystemISDBS} {
		if err := device.SetSystem(system); err == nil {
			accepted = append(accepted, system)
		}
	}
	return accepted
}

func (px4Driver) SelectSystem(device *chardev.Device, system chardev.System) error {
	return device.SetSystem(system)
}

func (px4Driver) Channel(system chardev.System, kilohertz int64, properties map[string]string) (int, int, error) {
	if system == chardev.SystemISDBS {
		number, err := chardev.SatelliteChannel(kilohertz)
		if err != nil {
			return 0, 0, err
		}
		// The slot names the stream inside the multiplex: below twelve a position in
		// the TMCC table, above it a stream identifier -- the same convention the USB
		// backends read STREAM_ID with. Absent means the first stream.
		streamID, err := tuning.StreamID(properties)
		if err != nil {
			return 0, 0, err
		}
		return number, int(streamID), nil
	}
	return chardev.TerrestrialChannel(kilohertz)
}

// TuneOutcome reads px4_drv's answer, which is an ordinary errno.
func (px4Driver) TuneOutcome(err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, chardev.ErrNoLock):
		return false, nil
	default:
		return false, err
	}
}

// pt3Driver is the Earthsoft PT3 driver, which publishes the tuners of a card
// on the PCI bus. It came first and px4_drv follows it: the ioctl numbers and
// channel numbering match, both being what recpt1 expects.
type pt3Driver struct{}

func (pt3Driver) Driver() string { return chardev.DriverPT3 }

// RelativeStreams is the eight-entry TMCC table this driver indexes. See
// pt3Slot for why an identifier cannot be passed instead.
func (pt3Driver) RelativeStreams() int { return pt3TMCCSlots }

// SupplyForUnset drops the line, and says so rather than leaving it.
//
// An unfed line that wanted feeding just produces nothing; a line already fed
// by a power inserter, a booster or the cable company and fed again from here
// has two supplies pushing at each other, which is the one mistake here that
// damages hardware. Off is also what the other programs default to.
//
// A dead line does not look like an error from down here, so it is caught by
// the sync check the stream endpoint runs. A line that must be fed says so with
// VOLTAGE=high.
func (pt3Driver) SupplyForUnset() (int, bool) { return chardev.LNBOff, true }

// Systems reports what a node receives, which is fixed and known from its name.
// This driver has no PTX_SET_SYSTEM_MODE and needs none: a PT3's tuners each
// have a node of their own, so unlike px4_drv's nodes these can be listed
// without being opened.
func (pt3Driver) Systems(_ *chardev.Device, path string) []chardev.System {
	return earthsoftSystems(chardev.DriverPT3, path)
}

// earthsoftSystems reads a node's standard out of its name, which is where
// these drivers keep it. The node is checked against the driver asking: the two
// lay out the same way, so a mismatch would give an answer that is right for
// the wrong card.
func earthsoftSystems(driver, path string) []chardev.System {
	if published, ok := chardev.DriverOfNode(path); !ok || published != driver {
		return nil
	}
	system, known := chardev.SystemOfNode(path)
	if !known {
		return nil
	}
	return []chardev.System{system}
}

// SelectSystem has nothing to do: the node is wired to one standard, and
// Supports refused anything else.
func (pt3Driver) SelectSystem(*chardev.Device, chardev.System) error { return nil }

func (pt3Driver) Channel(system chardev.System, kilohertz int64, properties map[string]string) (int, int, error) {
	if system == chardev.SystemISDBS {
		number, err := chardev.SatelliteChannel(kilohertz)
		if err != nil {
			return 0, 0, err
		}
		slot, err := pt3Slot(properties)
		if err != nil {
			return 0, 0, err
		}
		return number, slot, nil
	}
	// No offset. px4_drv adds the slot field to the frequency it looked up on
	// the terrestrial path; this driver reads the field for satellite only and
	// tunes straight from its table, so there is nowhere to carry one.
	number, err := chardev.PT3TerrestrialChannel(kilohertz)
	return number, 0, err
}

// pt3TMCCSlots is how many streams the driver's TMCC table holds.
const pt3TMCCSlots = 8

// pt3Slot reads which stream of a satellite multiplex to take. Unlike px4_drv,
// which reads a slot of twelve or more as a stream identifier, this driver only
// ever indexes the eight-entry TMCC table with the value and does not
// bounds-check it -- an index past the end reads off the end of a kernel
// structure. So a stream identifier is refused here.
func pt3Slot(properties map[string]string) (int, error) {
	streamID, err := tuning.StreamID(properties)
	if err != nil {
		return 0, err
	}
	if int(streamID) >= pt3TMCCSlots {
		return 0, fmt.Errorf("the PT3 driver takes STREAM_ID as a relative stream number of 0..%d, not as a stream identifier; %d is out of that range",
			pt3TMCCSlots-1, streamID)
	}
	return int(streamID), nil
}

// TuneOutcome reads the PT3 driver's answer, which is not an errno: SET_CHANNEL
// returns the negative of a status code from the driver's own enum. The codes
// are matched by value, and the ones meaning the signal was not there are
// reported as a failed lock rather than a fault; an unrecognised code is named.
func (pt3Driver) TuneOutcome(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false, err
	}
	switch uintptr(errno) {
	case chardev.PT3StatusPLLLockTimeout, chardev.PT3StatusPLLOutOfRange:
		// The tuner's own synthesiser never settled, which is what an empty
		// channel looks like from below the demodulator.
		return false, nil
	case chardev.PT3StatusGeneralError:
		// The terrestrial demodulator never reported a valid channel.
		return false, nil
	case chardev.PT3StatusInvalidParam:
		// The satellite demodulator never came back with the stream that was
		// asked for. The name is the driver's; it is what a satellite channel
		// that did not lock returns.
		return false, nil
	case chardev.PT3StatusI2CError:
		return false, fmt.Errorf("PT3 SET_CHANNEL: the card would not answer on its I2C bus")
	case chardev.PT3StatusTunerIsSleep:
		return false, fmt.Errorf("PT3 SET_CHANNEL: the tuner is asleep")
	}
	if uintptr(errno) >= 0x100 {
		return false, fmt.Errorf("PT3 SET_CHANNEL: driver status 0x%x", uintptr(errno))
	}
	return false, fmt.Errorf("PT3 SET_CHANNEL: %w", err)
}

// pt1Driver is the Earthsoft PT1 and PT2 driver, one driver for the two cards.
// It is the oldest of the three and the one the others follow: identical ioctl
// numbers and channel numbering. It differs from the PT3 driver in reaching one
// more terrestrial channel and the odd-numbered CS transponders, and in
// answering a tune with an ordinary errno.
type pt1Driver struct{}

func (pt1Driver) Driver() string { return chardev.DriverPT1 }

// RelativeStreams is the same eight-entry table, indexed the same way.
func (pt1Driver) RelativeStreams() int { return pt3TMCCSlots }

// SupplyForUnset drops the line, as on the PT3.
func (pt1Driver) SupplyForUnset() (int, bool) { return chardev.LNBOff, true }

func (pt1Driver) Systems(_ *chardev.Device, path string) []chardev.System {
	return earthsoftSystems(chardev.DriverPT1, path)
}

// SelectSystem has nothing to do: as on the PT3, the node is wired to one
// standard and there is no ioctl that would change it.
func (pt1Driver) SelectSystem(*chardev.Device, chardev.System) error { return nil }

func (pt1Driver) Channel(system chardev.System, kilohertz int64, properties map[string]string) (int, int, error) {
	if system == chardev.SystemISDBS {
		number, err := chardev.PT1SatelliteChannel(kilohertz)
		if err != nil {
			return 0, 0, err
		}
		// The same eight-entry table as the PT3 driver, indexed the same way
		// and checked no more carefully, so the same refusal applies.
		slot, err := pt1Slot(properties)
		if err != nil {
			return 0, 0, err
		}
		return number, slot, nil
	}
	// This driver reads the slot on the terrestrial path too, but adds it in
	// whole megahertz against channels 6 MHz apart, so it is left at zero.
	number, err := chardev.PT1TerrestrialChannel(kilohertz)
	return number, 0, err
}

// pt1Slot reads which stream of a satellite multiplex to take. As on the PT3,
// the driver indexes the eight-entry TMCC table with this without bounds
// checking, so a stream identifier is refused here rather than passed on.
func pt1Slot(properties map[string]string) (int, error) {
	streamID, err := tuning.StreamID(properties)
	if err != nil {
		return 0, err
	}
	if int(streamID) >= pt3TMCCSlots {
		return 0, fmt.Errorf("the PT1 driver takes STREAM_ID as a relative stream number of 0..%d, not as a stream identifier; %d is out of that range",
			pt3TMCCSlots-1, streamID)
	}
	return int(streamID), nil
}

// TuneOutcome reads the PT1 driver's answer, which unlike the PT3's is a plain
// errno: EIO when a satellite tune did not lock, EINVAL when a terrestrial one
// did not. Both mean the signal was not there, the driver refusing an
// out-of-range channel number earlier.
func (pt1Driver) TuneOutcome(err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EIO), errors.Is(err, syscall.EINVAL):
		return false, nil
	default:
		return false, err
	}
}
