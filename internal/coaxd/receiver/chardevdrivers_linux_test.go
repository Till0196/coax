// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package receiver

import (
	"errors"
	"syscall"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/chardev"
)

// A PT3 node says what it receives in its name: two satellite tuners and two
// terrestrial ones per card, and a second card carries on from four.
func TestPT3SystemsComeFromTheNodeName(t *testing.T) {
	for _, one := range []struct {
		path   string
		system chardev.System
	}{
		{"/dev/pt3video0", chardev.SystemISDBS},
		{"/dev/pt3video1", chardev.SystemISDBS},
		{"/dev/pt3video2", chardev.SystemISDBT},
		{"/dev/pt3video3", chardev.SystemISDBT},
		{"/dev/pt3video4", chardev.SystemISDBS},
		{"/dev/pt3video7", chardev.SystemISDBT},
	} {
		systems := pt3Driver{}.Systems(nil, one.path)
		if len(systems) != 1 || systems[0] != one.system {
			t.Errorf("%s receives %v, want %v", one.path, systems, one.system)
		}
	}
	// A node of the other driver is not this driver's to describe.
	if systems := (pt3Driver{}).Systems(nil, "/dev/px4video0"); systems != nil {
		t.Errorf("px4video0 is not a PT3 node, got %v", systems)
	}
}

// The PT3 driver uses the slot as an index into the eight-entry table it just
// read and does not check it, so a stream identifier is refused here rather
// than passed on to be read off the end of that table in the kernel.
func TestPT3RefusesAStreamIdentifierAsASlot(t *testing.T) {
	// A relative stream number is what it takes.
	number, slot, err := pt3Driver{}.Channel(chardev.SystemISDBS, 1_049_480,
		map[string]string{"STREAM_ID": "3"})
	if err != nil || number != 0 || slot != 3 {
		t.Fatalf("Channel = %d, %d, %v", number, slot, err)
	}
	// A TSID, which is what px4_drv would take and a channel file may well
	// carry, is not.
	if _, _, err := (pt3Driver{}).Channel(chardev.SystemISDBS, 1_049_480,
		map[string]string{"STREAM_ID": "16400"}); err == nil {
		t.Error("a stream identifier should be refused with an explanation")
	}
	// px4_drv takes the same value, because it looks the stream up by it.
	if _, slot, err := (px4Driver{}).Channel(chardev.SystemISDBS, 1_049_480,
		map[string]string{"STREAM_ID": "16400"}); err != nil || slot != 16400 {
		t.Errorf("px4_drv should pass a stream identifier through, got %d, %v", slot, err)
	}
}

// The PT3 driver has no slot to carry a frequency offset on the terrestrial
// path: it reads that field for satellite only.
func TestPT3TerrestrialTakesNoOffset(t *testing.T) {
	number, slot, err := pt3Driver{}.Channel(chardev.SystemISDBT, 473_143, nil)
	if err != nil || number != 63 || slot != 0 {
		t.Fatalf("Channel = %d, %d, %v", number, slot, err)
	}
	// px4_drv does carry one, which is how it tunes a frequency that is not
	// exactly on its table.
	if _, offset, err := (px4Driver{}).Channel(chardev.SystemISDBT, 473_243, nil); err != nil || offset != 100 {
		t.Errorf("px4_drv should carry the offset, got %d, %v", offset, err)
	}
}

