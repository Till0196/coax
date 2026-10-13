// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"reflect"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// A tuner with nothing to set says nothing, so a client offers nothing; one
// whose supply cannot be switched off is not offered "off"; an amplifier with
// no levels is a switch.
func TestControlsSayOnlyWhatWillBeHonoured(t *testing.T) {
	if got := controls(device.Controls{}); got != nil {
		t.Errorf("nothing to set rendered as %+v", got)
	}
	got := controls(device.Controls{
		LNB:  &device.LNBControl{Low: "13V", High: "18V"},
		Gain: &device.GainControl{},
	})
	want := &hostapi.Controls{
		LNB:  &hostapi.LNBControl{Levels: []string{"low", "high"}, Low: "13V", High: "18V"},
		Gain: &hostapi.GainControl{Kind: "switch", Decibels: []float64{}, Deliveries: []string{}},
	}
	if got.LNB == nil || !reflect.DeepEqual(got.LNB, want.LNB) {
		t.Errorf("supply %+v, want %+v", got.LNB, want.LNB)
	}
	if got.Gain == nil || got.Gain.Kind != "switch" || len(got.Gain.Decibels) != 0 {
		t.Errorf("gain %+v, want a switch", got.Gain)
	}
	shared := controls(device.Controls{
		LNB:  &device.LNBControl{Off: true, Shared: "board"},
		Gain: &device.GainControl{Decibels: []float64{0, 1, 2, 3}, Deliveries: []string{"ISDB-S"}},
	})
	if !reflect.DeepEqual(shared.LNB.Levels, []string{"off", "low", "high"}) || shared.LNB.Shared != "board" {
		t.Errorf("shared supply %+v", shared.LNB)
	}
	if shared.Gain.Kind != "steps" || !reflect.DeepEqual(shared.Gain.Deliveries, []string{"ISDB-S"}) {
		t.Errorf("stepped gain %+v", shared.Gain)
	}
}
