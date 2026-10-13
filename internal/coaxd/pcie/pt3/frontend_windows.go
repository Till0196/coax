// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package pt3

import (
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/tuner/mxl301rf"
	"github.com/Till0196/coax/internal/coaxd/tuner/qm1d1c0042"
)

// frontend is one of the card's four receivers: a demodulator core and the
// tuner in front of it. The two tuners are held as their own types rather than
// behind one interface because almost nothing is shared: one is tuned in
// kilohertz and the other in hertz, one selects a stream afterwards and the
// other layers.
type frontend struct {
	card    *card
	channel int

	demod *tc90522.Demod
	qm    *qm1d1c0042.Tuner // satellite only
	mxl   *mxl301rf.Tuner   // terrestrial only
}

func (fe *frontend) satellite() bool { return fe.qm != nil }

// Where each channel's tuner answers, as an 8-bit address. Unlike the demodulator cores
// these are not derived from the channel number: the addresses are strapped and the
// pairing with the cores is the board's wiring.
//
//	channel 0  satellite, tuner 0  0x63    channel 2  terrestrial, tuner 0  0x62
//	channel 1  satellite, tuner 1  0x60    channel 3  terrestrial, tuner 1  0x61
var tunerAddresses = [channels]byte{0x63, 0x60, 0x62, 0x61}

// The frequency each frontend is tuned to once at bring-up: the manufacturer
// asks for this so the four settle away from each other. Two satellite
// frequencies either side of the first transponder, in kilohertz, and two
// terrestrial channels, in hertz.
var settlingFrequencies = [channels]uint32{1049480 - 300, 1049480 + 300, 515142857, 521142857}

// newFrontend builds the stack for one channel without talking to the card.
func newFrontend(held *card, channel int) *frontend {
	fe := &frontend{
		card:    held,
		channel: channel,
		demod:   tc90522.New(bus{card: held}, demodAddress(channel)<<1, tunerOf(channel) == 1),
	}
	tunerBus := fe.demod.TunerBus()
	if satelliteChannel(channel) {
		fe.qm = qm1d1c0042.New(tunerBus, tunerAddresses[channel]<<1, qm1d1c0042.Config{LPF: true})
	} else {
		fe.mxl = mxl301rf.New(tunerBus, tunerAddresses[channel]<<1)
	}
	return fe
}

// The demodulator registers this package works directly. The shared TC90522
// driver's own wake, sleep and pin functions write values meant for boards that
// use the chip differently, so this card needs the sequences below instead. The
// rest of that driver is the chip rather than the board and is used as it is.
var (
	// Wake and sleep, per standard.
	wakeSatellite    = []byte{0x17, 0x00}
	sleepSatellite   = []byte{0x17, 0x01}
	wakeTerrestrial  = []byte{0x03, 0x80}
	sleepTerrestrial = []byte{0x03, 0x90}

	// What each core needs before the tuners are powered, and what its transport
	// stream pins need afterwards.
	startSatellite   = []byte{0x03, 0x01, 0x1e, 0x10}
	startTerrestrial = []byte{0x01, 0x40, 0x1c, 0x10}
	pinsSatellite    = []byte{0x1c, 0x15, 0x1f, 0x04}
	pinsTerrestrial  = []byte{0x1d, 0x01}

	// Every hierarchical layer selected, for handing on the whole stream.
	allLayersSelected = []byte{0x71, 0x00}
)

// The gain control, which has to be written around every tune. The card's ROM
// initialisation does not leave the gain running: measured, with a real
// transponder on the lead, the satellite core reports its input below the
// minimum level at every frequency and its gain register never moves off 0x20;
// writing the sequence below makes it lock at once. The gain is held while the
// tuner moves and let run once it has settled. The last pair of each is the
// core's reset, which is what makes it start acquiring.
func (fe *frontend) setGainControl(automatic bool) error {
	var pairs []byte
	switch {
	case fe.satellite() && automatic:
		pairs = []byte{0x0a, 0xff, 0x10, fe.gainReference() | 0x01, 0x11, 0x40, 0x03, 0x01}
	case fe.satellite():
		pairs = []byte{0x0a, 0x00, 0x10, fe.gainReference(), 0x11, 0x00, 0x03, 0x01}
	case automatic:
		pairs = []byte{0x25, 0x40, 0x23, 0x4c, 0x01, 0x40}
	default:
		pairs = []byte{0x25, 0x00, 0x23, 0x4d, 0x01, 0x40}
	}
	return fe.wrap("set the gain control of", fe.demod.WritePairs(pairs))
}

// gainReference differs between the card's two tuners.
func (fe *frontend) gainReference() byte {
	if tunerOf(fe.channel) == 1 {
		return 0x30
	}
	return 0xb0
}

