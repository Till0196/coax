// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package chardev

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultRoot is where the driver's device nodes are.
const DefaultRoot = "/dev"

// DefaultClassRoot is where the kernel describes them. It is read only to find
// which USB device each node belongs to, and a tree without it still lists.
const DefaultClassRoot = "/sys/class"

// Backend is the one name every node found here is listed under: one way in, a
// character device claimed exclusively by whoever opens it and driven through
// the "ptx" ioctls. Which driver published it is said in the description.
const Backend = "chardev"

// The drivers that publish these nodes. They share the ioctl numbers but differ
// in what a channel number means at the edges, whether a node can be asked what
// it receives, what a failed tune says, and whether a satellite stream is named
// by its identifier or its place. See DriverOfNode.
const (
	DriverPx4 = "px4"
	DriverPT3 = "pt3"
	DriverPT1 = "pt1"
)

// A family is one of the device classes a driver registers. Its node prefix
// says which driver and product line a node belongs to without opening it.
type family struct {
	driver      string
	class       string
	prefix      string
	description string
	// systemOf reports what a node of this family receives, worked out from
	// the number in its name, or false when the node has to be asked.
	systemOf func(index int) (System, bool)
}

// families are the drivers' own, from px4_usb_register in px4_usb.c and from
// pt3_pci.c. The descriptions are this program's and name the product.
var families = []family{
	{DriverPx4, "px4", "px4video", "PLEX PX-W3U4/Q3U4/W3PE4/Q3PE4/W3PE5/Q3PE5 (px4_drv)", nil},
	{DriverPx4, "pxmlt5", "pxmlt5video", "PLEX PX-MLT5U/MLT5PE, e-Better DTV02A-5TS-P (px4_drv)", nil},
	{DriverPx4, "pxmlt8", "pxmlt8video", "PLEX PX-MLT8PE (px4_drv)", nil},
	{DriverPx4, "isdb2056", "isdb2056video", "DigiBest ISDB2056/ISDB2056N (px4_drv)", nil},
	{DriverPx4, "isdb6014", "isdb6014video", "DigiBest ISDB6014 (px4_drv)", nil},
	{DriverPx4, "pxm1ur", "pxm1urvideo", "PLEX PX-M1UR (px4_drv)", nil},
	{DriverPx4, "pxs1ur", "pxs1urvideo", "PLEX PX-S1UR (px4_drv)", nil},
	{DriverPx4, "isdbt2071", "isdbt2071video", "DigiBest ISDBT2071 (px4_drv)", nil},
	// The PT3 driver puts all four of a card's nodes in one class, and the
	// class is called "ptx" while the nodes are called "pt3video".
	{DriverPT3, "ptx", "pt3video", "Earthsoft PT3 (pt3_drv)", earthsoftSystemOf},
	// One driver serves the PT1 and the PT2; the card only changes tuner
	// initialisation, nothing a caller can see. Nodes lay out as the PT3's do.
	{DriverPT1, "pt1video", "pt1video", "Earthsoft PT1 / PT2 (pt1_drv)", earthsoftSystemOf},
}

// earthsoftSystemOf says what a PT1, PT2 or PT3 node receives. These cards carry
// two satellite tuners then two terrestrial ones, fixed in the driver with no
// ioctl to change it, and a second card continues the numbering.
func earthsoftSystemOf(index int) (System, bool) {
	if index%earthsoftNodesPerCard < earthsoftSatelliteNodes {
		return SystemISDBS, true
	}
	return SystemISDBT, true
}

const (
	earthsoftNodesPerCard   = 4
	earthsoftSatelliteNodes = 2
)

// Node is one character device a driver is offering, found without opening it.
type Node struct {
	// Driver is what published it: DriverPx4, DriverPT3 or DriverPT1. Every
	// node is listed under the one Backend whichever it is.
	Driver string
	// Name is the node's own name, "px4video0", as every other tool on the
	// system refers to it.
	Name string
	// Path is the device node.
	Path string
	// Description is the product line the node's prefix identifies.
	Description string
	// USB is the "bus:address" of the USB device the node belongs to, in the
	// form the USB backends use, or empty when unknown or on the PCI bus. It
	// lets the same board be recognised on both sides of the daemon.
	USB string
	// System is what the node receives when that is known from its name, and
	// SystemUnspecified when the node has to be asked.
	System System
	// index is the number in the node's name, kept so that a listing can be
	// put in the order the driver numbered them rather than in the order the
	// names happen to sort in.
	index int
}

