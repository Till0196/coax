// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/host/native"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

// TestBothKindsOfTunerAreListedTogether: DVB adapters survive the merge even
// when the USB half yields nothing -- a container without /dev/bus/usb has no
// USB tree to read and must not be reported as having no devices.
func TestBothKindsOfTunerAreListedTogether(t *testing.T) {
	root := t.TempDir()
	for _, node := range []string{"adapter0/frontend0", "adapter1/frontend0"} {
		path := filepath.Join(root, node)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
	}
	previous := native.Root
	native.Root = root
	t.Cleanup(func() { native.Root = previous })

	found, err := registry{}.Detect()
	if err != nil {
		// No USB boards is fine; losing the DVB adapters is not.
		t.Fatalf("detect: %v", err)
	}
	names := map[string]bool{}
	for _, one := range found {
		names[one.Backend+":"+one.Name] = true
	}
	for _, want := range []string{"dvb:adapter0.frontend0", "dvb:adapter1.frontend0"} {
		if !names[want] {
			t.Errorf("%s is missing from the listing: %v", want, names)
		}
	}
}

// A board px4_drv has bound to is listed only as the driver's character device,
// not also as a USB device this program could drive: the kernel driver holds the
// interface.
func TestClaimedUSBBoardsAreLeftOutOfTheListing(t *testing.T) {
	attached := []pool.Detected{
		{Backend: "px4", Selector: "001:007"},
		{Backend: "t230", Selector: "001:008"},
	}
	kept := withoutClaimedBy(attached, map[string]bool{"001:007": true})
	if len(kept) != 1 || kept[0].Selector != "001:008" {
		t.Fatalf("kept %v, want only the board no driver has", kept)
	}
}

// Nothing claimed means nothing dropped: a machine without px4_drv installed
// has every board to itself.
func TestNothingIsDroppedWhenNoDriverHasAnything(t *testing.T) {
	attached := []pool.Detected{{Backend: "px4", Selector: "001:007"}}
	if kept := withoutClaimedBy(attached, nil); len(kept) != 1 {
		t.Fatalf("kept %d boards, want 1", len(kept))
	}
}
