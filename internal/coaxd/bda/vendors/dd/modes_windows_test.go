// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package dd

import "testing"

// TestIsRecognisesTheirBus checks Is against both the synthetic DD_DVB bus path
// and a PCI path carrying the vendor id, and that another vendor's card is not
// claimed.
func TestIsRecognisesTheirBus(t *testing.T) {
	for _, test := range []struct {
		path string
		want bool
	}{
		{`@device:pnp:\\?\dd_dvb#ven_dd01&dev_000a&subsys_0050dd01#5&1&0#{guid}\{x}`, true},
		{`@device:pnp:\\?\pci#ven_dd01&dev_0011#4&1&0#{guid}\{x}`, true},
		{`@device:pnp:\\?\pci#ven_544d&dev_6178#4&1&0#{guid}\{x}`, false},
	} {
		if got := Is(Tuner{Path: test.path}); got != test.want {
			t.Errorf("Is(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

// TestStandardOfPrefersTheLongerName guards names that contain another: matching
// the short one first would read an ISDB-S3 filter as ISDB-S, or a DVB-T2 one as
// DVB-T.
func TestStandardOfPrefersTheLongerName(t *testing.T) {
	for name, want := range map[string]DemodMode{
		"Digital Devices ISDB-T Tuner 1":  ModeISDBT,
		"Digital Devices ISDB-S Tuner 2":  ModeISDBS,
		"Digital Devices ISDB-S3 Tuner 2": ModeISDBS3,
		"Digital Devices DVB-T Tuner 3":   ModeDVBT,
		"Digital Devices DVB-T2 Tuner 3":  ModeDVBT2,
		"Digital Devices DVB-S2X Tuner 4": ModeDVBS2X,
		"Digital Devices J83-B Tuner 4":   ModeJ83B,
	} {
		got, ok := StandardOf(Tuner{Name: name})
		if !ok || got != want {
			t.Errorf("StandardOf(%q) = %v (%v), want %v", name, got, ok, want)
		}
	}
	if _, ok := StandardOf(Tuner{Name: "Some Other Tuner 1"}); ok {
		t.Error("a name with no standard in it should not name one")
	}
}

// TestModeNamesReadsTheMask checks the mask a card answers with, which is one
// bit per standard in this vendor's own numbering.
func TestModeNamesReadsTheMask(t *testing.T) {
	// What a MAX M4 reports it can receive.
	names := ModeNames(0x0001073F)
	want := map[string]bool{
		"DVB-T": true, "DVB-T2": true, "DVB-C": true, "DVB-C2": true,
		"DVB-S": true, "DVB-S2": true, "ISDB-T": true, "ISDB-C": true,
		"ISDB-S": true, "J83B": true,
	}
	if len(names) != len(want) {
		t.Errorf("mask names %v, want %d of them", names, len(want))
	}
	for _, name := range names {
		if !want[name] {
			t.Errorf("mask should not name %q", name)
		}
	}
}
