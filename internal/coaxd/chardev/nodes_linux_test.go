// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package chardev

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// tree builds the two halves of what detection reads: a directory of device
// nodes, and the sysfs class tree laid out as the kernel does for these
// drivers -- the class entry's "device" points at the USB interface px4_drv
// bound to, with bus and address on the USB device above it.
func tree(t *testing.T) (root, classRoot string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "dev")
	classRoot = filepath.Join(base, "sys", "class")
	usb := filepath.Join(base, "sys", "devices", "usb1", "1-2")
	interfaceDirectory := filepath.Join(usb, "1-2:1.0")

	if err := os.MkdirAll(interfaceDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(usb, "busnum"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(usb, "devnum"), []byte("7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"px4video0", "px4video1", "pxmlt5video0", "null", "px4videoX"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, one := range []struct{ class, node string }{
		{"px4", "px4video0"},
		{"px4", "px4video1"},
		{"pxmlt5", "pxmlt5video0"},
	} {
		directory := filepath.Join(classRoot, one.class, one.node)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(interfaceDirectory, filepath.Join(directory, "device")); err != nil {
			t.Fatal(err)
		}
	}
	return root, classRoot
}

func TestNodesListsTheDriversDevices(t *testing.T) {
	root, classRoot := tree(t)
	nodes, err := Nodes(root, classRoot)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	var names []string
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	// "null" is not one of the driver's, and "px4videoX" carries the prefix
	// without a number after it, which is not a node of the driver's either.
	want := []string{"px4video0", "px4video1", "pxmlt5video0"}
	if len(names) != len(want) {
		t.Fatalf("listed %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("listed %v, want %v", names, want)
		}
	}
	if nodes[0].Path != filepath.Join(root, "px4video0") {
		t.Errorf("path = %q", nodes[0].Path)
	}
	if nodes[0].Description == "" || nodes[0].Description == nodes[2].Description {
		t.Errorf("the two product lines should be described differently, got %q and %q",
			nodes[0].Description, nodes[2].Description)
	}
}

// The USB identity is spelled the way the USB backends spell it, because the
// whole point of reading it is to compare the two listings.
func TestNodesReportTheUSBDeviceBehindThem(t *testing.T) {
	root, classRoot := tree(t)
	nodes, err := Nodes(root, classRoot)
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	for _, node := range nodes {
		if node.USB != "001:007" {
			t.Errorf("%s: USB = %q, want %q", node.Name, node.USB, "001:007")
		}
	}
}

// A tree without sysfs still lists: an unmatched node costs a duplicate in the
// listing, not a tuner.
func TestNodesListWithoutSysfs(t *testing.T) {
	root, _ := tree(t)
	nodes, err := Nodes(root, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("listed %d nodes, want 3", len(nodes))
	}
	for _, node := range nodes {
		if node.USB != "" {
			t.Errorf("%s: USB = %q, want it left unsaid", node.Name, node.USB)
		}
	}
}

// A machine without the driver loaded has no such tuners, which is a fact
// rather than a failure.
func TestNodesTreatsAMissingRootAsNoDevices(t *testing.T) {
	nodes, err := Nodes(filepath.Join(t.TempDir(), "absent"), "")
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("listed %d nodes, want none", len(nodes))
	}
}

// A PT3 card's four nodes are two satellite tuners then two terrestrial ones,
// fixed by the driver, so a listing can say what each receives without opening
// anything.
func TestPT3NodesCarryTheirSystemInTheirName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dev")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		name := filepath.Join(root, "pt3video"+strconv.Itoa(index))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := Nodes(root, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 8 {
		t.Fatalf("listed %d nodes, want 8", len(nodes))
	}
	// Two cards' worth: the second card carries on from four.
	want := []System{
		SystemISDBS, SystemISDBS, SystemISDBT, SystemISDBT,
		SystemISDBS, SystemISDBS, SystemISDBT, SystemISDBT,
	}
	for index, node := range nodes {
		if node.Driver != DriverPT3 {
			t.Errorf("%s: driver = %q, want %q", node.Name, node.Driver, DriverPT3)
		}
		if node.System != want[index] {
			t.Errorf("%s: receives %v, want %v", node.Name, node.System, want[index])
		}
		// A card on the PCI bus is not something the USB half of a listing
		// could have offered, so there is nothing to match it against.
		if node.USB != "" {
			t.Errorf("%s: USB = %q, want it left unsaid", node.Name, node.USB)
		}
	}
}

// The three drivers' nodes are told apart by their names. They share one
// backend name, so the node name is the only place the driver can be read
// from, which is what DriverOfNode does.
func TestNodesTellTheDriversApart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dev")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"px4video0", "pt3video0", "pt1video0"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := Nodes(root, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("listed %d nodes, want 3", len(nodes))
	}
	// Sorted by name: pt1video0, pt3video0, px4video0.
	want := []string{DriverPT1, DriverPT3, DriverPx4}
	for index, node := range nodes {
		if node.Driver != want[index] {
			t.Errorf("%s: driver = %q, want %q", node.Name, node.Driver, want[index])
		}
		// The same answer read off the path alone, which is all the receiver
		// has by the time it has to pick a driver.
		if driver, ok := DriverOfNode(filepath.Join("/dev", node.Name)); !ok || driver != want[index] {
			t.Errorf("DriverOfNode(%s) = %q, %v; want %q", node.Name, driver, ok, want[index])
		}
	}
	// A px4_drv node's standard is not known from its name, and is left unsaid
	// rather than guessed at.
	if nodes[2].System != SystemUnspecified {
		t.Errorf("px4video0 should not claim a standard from its name, got %v", nodes[2].System)
	}
}

// Once a prefix reaches ten nodes -- two PX-MLT8PEs, or three PT3s -- sorting
// the names as text puts video10 between video1 and video2, and the listing
// reads as though the cards were interleaved. The number is what orders them.
func TestNodesAreOrderedByNumberRatherThanBySpelling(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dev")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Written out of order, because readdir order is not something to rely on
	// either way.
	for _, index := range []int{7, 15, 0, 11, 3, 9, 12, 1, 10, 2, 8, 4, 13, 5, 14, 6} {
		name := filepath.Join(root, fmt.Sprintf("pxmlt8video%d", index))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := Nodes(root, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 16 {
		t.Fatalf("listed %d nodes, want 16", len(nodes))
	}
	for index, node := range nodes {
		want := fmt.Sprintf("pxmlt8video%d", index)
		if node.Name != want {
			t.Errorf("node %d is %s, want %s", index, node.Name, want)
		}
	}
}

// Three PT3s: the third card's nodes carry on from eight, and each card is
// still two satellite tuners then two terrestrial ones.
func TestThreeEarthsoftCardsKeepTheirOrderAndTheirSystems(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dev")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for index := range 12 {
		name := filepath.Join(root, fmt.Sprintf("pt3video%d", index))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := Nodes(root, filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	if len(nodes) != 12 {
		t.Fatalf("listed %d nodes, want 12", len(nodes))
	}
	want := []System{
		SystemISDBS, SystemISDBS, SystemISDBT, SystemISDBT,
		SystemISDBS, SystemISDBS, SystemISDBT, SystemISDBT,
		SystemISDBS, SystemISDBS, SystemISDBT, SystemISDBT,
	}
	for index, node := range nodes {
		if node.Name != fmt.Sprintf("pt3video%d", index) {
			t.Errorf("node %d is %s, out of order", index, node.Name)
		}
		if node.System != want[index] {
			t.Errorf("%s: receives %v, want %v", node.Name, node.System, want[index])
		}
	}
}
