// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package dvb

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestAdaptersListsFrontendsInOrder builds a device tree of ordinary files.
// Listing them only reads directory names, which is the point: nothing is
// opened, because opening a frontend can power hardware up and a frontend
// already in use answers EBUSY.
func TestAdaptersListsFrontendsInOrder(t *testing.T) {
	root := t.TempDir()
	for _, node := range []string{
		"adapter1/frontend0", "adapter1/demux0", "adapter1/dvr0",
		"adapter0/frontend1", "adapter0/frontend0",
		"adapter0/net0", "notanadapter/frontend0", "adapter10/frontend0",
	} {
		path := filepath.Join(root, node)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("build the device tree: %v", err)
		}
	}

	found, err := Adapters(root)
	if err != nil {
		t.Fatalf("list the adapters: %v", err)
	}
	var names []string
	for _, one := range found {
		names = append(names, one.Name())
	}
	// Numeric order, not the lexical order the names would sort in: adapter10
	// comes after adapter1, and readdir order is not an order at all.
	want := []string{
		"adapter0.frontend0", "adapter0.frontend1",
		"adapter1.frontend0", "adapter10.frontend0",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("listed %v, want %v", names, want)
	}

	first := found[0]
	if first.Path != filepath.Join(root, "adapter0/frontend0") {
		t.Errorf("frontend path is %q", first.Path)
	}
	// The demux and DVR nodes are named after the frontend even though this
	// tree has none: they are what the stream is read through, and every
	// adapter in the tree numbers them to match.
	if first.Demux != filepath.Join(root, "adapter0/demux0") || first.DVR != filepath.Join(root, "adapter0/dvr0") {
		t.Errorf("stream nodes are %q and %q", first.Demux, first.DVR)
	}
}

// TestAdaptersWithNoDVBAtAll checks that an absent /dev/dvb is reported as no
// adapters rather than as an error: a machine with no DVB drivers loaded has
// none, which is a fact about the machine.
func TestAdaptersWithNoDVBAtAll(t *testing.T) {
	found, err := Adapters(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("a machine with no DVB adapters reported an error: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("found %d adapters where there is no device tree", len(found))
	}
}

// TestDeliveryNamesAreTheOnesChannelFilesUse checks that every name reported
// tunes back to the value it came from: these names go into a listing beside
// the names a channel file was written with.
func TestDeliveryNamesAreTheOnesChannelFilesUse(t *testing.T) {
	for name, value := range deliverySystemValues {
		reported := DeliveryName(value)
		if reported == "" {
			t.Errorf("delivery system %d (%s) has no name to report", value, name)
			continue
		}
		// Whatever name comes back has to be one that can be tuned with.
		if back, ok := deliverySystemValues[reported]; !ok || back != value {
			t.Errorf("delivery system %d is reported as %q, which does not tune back to it", value, reported)
		}
	}
}
