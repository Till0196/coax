// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import (
	"testing"
)

func TestTuneRefusesWhatItCannotDo(t *testing.T) {
	cold := New(newFakeBus(), Config{DemodAddress: testAddress, AlreadyBooted: true})
	if err := cold.Tune(Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_245_000, SymbolRate: 22_000_000}); err != ErrNotInitialized {
		t.Fatalf("an uninitialized chip answered %v, want ErrNotInitialized", err)
	}
	demod := newTestDemod(newFakeBus())
	cases := []struct {
		name   string
		params Params
	}{
		{"a terrestrial delivery system", Params{DeliverySystem: "DVBT", FrequencyKHz: 1_245_000, SymbolRate: 22_000_000}},
		{"a symbol rate below the chip's floor", Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_245_000, SymbolRate: 1000}},
		{"a symbol rate above the chip's ceiling", Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_245_000, SymbolRate: 600_000_000}},
		{"a frequency below the band", Params{DeliverySystem: "DVBS2", FrequencyKHz: 900_000, SymbolRate: 22_000_000}},
		{"a frequency above the band", Params{DeliverySystem: "DVBS2", FrequencyKHz: 2_200_000, SymbolRate: 22_000_000}},
		// Half the master clock is where the unported wideband and sliced
		// modes begin.
		{"a wideband carrier", Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_245_000, SymbolRate: 70_000_000}},
	}
	for _, test := range cases {
		if err := demod.Tune(test.params); err == nil {
			t.Errorf("%s was accepted", test.name)
		}
	}
}

func TestSetRegInitValues(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	// Forced values left behind, which the sequence must clear without
	// disturbing the rest of those registers.
	bus.registers[demod.pktdelinReg(regBBHCTRL2)] = 0xff
	bus.registers[demod.hwareReg(regTSCFG0)] = 0xff
	if err := demod.setRegInitValues(); err != nil {
		t.Fatalf("setRegInitValues: %v", err)
	}
	// The whole-register writes of setRegInitValues.
	cases := []struct {
		name    string
		address uint16
		want    byte
	}{
		{"DEMOD", demod.demodReg(regDEMOD), 0x00},
		{"DMDCFGMD", demod.demodReg(regDMDCFGMD), 0xc8},
		{"CARFREQ", demod.demodReg(regCARFREQ), 0x45},
		{"CFR1BCFG", demod.demodReg(regCFR1BCFG), 0x67},
		{"PLSMSCRAMBB", demod.demodReg(regPLSMSCRAMBB), 0x70},
		{"TSDLYSET2", demod.hwareReg(regTSDLYSET2), 0x31},
		{"TSDLYSET1", demod.hwareReg(regTSDLYSET1), 0x2b},
		{"TSDLYSET0", demod.hwareReg(regTSDLYSET0), 0x00},
		{"CORRELMANT", demod.demodReg(regCORRELMANT), 0x78},
		{"CORRELABS", demod.demodReg(regCORRELABS), 0x70},
		{"CORRELCOEF", demod.demodReg(regCORRELCOEF), 0x20},
		{"CORRELFREQ", demod.demodReg(regCORRELFREQ), 0x70},
		{"CARHDR", demod.demodReg(regCARHDR), 0x08},
		{"DFECFG", demod.demodReg(regDFECFG), 0xc1},
		{"XFECFG2", demod.demodReg(regXFECFG2), 0x03},
		{"CFR2CFR1", demod.demodReg(regCFR2CFR1), 0x25},
	}
	for _, test := range cases {
		if got := bus.registers[test.address]; got != test.want {
			t.Errorf("%s became %#02x, want %#02x", test.name, got, test.want)
		}
	}
	// The field clears, each leaving the rest of its register alone.
	if got := bus.registers[demod.pktdelinReg(regBBHCTRL2)]; got != 0xff&^fldFORCEMATYPEMSB {
		t.Errorf("BBHCTRL2 became %#02x, want the forced matype bit cleared", got)
	}
	if got := bus.registers[demod.hwareReg(regTSCFG0)]; got != 0xff&^fldTSFIFOEMBINDVB {
		t.Errorf("TSCFG0 became %#02x, want the DVB embedding bit cleared", got)
	}
}

