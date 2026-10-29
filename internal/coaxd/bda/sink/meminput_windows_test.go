// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"bytes"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Exercise the COM receive entry itself: sample boundaries may divide a TLV
// header, payload, or even its sync byte from the rest of the packet.
func TestReceivePreservesRawSamplesAcrossAttach(t *testing.T) {
	var sampleData []byte
	vtable := [19]uintptr{}
	vtable[3] = syscall.NewCallback(func(this, out uintptr) uintptr {
		*(*unsafe.Pointer)(comPointer(out)) = unsafe.Pointer(&sampleData[0])
		return hrOK
	})
	vtable[11] = syscall.NewCallback(func(this uintptr) uintptr { return uintptr(len(sampleData)) })
	sample := struct{ vtbl *[19]uintptr }{&vtable}
	var output bytes.Buffer
	renderer := New(&output)
	defer renderer.Release()
	want := []byte{0x7f, 0xff, 0, 5, 1, 2, 3, 4, 5, 0x7f, 0xff, 0, 0}
	for _, size := range []int{1, 2, 3, 7} {
		renderer.Attach(&output)
		output.Reset()
		for at := 0; at < len(want); {
			end := min(at+size, len(want))
			sampleData = want[at:end]
			result := memReceive(uintptr(unsafe.Pointer(&renderer.pin.memVtbl)), uintptr(unsafe.Pointer(&sample)))
			if result != hrOK {
				t.Fatalf("receive: %#x", result)
			}
			at = end
		}
		if !bytes.Equal(output.Bytes(), want) {
			t.Fatalf("sample size %d: got %x", size, output.Bytes())
		}
	}
	runtime.KeepAlive(sample)
	runtime.KeepAlive(sampleData)
}
