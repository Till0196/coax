// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package native

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectListsAdaptersItCannotDescribe: asking a frontend what it is may fail
// -- it may be busy, and here it is not a device node at all -- and a tuner that
// vanished from the listing mid-recording is worse than one listed bare.
func TestDetectListsAdaptersItCannotDescribe(t *testing.T) {
	root := t.TempDir()
	for _, node := range []string{"adapter0/frontend0", "adapter0/demux0", "adapter0/dvr0"} {
		path := filepath.Join(root, node)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
	}
	previous := Root
	Root = root
	t.Cleanup(func() { Root = previous })

	found, err := Detect()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d adapters, want 1", len(found))
	}
	one := found[0]
	if one.Backend != Backend || one.Name != "adapter0.frontend0" {
		t.Errorf("named it %s:%s", one.Backend, one.Name)
	}
	if one.Selector != filepath.Join(root, "adapter0/frontend0") {
		t.Errorf("the selector is %q", one.Selector)
	}
	// One tuner is known without asking: an adapter is a frontend.
	if len(one.Tuners) != 1 || one.Tuners[0].Index != 0 {
		t.Errorf("the layout is %+v", one.Tuners)
	}
	// Nothing was learnt about what it receives, and nothing is claimed.
	if len(one.Deliveries) != 0 {
		t.Errorf("claimed to know it receives %v", one.Deliveries)
	}
}

func TestDetectWithNoAdapters(t *testing.T) {
	previous := Root
	Root = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { Root = previous })

	found, err := Detect()
	if err != nil {
		t.Fatalf("a machine with no DVB adapters reported an error: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("found %d adapters where there are none", len(found))
	}
}

// TestNamesTakeApartAgain: the name in a tuner ID is what the demux and DVR
// nodes are found from when the adapter is opened, so it must survive the round
// trip.
func TestNamesTakeApartAgain(t *testing.T) {
	adapter, frontend, ok := numbersIn("adapter2.frontend1")
	if !ok || adapter != 2 || frontend != 1 {
		t.Errorf("read adapter2.frontend1 as %d/%d (ok=%v)", adapter, frontend, ok)
	}
	if _, _, ok := numbersIn("px4:ABC123"); ok {
		t.Error("read a USB board name as a DVB adapter")
	}
}

// TestDetectListsCharacterDevices: px4_drv nodes come out of Detect under the
// character-device backend, named and described from the node, though nothing
// here can be opened to ask what it receives.
func TestDetectListsCharacterDevices(t *testing.T) {
	devices := t.TempDir()
	for _, node := range []string{"px4video0", "px4video1"} {
		if err := os.WriteFile(filepath.Join(devices, node), nil, 0o644); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
	}
	previousRoot, previousChardev := Root, ChardevRoot
	Root, ChardevRoot = t.TempDir(), devices
	t.Cleanup(func() { Root, ChardevRoot = previousRoot, previousChardev })

	found, err := Detect()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d tuners, want 2", len(found))
	}
	for index, name := range []string{"px4video0", "px4video1"} {
		one := found[index]
		if one.Backend != ChardevBackend {
			t.Errorf("%s: backend = %q, want %q", name, one.Backend, ChardevBackend)
		}
		if one.Name != name {
			t.Errorf("name = %q, want %q", one.Name, name)
		}
		if one.Selector != filepath.Join(devices, name) {
			t.Errorf("%s: selector = %q", name, one.Selector)
		}
		if one.Description == "" {
			t.Errorf("%s: the product line is known from the node name and should be reported", name)
		}
		if len(one.Tuners) != 1 {
			t.Errorf("%s: a character device is one tuner, got %d", name, len(one.Tuners))
		}
	}
}

// Both kinds are claimed and nothing else is, so the USB registry is still
// asked for the rest.
func TestOwnsClaimsBothKindsAndNothingElse(t *testing.T) {
	for _, backend := range []string{Backend, ChardevBackend} {
		if !Owns(backend) {
			t.Errorf("Owns(%q) = false", backend)
		}
	}
	// The drivers' own names are not this backend's, so they fall through to the
	// USB registry like anything else.
	for _, backend := range []string{"px4", "pxmlt", "t230", "pt1", "pt2", "pt3", ""} {
		if Owns(backend) {
			t.Errorf("Owns(%q) = true", backend)
		}
	}
}

// TestPT3NodesAreListedWithWhatTheyReceive: unlike a px4_drv node, a PT3 node
// says what it is in its name, so a listing describes it without opening
// anything, in use or not.
func TestPT3NodesAreListedWithWhatTheyReceive(t *testing.T) {
	devices := t.TempDir()
	for _, node := range []string{"pt3video0", "pt3video1", "pt3video2", "pt3video3"} {
		if err := os.WriteFile(filepath.Join(devices, node), nil, 0o644); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
	}
	previousRoot, previousChardev := Root, ChardevRoot
	Root, ChardevRoot = t.TempDir(), devices
	t.Cleanup(func() { Root, ChardevRoot = previousRoot, previousChardev })

	found, err := Detect()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) != 4 {
		t.Fatalf("found %d tuners, want 4", len(found))
	}
	want := []string{"ISDB-S", "ISDB-S", "ISDB-T", "ISDB-T"}
	for index, one := range found {
		if one.Backend != ChardevBackend {
			t.Errorf("%s: backend = %q, want %q", one.Name, one.Backend, ChardevBackend)
		}
		if len(one.Deliveries) != 1 || one.Deliveries[0] != want[index] {
			t.Errorf("%s: deliveries = %v, want [%s]", one.Name, one.Deliveries, want[index])
		}
		if len(one.Tuners) != 1 || len(one.Tuners[0].Deliveries) != 1 {
			t.Errorf("%s: the tuner should carry the same one delivery system", one.Name)
		}
	}
}
