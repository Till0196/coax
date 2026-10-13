// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package tbs

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// Reading and changing the standard a card is wired for. The property carries
// the whole card's modes in one value, so a tuner reads the byte that is its own
// and writes it back among the others unaltered -- on a 6812 the other tuner may
// legitimately be on the other standard. model_windows.go is what the values
// mean on each card.

const (
	// switchTimeout and switchPoll bound the wait for the card to come back.
	// Five seconds is what a 6205SE takes; the rest is headroom.
	switchTimeout = 45 * time.Second
	switchPoll    = 500 * time.Millisecond
)

// ReadModes reads every mode byte the property carries.
func ReadModes(properties vendors.Properties, model Model) ([]byte, error) {
	value := make([]byte, model.Reply)
	if _, err := properties.Get(Set, PropDemodMode, value); err != nil {
		return nil, fmt.Errorf("read the demodulator mode: %w", err)
	}
	return value[:model.Width], nil
}

// WriteModes writes them all back.
func WriteModes(properties vendors.Properties, model Model, modes []byte) error {
	if err := properties.Set(Set, PropDemodMode, modes[:model.Width]); err != nil {
		return fmt.Errorf("set the demodulator mode: %w", err)
	}
	return nil
}

// Switch is one tuner's view of its card's mode property: what the property
// means on this model, which of its bytes is this tuner's own, and how to reach
// it. Reaching it costs a graph -- the property is on the tuner filter's input
// pin, and a KS pin has no handle until connected -- so Open builds one, Live is
// the pin of an already running graph, and Streaming says what else on the card
// would be interrupted.
type Switch struct {
	Model     Model
	Slot      int
	Open      func() (vendors.Properties, func(), error)
	Live      func() (vendors.Properties, error)
	Streaming func() int
}

// Current is the mode this tuner is in. A caller with a running graph passes
// its property set; one without passes nil and a graph is built for the read.
func (mode Switch) Current(live vendors.Properties) (DemodMode, error) {
	// A card whose driver has no such property is not asked for it.
	if mode.Model.NoMode {
		return ModeUnknown, nil
	}
	if live != nil {
		return mode.pick(ReadModes(live, mode.Model))
	}
	properties, done, err := mode.Open()
	if err != nil {
		return ModeUnknown, err
	}
	defer done()
	return mode.pick(ReadModes(properties, mode.Model))
}

// pick takes this tuner's byte out of a reading of the whole property.
func (mode Switch) pick(modes []byte, err error) (DemodMode, error) {
	if err != nil {
		return ModeUnknown, err
	}
	return DemodMode(modes[mode.Slot]), nil
}

// Ensure puts this tuner into the mode a delivery system needs, waits for it to
// get there, and reports the mode it ended up in. A card that does not answer to
// the property is left alone: there is nothing to switch.
func (mode Switch) Ensure(system string) (DemodMode, error) {
	wanted, mapped := mode.Model.ModeFor(system)
	if !mapped {
		return ModeUnknown, nil
	}
	return mode.Set(wanted)
}

// Set puts this tuner into a mode named by value rather than by delivery system:
// what Ensure does once it knows which value it wants, and what putting a card
// back the way it was found needs.
func (mode Switch) Set(wanted DemodMode) (DemodMode, error) {
	if mode.Model.NoMode {
		return ModeUnknown, nil
	}
	properties, done, err := mode.Open()
	if err != nil {
		// Reaching the property needs a graph, which can fail for reasons that
		// have nothing to do with the mode; tuning reports it better.
		return ModeUnknown, nil
	}
	modes, err := ReadModes(properties, mode.Model)
	if err != nil {
		done()
		return ModeUnknown, nil
	}
	current := DemodMode(modes[mode.Slot])
	if current == wanted {
		done()
		return current, nil
	}
	// Writing the mode restarts every tuner on this card, so one that is
	// recording is a reason to stop rather than something to interrupt.
	if streaming := mode.Streaming(); streaming > 0 {
		done()
		return current, fmt.Errorf(
			"this tuner is in %s and %s is wanted, but %d tuner(s) on the same card are streaming: writing the mode restarts all of them",
			mode.Model.NameFor(current), mode.Model.NameFor(wanted), streaming)
	}
	modes[mode.Slot] = byte(wanted)
	err = WriteModes(properties, mode.Model, modes)
	done()
	if err != nil {
		return current, fmt.Errorf("set this tuner to %s: %w", mode.Model.NameFor(wanted), err)
	}

	// The card drops off and comes back; both the graph and the read can fail
	// meanwhile, so only a matching reading ends the wait.
	deadline := time.Now().Add(switchTimeout)
	for {
		time.Sleep(switchPoll)
		if reading, err := mode.Current(nil); err == nil && reading == wanted {
			return wanted, nil
		}
		if time.Now().After(deadline) {
			return current, fmt.Errorf("this %s tuner did not change to %s within %s",
				mode.Model.Name, mode.Model.NameFor(wanted), switchTimeout)
		}
	}
}
