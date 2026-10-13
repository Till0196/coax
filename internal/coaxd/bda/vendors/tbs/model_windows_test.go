// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import "testing"

// TestModelForRecognisesRatherThanAssumes guards against handing every
// unrecognised card the DVB layout: a Digital Devices card answers the same
// identifier on the same GUID with a number of its own -- its filter's standard,
// not a mode -- so the value compares unequal to what the channel wanted and the
// next step is a write to another vendor's card.
func TestModelForRecognisesRatherThanAssumes(t *testing.T) {
	for _, test := range []struct {
		name  string
		tuner Tuner
		want  string
	}{
		{"a 6205SE, by its subsystem id",
			Tuner{Path: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016205#4&1&0#{guid}\{x}`,
				Name: "TBS 6205se Tuner 0"}, Model6205SE.Name},
		{"a 6812, by its subsystem id",
			Tuner{Path: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016812#4&1&0#{guid}\{x}`,
				Name: "TBS 6812 Tuner 0"}, Model6812.Name},
		{"a 6812, by name alone",
			Tuner{Path: `@device:pnp:\\?\usb#vid_0000&pid_0000#0#{guid}\{x}`,
				Name: "TBS 6812 Tuner 1"}, Model6812.Name},
		{"a 6508, which gives every tuner a mode",
			Tuner{Path: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016508#4&1&0#{guid}\{x}`,
				Name: "TBS 6508 Tuner 5"}, Model6508.Name},
		{"a card of this vendor whose model is not in the table",
			Tuner{Path: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00019999#4&1&0#{guid}\{x}`,
				Name: "TBS 9999 Tuner 0"}, ModelDefault.Name},
		{"another vendor's card is not this vendor's",
			Tuner{Path: `@device:pnp:\\?\dd_dvb#ven_dd01&dev_000a#5&1&0#{guid}\{x}`,
				Name: "Digital Devices ISDB-T Tuner 1"}, ""},
		{"nobody's card in particular",
			Tuner{Path: `@device:pnp:\\?\usb#vid_0511&pid_0045#0#{guid}\{x}`,
				Name: "Generic DVB-T Tuner"}, ""},
	} {
		model, ok := ModelFor(test.tuner)
		got := ""
		if ok {
			got = model.Name
		}
		if got != test.want {
			t.Errorf("%s: ModelFor = %q, want %q", test.name, got, test.want)
		}
	}
}

// TestSlotsAreNotShared: the 6812's two tuners own different bytes of the
// property, and writing one tuner's mode into the other's byte takes that tuner
// off its standard.
func TestSlotsAreNotShared(t *testing.T) {
	first := Tuner{Name: "TBS 6812 Tuner 0",
		Path: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016812#4&1&0#{guid}\{1f7c3bfb-f6dd-438e-ba22-5e42d7f28fea}`}
	second := Tuner{Name: "TBS 6812 Tuner 1",
		Path: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016812#4&1&0#{guid}\{8aaa8912-3af3-448c-b78d-1a84379c0ca3}`}
	if slot := Model6812.SlotFor(first); slot != 0 {
		t.Errorf("the first tuner owns byte %d, want 0", slot)
	}
	if slot := Model6812.SlotFor(second); slot != 1 {
		t.Errorf("the second tuner owns byte %d, want 1", slot)
	}
	// The DVB cards keep one mode for the whole card, so every tuner uses byte 0.
	if slot := Model6205SE.SlotFor(first); slot != 0 {
		t.Errorf("a card-wide mode should be byte 0, not %d", slot)
	}
}

// TestTheSameByteMeansDifferentThings is why the models are separate files.
func TestTheSameByteMeansDifferentThings(t *testing.T) {
	dvb, _ := Model6205SE.ModeFor("DVBT")
	isdb, _ := Model6812.ModeFor("ISDBT")
	if byte(dvb) != byte(isdb) {
		t.Fatalf("this test is about the two meaning the same number: %d and %d", dvb, isdb)
	}
	if Model6205SE.NameFor(dvb) == Model6812.NameFor(isdb) {
		t.Errorf("mode %d should read as %q on one card and %q on the other",
			byte(dvb), Model6205SE.NameFor(dvb), Model6812.NameFor(isdb))
	}
}

// TestEveryModelIsWideEnoughForItsTuners checks the two sizes against each
// other. Reply is what a read asks for and Width what a write carries: a write
// wider than the read would send bytes nobody read back, and a slot past the
// width would set a byte the write does not carry.
func TestEveryModelIsWideEnoughForItsTuners(t *testing.T) {
	for model_, model := range models {
		if model.Width < 1 || model.Reply < model.Width {
			t.Errorf("%s (%s): writes %d bytes and reads %d", model_, model.Name, model.Width, model.Reply)
		}
		for tuner := 0; tuner < 8; tuner++ {
			slot := model.SlotFor(Tuner{Name: model.Name + " Tuner " + string(rune('0'+tuner))})
			if slot < 0 || slot >= model.Width {
				t.Errorf("%s (%s): tuner %d takes byte %d of %d", model_, model.Name, tuner, slot, model.Width)
			}
		}
	}
}

// TestTunerNumberComesFromTheName pins how a tuner says which one it is.
func TestTunerNumberComesFromTheName(t *testing.T) {
	for name, want := range map[string]int{
		"TBS 6508 Tuner 0": 0,
		"TBS 6508 Tuner 7": 7,
		"TBS 6205se":       0,
		"Tuner 3":          3,
	} {
		if got := tunerNumberOf(Tuner{Name: name}); got != want {
			t.Errorf("tunerNumberOf(%q) = %d, want %d", name, got, want)
		}
	}
}

// TestACardWithNoModePropertyIsNeverAsked pins the 6902 family: their driver
// carries no mode property, and a card that cannot be read would look like a
// card that cannot be opened.
func TestACardWithNoModePropertyIsNeverAsked(t *testing.T) {
	for _, name := range []string{"6902", "6903", "6904", "6905", "6908", "6909", "6910"} {
		model, known := models[name]
		if !known {
			t.Errorf("the %s is not in the table", name)
			continue
		}
		if !model.NoMode {
			t.Errorf("the %s should be marked as having no mode property", name)
		}
		if _, mapped := model.ModeFor("DVBS2"); mapped {
			t.Errorf("the %s maps a delivery system to a mode it cannot write", name)
		}
		// Both answer without a property set to reach -- which is what the nil
		// below would panic on if either tried.
		mode := Switch{Model: *model}
		if current, err := mode.Current(nil); err != nil || current != ModeUnknown {
			t.Errorf("the %s answered %d, %v", name, current, err)
		}
		if settled, err := mode.Ensure("DVBS2"); err != nil || settled != ModeUnknown {
			t.Errorf("the %s settled on %d, %v", name, settled, err)
		}
	}
}
