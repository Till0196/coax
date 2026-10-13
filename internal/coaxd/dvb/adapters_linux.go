// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package dvb

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"unsafe"
)

// DefaultRoot is where the kernel puts the DVB device nodes.
const DefaultRoot = "/dev/dvb"

// Adapter is one frontend the kernel is offering, found without opening it.
// Opening is not free -- a driver may power the tuner up, and a frontend
// already in use answers EBUSY -- so listing is kept separate from asking.
type Adapter struct {
	// Adapter and Frontend are the numbers in the device path, which is how
	// every other tool on the system refers to them.
	Adapter  int
	Frontend int
	// Path is the frontend device node.
	Path string
	// Demux and DVR are the nodes the stream is read through, numbered to
	// match the frontend as every adapter in the tree does.
	Demux string
	DVR   string
}

// Name is how this frontend is written in a tuner name: "adapter0.frontend0".
// It is the adapter's position, not a serial number, so it holds while the
// device stays where it is and changes if the adapters are renumbered.
func (a Adapter) Name() string {
	return fmt.Sprintf("adapter%d.frontend%d", a.Adapter, a.Frontend)
}

var (
	adapterDirectory = regexp.MustCompile(`^adapter(\d+)$`)
	frontendNode     = regexp.MustCompile(`^frontend(\d+)$`)
)

// Adapters lists the frontends under a device root, /dev/dvb when empty. A
// root that does not exist is not an error.
func Adapters(root string) ([]Adapter, error) {
	if root == "" {
		root = DefaultRoot
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	var found []Adapter
	for _, entry := range entries {
		match := adapterDirectory.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		nodes, err := os.ReadDir(directory)
		if err != nil {
			// One unreadable adapter must not hide the others.
			continue
		}
		for _, node := range nodes {
			frontend := frontendNode.FindStringSubmatch(node.Name())
			if frontend == nil {
				continue
			}
			index, err := strconv.Atoi(frontend[1])
			if err != nil {
				continue
			}
			found = append(found, Adapter{
				Adapter:  number,
				Frontend: index,
				Path:     filepath.Join(directory, node.Name()),
				Demux:    filepath.Join(directory, fmt.Sprintf("demux%d", index)),
				DVR:      filepath.Join(directory, fmt.Sprintf("dvr%d", index)),
			})
		}
	}
	// Readdir order is whatever the filesystem gives; a listing that reorders
	// itself between runs cannot be compared.
	sort.Slice(found, func(i, j int) bool {
		if found[i].Adapter != found[j].Adapter {
			return found[i].Adapter < found[j].Adapter
		}
		return found[i].Frontend < found[j].Frontend
	})
	return found, nil
}

// Info is what a frontend says about itself.
type Info struct {
	// Name is the driver's own name for the hardware, e.g. "Sony CXD2856
	// DVB-T/T2/C demodulator".
	Name string
	// FrequencyMin and FrequencyMax are the frequencies it accepts, in
	// whatever unit the delivery system uses: kHz for satellite, Hz for
	// everything else. A zero maximum means the driver did not say.
	FrequencyMin uint32
	FrequencyMax uint32
	// Deliveries are the delivery systems it can tune, by their channel-file
	// names. Empty means the driver would not enumerate them, which several
	// older drivers do not.
	Deliveries []string
}

// Info asks the frontend what it is and what it can do.
func (f *Frontend) Info() (Info, error) {
	var raw frontendInfo
	request := ioctlRequest(iocRead, dvbIoctlType, feGetInfoNR, unsafe.Sizeof(raw))
	if err := ioctlPointer(f.file, request, unsafe.Pointer(&raw)); err != nil {
		return Info{}, fmt.Errorf("FE_GET_INFO: %w", err)
	}
	info := Info{
		Name:         cString(raw.Name[:]),
		FrequencyMin: raw.FrequencyMin,
		FrequencyMax: raw.FrequencyMax,
	}
	// A driver that will not enumerate its delivery systems still has a name
	// and a frequency range worth reporting, so this is not fatal.
	if systems, err := f.DeliverySystems(); err == nil {
		for _, system := range systems {
			if name := DeliveryName(system); name != "" {
				info.Deliveries = append(info.Deliveries, name)
			}
		}
	}
	return info, nil
}

func cString(field []byte) string {
	for index, character := range field {
		if character == 0 {
			return string(field[:index])
		}
	}
	return string(field)
}