func TestSetPLS(t *testing.T) {
	// The stream identifier becomes a scrambling mode and root; a missing one
	// is the broadcast default of mode zero and root one.
	cases := []struct {
		name     string
		streamID int32
		mode     byte
		root     uint32
	}{
		{"no filter at all", noStreamIDFilter, 0, 1},
		{"the broadcast root, spelled as zero", 0, 0, 1},
		{"a gold code", 1<<26 | 0x12345<<8, 1, 0x12345},
		{"a root in mode zero", 0x2af8 << 8, 0, 0x2af8},
	}
	for _, test := range cases {
		bus := newFakeBus()
		demod := newTestDemod(bus)
		if err := demod.setPLS(test.streamID); err != nil {
			t.Fatalf("%s: setPLS: %v", test.name, err)
		}
		high := bus.registers[demod.demodReg(regPLROOT2)]
		mode := (high & fldPLSCRAMBMODE) >> 2
		root := uint32(high&fldPLSCRAMBROOTHigh)<<16 |
			uint32(bus.registers[demod.demodReg(regPLROOT1)])<<8 |
			uint32(bus.registers[demod.demodReg(regPLROOT0)])
		if mode != test.mode || root != test.root {
			t.Errorf("%s gave mode %d root %#x, want mode %d root %#x",
				test.name, mode, root, test.mode, test.root)
		}
	}
}

func TestSetSymbolRateWritesThreeBytesMostSignificantFirst(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	if err := demod.setSymbolRate(22_000_000); err != nil {
		t.Fatalf("setSymbolRate: %v", err)
	}
	const want = 238127
	run := bus.runTo(demod.demodReg(regSFRINIT2))
	if len(run) != 3 {
		t.Fatalf("the run at SFRINIT2 was %v, want three bytes", run)
	}
	got := uint32(run[0])<<16 | uint32(run[1])<<8 | uint32(run[2])
	if got != want {
		t.Fatalf("SFRINIT read back %d, want %d", got, want)
	}
}

func TestSetCarrierFrequencyInit(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	const tunerFrequency = 1_245_000_000 - 1_550_000_000
	if err := demod.setCarrierFrequencyInit(tunerFrequency); err != nil {
		t.Fatalf("setCarrierFrequencyInit: %v", err)
	}
	if got := bus.registers[demod.demodReg(regCARCFG)]; got != 0x46 {
		t.Fatalf("CARCFG became %#02x, want 0x46", got)
	}
	// The three registers hold the low 24 bits of the signed value, most
	// significant byte first.
	cases := []struct {
		name  string
		first uint16
		want  int32
	}{
		{"CFRUP", demod.demodReg(regCFRUP2), -3_246_080},
		{"CFRLOW", demod.demodReg(regCFRLOW2), -3_354_624},
		{"CFRINIT", demod.demodReg(regCFRINIT2), -3_301_342},
	}
	for _, test := range cases {
		run := bus.runTo(test.first)
		if len(run) != 3 {
			t.Fatalf("the run at %s was %v, want three bytes", test.name, run)
		}
		got := int32(run[0])<<16 | int32(run[1])<<8 | int32(run[2])
		if got != test.want&0xffffff {
			t.Errorf("%s read back %#06x, want %#06x", test.name, got, test.want&0xffffff)
		}
	}
}

func TestSetSearchStandard(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	if err := demod.setSearchStandard("DVBS2"); err != nil {
		t.Fatalf("setSearchStandard: %v", err)
	}
	config := bus.registers[demod.demodReg(regDMDCFGMD)]
	if config&fldDVBS1ENABLE != 0 || config&fldDVBS2ENABLE == 0 {
		t.Fatalf("DMDCFGMD became %#02x, want DVB-S2 alone", config)
	}
	bus = newFakeBus()
	demod = newTestDemod(bus)
	if err := demod.setSearchStandard("DVBS"); err != nil {
		t.Fatalf("setSearchStandard: %v", err)
	}
	config = bus.registers[demod.demodReg(regDMDCFGMD)]
	if config&fldDVBS1ENABLE == 0 || config&fldDVBS2ENABLE != 0 {
		t.Fatalf("DMDCFGMD became %#02x, want DVB-S alone", config)
	}
	// DSS off, and every puncture rate but 6/7 allowed, which is what an
	// unknown rate asks for.
	if got := bus.registers[demod.viterbiReg(regFECM)]; got != 0x00 {
		t.Errorf("FECM became %#02x, want 0x00", got)
	}
	if got := bus.registers[demod.viterbiReg(regPRVIT)]; got != 0x2f {
		t.Errorf("PRVIT became %#02x, want 0x2f", got)
	}
	if got := bus.registers[demod.hwareReg(regSFDLYSET2)]; got != 0x00 {
		t.Errorf("SFDLYSET2 became %#02x, want 0x00", got)
	}
}

