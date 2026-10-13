// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package siano

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// message builds one wire message: header then body.
func message(messageType uint16, flags uint16, body []byte) []byte {
	raw := make([]byte, HeaderSize+len(body))
	binary.LittleEndian.PutUint16(raw, messageType)
	raw[2] = 0
	raw[3] = hifTask
	binary.LittleEndian.PutUint16(raw[4:], uint16(len(raw)))
	binary.LittleEndian.PutUint16(raw[6:], flags)
	copy(raw[HeaderSize:], body)
	return raw
}

// newTestDevice builds a Device with the reader goroutine left unstarted, so a
// test can hand messages to dispatch directly.
func newTestDevice(alignment int) *Device {
	return &Device{
		alignment: alignment,
		waiters:   make(map[uint16]chan []byte),
		data:      make(chan []byte, 4),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

func TestWords(t *testing.T) {
	got := Words(1, 0x2000)
	want := []byte{1, 0, 0, 0, 0x00, 0x20, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("Words = %x; want %x", got, want)
	}
}

func TestDispatchDeliversToWaiter(t *testing.T) {
	device := newTestDevice(0)
	waiter := make(chan []byte, 1)
	device.waiters[MsgInitDeviceRes] = waiter

	device.dispatch(message(MsgInitDeviceRes, 0, []byte{6, 0, 0, 0}))

	select {
	case body := <-waiter:
		if !bytes.Equal(body, []byte{6, 0, 0, 0}) {
			t.Fatalf("body = %x", body)
		}
	default:
		t.Fatal("the waiter received nothing")
	}
	if _, still := device.waiters[MsgInitDeviceRes]; still {
		t.Error("the waiter was not removed after delivery")
	}
}

func TestDispatchRoutesTransportStream(t *testing.T) {
	device := newTestDevice(0)
	packet := bytes.Repeat([]byte{0x47}, 188)

	device.dispatch(message(MsgDVBTBDAData, 0, packet))

	select {
	case block := <-device.Data():
		if !bytes.Equal(block, packet) {
			t.Fatalf("block = %x", block[:8])
		}
	default:
		t.Fatal("no transport stream block was queued")
	}
}

func TestDispatchTracksLockIndications(t *testing.T) {
	device := newTestDevice(0)
	if device.Locked() {
		t.Fatal("a fresh device should not report lock")
	}
	device.dispatch(message(MsgSignalDetectedInd, 0, nil))
	if !device.Locked() {
		t.Fatal("the signal indication was not recorded")
	}
	device.dispatch(message(MsgNoSignalInd, 0, nil))
	if device.Locked() {
		t.Fatal("the no-signal indication was not recorded")
	}
	device.setLocked(true)
	device.ClearLock()
	if device.Locked() {
		t.Fatal("ClearLock left the lock set")
	}
}

// A split response repeats its header one packet in, and the body the host
// wants follows there rather than at the head of the buffer.
func TestDispatchSplitMessage(t *testing.T) {
	const alignment = 56
	device := newTestDevice(alignment)
	waiter := make(chan []byte, 1)
	device.waiters[MsgDataDownloadRes] = waiter

	body := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	offset := alignment + 2 // the low bits of the flags carry the extra 2
	raw := make([]byte, offset+HeaderSize+len(body))
	header := message(MsgDataDownloadRes, flagSplitMessage|(2<<8), body)
	copy(raw, header[:HeaderSize])
	copy(raw[offset:], header)

	device.dispatch(raw)

	select {
	case got := <-waiter:
		if !bytes.Equal(got, body) {
			t.Fatalf("body = %x; want %x", got, body)
		}
	default:
		t.Fatal("the split message was not delivered")
	}
}

func TestDispatchIgnoresTruncatedMessages(t *testing.T) {
	device := newTestDevice(0)
	waiter := make(chan []byte, 1)
	device.waiters[MsgInitDeviceRes] = waiter

	// A header claiming more bytes than arrived must not be trusted.
	raw := message(MsgInitDeviceRes, 0, []byte{1, 2, 3, 4})
	binary.LittleEndian.PutUint16(raw[4:], uint16(len(raw)+16))
	device.dispatch(raw)
	device.dispatch(raw[:4])

	select {
	case <-waiter:
		t.Fatal("a truncated message was delivered")
	default:
	}
}

func TestDrainData(t *testing.T) {
	device := newTestDevice(0)
	device.dispatch(message(MsgDVBTBDAData, 0, []byte{1}))
	device.dispatch(message(MsgDVBTBDAData, 0, []byte{2}))
	device.DrainData()
	select {
	case <-device.Data():
		t.Fatal("DrainData left a block queued")
	default:
	}
}

func TestParseFirmware(t *testing.T) {
	image := make([]byte, 12+8)
	binary.LittleEndian.PutUint32(image, 0xdeadbeef)  // checksum, unused
	binary.LittleEndian.PutUint32(image[4:], 8)       // length
	binary.LittleEndian.PutUint32(image[8:], 0x10000) // start address
	copy(image[12:], []byte{1, 2, 3, 4, 5, 6, 7, 8})

	firmware, err := ParseFirmware(image)
	if err != nil {
		t.Fatal(err)
	}
	if firmware.Length != 8 || firmware.StartAddress != 0x10000 {
		t.Fatalf("firmware = %+v", firmware)
	}
	if !bytes.Equal(firmware.Payload, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("payload = %x", firmware.Payload)
	}
}

func TestParseFirmwareRejectsShortImages(t *testing.T) {
	if _, err := ParseFirmware([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected an error for an image shorter than its header")
	}
	image := make([]byte, 12+2)
	binary.LittleEndian.PutUint32(image[4:], 64) // claims more than follows
	if _, err := ParseFirmware(image); err == nil {
		t.Fatal("expected an error for a truncated payload")
	}
}

func TestVersionMode(t *testing.T) {
	cold := Version{FirmwareID: 255}
	if cold.Mode() != ModeNone {
		t.Errorf("a ROM-only chip reported mode %d", cold.Mode())
	}
	warm := Version{FirmwareID: ModeISDBTBDA, SupportedProtocols: 1 << ModeISDBTBDA}
	if warm.Mode() != ModeISDBTBDA {
		t.Errorf("mode = %d", warm.Mode())
	}
	if !warm.Supports(ModeISDBTBDA) || warm.Supports(ModeDVBT) {
		t.Errorf("Supports: ISDB-T %v, DVB-T %v", warm.Supports(ModeISDBTBDA), warm.Supports(ModeDVBT))
	}
}
