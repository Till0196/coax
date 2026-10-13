// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import (
	"errors"
	"testing"
)

// recordingVoltage stands in for the card's supply switch.
type recordingVoltage struct {
	input     int
	high, off bool
	calls     int
	fail      error
}

func (setter *recordingVoltage) SetInputVoltage(rfInput int, high, off bool) error {
	setter.input, setter.high, setter.off, setter.calls = rfInput, high, off, setter.calls+1
	return setter.fail
}

func quattroDemod(bus *fakeBus) *Demod {
	return newTestDemodIn(bus, Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Quattro, Demod: 4, RFInput: 3})
}

func TestQuattroStartsOnInputZeroWhateverTheWiringSays(t *testing.T) {
	bus := bootedBus()
	quattro := New(bus, Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Quattro, Demod: 4, RFInput: 3})
	if err := quattro.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := quattro.RFInput(); got != 0 {
		t.Fatalf("a Quattro frontend started on input %d, want 0", got)
	}
	direct := New(bootedBus(), Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Demod: 4, RFInput: 3})
	if err := direct.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := direct.RFInput(); got != 3 {
		t.Fatalf("a Direct frontend started on input %d, want its wired 3", got)
	}
}

func TestQuattroInputMapping(t *testing.T) {
	// 18 V sets bit 1 and everything else clears it; the tone sets bit 0. So
	// inputs 0 and 1 are the 13 V pair, 2 and 3 the 18 V pair, with the tone
	// on 1 and 3. This is the opposite polarity from the MxL58x package, where
	// the same two bits mark 13 V and tone-off.
	cases := []struct {
		name  string
		high  bool
		tone  bool
		input int
	}{
		{"vertical, low band", false, false, 0},
		{"vertical, high band", false, true, 1},
		{"horizontal, low band", true, false, 2},
		{"horizontal, high band", true, true, 3},
	}
	for _, test := range cases {
		// Both orders, because a caller may set the tone before the voltage.
		for _, voltageFirst := range []bool{true, false} {
			demod := quattroDemod(newFakeBus())
			apply := []func() error{
				func() error { return demod.SetVoltage(test.high, false) },
				func() error { return demod.SetTone(test.tone) },
			}
			if !voltageFirst {
				apply[0], apply[1] = apply[1], apply[0]
			}
			for _, step := range apply {
				if err := step(); err != nil {
					t.Fatalf("%s: %v", test.name, err)
				}
			}
			if got := demod.RFInput(); got != test.input {
				t.Errorf("%s (voltage first %v) chose input %d, want %d",
					test.name, voltageFirst, got, test.input)
			}
		}
	}
}

func TestQuattroTreatsNoSupplyAsThirteenVolts(t *testing.T) {
	// The test is for the 18 V value alone, so no supply falls into the 13 V
	// branch and clears the bit.
	demod := quattroDemod(newFakeBus())
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if demod.RFInput() != 2 {
		t.Fatalf("18 V chose input %d, want 2", demod.RFInput())
	}
	if err := demod.SetVoltage(true, true); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if got := demod.RFInput(); got != 0 {
		t.Fatalf("no supply chose input %d, want the 13 V input 0", got)
	}
}

func TestQuattroWritesNoRegisters(t *testing.T) {
	// Nothing is switched in Quattro; choosing an input is bookkeeping.
	bus := newFakeBus()
	demod := quattroDemod(bus)
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if len(bus.writes) != 0 {
		t.Fatalf("Quattro wrote %d frames to the chip, want none: %v", len(bus.writes), bus.writes)
	}
}

func TestQuattroMovesTheCrossbarOnTheNextTune(t *testing.T) {
	// Re-pointing the crossbar on every tune is what makes a band request take
	// effect.
	bus := newFakeBus()
	demod := quattroDemod(bus)
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if err := demod.setRFMuxPath(); err != nil {
		t.Fatalf("setRFMuxPath: %v", err)
	}
	// RFMUX4 belongs to demodulator 5, and should now hold input 3.
	got, ok := bus.lastWriteTo(rfmuxBase + 4)
	if !ok || got&fldRFMUXDEMODSEL != 3 {
		t.Fatalf("RFMUX4 became %#02x (present %v), want the low two bits at 3", got, ok)
	}
}