// The PT3 driver answers a tune with the negative of its own status code, not
// with an error number. Read as an error number it is nonsense, so the codes
// that mean "no signal" have to be recognised as such.
func TestPT3TuneOutcomeReadsDriverStatusCodesNotErrnos(t *testing.T) {
	if locked, err := (pt3Driver{}).TuneOutcome(nil); !locked || err != nil {
		t.Errorf("a tune that answered zero locked, got %v, %v", locked, err)
	}
	for _, status := range []uintptr{
		chardev.PT3StatusPLLLockTimeout,
		chardev.PT3StatusPLLOutOfRange,
		chardev.PT3StatusGeneralError,
		chardev.PT3StatusInvalidParam,
	} {
		locked, err := pt3Driver{}.TuneOutcome(syscall.Errno(status))
		if locked || err != nil {
			t.Errorf("status %#x should be an unlocked tune, got %v, %v", status, locked, err)
		}
	}
	// A fault is still a fault, and is named rather than reported as an error
	// number nobody could look up.
	locked, err := pt3Driver{}.TuneOutcome(syscall.Errno(chardev.PT3StatusI2CError))
	if locked || err == nil {
		t.Fatalf("an I2C failure should be an error, got %v, %v", locked, err)
	}
	if errors.Is(err, syscall.Errno(chardev.PT3StatusI2CError)) {
		t.Error("the driver's status code should not be passed off as an error number")
	}
}

// The PT1 and PT2 nodes lay out exactly as the PT3's do.
func TestPT1SystemsComeFromTheNodeName(t *testing.T) {
	for _, one := range []struct {
		path   string
		system chardev.System
	}{
		{"/dev/pt1video0", chardev.SystemISDBS},
		{"/dev/pt1video1", chardev.SystemISDBS},
		{"/dev/pt1video2", chardev.SystemISDBT},
		{"/dev/pt1video3", chardev.SystemISDBT},
		{"/dev/pt1video5", chardev.SystemISDBS},
	} {
		systems := pt1Driver{}.Systems(nil, one.path)
		if len(systems) != 1 || systems[0] != one.system {
			t.Errorf("%s receives %v, want %v", one.path, systems, one.system)
		}
	}
	// One driver's node is not the other's to describe, even though the two
	// lay out the same way.
	if systems := (pt1Driver{}).Systems(nil, "/dev/pt3video0"); systems != nil {
		t.Errorf("pt3video0 is not a PT1 node, got %v", systems)
	}
}

// The PT1 driver answers a tune with an ordinary error number rather than with
// a status code of its own, and both of the numbers it uses mean the signal
// was not there.
func TestPT1TuneOutcomeReadsPlainErrorNumbers(t *testing.T) {
	if locked, err := (pt1Driver{}).TuneOutcome(nil); !locked || err != nil {
		t.Errorf("a tune that answered zero locked, got %v, %v", locked, err)
	}
	// EIO from a satellite tune, EINVAL from a terrestrial one.
	for _, errno := range []syscall.Errno{syscall.EIO, syscall.EINVAL} {
		locked, err := pt1Driver{}.TuneOutcome(errno)
		if locked || err != nil {
			t.Errorf("%v should be an unlocked tune, got %v, %v", errno, locked, err)
		}
	}
	// Anything else is a fault and is passed on.
	if _, err := (pt1Driver{}).TuneOutcome(syscall.EACCES); err == nil {
		t.Error("an unexpected error should be reported")
	}
}

// The PT1 driver indexes the same unchecked eight-entry table as the PT3's, so
// a stream identifier is refused rather than passed on.
func TestPT1RefusesAStreamIdentifierAsASlot(t *testing.T) {
	number, slot, err := pt1Driver{}.Channel(chardev.SystemISDBS, 1_049_480,
		map[string]string{"STREAM_ID": "5"})
	if err != nil || number != 0 || slot != 5 {
		t.Fatalf("Channel = %d, %d, %v", number, slot, err)
	}
	if _, _, err := (pt1Driver{}).Channel(chardev.SystemISDBS, 1_049_480,
		map[string]string{"STREAM_ID": "16400"}); err == nil {
		t.Error("a stream identifier should be refused with an explanation")
	}
}

// The PT1 driver would take a terrestrial offset, but only in whole megahertz
// against channels six megahertz apart, so none is carried.
func TestPT1TerrestrialTakesNoOffset(t *testing.T) {
	number, slot, err := pt1Driver{}.Channel(chardev.SystemISDBT, 473_143, nil)
	if err != nil || number != 63 || slot != 0 {
		t.Fatalf("Channel = %d, %d, %v", number, slot, err)
	}
}
