// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package directshow

import (
	"github.com/Till0196/coax/pkg/win32"
	"runtime"
	"syscall"
	"testing"
)

func TestBDAFrequencyPropertiesUseTheirOwnComSlots(t *testing.T) {
	var table [1024]uintptr
	called, value := -1, uint32(0)
	for slot := 3; slot <= 14; slot++ {
		table[slot] = syscall.NewCallback(func(self uintptr, v uint32) uintptr { called, value = slot, v; return 0 })
	}
	frequency := IBDA_FrequencyFilter{IUnknown: win32.IUnknown{LpVtbl: &table}}
	for _, test := range []struct {
		name  string
		call  func() error
		slot  int
		value uint32
	}{
		{"frequency", func() error { return frequency.Put_Frequency(527142) }, 5, 527142},
		{"polarity", func() error { return frequency.Put_Polarity(BDA_POLARISATION_LINEAR_V) }, 7, 2},
		{"bandwidth", func() error { return frequency.Put_Bandwidth(6) }, 11, 6},
		{"multiplier", func() error { return frequency.Put_FrequencyMultiplier(1000) }, 13, 1000},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err != nil {
				t.Fatal(err)
			}
			if called != test.slot || value != test.value {
				t.Fatalf("slot=%d value=%d, want slot=%d value=%d", called, value, test.slot, test.value)
			}
		})
	}
	runtime.KeepAlive(&frequency)
}
