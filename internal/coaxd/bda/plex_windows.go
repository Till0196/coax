// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package bda

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/asicen"
	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"
)

// ASICEN drivers initialise the device on the first filter open and may tear
// it down after the last closes. A tuner keeps a filter open so subsequent
// graphs need not repeat that initialisation. Opens of filters on one device
// are serialised because an overlapping open can fail during initialisation.
// The observed timings depend on the hardware and driver version.
//
// On the PX-W3U3 V2 only the first bridge's filters reach the enclosure's I2C
// bus, where the demodulators are, so a tuner on the second bridge also keeps
// the first bridge's filter of the same reception standard.

// plexChannel is a PLEX filter held open, and the vendor interface on it once
// asked for.
type plexChannel struct {
	filter     *directshow.IBaseFilter
	properties *vendors.FilterProperties
	control    *asicen.Control
	// controlled is when the vendor interface was set up.
	controlled time.Time
}

// openPlexChannel opens a PLEX tuner's filter.
func openPlexChannel(tuner filters.Moniker) (*plexChannel, error) {
	lock, _ := bridgeLocks.LoadOrStore(bridgeOf(tuner.Display), &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	var unknown *win32.IUnknown
	if err := tuner.Moniker.BindToObject(nil, nil, &directshow.IID_IBaseFilter, &unknown); err != nil {
		return nil, fmt.Errorf("open BDA tuner %q: %w", tuner.Name, err)
	}
	return &plexChannel{filter: (*directshow.IBaseFilter)(unsafe.Pointer(unknown))}, nil
}

// vendor sets the vendor interface up on first use.
func (channel *plexChannel) vendor() (*asicen.Control, error) {
	if channel.control != nil {
		return channel.control, nil
	}
	properties, err := vendors.PropertySet(channel.filter)
	if err != nil {
		return nil, err
	}
	control, err := asicen.Open(properties)
	if err != nil {
		properties.Release()
		return nil, err
	}
	channel.properties, channel.control, channel.controlled = properties, control, time.Now()
	return control, nil
}

func (channel *plexChannel) release() {
	if channel.control != nil {
		channel.control.Close()
		channel.properties.Release()
	}
	channel.filter.Release()
}

// bridgeLocks keeps the opens of one bridge's filters apart, by device
// instance.
var bridgeLocks sync.Map

// Card aliases may share a driver state even when they are on separate USB
// bridges. Serialise complete APDU exchanges within their physical enclosure;
// this does not assume that the enclosure has one or two independent slots.
var plexCardLocks sync.Map

// bridgeOf names the device instance a filter belongs to: its path up to the
// interface class.
func bridgeOf(path string) string {
	lower := strings.ToLower(path)
	if at := strings.Index(lower, "#{"); at >= 0 {
		return lower[:at]
	}
	return lower
}

// firstBridgeOf finds the filter a V2 tuner on the second bridge reaches its
// demodulator through: the matching reception standard on the first bridge of
// the same enclosure, behind the enclosure's hub.
func firstBridgeOf(tuner filters.Moniker, tuners []filters.Moniker) (filters.Moniker, bool) {
	hub, err := hubOf(tuner.Display)
	if err != nil {
		return filters.Moniker{}, false
	}
	for _, candidate := range tuners {
		if !asicen.IsControlPeer(tuner.Vendor(), candidate.Vendor()) {
			continue
		}
		if other, err := hubOf(candidate.Display); err == nil && other == hub {
			return candidate, true
		}
	}
	return filters.Moniker{}, false
}

// CardReaderGroup names the enclosure containing a published USB card path.
// It asks Configuration Manager only; listing readers must not initialise a
// tuner or open a DirectShow filter. An unresolved topology leaves the paths
// separate rather than merging independent devices.
func CardReaderGroup(display string) string {
	tuner := vendors.Tuner{Path: display}
	if !asicen.CardMayHavePeer(tuner) {
		return ""
	}
	hub, err := hubOf(display)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%04x:%s", asicen.USBProduct(tuner), hub)
}

// readPlexSignal fills the C/N from the vendor interface of a PLEX tuner.
func (device *Device) readPlexSignal(status *Status) {
	if device.plex == nil {
		return
	}
	control, err := device.plex.vendor()
	if err != nil {
		return
	}
	if cnr, measured, err := control.SignalLevel(); err == nil && measured {
		status.CNR = Measurement{Value: cnr, Known: true}
	}
}

// plexStreamAt is how a graph on this tuner reads its transmission control
// block, or nil when it has none to read.
func (device *Device) plexStreamAt() func(int) (uint16, error) {
	if !asicen.Is(device.tuner.Vendor()) || device.plex == nil {
		return nil
	}
	return device.streamAt
}

// streamAt reads the TSID in one slot of a satellite tuner's transmission
// control block, as the graph's tune left it.
func (device *Device) streamAt(slot int) (uint16, error) {
	if device.plex == nil {
		return 0, fmt.Errorf("this tuner has no transmission control block to read")
	}
	control, err := device.plex.vendor()
	if err != nil {
		return 0, err
	}
	var tsid uint16
	var filled bool
	// Prefer the driver's TMCC item over reproducing its hardware routing.
	for attempt := 0; attempt < 20; attempt++ {
		tsid, filled, err = control.TransmissionStream(slot)
		if err != nil || filled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err == nil && filled {
		return tsid, nil
	}
	address, known := asicen.SatelliteDemod(device.tuner.Vendor())
	if !known {
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("TMCC slot %d carries no stream", slot)
	}
	channel := device.plexBus
	if channel == nil {
		channel = device.plex
	}
	control, err = channel.vendor()
	if err != nil {
		return 0, err
	}
	for attempt := 0; attempt < 20; attempt++ {
		tsid, filled, err = control.StreamAt(address, slot)
		if err != nil || filled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		return 0, err
	}
	if !filled {
		return 0, fmt.Errorf("TMCC slot %d carries no stream", slot)
	}
	return tsid, nil
}

// The configuration manager, asked for a device instance's parent.
var (
	cfgmgr32              = syscall.NewLazyDLL("cfgmgr32.dll")
	procCMLocateDevNodeW  = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	procCMGetParent       = cfgmgr32.NewProc("CM_Get_Parent")
	procCMGetDeviceIDW    = cfgmgr32.NewProc("CM_Get_Device_IDW")
	procCMGetDeviceIDSize = cfgmgr32.NewProc("CM_Get_Device_ID_Size")
)

// hubOf names the hub a USB device hangs off. A composite interface has an
// extra parent between it and the physical USB device.
func hubOf(display string) (string, error) {
	instance := bridgeOf(display)
	if at := strings.Index(instance, `\\?\`); at >= 0 {
		instance = instance[at+4:]
	}
	name, err := syscall.UTF16PtrFromString(strings.ReplaceAll(instance, "#", `\`))
	if err != nil {
		return "", err
	}
	var node uint32
	if ret, _, _ := procCMLocateDevNodeW.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(name)), 0); ret != 0 {
		return "", fmt.Errorf("locate %s: CONFIGRET %d", instance, ret)
	}
	depth := 1
	if strings.Contains(strings.ToLower(display), "&mi_") {
		depth = 2
	}
	for range depth {
		var parent uint32
		if ret, _, _ := procCMGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(node), 0); ret != 0 {
			return "", fmt.Errorf("find the parent of %s: CONFIGRET %d", instance, ret)
		}
		node = parent
	}
	var length uint32
	if ret, _, _ := procCMGetDeviceIDSize.Call(uintptr(unsafe.Pointer(&length)), uintptr(node), 0); ret != 0 {
		return "", fmt.Errorf("size the hub's instance: CONFIGRET %d", ret)
	}
	buffer := make([]uint16, length+1)
	if ret, _, _ := procCMGetDeviceIDW.Call(uintptr(node), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0); ret != 0 {
		return "", fmt.Errorf("read the hub's instance: CONFIGRET %d", ret)
	}
	return strings.ToLower(syscall.UTF16ToString(buffer)), nil
}
