// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package siano

import (
	"encoding/binary"
	"testing"
)

func TestISDBTSNR(t *testing.T) {
	statistics := make([]byte, 284)
	binary.LittleEndian.PutUint32(statistics, ModeISDBT)
	binary.LittleEndian.PutUint32(statistics[4:], uint32(len(statistics)))
	binary.LittleEndian.PutUint32(statistics[12:], 1)
	binary.LittleEndian.PutUint32(statistics[20:], 27)
	// Reception quality and full-resolution SNR are distinct fields. The RIO
	// firmware leaves the latter at zero even when whole-dB SNR is available.
	binary.LittleEndian.PutUint32(statistics[228:], 87)
	if got := ISDBTSNR(statistics); !got.Known || got.Value != 27 {
		t.Fatalf("SNR=%+v", got)
	}
	for _, tc := range []struct {
		name string
		at   int
		word uint32
	}{
		{"unknown type", 0, ModeDVBT},
		{"short declared size", 4, 20},
		{"truncated structure", 4, 288},
		{"oversized structure", 4, 0xffffffff},
		{"unlocked", 12, 0},
		{"no SNR", 20, 0},
		{"negative SNR", 20, 0xffffffff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := append([]byte(nil), statistics...)
			binary.LittleEndian.PutUint32(body[tc.at:], tc.word)
			if got := ISDBTSNR(body); got.Known {
				t.Fatalf("SNR=%+v", got)
			}
		})
	}
	for size := 0; size < len(statistics); size++ {
		if got := ISDBTSNR(statistics[:size]); got.Known {
			t.Fatalf("%d bytes: SNR=%+v", size, got)
		}
	}
}

func TestISDBTInBandPower(t *testing.T) {
	// As an S270 on its own driver reported 22ch: RF and demodulator locked,
	// SNR 26 dB, RSSI -57 dBm, in-band power -77 dBm.
	statistics := make([]byte, 284)
	for at, word := range []int32{ModeISDBT, 284, 1, 1, 0, 26, -57, -77, -6403, 527142000} {
		binary.LittleEndian.PutUint32(statistics[4*at:], uint32(word))
	}
	if got := ISDBTInBandPower(statistics); !got.Known || got.Value != -77 {
		t.Fatalf("power=%+v", got)
	}
	for _, tc := range []struct {
		name string
		at   int
		word uint32
	}{
		{"unknown type", 0, ModeDVBT},
		{"short declared size", 4, 28},
		{"truncated structure", 4, 288},
		{"no RF lock", 8, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := append([]byte(nil), statistics...)
			binary.LittleEndian.PutUint32(body[tc.at:], tc.word)
			if got := ISDBTInBandPower(body); got.Known {
				t.Fatalf("power=%+v", got)
			}
		})
	}
	for size := 0; size < 32; size++ {
		if got := ISDBTInBandPower(statistics[:size]); got.Known {
			t.Fatalf("%d bytes: power=%+v", size, got)
		}
	}
}
