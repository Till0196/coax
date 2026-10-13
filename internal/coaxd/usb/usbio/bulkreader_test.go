// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbio

import (
	"bytes"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio/backend"
)

// countingDevice hands out a byte stream that counts up, through a ring or,
// with noRing set, a transfer at a time.
type countingDevice struct {
	backend.Device
	noRing bool
	next   byte
	opened int
	closed int
}

func (device *countingDevice) fill(buffer []byte) int {
	for index := range buffer {
		buffer[index] = device.next
		device.next++
	}
	return len(buffer)
}

func (device *countingDevice) Bulk(_ byte, buffer []byte, _ uint32) (int, error) {
	return device.fill(buffer), nil
}

func (device *countingDevice) OpenBulkStream(_ byte, size, _ int, _ uint32) (backend.Stream, error) {
	if device.noRing {
		return nil, backend.ErrUnsupported
	}
	device.opened++
	return &countingRing{device: device, size: size}, nil
}

type countingRing struct {
	device *countingDevice
	size   int
}

func (ring *countingRing) Read(buffer []byte, _ uint32) (int, error) {
	return ring.device.fill(buffer[:ring.size]), nil
}

func (ring *countingRing) Close() error { ring.device.closed++; return nil }

func TestBulkReaderHandsOnEveryByteToASmallBuffer(t *testing.T) {
	device := &countingDevice{}
	reader := (&Transport{device: device}).BulkReader(0x82)
	var got []byte
	small := make([]byte, 1000)
	for len(got) < 3*bulkStreamSize {
		read, err := reader.Read(small, 100)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, small[:read]...)
	}
	want := make([]byte, len(got))
	(&countingDevice{}).fill(want)
	if !bytes.Equal(got, want) {
		t.Fatal("bytes were lost or repeated across a buffer smaller than one transfer")
	}
	if device.opened != 1 {
		t.Fatalf("opened the stream %d times, want once", device.opened)
	}
}

func TestBulkReaderStopsAndOpensAgain(t *testing.T) {
	device := &countingDevice{}
	reader := (&Transport{device: device}).BulkReader(0x82)
	buffer := make([]byte, 100)
	if _, err := reader.Read(buffer, 100); err != nil {
		t.Fatal(err)
	}
	if err := reader.Stop(); err != nil {
		t.Fatal(err)
	}
	if device.closed != 1 {
		t.Fatalf("closed %d times, want once", device.closed)
	}
	// What was carried over from before the stop belongs to the last stream: the
	// next read starts on the new transfer, not at byte 100 of the old one.
	read, err := reader.Read(buffer, 100)
	if err != nil {
		t.Fatal(err)
	}
	if device.opened != 2 || buffer[0] != 0 || read != len(buffer) {
		t.Fatalf("after a stop: opened %d, first byte %d", device.opened, buffer[0])
	}
}

func TestBulkReaderFallsBackWithoutARing(t *testing.T) {
	device := &countingDevice{noRing: true}
	reader := (&Transport{device: device}).BulkReader(0x82)
	buffer := make([]byte, 10)
	for pass := 0; pass < 2; pass++ {
		if read, err := reader.Read(buffer, 100); err != nil || read != len(buffer) {
			t.Fatalf("read %d, %v", read, err)
		}
	}
	if buffer[0] != 10 {
		t.Fatalf("second read began at %d, want 10", buffer[0])
	}
}
