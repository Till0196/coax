// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package lwire

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/Till0196/coax/pkg/win32"
)

type recordingControl struct {
	write   bool
	request []byte
	answer  []byte
}

func (control *recordingControl) Exchange(_ *win32.GUID, _ uint32, write bool, request, data []byte) (uint32, error) {
	control.write = write
	control.request = append([]byte(nil), request...)
	copy(data, control.answer)
	return uint32(len(data)), nil
}

func TestWriteShiftsTheAddressAndCarriesTheBytes(t *testing.T) {
	control := &recordingControl{}
	bus := &Bus{Control: control}
	if err := bus.Write(0x64, []byte{0x14, 0x00, 0x02, 0x11, 0x9a, 0x14}); err != nil {
		t.Fatal(err)
	}
	request := control.request
	if !control.write || len(request) != requestBytes {
		t.Fatalf("write=%v, %d bytes", control.write, len(request))
	}
	for at, want := range map[int]uint32{modeAt: modeWrite, writeLenAt: 6, readLenAt: 0, addressAt: 0xc8} {
		if got := binary.LittleEndian.Uint32(request[at:]); got != want {
			t.Errorf("offset %d: %#x, want %#x", at, got, want)
		}
	}
	if !bytes.Equal(request[writeAt:writeAt+6], []byte{0x14, 0x00, 0x02, 0x11, 0x9a, 0x14}) {
		t.Errorf("write buffer % x", request[writeAt:writeAt+6])
	}
}

func TestReadComesBackFromTheReadBuffer(t *testing.T) {
	answer := make([]byte, requestBytes)
	copy(answer[readAt:], []byte{0x80, 0x00, 0x36, 0x00})
	control := &recordingControl{answer: answer}
	bus := &Bus{Control: control}
	got, err := bus.Read(0x64, 4)
	if err != nil {
		t.Fatal(err)
	}
	if control.write || binary.LittleEndian.Uint32(control.request[modeAt:]) != modeRead || binary.LittleEndian.Uint32(control.request[readLenAt:]) != 4 {
		t.Errorf("request % x", control.request[:40])
	}
	if !bytes.Equal(got, []byte{0x80, 0x00, 0x36, 0x00}) {
		t.Errorf("read % x", got)
	}
}
