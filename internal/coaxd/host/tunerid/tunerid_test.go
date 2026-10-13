// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tunerid

import "testing"

func TestBoardPrefersWhatSurvivesLongest(t *testing.T) {
	cases := []struct {
		name   string
		device Device
		want   string
		kind   Kind
	}{
		{
			name:   "a serial names the board on its own",
			device: Device{Backend: "t230", Serial: "ABC123", Port: "1-2", Location: "001:007"},
			want:   "t230:ABC123",
			kind:   FromSerial,
		},
		{
			// A Q3U4 enumerates as two USB devices with the same serial, so the
			// serial alone names neither.
			name:   "a shared serial is qualified by the socket",
			device: Device{Backend: "px4", Serial: "ABC123", Port: "1-2.1", Location: "001:007", SharedSerial: true},
			want:   "px4:ABC123@1-2.1",
			kind:   FromSerialAndPort,
		},
		{
			// Windows cannot read a serial without opening the device, so this is
			// the ordinary case there and on macOS.
			name:   "no serial falls back to the socket",
			device: Device{Backend: "px4", Port: "1-2.1", Location: "001:007", SharedSerial: true},
			want:   "px4:p1-2.1",
			kind:   FromPort,
		},
		{
			name:   "neither leaves only the bus address",
			device: Device{Backend: "fsusb2", Location: "001:007"},
			want:   "fsusb2:u001-007",
			kind:   FromLocation,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, kind := Board(one.device)
			if got != one.want {
				t.Errorf("named the board %q, want %q", got, one.want)
			}
			if kind != one.kind {
				t.Errorf("built the name from %v, want %v", kind, one.kind)
			}
		})
	}
}

// TestSharedSerialSurvivesTheTwinArriving is what the naming rule exists for: a
// name written down while one half of an enclosure was attached must keep
// meaning that half once the other appears.
func TestSharedSerialSurvivesTheTwinArriving(t *testing.T) {
	alone := Device{Backend: "px4", Serial: "SHARED", Port: "1-2.1", Location: "001:007", SharedSerial: true}
	together, _ := Board(alone)

	// The twin is plugged in: same serial, another socket, and the first board's
	// bus address has moved besides.
	moved := Device{Backend: "px4", Serial: "SHARED", Port: "1-2.1", Location: "001:012", SharedSerial: true}
	twin := Device{Backend: "px4", Serial: "SHARED", Port: "1-2.2", Location: "001:013", SharedSerial: true}

	if again, _ := Board(moved); again != together {
		t.Errorf("the board's name changed from %q to %q when its twin arrived", together, again)
	}
	if other, _ := Board(twin); other == together {
		t.Errorf("both halves of the enclosure are called %q", other)
	}
}

func TestStable(t *testing.T) {
	for _, kind := range []Kind{FromSerial, FromSerialAndPort, FromPort} {
		if !kind.Stable() {
			t.Errorf("%v should survive a replug", kind)
		}
	}
	if FromLocation.Stable() {
		t.Error("a name built from the bus address does not survive a replug")
	}
}

func TestTunerAndSplitAreInverses(t *testing.T) {
	device := Device{Backend: "px4", Serial: "ABC123", Port: "1-2", SharedSerial: true}
	name, _ := Tuner(device, 3)
	if name != "px4:ABC123@1-2:3" {
		t.Fatalf("named the tuner %q", name)
	}
	board, index, ok := Split(name)
	if !ok {
		t.Fatal("the name did not come apart again")
	}
	if want, _ := Board(device); board != want {
		t.Errorf("split out the board %q, want %q", board, want)
	}
	if index != 3 {
		t.Errorf("split out tuner %d, want 3", index)
	}
	if _, _, ok := Split("no-tuner-number"); ok {
		t.Error("a name with no tuner number came apart")
	}
}

// TestCleanKeepsNamesParseable: a manufacturer's serial cannot change how a
// name is read.
func TestCleanKeepsNamesParseable(t *testing.T) {
	device := Device{Backend: "px4", Serial: "A:B@C D", Port: "1-2", SharedSerial: true}
	name, _ := Tuner(device, 0)
	board, index, ok := Split(name)
	if !ok || index != 0 {
		t.Fatalf("the name %q did not come apart", name)
	}
	if want, _ := Board(device); board != want {
		t.Errorf("split out %q, want %q", board, want)
	}
	if name != "px4:A-B-C-D@1-2:0" {
		t.Errorf("cleaned the name to %q", name)
	}
}

// TestADeviceTheSystemNamesItself covers tuners that are not USB devices: a
// Linux DVB adapter and a Windows BDA tuner are named by the system, and that
// name beats anything this package could build.
func TestADeviceTheSystemNamesItself(t *testing.T) {
	name, kind := Board(Device{Backend: "dvb", Name: "adapter0.frontend0"})
	if name != "dvb:adapter0.frontend0" {
		t.Errorf("named a DVB adapter %q", name)
	}
	if kind != FromDeviceName || !kind.Stable() {
		t.Errorf("a name the system gave is %v, stable=%v", kind, kind.Stable())
	}
	if kind.String() != "device" {
		t.Errorf("a listing would call it %q", kind.String())
	}

	// A system name wins over anything else known: the question is answered.
	name, _ = Board(Device{Backend: "bda", Name: "a1b2c3d4", Serial: "ABC", Port: "1-2", Location: "001:005"})
	if name != "bda:a1b2c3d4" {
		t.Errorf("named a BDA tuner %q", name)
	}
}

// TestABoardWithAllDigitSerialIsNotATunerName is the case that got this wrong on
// real hardware: a MyGica T230C2 reports an all-digit serial such as 424242,
// so the board is named t230:424242, which ends in a number just as a tuner
// name does. Reading it as tuner 424242 of "t230" produced a well-formed
// request for absent hardware.
func TestABoardWithAllDigitSerialIsNotATunerName(t *testing.T) {
	if _, _, ok := Split("t230:424242"); ok {
		t.Error("a board name with an all-digit serial was read as a tuner name")
	}

	board, index, ok := Split("t230:424242:0")
	if !ok || board != "t230:424242" || index != 0 {
		t.Errorf("Split(t230:424242:0) = %q, %d, %v", board, index, ok)
	}
}

// TestSplitRoundTripsEveryKindOfName: a name this package builds must be one it
// can take apart again.
func TestSplitRoundTripsEveryKindOfName(t *testing.T) {
	devices := []Device{
		{Backend: "px4", Serial: "ABC12345"},
		{Backend: "px4", Serial: "ABC12345", Port: "1-2.1", SharedSerial: true},
		{Backend: "t230", Serial: "424242"},
		{Backend: "t230", Port: "1-1"},
		{Backend: "t230", Location: "001:005"},
		{Backend: "dvb", Name: "adapter0.frontend0"},
		{Backend: "bda", Name: "3f2a9c1d80b45e67"},
	}
	for _, device := range devices {
		wantBoard, _ := Board(device)
		for _, index := range []int{0, 3, 12} {
			name, _ := Tuner(device, index)
			board, got, ok := Split(name)
			if !ok || board != wantBoard || got != index {
				t.Errorf("Split(%q) = %q, %d, %v; want %q, %d", name, board, got, ok, wantBoard, index)
			}
		}
		if _, _, ok := Split(wantBoard); ok {
			t.Errorf("the board name %q was read as a tuner name", wantBoard)
		}
	}
}
