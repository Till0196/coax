// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package injected

import (
	"errors"
	"os"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// openFile makes a descriptor to stand in for a device node. Nothing reads it;
// what is tested is that it is owned, listed and closed at the right times.
func openFile(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "device")
	if err != nil {
		t.Fatalf("make a stand-in descriptor: %v", err)
	}
	return file
}

// closed reports whether a file has been closed: a second Close fails.
func closed(file *os.File) bool { return file.Close() != nil }

func TestAttachListsTheDevice(t *testing.T) {
	registry := New()
	device := Device{
		ID:       backend.ID{Vendor: 0x0511, Product: 0x0045},
		Location: "001:004",
		Serial:   "ABC12345",
		Port:     "1-2.1",
		File:     openFile(t),
	}
	location, err := registry.Attach(device)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if location != "001:004" {
		t.Errorf("Attach listed the device at %q, want the location it was given", location)
	}
	listed := registry.List()
	if len(listed) != 1 {
		t.Fatalf("List gave %d devices, want 1", len(listed))
	}
	want := Attached{ID: device.ID, Location: "001:004", Serial: "ABC12345", Port: "1-2.1"}
	if listed[0] != want {
		t.Errorf("List gave %+v, want %+v", listed[0], want)
	}
}

func TestAttachNamesADeviceThatCameWithoutOne(t *testing.T) {
	registry := New()
	first, err := registry.Attach(Device{File: openFile(t)})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	second, err := registry.Attach(Device{File: openFile(t)})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if first == second {
		t.Fatalf("two unnamed devices were both listed at %q", first)
	}
	if registry.Len() != 2 {
		t.Errorf("Len = %d, want 2", registry.Len())
	}
}

func TestAttachWithoutADescriptorFails(t *testing.T) {
	registry := New()
	if _, err := registry.Attach(Device{Location: "001:004"}); err == nil {
		t.Fatal("attaching without a descriptor should fail")
	}
}

func TestAttachAgainReplacesAndClosesTheOldDescriptor(t *testing.T) {
	registry := New()
	first, second := openFile(t), openFile(t)
	if _, err := registry.Attach(Device{Location: "001:004", File: first}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := registry.Attach(Device{Location: "001:004", File: second}); err != nil {
		t.Fatalf("Attach again: %v", err)
	}
	if registry.Len() != 1 {
		t.Errorf("Len = %d, want the replacement to have taken the same slot", registry.Len())
	}
	if !closed(first) {
		t.Error("the descriptor that was replaced should have been closed")
	}
	if closed(second) {
		t.Error("the descriptor that replaced it should still be open")
	}
}

func TestDetachClosesTheDescriptor(t *testing.T) {
	registry := New()
	file := openFile(t)
	if _, err := registry.Attach(Device{Location: "001:004", File: file}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if err := registry.Detach("001:004"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if registry.Len() != 0 {
		t.Errorf("Len = %d after Detach, want 0", registry.Len())
	}
	if !closed(file) {
		t.Error("Detach should have closed the descriptor")
	}
}

func TestDetachWhatIsNotThere(t *testing.T) {
	registry := New()
	err := registry.Detach("001:004")
	if !errors.Is(err, ErrNotAttached) {
		t.Fatalf("Detach of an absent device = %v, want ErrNotAttached", err)
	}
}

func TestFoundOpensThroughTheAdopter(t *testing.T) {
	registry := New()
	file := openFile(t)
	if _, err := registry.Attach(Device{
		ID:       backend.ID{Vendor: 0x0511, Product: 0x0045},
		Location: "001:004",
		File:     file,
	}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	var adopted *os.File
	found := registry.Found(func(handed *os.File) (backend.Device, error) {
		adopted = handed
		return nil, nil
	})
	if len(found) != 1 {
		t.Fatalf("Found gave %d candidates, want 1", len(found))
	}
	if found[0].ID != (backend.ID{Vendor: 0x0511, Product: 0x0045}) || found[0].Location != "001:004" {
		t.Errorf("Found gave %+v, want the attached device's ID and location", found[0])
	}
	if adopted != nil {
		t.Fatal("listing should not have opened anything")
	}
	if _, err := found[0].Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if adopted != file {
		t.Error("Open should have handed the attached descriptor to the adopter")
	}
}

func TestFoundGivesEachCandidateItsOwnDescriptor(t *testing.T) {
	registry := New()
	first, second := openFile(t), openFile(t)
	if _, err := registry.Attach(Device{Location: "001:004", File: first}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, err := registry.Attach(Device{Location: "001:005", File: second}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	opened := map[string]*os.File{}
	found := registry.Found(func(handed *os.File) (backend.Device, error) {
		opened[handed.Name()] = handed
		return nil, nil
	})
	if len(found) != 2 {
		t.Fatalf("Found gave %d candidates, want 2", len(found))
	}
	for _, candidate := range found {
		if _, err := candidate.Open(); err != nil {
			t.Fatalf("Open %s: %v", candidate.Location, err)
		}
	}
	// Every closure must carry the device it was made for; one shared capture
	// would open the last device twice.
	if len(opened) != 2 {
		t.Fatalf("opening both candidates reached %d descriptors, want 2", len(opened))
	}
	if opened[first.Name()] != first || opened[second.Name()] != second {
		t.Error("a candidate opened a descriptor belonging to another device")
	}
}

func TestCloseDetachesEverything(t *testing.T) {
	registry := New()
	first, second := openFile(t), openFile(t)
	for _, file := range []*os.File{first, second} {
		if _, err := registry.Attach(Device{File: file}); err != nil {
			t.Fatalf("Attach: %v", err)
		}
	}
	if err := registry.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if registry.Len() != 0 {
		t.Errorf("Len = %d after Close, want 0", registry.Len())
	}
	if !closed(first) || !closed(second) {
		t.Error("Close should have closed every descriptor")
	}
}

func TestAttachHasALimit(t *testing.T) {
	registry := New()
	defer registry.Close()
	for i := 0; i < maxAttached; i++ {
		if _, err := registry.Attach(Device{File: openFile(t)}); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
	}
	if _, err := registry.Attach(Device{File: openFile(t)}); err == nil {
		t.Error("attached past the limit")
	}
}
