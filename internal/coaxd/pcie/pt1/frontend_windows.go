// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt1

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/tuner/qm1d1b0004"
	"github.com/Till0196/coax/internal/coaxd/tuner/tda6651"
)

// The register pairs each demodulator core is sent before use: one set per
// standard per card, because the two cards clock their demodulators from
// different crystals. What the registers mean is undocumented and the write
// order matters, so each table is kept exactly as it stands. The terrestrial
// tables begin by putting the core to sleep, which is why waking it is a
// separate step afterwards.
var (
	satellite20MHz = []byte{
		0x04, 0x02, 0x0d, 0x55, 0x11, 0x40, 0x13, 0x80, 0x17, 0x01,
		0x1c, 0x0a, 0x1d, 0xaa, 0x1e, 0x20, 0x1f, 0x88, 0x51, 0xb0,
		0x52, 0x89, 0x53, 0xb3, 0x5a, 0x2d, 0x5b, 0xd3, 0x85, 0x69,
		0x87, 0x04, 0x8e, 0x02, 0xa3, 0xf7, 0xa5, 0xc0,
	}
	satellite25MHz = []byte{
		0x04, 0x02, 0x11, 0x40, 0x13, 0x80, 0x17, 0x01, 0x1c, 0x0a,
		0x1d, 0xaa, 0x1e, 0x20, 0x1f, 0x88, 0x51, 0xb0, 0x52, 0x89,
		0x53, 0xb3, 0x5a, 0x2d, 0x5b, 0xd3, 0x85, 0x69, 0x87, 0x04,
		0x8e, 0x26, 0xa3, 0xf7, 0xa5, 0xc0,
	}
	terrestrial20MHz = []byte{
		0x03, 0x90, 0x14, 0x8f, 0x1c, 0x2a, 0x1d, 0xa8, 0x1e, 0xa2,
		0x22, 0x83, 0x31, 0x0d, 0x32, 0xe0, 0x39, 0xd3, 0x3a, 0x00,
		0x3b, 0x11, 0x3c, 0x3f,
		0x5c, 0x40, 0x5f, 0x80, 0x75, 0x02, 0x76, 0x4e, 0x77, 0x03,
		0xef, 0x01,
	}
	terrestrial25MHz = []byte{
		0x03, 0x90, 0x1c, 0x2a, 0x1d, 0xa8, 0x1e, 0xa2, 0x22, 0x83,
		0x3a, 0x04, 0x3b, 0x11, 0x3c, 0x3f, 0x5c, 0x40, 0x5f, 0x80,
		0x75, 0x0a, 0x76, 0x4c, 0x77, 0x03, 0xef, 0x01,
	}
)

// What the cores are sent by hand rather than out of a table.
var (
	resetCore          = []byte{0x01, 0x80}
	wakeSatellite      = []byte{0x17, 0x00}
	sleepSatellite     = []byte{0x17, 0x01}
	wakeTerrestrial    = []byte{0x03, 0x80}
	sleepTerrestrial   = []byte{0x03, 0x90}
	restartSatellite   = []byte{0x03, 0x01}
	restartTerrestrial = []byte{0x01, 0x40}
	// allLayersSelected takes every hierarchical layer, which is what a
	// receiver handing the whole transport stream on wants.
	allLayersSelected = []byte{0x71, 0x00}
)

// satelliteIdentity is what a satellite core's register 7 reads. It is the one
// value on this card known in advance, so it is what a newly written I2C path
// is checked against; a core answering anything else is refused.
const satelliteIdentity = 0x41

// frontend is one of the card's four receivers: a demodulator core and the
// tuner in front of it. The two tuners are held as their own types rather than
// behind one interface because they share almost nothing: one is tuned in
// kilohertz and takes a symbol rate, the other in hertz and takes neither.
type frontend struct {
	card     *card
	receiver int

	demod *tc90522.Demod
	qm    *qm1d1b0004.Tuner // satellite only
	tda   *tda6651.Tuner    // terrestrial only
}

func (fe *frontend) satellite() bool { return fe.qm != nil }

// newFrontend builds the stack for one receiver. Nothing is talked to here.
func newFrontend(held *card, receiver int) *frontend {
	fe := &frontend{
		card:     held,
		receiver: receiver,
		// The shared driver's second-core flag decides nothing on this card: it picks
		// a gain reference used only by that driver's own gain control, and this
		// card's gain comes from the board table. Passed truthfully rather than left
		// false.
		demod: tc90522.New(held.Bus(), demodAddresses[receiver]<<1, receiver >= 2),
	}
	behind := fe.demod.TunerBus()
	if satelliteReceiver(receiver) {
		fe.qm = qm1d1b0004.New(behind, tunerAddresses[receiver]<<1, qm1d1b0004.Config{AwaitLock: true})
	} else {
		fe.tda = tda6651.New(behind, tunerAddresses[receiver]<<1)
	}
	return fe
}