// start puts the core into the state its tuner expects, before power.
func (fe *frontend) start() error {
	pairs := startTerrestrial
	if fe.satellite() {
		pairs = startSatellite
	}
	return fe.wrap("start", fe.demod.WritePairs(pairs))
}

// configurePins sets the core's transport stream pins, after the tuners are
// powered and before they are used.
func (fe *frontend) configurePins() error {
	pairs := pinsTerrestrial
	if fe.satellite() {
		pairs = pinsSatellite
	}
	return fe.wrap("configure the transport stream pins of", fe.demod.WritePairs(pairs))
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

// wakeTuner brings the tuner out of standby. On the satellite side this is the
// tuner's whole initialisation, including the one check that a newly written
// I2C path works: the chip has to name itself. Terrestrial initialisation is in
// the card's ROM and runs for both tuners.
func (fe *frontend) wakeTuner() error {
	if fe.satellite() {
		return fe.wrap("initialise the tuner of", fe.qm.Initialize())
	}
	return fe.wrap("wake the tuner of", fe.mxl.Wake())
}

func (fe *frontend) sleepTuner() error {
	if fe.satellite() {
		return fe.wrap("put the tuner to sleep on", fe.qm.Sleep())
	}
	return fe.wrap("put the tuner to sleep on", fe.mxl.Sleep())
}

// Tune sets the frontend to a frequency in kHz and restarts the demodulator on
// it. Kilohertz for both halves although the terrestrial tuner is tuned in
// hertz: its step is 15.625 kHz. The tuner moves first and the demodulator's
// reset comes last, because the reset is what makes the core acquire.
func (fe *frontend) Tune(frequencyKHz int64) error {
	if err := fe.setGainControl(false); err != nil {
		return err
	}
	if fe.satellite() {
		if err := fe.wrap("tune", fe.qm.Tune(uint32(frequencyKHz))); err != nil {
			return err
		}
		if err := fe.waitForTuner(); err != nil {
			return err
		}
		return fe.setGainControl(true)
	}
	if err := fe.wrap("tune", fe.mxl.Tune(uint32(frequencyKHz*1000))); err != nil {
		return err
	}
	if err := fe.waitForTuner(); err != nil {
		return err
	}
	if err := fe.wrap("select the layers of", fe.demod.WritePairs(allLayersSelected)); err != nil {
		return err
	}
	return fe.setGainControl(true)
}

// tunerSettling is how long a tuner is given to settle where it was placed.
// Measured on the card, both loops settle immediately, so this is generous.
const tunerSettling = 100 * time.Millisecond

// waitForTuner refuses to go on without the tuner's own loops: an unsettled tuner
// is not on the frequency, so the demodulator would report about somewhere else.
func (fe *frontend) waitForTuner() error {
	var locked bool
	var err error
	if fe.satellite() {
		locked, err = fe.qm.WaitForPLL(tunerSettling)
	} else {
		locked, err = fe.mxl.WaitForPLLs(tunerSettling)
	}
	if err != nil {
		return fe.wrap("wait for the tuner of", err)
	}
	if !locked {
		return fmt.Errorf("the tuner of channel %d did not settle within %v", fe.channel, tunerSettling)
	}
	return nil
}

// settle is the once-per-bring-up placement of a tuner, without restarting the
// demodulator behind it: nothing will be received on it.
func (fe *frontend) settle() error {
	frequency := settlingFrequencies[fe.channel]
	if fe.satellite() {
		return fe.wrap("place the tuner of", fe.qm.Tune(frequency))
	}
	return fe.wrap("place the tuner of", fe.mxl.Tune(frequency))
}

// SelectStream picks which of a satellite transponder's streams to hand on.
// Below twelve the number is a slot in the transmitted table rather than a
// stream identifier, which the demodulator driver resolves.
func (fe *frontend) SelectStream(stream uint16) error {
	if !fe.satellite() {
		return fmt.Errorf("channel %d is terrestrial and carries one stream", fe.channel)
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

// CNR reports the carrier-to-noise ratio in dB, and whether the demodulator has
// a measurement to report. Measured: a terrestrial core answers nothing for
// about a second after acquiring, a satellite core for less. That is reported
// rather than waited out.
func (fe *frontend) CNR() (float64, bool, error) {
	if fe.satellite() {
		return fe.demod.CNRS()
	}
	return fe.demod.CNRT()
}

// WaitForLock waits for the demodulator to acquire, reporting rather than failing
// if it does not: a frequency with nothing on it is an ordinary answer.
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

func (fe *frontend) wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s channel %d: %w", what, fe.channel, err)
}
