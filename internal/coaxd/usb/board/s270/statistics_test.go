// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package s270

import (
	"encoding/binary"
	"testing"
)

func TestStatisticsStatus(t *testing.T) {
	body := make([]byte, 288)
	binary.LittleEndian.PutUint32(body[4:], 5)
	binary.LittleEndian.PutUint32(body[8:], 284)
	binary.LittleEndian.PutUint32(body[16:], 1)
	binary.LittleEndian.PutUint32(body[12:], 1)
	binary.LittleEndian.PutUint32(body[24:], 27)
	minus77 := int32(-77)
	binary.LittleEndian.PutUint32(body[32:], uint32(minus77))
	got := modes["ISDBT"].status(body, false)
	if !got.Locked || len(got.Names) != 5 || !got.CNR.Known || got.CNR.Value != 27 ||
		!got.Strength.Known || got.Strength.Value != -77 {
		t.Fatalf("status=%+v", got)
	}
	// A failed or incomplete request may still use the lock indication, but
	// must not publish a measurement from that reply.
	unlocked := append([]byte(nil), body...)
	binary.LittleEndian.PutUint32(unlocked[16:], 0)
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"missing", nil},
		{"short", body[:24]},
		{"failed", append([]byte{1, 0, 0, 0}, body[4:]...)},
		{"unlocked", unlocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := modes["ISDBT"].status(tc.body, true)
			if !got.Locked || got.CNR.Known || got.Strength.Known {
				t.Fatalf("status=%+v", got)
			}
		})
	}
	if got := modes["ISDBT"].status(nil, false); got.Locked || got.CNR.Known || len(got.Names) != 0 {
		t.Fatalf("status=%+v", got)
	}
	plain := make([]byte, 16)
	binary.LittleEndian.PutUint32(plain[12:], 1)
	if got := modes["DVBT"].status(plain, false); !got.Locked || got.CNR.Known {
		t.Fatalf("DVB-T status=%+v", got)
	}
}
