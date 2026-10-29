// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package vendors

import (
	"encoding/binary"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
)

func TestNodePropertyReachesTheKsTopology(t *testing.T) {
	var request, value []byte
	callback := syscall.NewCallback(func(self uintptr, property *byte, propertyBytes uintptr, data *byte, dataBytes uintptr, returned *uint32) uintptr {
		request = append([]byte(nil), unsafe.Slice(property, int(propertyBytes))...)
		value = append([]byte(nil), unsafe.Slice(data, int(dataBytes))...)
		*returned = uint32(dataBytes)
		return 0
	})
	table := [6]uintptr{3: callback}
	control := &Control{vtbl: &table}
	set := win32.GUID{Data1: 0x0aa8a605, Data2: 0xa240, Data3: 0x11de,
		Data4: [8]byte{0xb1, 0x30, 0, 0, 0, 0, 0x4d, 0x56}}
	if err := control.SetNode(set, 0, 1, []byte{8, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(control)
	if len(request) != 32 || binary.LittleEndian.Uint32(request[20:]) != typeSet|typeTopology ||
		binary.LittleEndian.Uint32(request[24:]) != 1 || binary.LittleEndian.Uint32(request[28:]) != 0 {
		t.Fatalf("node property header: %x", request)
	}
	if binary.LittleEndian.Uint32(request) != set.Data1 || len(value) != 4 || binary.LittleEndian.Uint32(value) != 8 {
		t.Fatalf("node property set=%x value=%x", request[:16], value)
	}
}