func TestSetMISFiltering(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	if err := demod.setMISFiltering(0x2a); err != nil {
		t.Fatalf("setMISFiltering: %v", err)
	}
	if got := bus.registers[demod.pktdelinReg(regISIENTRY)]; got != 0x2a {
		t.Errorf("ISIENTRY became %#02x, want 0x2a", got)
	}
	if got := bus.registers[demod.pktdelinReg(regISIBITENA)]; got != 0xff {
		t.Errorf("ISIBITENA became %#02x, want the whole byte compared", got)
	}
	// The delineator reset is pulsed afterwards, so it ends released.
	final := bus.registers[demod.pktdelinReg(regPDELCTRL1)]
	if final&fldFILTEREN == 0 {
		t.Errorf("PDELCTRL1 became %#02x, want the filter enabled", final)
	}
	if final&fldALGOSWRST != 0 {
		t.Errorf("PDELCTRL1 became %#02x, want the delineator reset released", final)
	}

	bus = newFakeBus()
	demod = newTestDemod(bus)
	if err := demod.setMISFiltering(noStreamIDFilter); err != nil {
		t.Fatalf("setMISFiltering: %v", err)
	}
	if got := bus.registers[demod.pktdelinReg(regPDELCTRL1)]; got&fldFILTEREN != 0 {
		t.Errorf("PDELCTRL1 became %#02x, want the filter disabled", got)
	}
}

func TestGetModeCode(t *testing.T) {
	// DMDMODCOD is unpacked differently depending on whether the header's top
	// bit marks it as a DVB-S2X code.
	cases := []struct {
		name       string
		standard   trackingStandard
		raw        byte
		wantCode   modcode
		wantPilots bool
	}{
		// Legacy DVB-S2: five bits from bit 2, pilots in bit 0.
		{"QPSK 3/4 without pilots", standardDVBS2, 0x1c, 0x07, false},
		{"QPSK 3/4 with pilots", standardDVBS2, 0x1d, 0x07, true},
		// DVB-S2X: seven bits from bit 1.
		{"8APSK 5/9 linear", standardDVBS2, 0x8b, 0x45, true},
		// The pilots-off aggregate codes force the pilot flag on, and the
		// modcode itself stays at the pilots-off value.
		{"the 8PSK aggregate", standardDVBS2, 0xfa, 0x7d, true},
		// DVB-S: six bits from bit 2.
		{"DVB-S 3/4", standardDVBS1, 0x22 << 2, 0x22, false},
		{"a DVB-S code outside the range", standardDVBS1, 0x3f << 2, 0, false},
	}
	for _, test := range cases {
		bus := newFakeBus()
		demod := newTestDemod(bus)
		bus.registers[demod.demodReg(regDMDMODCOD)] = test.raw
		code, pilots, err := demod.getModeCode(test.standard)
		if err != nil {
			t.Fatalf("%s: getModeCode: %v", test.name, err)
		}
		if code != test.wantCode || pilots != test.wantPilots {
			t.Errorf("%s gave modcode %#02x pilots %v, want %#02x %v",
				test.name, code, pilots, test.wantCode, test.wantPilots)
		}
	}
}

func TestSetCarrierLoop(t *testing.T) {
	// A QPSK carrier writes its coefficient into the QPSK register; anything
	// denser parks the QPSK register and writes its own.
	cases := []struct {
		name     string
		code     modcode
		register uint16
		want     byte
	}{
		{"QPSK 1/2", 0x04, regACLC2S2Q, 0x27},
		{"8PSK 3/5", 0x0c, regACLC2S28, 0x07},
		{"16APSK 2/3", 0x12, regACLC2S216A, 0x28},
		{"32APSK 4/5", 0x19, regACLC2S232A, 0x09},
		{"64APSK 11/15", 0x5d, regACLC2S264A, 0x09},
		{"128APSK 3/4", 0x64, regACLC2S2128A, 0x09},
		{"256APSK 3/4", 0x6b, regACLC2S2256A, 0x09},
	}
	for _, test := range cases {
		bus := newFakeBus()
		demod := newTestDemod(bus)
		if err := demod.setCarrierLoop(test.code); err != nil {
			t.Fatalf("%s: setCarrierLoop: %v", test.name, err)
		}
		if got := bus.registers[demod.demodReg(test.register)]; got != test.want {
			t.Errorf("%s wrote %#02x to its coefficient register, want %#02x",
				test.name, got, test.want)
		}
		if test.register != regACLC2S2Q {
			if got := bus.registers[demod.demodReg(regACLC2S2Q)]; got != carrierLoopQPSKHold {
				t.Errorf("%s left the QPSK register at %#02x, want %#02x",
					test.name, got, carrierLoopQPSKHold)
			}
		}
	}
}