// reset puts a core and its partner back to a known state. The two cores of one
// chip share a reset, so resetting a terrestrial one resets the satellite one
// beside it -- which is why all four are reset before any is configured.
func (fe *frontend) reset() error {
	if err := fe.demod.WritePairs(resetCore); err != nil {
		return fe.wrap("reset", err)
	}
	// A core needs 30-50 ms after a reset before it will answer.
	time.Sleep(40 * time.Millisecond)
	return nil
}

// configure sends the core its board table, checking a satellite core first.
func (fe *frontend) configure() error {
	if fe.satellite() {
		answer, err := fe.demod.ReadRegister(0x07)
		if err != nil {
			return fe.wrap("read the identity of", err)
		}
		if answer != satelliteIdentity {
			return fmt.Errorf("the satellite core of receiver %d answered %#02x, and this drives %#02x",
				fe.receiver, answer, satelliteIdentity)
		}
	}
	return fe.wrap("configure", fe.demod.WritePairs(fe.table()))
}

func (fe *frontend) table() []byte {
	switch {
	case fe.satellite() && fe.card.model == ModelPT1:
		return satellite20MHz
	case fe.satellite():
		return satellite25MHz
	case fe.card.model == ModelPT1:
		return terrestrial20MHz
	default:
		return terrestrial25MHz
	}
}

func (fe *frontend) wakeDemod() error {
	pairs := wakeTerrestrial
	if fe.satellite() {
		pairs = wakeSatellite
	}
	return fe.wrap("wake", fe.demod.WritePairs(pairs))
}

func (fe *frontend) sleepDemod() error {
	pairs := sleepTerrestrial
	if fe.satellite() {
		pairs = sleepSatellite
	}
	return fe.wrap("put to sleep", fe.demod.WritePairs(pairs))
}

// wakeTuner brings the tuner out of standby. Neither chip has an identity
// register, so unlike the PT3 there is nothing to check here; the
// demodulator's own identity, checked in configure, stands in for it.
func (fe *frontend) wakeTuner() error {
	if fe.satellite() {
		// The satellite tuner takes no initialisation beyond a tune.
		return nil
	}
	return fe.wrap("initialise the tuner of", fe.tda.Initialize())
}

// Tune sets the receiver to a frequency in kHz and restarts the demodulator.
// Kilohertz for both halves, although the terrestrial tuner is tuned in hertz:
// its step is 1/7 MHz. The tuner moves first and the demodulator's restart is
// last, because the restart is what makes the core acquire.
func (fe *frontend) Tune(frequencyKHz int64, symbolRateKHz uint32) error {
	if fe.satellite() {
		if err := fe.wrap("tune", fe.qm.Tune(uint32(frequencyKHz))); err != nil {
			return err
		}
		return fe.wrap("restart", fe.demod.WritePairs(restartSatellite))
	}
	if err := fe.wrap("tune", fe.tda.Tune(frequencyKHz*1000)); err != nil {
		return err
	}
	if err := fe.wrap("select the layers of", fe.demod.WritePairs(allLayersSelected)); err != nil {
		return err
	}
	return fe.wrap("restart", fe.demod.WritePairs(restartTerrestrial))
}

// SelectStream picks which of a satellite transponder's streams to hand on.
// Below twelve the number is a slot in the transmitted table rather than a
// stream identifier, which the demodulator driver resolves.
func (fe *frontend) SelectStream(stream uint16) error {
	if !fe.satellite() {
		return fmt.Errorf("receiver %d is terrestrial and carries one stream", fe.receiver)
	}
	return fe.wrap("select a stream on", fe.demod.SelectStreamS(stream))
}

// Locked reports whether the demodulator has acquired what it was tuned to.
func (fe *frontend) Locked() (bool, error) {
	if fe.satellite() {
		return fe.demod.LockedS()
	}
	return fe.demod.LockedT()
}

// CNR reports the carrier-to-noise ratio in dB and whether the demodulator has
// a measurement at all; it has none for about a second after acquiring.
func (fe *frontend) CNR() (float64, bool, error) {
	if fe.satellite() {
		return fe.demod.CNRS()
	}
	return fe.demod.CNRT()
}

// WaitForLock waits for the demodulator to acquire, reporting failure to
// acquire rather than erroring: a frequency with nothing on it is ordinary.
func (fe *frontend) WaitForLock(budget time.Duration) (bool, error) {
	deadline := time.Now().Add(budget)
	for {
		locked, err := fe.Locked()
		if err != nil || locked {
			return locked, err
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// wrap says which receiver a failure was on.
func (fe *frontend) wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s receiver %d: %w", what, fe.receiver, err)
}