func TestQuattroMovesThePerInputBlocks(t *testing.T) {
	// Both blocks are indexed by RF input, so both follow the input a band
	// request composed, not the configured one.
	bus := newFakeBus()
	demod := quattroDemod(bus)
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	bus.registers[agcrfBase[3]+regAGCRFIN1] = 0x5a
	bus.registers[agcrfBase[3]+regAGCRFIN1+1] = 0xa5
	gain, err := demod.GainIntegrator()
	if err != nil {
		t.Fatalf("GainIntegrator: %v", err)
	}
	if gain != 0x5aa5 {
		t.Fatalf("the gain reading came from the wrong input: %#04x, want 0x5aa5", gain)
	}
	// SendDiSEqC has no Quattro early return, so this really does send, on the
	// same input.
	bus.registers[diseqcBase[3]+regDISTXSTATUS] = fldTXIDLE
	if err := demod.SendDiSEqC([]byte{0xe0, 0x10, 0x38, 0xf0}); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	if _, ok := bus.lastWriteTo(diseqcBase[3] + regDISTXFIFO); !ok {
		t.Fatal("the message did not reach the fourth transmitter")
	}
	if _, ok := bus.lastWriteTo(diseqcBase[0] + regDISTXFIFO); ok {
		t.Fatal("the message reached the first transmitter")
	}
}

func TestDirectSetVoltageGoesToTheCard(t *testing.T) {
	setter := &recordingVoltage{}
	demod := newTestDemodIn(newFakeBus(), Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Demod: 2, RFInput: 1, Voltage: setter})
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if setter.calls != 1 || setter.input != 1 || !setter.high || setter.off {
		t.Fatalf("the card was asked for input %d high %v off %v after %d calls,"+
			" want input 1 high true off false after 1",
			setter.input, setter.high, setter.off, setter.calls)
	}
	// The input does not move in Direct mode.
	if got := demod.RFInput(); got != 1 {
		t.Fatalf("Direct mode moved the input to %d", got)
	}
	setter.fail = errors.New("the regulator is not answering")
	if err := demod.SetVoltage(false, false); err == nil {
		t.Fatal("a failing card switch was reported as a success")
	}
}

func TestDirectSetVoltageRefusesWithNoCardSwitch(t *testing.T) {
	// The 6903X and the 6912 have no supply switch of their own -- the
	// regulator belongs to a separate SEC frontend -- so an error beats
	// reporting a polarisation change that did not happen.
	demod := newTestDemodIn(newFakeBus(), Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Demod: 2, RFInput: 1})
	if err := demod.SetVoltage(true, false); err == nil {
		t.Fatal("a demodulator with no way to reach the supply reported success")
	}
}

func TestDirectSuppressed22kHzWritesNothing(t *testing.T) {
	// Exactly one configuration suppresses the tone: the second STiD135 of the
	// 6916.
	bus := newFakeBus()
	demod := newTestDemodIn(bus, Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Suppress22kHz: true})
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if len(bus.writes) != 0 {
		t.Fatalf("a suppressed tone wrote %d frames, want none", len(bus.writes))
	}
	// DiSEqC is unaffected: only the tone is suppressed.
	bus.registers[demod.diseqcReg(regDISTXSTATUS)] = fldTXIDLE
	if err := demod.SendDiSEqC([]byte{0xe0}); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	if _, ok := bus.lastWriteTo(demod.diseqcReg(regDISTXFIFO)); !ok {
		t.Fatal("a suppressed tone also stopped DiSEqC")
	}
}

func TestSetVoltageRefusesAnUninitializedChip(t *testing.T) {
	demod := New(newFakeBus(), Config{DemodAddress: testAddress, AlreadyBooted: true})
	if err := demod.SetVoltage(true, false); err != ErrNotInitialized {
		t.Errorf("SetVoltage answered %v, want ErrNotInitialized", err)
	}
}
