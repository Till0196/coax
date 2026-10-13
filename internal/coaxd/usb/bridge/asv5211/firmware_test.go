// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asv5211

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plausibleImage is an image shaped the way the hardware's is: the holes the
// upload skips are zeroed, and everything else is not.
func plausibleImage() []byte {
	image := make([]byte, firmwareSize)
	for offset := range image {
		image[offset] = byte(offset>>8) | 1
	}
	for _, hole := range firmwareHoles {
		for offset := hole.start; offset < hole.end; offset++ {
			image[offset] = 0
		}
	}
	return image
}

func writeTemp(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadFirmwareAcceptsARawImage(t *testing.T) {
	path := writeTemp(t, "image.bin", plausibleImage())
	firmware, err := ReadFirmware(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(firmware.Image) != firmwareSize {
		t.Errorf("image is %#x bytes; want %#x", len(firmware.Image), firmwareSize)
	}
	// Not one of the builds knownFirmware lists, so the caller names the address.
	if firmware.Known {
		t.Errorf("unrecognised image reported a known jump address %#04x", firmware.Index)
	}
}

// TestReadFirmwareRejectsDataInTheHoles pins the structural check: an image with
// data where the upload transfers nothing would be uploaded incomplete and then
// jumped into, which only a replug recovers from.
func TestReadFirmwareRejectsDataInTheHoles(t *testing.T) {
	image := plausibleImage()
	image[firmwareHoles[0].start+1] = 0xff
	_, err := ReadFirmware(writeTemp(t, "image.bin", image))
	if err == nil {
		t.Fatal("accepted an image with data in a hole")
	}
	if !strings.Contains(err.Error(), "gap") {
		t.Errorf("error does not explain the gap: %v", err)
	}
}

func TestReadFirmwareRejectsAShortFile(t *testing.T) {
	// This package takes a decoded image and nothing else; a loader module or
	// anything else is rejected on size.
	_, err := ReadFirmware(writeTemp(t, "image.bin", []byte("not firmware")))
	if err == nil {
		t.Fatal("accepted a short file")
	}
	if !strings.Contains(err.Error(), "0x4000") {
		t.Errorf("error does not say what size was expected: %v", err)
	}
}

func TestReadFirmwareReportsMissingFile(t *testing.T) {
	if _, err := ReadFirmware(filepath.Join(t.TempDir(), "absent.bin")); err == nil {
		t.Fatal("accepted a missing file")
	}
}

// TestKnownFirmwareDigests pins the digest table: the images a jump address can
// be named for without being told one. A typo here would silently demote a
// recognised image to needing --firmware-index.
func TestKnownFirmwareDigests(t *testing.T) {
	wanted := map[string]uint16{
		"b45d510200a1690b3ca358d93de13f40e1d3567b663c17e773349ad96f597aa8": LinuxFirmwareIndex,
		"5db39facda140bdcb3a238dad10da5aac752a1ba3aaf17e2239f8d79a0ad6a2a": BDAFirmwareIndex,
		"f4848c8c091634897f9829e50d2ff8e5dc28792c6b20cf095d38d40379518c7a": PBDAFirmwareIndex,
		"d134890df91427d405ad5619588e63e3220e21f972ede18a9701fec5686b4816": UnusedFirmwareIndex,
	}
	if len(knownFirmware) != len(wanted) {
		t.Fatalf("knownFirmware holds %d images; want %d", len(knownFirmware), len(wanted))
	}
	for digest, index := range wanted {
		known, recognised := knownFirmware[digest]
		if !recognised {
			t.Errorf("digest %s is not listed", digest)
			continue
		}
		if known.index != index {
			t.Errorf("digest %s starts at %#04x; want %#04x", digest, known.index, index)
		}
		if known.description == "" {
			t.Errorf("digest %s has no description", digest)
		}
	}
}

// TestFirmwareIndexesDiffer guards the mistake this whole arrangement exists
// to prevent: the four builds look interchangeable but do not start at the
// same address, and two of them are only two bytes apart.
func TestFirmwareIndexesDiffer(t *testing.T) {
	indexes := map[uint16]string{
		LinuxFirmwareIndex:  "LinuxFirmwareIndex",
		UnusedFirmwareIndex: "UnusedFirmwareIndex",
		BDAFirmwareIndex:    "BDAFirmwareIndex",
		PBDAFirmwareIndex:   "PBDAFirmwareIndex",
	}
	if len(indexes) != 4 {
		t.Fatalf("two firmware jump addresses collide: %v", indexes)
	}
}

func TestDigestMatchesTheImage(t *testing.T) {
	firmware := Firmware{Image: make([]byte, firmwareSize)}
	// Printed for comparison against sha256sum, so it must be plain hex.
	if got := firmware.Digest(); len(got) != 64 {
		t.Fatalf("digest %q is not a SHA-256 hex string", got)
	}
	other := Firmware{Image: plausibleImage()}
	if firmware.Digest() == other.Digest() {
		t.Error("different images share a digest")
	}
}

type recordedTransfer struct {
	requestType byte
	request     byte
	value       uint16
	index       uint16
	length      int
	timeoutMS   uint32
	first       byte
}

type recordingDevice struct {
	transfers []recordedTransfer
	failAt    int
}

func (device *recordingDevice) Control(requestType, request byte, value, index uint16, data []byte, timeout uint32) (int, error) {
	device.transfers = append(device.transfers, recordedTransfer{requestType, request, value, index, len(data), timeout, data[0]})
	if device.failAt != 0 && len(device.transfers) == device.failAt {
		return 0, errors.New("transfer failed")
	}
	return len(data), nil
}

func TestUploadFirmware(t *testing.T) {
	firmware := make([]byte, firmwareSize)
	for index := range firmware {
		firmware[index] = byte(index >> 8)
	}
	device := &recordingDevice{}
	if err := UploadFirmwareAt(device, firmware, BDAFirmwareIndex); err != nil {
		t.Fatal(err)
	}
	if len(device.transfers) != len(firmwareSegments) {
		t.Fatalf("got %d transfers; want %d", len(device.transfers), len(firmwareSegments))
	}
	for index, segment := range firmwareSegments {
		got := device.transfers[index]
		if got.requestType != 0x40 || got.request != segment.request || got.value != segment.offset ||
			got.index != BDAFirmwareIndex || got.length != segment.length || got.timeoutMS != timeoutMS ||
			got.first != byte(segment.offset>>8) {
			t.Errorf("transfer %d = %+v; segment = %+v", index, got, segment)
		}
	}
}

func TestUploadLinuxFirmwareIndex(t *testing.T) {
	device := &recordingDevice{}
	if err := UploadFirmwareAt(device, make([]byte, firmwareSize), LinuxFirmwareIndex); err != nil {
		t.Fatal(err)
	}
	for _, transfer := range device.transfers {
		if transfer.index != LinuxFirmwareIndex {
			t.Fatalf("firmware index = %#04x; want %#04x", transfer.index, LinuxFirmwareIndex)
		}
	}
}

func TestUploadFirmwareRejectsWrongSize(t *testing.T) {
	for _, size := range []int{0, firmwareSize - 1, firmwareSize + 1} {
		if err := UploadFirmwareAt(&recordingDevice{}, make([]byte, size), BDAFirmwareIndex); err == nil {
			t.Errorf("accepted firmware size %#x", size)
		}
	}
}

func TestUploadFirmwareStopsAtAFailedSegment(t *testing.T) {
	device := &recordingDevice{failAt: 3}
	if err := UploadFirmwareAt(device, make([]byte, firmwareSize), BDAFirmwareIndex); err == nil {
		t.Fatal("accepted a failed transfer")
	}
	if len(device.transfers) != 3 {
		t.Fatalf("continued after failed transfer: %d calls", len(device.transfers))
	}
}
