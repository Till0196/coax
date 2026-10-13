// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// Which card a tuner is on. A BDA tuner is enumerated as a child of the card's
// PCI function with an instance of its own that shares no text with the
// parent's, so with two cards of the same model the parent has to be asked for
// rather than inferred.

var (
	cfgmgr32              = syscall.NewLazyDLL("cfgmgr32.dll")
	procCMLocateDevNodeW  = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	procCMGetParent       = cfgmgr32.NewProc("CM_Get_Parent")
	procCMGetDeviceIDW    = cfgmgr32.NewProc("CM_Get_Device_IDW")
	procCMGetDeviceIDSize = cfgmgr32.NewProc("CM_Get_Device_ID_Size")
)

// crSuccess is CONFIGRET's success value; every other value is a failure.
const crSuccess = 0

// parentInstanceOf reports the device instance of the card a BDA tuner is on.
// The selector is the tuner's moniker display name, which carries its device
// interface path.
func parentInstanceOf(selector string) (string, error) {
	instance := instanceFromMoniker(selector)
	if instance == "" {
		return "", fmt.Errorf("no device instance in %q", selector)
	}
	name, err := syscall.UTF16PtrFromString(instance)
	if err != nil {
		return "", err
	}
	var node uint32
	if ret, _, _ := procCMLocateDevNodeW.Call(
		uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(name)), 0); ret != crSuccess {
		return "", fmt.Errorf("locate the device %q: CONFIGRET %d", instance, ret)
	}
	var parent uint32
	if ret, _, _ := procCMGetParent.Call(
		uintptr(unsafe.Pointer(&parent)), uintptr(node), 0); ret != crSuccess {
		return "", fmt.Errorf("find the parent of %q: CONFIGRET %d", instance, ret)
	}
	return deviceInstanceID(parent)
}

func deviceInstanceID(node uint32) (string, error) {
	var length uint32
	if ret, _, _ := procCMGetDeviceIDSize.Call(
		uintptr(unsafe.Pointer(&length)), uintptr(node), 0); ret != crSuccess {
		return "", fmt.Errorf("size the device instance: CONFIGRET %d", ret)
	}
	// The size is in characters and excludes the terminator.
	buffer := make([]uint16, length+1)
	if ret, _, _ := procCMGetDeviceIDW.Call(
		uintptr(node), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0); ret != crSuccess {
		return "", fmt.Errorf("read the device instance: CONFIGRET %d", ret)
	}
	return strings.ToLower(syscall.UTF16ToString(buffer)), nil
}

// instanceFromMoniker digs the device instance out of a moniker display name,
// which reads "@device:pnp:\\?\<instance with hashes>#{interface class}\...".
func instanceFromMoniker(selector string) string {
	lower := strings.ToLower(selector)
	start := strings.Index(lower, `\\?\`)
	if start < 0 {
		return ""
	}
	rest := lower[start+4:]
	if end := strings.Index(rest, "#{"); end >= 0 {
		rest = rest[:end]
	}
	return strings.ReplaceAll(rest, "#", `\`)
}
