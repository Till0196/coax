// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"fmt"
)

// Diseqc is one DiSEqC exchange: the message, the voltage and tone that frame
// it, and whether the burst after it is the B one. A value rather than a call so
// the two backends that can send one -- the Linux frontend and a BDA tuning
// space on Windows -- are given the same thing.
type Diseqc struct {
	Message [6]byte
	Length  byte
	// Vertical is the polarisation, which is carried as the voltage on the
	// lead: the lower of the two for vertical and right-hand circular, the
	// higher for horizontal and left-hand.
	Vertical bool
	// Supply says the lead is fed at all. A distribution that feeds itself is
	// asked for no supply, and then there is nothing to send a message on.
	Supply bool
	// ToneOn is the continuous 22 kHz tone the lead is left carrying, which on
	// a universal block selects the upper band and on the premium blocks
	// selects the satellite.
	ToneOn bool
	// BurstB asks for the second of the two tone bursts, which is how a switch
	// that predates DiSEqC proper is told which of two leads to take.
	BurstB bool
}

// SatNumberProperty is the channel-file key naming the DiSEqC position, spelled
// the way libdvbv5 spells it.
const SatNumberProperty = "SAT_NUMBER"

// DiseqcFor reads the DiSEqC exchange a channel asks for, and reports whether
// it asks for one at all.
//
// A channel that does not name a position gets nothing sent, unlike szap-s2j
// which sends a message on every tune: a dish with one block has nothing
// listening, and switching a lead nobody asked to switch is a change the
// channel did not ask for.
//
// The message is the committed-switch command of DiSEqC 1.0, with the port in
// its low bits beside the band the tone selects and the polarisation the
// voltage selects. Those two must agree with what is put on the lead, which is
// why they are read from the channel here.
func DiseqcFor(properties map[string]string) (Diseqc, bool, error) {
	written := properties[SatNumberProperty]
	if written == "" {
		return Diseqc{}, false, nil
	}
	position, err := Number(properties, SatNumberProperty, true)
	if err != nil {
		return Diseqc{}, false, err
	}
	if position > 3 {
		return Diseqc{}, false, fmt.Errorf(
			"%s is a DiSEqC position of 0 to 3, not %d; a committed switch has four ports",
			SatNumberProperty, position)
	}
	if !IsSatellite(properties["DELIVERY_SYSTEM"]) {
		return Diseqc{}, false, fmt.Errorf(
			"%s asks a switch on a satellite lead, and %q is not a satellite delivery system",
			SatNumberProperty, properties["DELIVERY_SYSTEM"])
	}

	supply, err := SatelliteSupply(properties)
	if err != nil {
		return Diseqc{}, false, err
	}
	command := Diseqc{
		Length:   4,
		Vertical: !supply.High,
		Supply:   !supply.Off,
		ToneOn:   ToneWanted(properties),
		BurstB:   position%2 == 1,
	}
	command.Message = [6]byte{
		0xe0, // framing: master, no reply expected, first transmission
		0x10, // address: any LNB, switch or SMATV
		0x38, // command: write to port group 0, the committed switch
		0xf0 | byte(position)<<2 | boolBit(command.ToneOn, 0) | boolBit(!command.Vertical, 1),
	}
	return command, true, nil
}

func boolBit(set bool, bit uint) byte {
	if set {
		return 1 << bit
	}
	return 0
}
