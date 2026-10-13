// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import "testing"

// The message is the committed-switch command of DiSEqC 1.0, and every bit of
// its fourth byte is checked: the port in the two above the bottom, the band
// the tone selects in the bottom one, and the polarisation the voltage selects
// beside it.
//
// The values are what szap-s2j puts on the lead for the same channel, which is
// what these switches have been listening to for twenty years.
func TestDiseqcMessageIsTheCommittedSwitchCommand(t *testing.T) {
	for _, one := range []struct {
		name       string
		properties map[string]string
		message    [4]byte
		vertical   bool
		toneOn     bool
		burstB     bool
	}{
		{
			// Port 0, vertical, tone off: the lowest of everything.
			name: "the first port at the lower band",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "0",
				"POLARIZATION": "VERTICAL", "TONE": "OFF", "VOLTAGE": "low",
			},
			message:  [4]byte{0xe0, 0x10, 0x38, 0xf0},
			vertical: true,
		},
		{
			// Horizontal sets bit 1, which is what the higher voltage means
			// to a switch.
			name: "horizontal",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "0",
				"POLARIZATION": "HORIZONTAL", "TONE": "OFF", "VOLTAGE": "high",
			},
			message: [4]byte{0xe0, 0x10, 0x38, 0xf2},
		},
		{
			// The tone sets bit 0.
			name: "the upper band",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "0",
				"POLARIZATION": "VERTICAL", "TONE": "ON", "VOLTAGE": "low",
			},
			message:  [4]byte{0xe0, 0x10, 0x38, 0xf1},
			vertical: true,
			toneOn:   true,
		},
		{
			// The port goes two bits up, and an odd port asks for the second
			// burst as well.
			name: "the second port",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "1",
				"POLARIZATION": "HORIZONTAL", "TONE": "ON", "VOLTAGE": "high",
			},
			message: [4]byte{0xe0, 0x10, 0x38, 0xf7},
			toneOn:  true,
			burstB:  true,
		},
		{
			name: "the last port a committed switch has",
			properties: map[string]string{
				"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "3",
				"POLARIZATION": "VERTICAL", "TONE": "OFF", "VOLTAGE": "low",
			},
			message:  [4]byte{0xe0, 0x10, 0x38, 0xfc},
			vertical: true,
			burstB:   true,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			command, wanted, err := DiseqcFor(one.properties)
			if err != nil || !wanted {
				t.Fatalf("DiseqcFor: %v, wanted %t", err, wanted)
			}
			if command.Length != 4 {
				t.Errorf("the message is %d bytes, want 4", command.Length)
			}
			if got := [4]byte(command.Message[:4]); got != one.message {
				t.Errorf("message = % x, want % x", got, one.message)
			}
			if command.Vertical != one.vertical || command.ToneOn != one.toneOn ||
				command.BurstB != one.burstB {
				t.Errorf("vertical %t tone %t burst B %t, want %t %t %t",
					command.Vertical, command.ToneOn, command.BurstB,
					one.vertical, one.toneOn, one.burstB)
			}
			if !command.Supply {
				t.Error("a channel that named a voltage asks for the lead to be fed")
			}
		})
	}
}

// A channel that names no position is left alone. Putting a switch command on
// a lead nobody asked to switch is a change to the lead the channel did not
// ask for, and a dish with one block on it has nothing listening for it.
func TestNothingIsSentForAChannelThatNamedNoPosition(t *testing.T) {
	_, wanted, err := DiseqcFor(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "POLARIZATION": "VERTICAL",
	})
	if err != nil || wanted {
		t.Fatalf("DiseqcFor: %v, wanted %t", err, wanted)
	}
}

func TestDiseqcRefusesWhatItCannotSend(t *testing.T) {
	for _, one := range []struct {
		name       string
		properties map[string]string
	}{
		{"a port a committed switch does not have", map[string]string{
			"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "4"}},
		{"a position that is not a number", map[string]string{
			"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "second"}},
		{"a switch on a lead with no satellite on it", map[string]string{
			"DELIVERY_SYSTEM": "ISDBT", "SAT_NUMBER": "0"}},
	} {
		t.Run(one.name, func(t *testing.T) {
			if _, _, err := DiseqcFor(one.properties); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// A lead the installation feeds itself is asked for no supply, and then there
// is nothing to send a message on.
func TestAnUnfedLeadCarriesNoVoltage(t *testing.T) {
	command, wanted, err := DiseqcFor(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "SAT_NUMBER": "0", "VOLTAGE": "off",
	})
	if err != nil || !wanted {
		t.Fatalf("DiseqcFor: %v, wanted %t", err, wanted)
	}
	if command.Supply {
		t.Error("a lead asked for no supply was going to be fed")
	}
}
