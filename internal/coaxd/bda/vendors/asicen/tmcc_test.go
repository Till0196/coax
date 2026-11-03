// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/Till0196/coax/pkg/win32"
)

type tmccDriver struct {
	protocol
	slots             [8]uint16
	count             byte
	short             bool
	address, register byte
	lastAddress       byte
}

func (p *tmccDriver) Get(g win32.GUID, id uint32, data []byte) (uint32, error) {
	switch id {
	case 45:
		for i, value := range p.slots {
			binary.LittleEndian.PutUint32(data[i*4:], uint32(value))
		}
		data[32] = p.count
	case propRead:
		value := p.slots[int(p.register-tmccStreams)/2]
		if p.register&1 == 0 {
			data[0] = byte(value >> 8)
		} else {
			data[0] = byte(value)
		}
	default:
		return p.protocol.Get(g, id, data)
	}
	n := uint32(len(data))
	if p.short {
		n--
	}
	return n, nil
}
func (p *tmccDriver) Set(g win32.GUID, id uint32, data []byte) error {
	if id == propRead {
		p.lastAddress, p.register = data[0], data[1]
		if data[0] != p.address || data[1] < tmccStreams || data[1] >= tmccStreams+16 {
			return errors.New("wrong demodulator or register")
		}
		return nil
	}
	return p.protocol.Set(g, id, data)
}

func TestDriverTMCCAndRawI2CFallbackAgree(t *testing.T) {
	// These are the distinct satellite addresses in the supported USB and PCI
	// layouts. The driver item must not need to know any of them.
	for _, address := range []byte{0x22, 0x26, 0x2a, 0x2e, 0x32, 0x36} {
		driver := &tmccDriver{address: address, count: 8, slots: [8]uint16{0x4010, 0x4631, 0xffff, 0, 0x4777}}
		control, err := Open(driver)
		if err != nil {
			t.Fatal(err)
		}
		for _, slot := range []int{0, 1, 2, 3, 4} {
			tsid, filled, err := control.TransmissionStream(slot)
			if err != nil {
				t.Fatal(err)
			}
			if driver.lastAddress != 0 && slot == 0 {
				t.Fatal("driver TMCC accessed I2C")
			}
			raw, rawFilled, err := control.StreamAt(address, slot)
			if err != nil || tsid != raw || filled != rawFilled {
				t.Fatalf("%#x slot %d: driver %#x/%v, raw %#x/%v: %v", address, slot, tsid, filled, raw, rawFilled, err)
			}
		}
	}
}

func TestTMCCRejectsShortRepliesAndInvalidSlots(t *testing.T) {
	driver := &tmccDriver{address: 0x32, count: 8, short: true}
	control, err := Open(driver)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := control.TransmissionStream(0); err == nil {
		t.Fatal("accepted a short TMCC reply")
	}
	if _, _, err := control.StreamAt(0x32, 0); err == nil {
		t.Fatal("accepted a short I2C reply")
	}
	driver.short = false
	for _, slot := range []int{-1, 8, 12} {
		if _, _, err := control.TransmissionStream(slot); err == nil {
			t.Fatalf("accepted slot %d", slot)
		}
		if _, _, err := control.StreamAt(0x32, slot); err == nil {
			t.Fatalf("accepted raw slot %d", slot)
		}
	}
	driver.count = 9
	if _, _, err := control.TransmissionStream(0); err == nil {
		t.Fatal("accepted too many TMCC slots")
	}
	driver.count = 1
	if _, filled, err := control.TransmissionStream(1); err != nil || filled {
		t.Fatalf("read beyond declared slots: %v/%v", filled, err)
	}
	control.Close()
	if _, _, err := control.TransmissionStream(0); err != io.ErrClosedPipe {
		t.Fatalf("closed TMCC: %v", err)
	}
}
