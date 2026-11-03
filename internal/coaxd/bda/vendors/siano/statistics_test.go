// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package siano

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestControlPathKeepsTheCompleteUSBInstance(t *testing.T) {
	for _, instance := range []string{"5&abc&0&1", "5&abc&0&10"} {
		path := `@device:pnp:\\?\usb#vid_187f&pid_0600#` + instance + `#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\tuner`
		got, ok := ControlPath(strings.ToUpper(path))
		want := `\\?\usb#vid_187f&pid_0600#` + instance + `#{212cb866-fe82-40f2-94bf-5f0badef5931}\NewLegacyInterface`
		if !ok || got != want {
			t.Fatalf("path=%q, ok=%v", got, ok)
		}
	}
	for _, path := range []string{
		`@device:pnp:\\?\usb#vid_187f&pid_06000#instance#{category}`,
		`@device:pnp:\\?\usb#vid_187f&pid_0600##{category}`,
		`@device:pnp:\\?\pci#vid_187f&pid_0600#instance#{category}`,
		`test tuner`,
	} {
		if got, ok := ControlPath(path); ok {
			t.Fatalf("accepted %q as %q", path, got)
		}
	}
}

type recordingChannel struct {
	request []byte
	reply   []byte
	err     error
}

func (channel *recordingChannel) Exchange(request []byte) ([]byte, error) {
	channel.request = append([]byte(nil), request...)
	return channel.reply, channel.err
}

func TestReadStatisticsUsesItsOwnClientAndWholeDBStatistics(t *testing.T) {
	reply := make([]byte, 296)
	copy(reply, []byte{0x8e, 0x02, 11, statisticsClient, 0x28, 0x01, 1, 0})
	binary.LittleEndian.PutUint32(reply[12:], 5)
	binary.LittleEndian.PutUint32(reply[16:], 284)
	binary.LittleEndian.PutUint32(reply[20:], 1)
	binary.LittleEndian.PutUint32(reply[24:], 1)
	binary.LittleEndian.PutUint32(reply[32:], 27)
	minus77 := int32(-77)
	binary.LittleEndian.PutUint32(reply[40:], uint32(minus77))
	binary.LittleEndian.PutUint32(reply[240:], 87)
	channel := &recordingChannel{reply: reply}
	got, err := readStatistics(channel)
	if err != nil || !got.SNR.Known || got.SNR.Value != 27 || !got.Power.Known || got.Power.Value != -77 {
		t.Fatalf("reading=%+v, err=%v", got, err)
	}
	if !bytes.Equal(channel.request, []byte{0x8d, 0x02, 250, 11, 8, 0, 0, 0}) {
		t.Fatalf("request=%x", channel.request)
	}
	for _, tc := range []struct {
		name string
		at   int
		data []byte
	}{
		{"wrong response", 0, []byte{0x68, 0x02}},
		{"another client", 3, []byte{201}},
		{"wrong length", 4, []byte{0x24, 0x01}},
		{"split message", 6, []byte{4}},
		{"failed request", 8, []byte{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := append([]byte(nil), reply...)
			copy(body[tc.at:], tc.data)
			got, err := readStatistics(&recordingChannel{reply: body})
			if err == nil || got.SNR.Known || got.Power.Known {
				t.Fatalf("SNR=%+v, err=%v", got, err)
			}
		})
	}
	for size := 0; size < len(reply); size++ {
		if got, _ := readStatistics(&recordingChannel{reply: reply[:size]}); got.SNR.Known || got.Power.Known {
			t.Fatalf("%d bytes: SNR=%+v", size, got)
		}
	}
	failure := errors.New("control failed")
	if got, err := readStatistics(&recordingChannel{err: failure}); got.SNR.Known || !errors.Is(err, failure) {
		t.Fatalf("SNR=%+v, err=%v", got, err)
	}
}

func TestOnlyASianoStickIsListedForISDBT(t *testing.T) {
	stick := `@device:pnp:\\?\usb#vid_187f&pid_0600#5&abc&0&1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\tuner`
	if got := Deliveries(stick); len(got) != 1 || got[0] != "ISDB-T" {
		t.Fatalf("stick: %q", got)
	}
	if got := Deliveries(`@device:pnp:\\?\usb#vid_0b06&pid_0006&mi_00#a&1#{c}\{t}`); got != nil {
		t.Fatalf("another device: %q", got)
	}
}