// Nodes lists the character devices these drivers have published. A root with
// none of them in it is not an error.
func Nodes(root, classRoot string) ([]Node, error) {
	if root == "" {
		root = DefaultRoot
	}
	if classRoot == "" {
		classRoot = DefaultClassRoot
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	var found []Node
	for _, entry := range entries {
		one, ok := describeNode(entry.Name())
		if !ok {
			continue
		}
		one.Path = filepath.Join(root, entry.Name())
		// A card on the PCI bus has no USB device to be listed twice as, so
		// the walk is skipped rather than left to find nothing.
		if one.Driver == DriverPx4 {
			one.USB = usbOf(classRoot, one.Name)
		}
		found = append(found, one)
	}
	// Readdir order is whatever the filesystem gives; a listing that reorders
	// itself between runs cannot be compared.
	//
	// Within a prefix the number decides, not the spelling. Sorting the names
	// as text puts video10 between video1 and video2, which starts happening
	// as soon as one prefix reaches ten nodes -- two PX-MLT8PEs, or three
	// PT3s -- and makes a listing read as though the cards were interleaved.
	sort.Slice(found, func(i, j int) bool {
		left, right := found[i], found[j]
		if prefixOf(left.Name) != prefixOf(right.Name) {
			return prefixOf(left.Name) < prefixOf(right.Name)
		}
		return left.index < right.index
	})
	return found, nil
}

// prefixOf is the node name without its number, which is the family it belongs
// to. A name that matched no family never reaches here.
func prefixOf(name string) string {
	return strings.TrimRight(name, "0123456789")
}

// describeNode matches a directory entry against the drivers' node prefixes. The
// number after the prefix only has to be a number: it is the position the node
// was given as devices were probed.
func describeNode(name string) (Node, bool) {
	for _, one := range families {
		digits, ok := strings.CutPrefix(name, one.prefix)
		if !ok || digits == "" {
			continue
		}
		index, err := strconv.Atoi(digits)
		if err != nil {
			continue
		}
		node := Node{Driver: one.driver, Name: name, Description: one.description, index: index}
		if one.systemOf != nil {
			if system, known := one.systemOf(index); known {
				node.System = system
			}
		}
		return node, true
	}
	return Node{}, false
}

func classOf(name string) (string, bool) {
	for _, one := range families {
		if strings.HasPrefix(name, one.prefix) {
			return one.class, true
		}
	}
	return "", false
}

// usbOf finds the USB device a node belongs to, as "bus:address". This keeps one
// board from being offered twice: px4_drv holds the USB interface, so the direct
// USB backends cannot claim it but would still list it. Failure costs a
// duplicate listing, not a tuner.
func usbOf(classRoot, name string) string {
	class, ok := classOf(name)
	if !ok {
		return ""
	}
	// The node's device is the USB interface px4_drv bound to; the bus and
	// address live on the USB device however many levels above it, so the tree
	// is walked rather than assumed.
	directory, err := filepath.EvalSymlinks(filepath.Join(classRoot, class, name, "device"))
	if err != nil {
		return ""
	}
	for level := 0; level < 8; level++ {
		bus, busErr := readNumber(filepath.Join(directory, "busnum"))
		address, addressErr := readNumber(filepath.Join(directory, "devnum"))
		if busErr == nil && addressErr == nil {
			// The same spelling the USB backends use, so the two listings
			// can be compared.
			return fmt.Sprintf("%03d:%03d", bus, address)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
		directory = parent
	}
	return ""
}

// SystemOfNode reports what the node at a path receives, from its name. For the
// Earthsoft drivers that is the whole answer: there is no ioctl that would say.
func SystemOfNode(path string) (System, bool) {
	node, ok := describeNode(filepath.Base(path))
	if !ok {
		return SystemUnspecified, false
	}
	return node.System, node.System != SystemUnspecified
}

// DriverOfNode reports which driver published the node at a path: each driver
// registers its own class with its own node prefix.
func DriverOfNode(path string) (string, bool) {
	node, ok := describeNode(filepath.Base(path))
	if !ok {
		return "", false
	}
	return node.Driver, true
}

func readNumber(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(content)))
}
