// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tbs

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

type equipmentFake struct {
	calls   []string
	message []byte
	failure error
}

func (f *equipmentFake) SetVoltage(high, off bool) error {
	f.calls = append(f.calls, fmt.Sprintf("voltage %t %t", high, off))
	return nil
}
func (f *equipmentFake) SetTone(on bool) error {
	f.calls = append(f.calls, fmt.Sprintf("tone %t", on))
	return nil
}
func (f *equipmentFake) SendDiSEqC(message []byte) error {
	f.calls = append(f.calls, "message")
	f.message = append([]byte(nil), message...)
	return f.failure
}

func TestSatelliteEquipmentUsesRequestedVoltageAndRestoresTone(t *testing.T) {
	for _, fails := range []bool{false, true} {
		front := &equipmentFake{}
		if fails {
			front.failure = errors.New("transmitter failed")
		}
		properties := map[string]string{"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1250000", "SYMBOL_RATE": "22000000", "SAT_NUMBER": "2", "VOLTAGE": "18", "TONE": "ON"}
		bridge := func(high, off bool) error {
			front.calls = append(front.calls, fmt.Sprintf("bridge %t %t", high, off))
			return nil
		}
		err := satelliteEquipment(front, properties, bridge, false)
		if !errors.Is(err, front.failure) {
			t.Fatal(err)
		}
		if want := []string{"bridge true false", "voltage true false", "tone false", "message", "tone true"}; !reflect.DeepEqual(front.calls, want) {
			t.Fatalf("calls=%v", front.calls)
		}
		if want := []byte{0xe0, 0x10, 0x38, 0xfb}; !reflect.DeepEqual(front.message, want) {
			t.Fatalf("message=%x, want %x", front.message, want)
		}
	}
}

func TestQuattroSelectsAnInputWithoutChangingTheSharedSupply(t *testing.T) {
	front := &equipmentFake{}
	bridge := func(bool, bool) error { t.Fatal("changed shared supply"); return nil }
	props := map[string]string{"DELIVERY_SYSTEM": "DVBS2", "VOLTAGE": "18", "TONE": "ON"}
	if err := satelliteEquipment(front, props, bridge, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"voltage true false", "tone true"}; !reflect.DeepEqual(front.calls, want) {
		t.Fatal(front.calls)
	}
	front.calls = nil
	props["SAT_NUMBER"] = "1"
	if err := satelliteEquipment(front, props, bridge, true); err == nil {
		t.Fatal("allowed a shared aerial to be switched")
	}
	if len(front.calls) != 0 {
		t.Fatal("changed hardware before rejecting shared switching")
	}
}

func TestUnsupportedDiseqcIsNotSilentlyDropped(t *testing.T) {
	props := map[string]string{"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "1"}
	bridge := func(bool, bool) error { t.Fatal("powered hardware before rejecting unsupported message"); return nil }
	if err := satelliteEquipment(struct{}{}, props, bridge, false); err == nil {
		t.Fatal("silently dropped DiSEqC")
	}
}

func TestDrivenFrequencyKeepsTheIntermediateFrequency(t *testing.T) {
	props := map[string]string{"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1250000", "SYMBOL_RATE": "22000000", tuning.LNBProperty: "UNIVERSAL", "TONE": "ON"}
	// Use the same LNBf metadata the server retains after downconversion.
	if err := tuning.ApplyLNBf(props); err == nil {
		t.Fatal("IF was accepted as a dish downlink")
	}
	props = map[string]string{"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "12000000", "SYMBOL_RATE": "22000000", tuning.LNBProperty: "UNIVERSAL"}
	if err := tuning.ApplyLNBf(props); err != nil {
		t.Fatal(err)
	}
	got, err := channelFrequency(props)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1400000 {
		t.Fatalf("chip received %d kHz instead of 1400000 kHz IF", got)
	}
}
