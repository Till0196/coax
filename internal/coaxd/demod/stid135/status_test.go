// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import "testing"

// lockDVBS2 puts the register file into the state a chip locked to a DVB-S2
// carrier holds: the definitive lock flag, the search state at DVB-S2, and the
// flywheel counter at fifteen, which getStandard needs before it believes the
// state.
func lockDVBS2(bus *fakeBus, demod *Demod) {
	bus.registers[demod.demodReg(regDSTATUS)] = fldLOCKDEFINITIF
	bus.registers[demod.demodReg(regDMDSTATE)] = stateDVBS2Found << 5
	bus.registers[demod.demodReg(regDMDFLYW)] = 0x0f
}

func TestStatusWithoutLock(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Locked || status.HasCNR {
		t.Fatalf("an unlocked demodulator reported %+v", status)
	}
}

func TestStatusReadsTheDVBS2NoiseIndicator(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	lockDVBS2(bus, demod)
	// 2484 is the table entry for 14.8 dB.
	bus.registers[demod.demodReg(regNNOSPLHT1)] = byte(2484 >> 8)
	bus.registers[demod.demodReg(regNNOSPLHT1)+1] = byte(2484 & 0xff)
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked {
		t.Fatal("a locked demodulator was reported unlocked")
	}
	if !status.HasCNR || status.CNRdB != 14.8 {
		t.Fatalf("carrier-to-noise %v (present %v), want 14.8", status.CNRdB, status.HasCNR)
	}
}

func TestStatusReadsTheDVBSNoiseIndicator(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[demod.demodReg(regDSTATUS)] = fldLOCKDEFINITIF
	bus.registers[demod.demodReg(regDMDSTATE)] = stateDVBSFound << 5
	// 1000 is the table entry for 22.8 dB in the DVB-S table.
	bus.registers[demod.demodReg(regNNOSDATAT1)] = byte(1000 >> 8)
	bus.registers[demod.demodReg(regNNOSDATAT1)+1] = byte(1000 & 0xff)
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.HasCNR || status.CNRdB != 22.8 {
		t.Fatalf("carrier-to-noise %v (present %v), want 22.8", status.CNRdB, status.HasCNR)
	}
}

func TestStatusWithdrawsAnUnreadableCarrierToNoise(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	lockDVBS2(bus, demod)
	// Noisier than the table's first entry, which is left at its initial value
	// rather than extrapolated.
	bus.registers[demod.demodReg(regNNOSPLHT1)] = 0xff
	bus.registers[demod.demodReg(regNNOSPLHT1)+1] = 0xff
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked {
		t.Fatal("a locked demodulator was reported unlocked")
	}
	if status.HasCNR {
		t.Fatalf("an off-scale reading was reported as %v dB", status.CNRdB)
	}
}

func TestGainIntegratorReadsTheGainControl(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[demod.agcrfReg(regAGCRFIN1)] = 0x12
	bus.registers[demod.agcrfReg(regAGCRFIN1)+1] = 0x34
	strength, err := demod.GainIntegrator()
	if err != nil {
		t.Fatalf("GainIntegrator: %v", err)
	}
	if strength != 0x1234 {
		t.Fatalf("gain integrator %#04x, want 0x1234", strength)
	}
}

func TestGainIntegratorFollowsTheRFInputNotTheDemodulator(t *testing.T) {
	// Four gain controls behind eight demodulators, so two demodulators
	// sharing an input share a reading.
	bus := newFakeBus()
	demod := newTestDemodIn(bus, Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Demod: 6, RFInput: 3})
	bus.registers[agcrfBase[3]+regAGCRFIN1] = 0xab
	bus.registers[agcrfBase[3]+regAGCRFIN1+1] = 0xcd
	strength, err := demod.GainIntegrator()
	if err != nil {
		t.Fatalf("GainIntegrator: %v", err)
	}
	if strength != 0xabcd {
		t.Fatalf("gain integrator %#04x, want 0xabcd", strength)
	}
}
