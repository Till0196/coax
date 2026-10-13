// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices GmbH; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stv091x

import (
	"errors"
	"testing"
)

const testAddress = 0x68

// fakeBus is a sixteen-bit-addressed register map with a write log. Reads of
// registers never seeded return zero.
type fakeBus struct {
	registers map[uint16]byte
	writes    [][3]byte
}

func newFakeBus() *fakeBus { return &fakeBus{registers: map[uint16]byte{}} }

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testAddress || len(data) != 3 {
		return errors.New("unexpected transfer")
	}
	register := uint16(data[0])<<8 | uint16(data[1])
	bus.registers[register] = data[2]
	bus.writes = append(bus.writes, [3]byte{data[0], data[1], data[2]})
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if address != testAddress || len(write) != 2 {
		return nil, errors.New("unexpected transfer")
	}
	register := uint16(write[0])<<8 | uint16(write[1])
	out := make([]byte, readLength)
	for offset := range out {
		out[offset] = bus.registers[register+uint16(offset)]
	}
	return out, nil
}

// newTestDemod fakes the state Initialize would leave -- warm, with the master
// clock a 30 MHz crystal gives -- without running it, so the write log holds
// only the test's own writes.
func newTestDemod(bus *fakeBus, index int) *Demod {
	demod := New(bus, Config{
		DemodAddress:  testAddress,
		Index:         index,
		TSMode:        TSModeParallel,
		RepeaterLevel: 3,
		Repeater:      RepeaterPath1,
	})
	demod.warm = true
	demod.mclkHz = masterClockHz
	return demod
}

func TestInitializeRefusesAChipThatIsNotOne(t *testing.T) {
	bus := newFakeBus()
	bus.registers[regMID] = 0x60
	if err := New(bus, Config{DemodAddress: testAddress}).Initialize(); err == nil {
		t.Fatal("a chip answering the wrong identity was accepted")
	}
}

// TestInitializeProgramsTheMasterClockAndTheToneDivider works set_mclock and
// init_diseqc by hand for a 30 MHz crystal. 135 MHz is asked for with the input
// divider fixed at one and the output divider at four, so the feedback divider
// is 135*4*1/30 = 18, below the 72 where the charge pump starts stepping:
// NCOARSE is (7<<3)|1 = 0x39. The VCO then runs at 1080 MHz and the master
// clock at 135 MHz; the tone divider is 135352000/704000 = 192.
func TestInitializeProgramsTheMasterClockAndTheToneDivider(t *testing.T) {
	bus := newFakeBus()
	bus.registers[regMID] = identity
	demod := New(bus, Config{DemodAddress: testAddress, TSMode: TSModeParallel, RepeaterLevel: 3})
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	for register, want := range map[uint16]byte{
		regNCOARSE:  0x39,
		regNCOARSE2: 0x04,
		regNCOARSE1: 18,
		// Both paths' DiSEqC transmitters get the divider.
		regDISTXF22:                192,
		regDISTXF22 + diseqcOffset: 192,
	} {
		if got := bus.registers[register]; got != want {
			t.Errorf("register %#04x = %#02x, want %#02x", register, got, want)
		}
	}
	if got := demod.MasterClockHz(); got != 135_000_000 {
		t.Errorf("master clock = %d Hz, want 135000000", got)
	}
	// The board's parallel wiring settles both configuration bytes worked out at
	// construction: no serial bit in TSCFGH, and no permutation.
	if got := bus.registers[regTSGENERAL]; got != 0x00 {
		t.Errorf("TSGENERAL = %#02x, want 0x00 for parallel output", got)
	}
	if demod.tscfgh != 0x20 {
		t.Errorf("tscfgh = %#02x, want 0x20 for parallel output", demod.tscfgh)
	}
}

// TestPathAndDiSEqCOffsetsSelectTheRightHalf pins the two address layouts: the
// register constants are named after the second path, so the first carries the
// 0x200 offset, while the DiSEqC constants are named after the first path and
// the second carries their 0x40.
func TestPathAndDiSEqCOffsetsSelectTheRightHalf(t *testing.T) {
	first := newTestDemod(newFakeBus(), 0)
	second := newTestDemod(newFakeBus(), 1)

	if got := first.path(regDMDSTATE); got != 0xf41b {
		t.Errorf("demodulator 0's DMDSTATE is %#04x, want 0xf41b", got)
	}
	if got := second.path(regDMDSTATE); got != 0xf21b {
		t.Errorf("demodulator 1's DMDSTATE is %#04x, want 0xf21b", got)
	}
	if got := first.diseqc(regDISTXCFG); got != 0xf702 {
		t.Errorf("demodulator 0's DISTXCFG is %#04x, want 0xf702", got)
	}
	if got := second.diseqc(regDISTXCFG); got != 0xf742 {
		t.Errorf("demodulator 1's DISTXCFG is %#04x, want 0xf742", got)
	}
}

// TestOpenTunerGateFollowsTheBoardsRepeaterPath covers the cards here: one
// tuner serves both demodulators off the first path's repeater, so even the
// second demodulator opens that one.
func TestOpenTunerGateFollowsTheBoardsRepeaterPath(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, 1)
	if err := demod.OpenTunerGate(true); err != nil {
		t.Fatalf("OpenTunerGate: %v", err)
	}
	// 0x0a with the repeater level 3 in bits 4..6 is 0x3a; clearing 0x86 and
	// setting the enable leaves 0xb8.
	if got := bus.registers[regP1I2CRPT]; got != 0xb8 {
		t.Errorf("the first path's repeater is %#02x, want 0xb8", got)
	}
	if _, written := bus.registers[regP2I2CRPT]; written {
		t.Error("the second path's repeater was written, but the tuner is not on it")
	}
	if err := demod.OpenTunerGate(false); err != nil {
		t.Fatalf("OpenTunerGate: %v", err)
	}
	if got := bus.registers[regP1I2CRPT]; got != 0x3a {
		t.Errorf("the first path's repeater is %#02x after closing, want 0x3a", got)
	}
}

// TestSearchWindowMatchesTheReferenceArithmetic pins Start's carrier sweep: a
// fixed 16 MHz search range plus a margin, in units of the master clock over
// 65536. A wide carrier sweeps 8600 kHz, which is 4174 after truncation; at or
// below 5 MSym/s 680 kHz comes off, leaving 7920 kHz and 3844.
func TestSearchWindowMatchesTheReferenceArithmetic(t *testing.T) {
	for _, test := range []struct {
		symbolRate uint32
		want       int32
	}{
		{22_000_000, 4174},
		{5_000_000, 3844},
	} {
		upper, lower := searchWindow(masterClockHz, test.symbolRate)
		if upper != test.want || lower != -test.want {
			t.Errorf("searchWindow at %d symbols per second = %d/%d, want %d/%d",
				test.symbolRate, upper, lower, test.want, -test.want)
		}
	}
}

// TestStartProgramsTheInitialSymbolRate pins Start's SFRINIT pair: the symbol
// rate as a fraction of the master clock in units of 1/65536, so
// 22000000*65536/135000000 = 10679 = 0x29b7 after truncation, of which only the
// low fifteen bits reach the chip.
func TestStartProgramsTheInitialSymbolRate(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, 1)
	if err := demod.start(Params{
		DeliverySystem: "DVBS2",
		FrequencyKHz:   1_550_000,
		SymbolRate:     22_000_000,
		StreamID:       -1,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := bus.registers[regSFRINIT1]; got != 0x29 {
		t.Errorf("SFRINIT1 = %#02x, want 0x29", got)
	}
	if got := bus.registers[regSFRINIT1+1]; got != 0xb7 {
		t.Errorf("SFRINIT0 = %#02x, want 0xb7", got)
	}
	// The last thing Start does is trigger the acquisition.
	if got := bus.registers[regDMDISTATE]; got != 0x15 {
		t.Errorf("DMDISTATE = %#02x, want the acquisition trigger 0x15", got)
	}
	// With no stream filter asked for, the scrambling sequence falls back to one
	// rather than staying at zero, and the filter bit stays clear.
	if got := bus.registers[regPLROOT2+2]; got != 0x01 {
		t.Errorf("PLROOT0 = %#02x, want the default sequence 0x01", got)
	}
	if got := bus.registers[regPDELCTRL1]; got&0x20 != 0 {
		t.Errorf("PDELCTRL1 = %#02x, want the stream filter left off", got)
	}
}

// TestSetModcodeEnablesEveryCombination works SetModcode's bit walk by hand
// with every modcod asked for. Four bits per modcod, two to a register, running
// down from MODCODLSTF; odd steps clear the high nibble and even ones the low.
// Three exceptions: MODCODLSTF is reached only by the first step and keeps its
// low nibble; the three 9/10 rates below 32APSK clear only two bits; and
// MODCODLST1 is masked to its low two bits.
func TestSetModcodeEnablesEveryCombination(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, 1)
	for register := uint16(regMODCODLST1); register <= regMODCODLSTF; register++ {
		bus.registers[register] = 0xff
	}
	if err := demod.setModcode(modcodeAll); err != nil {
		t.Fatalf("setModcode: %v", err)
	}
	for register, want := range map[uint16]byte{
		regMODCODLSTF:     0x0f,
		regMODCODLSTF - 1: 0x00,
		regMODCODLSTF - 5: 0xc0, // QPSK 9/10 shares this register
		regMODCODLSTF - 8: 0xc0, // 8PSK 9/10
		regMODCODLST1:     0x00,
	} {
		if got := bus.registers[register]; got != want {
			t.Errorf("register %#04x = %#02x, want %#02x", register, got, want)
		}
	}
}

// TestOptimalCarrierLoopIndexesTheVendorTable pins get_optim_cloop's three
// terms at a point countable off the table by hand: QPSK 3/4 is the seventh
// modcod, so its row starts at 60; 22 MSym/s adds six; pilots leave the index
// there. Entry 66 is the seventh of it.
func TestOptimalCarrierLoopIndexesTheVendorTable(t *testing.T) {
	if got := optimalCarrierLoop(7, true, 22_000_000); got != 0x1b {
		t.Errorf("QPSK 3/4 with pilots at 22 MSym/s = %#02x, want 0x1b", got)
	}
	if got := optimalCarrierLoop(7, false, 22_000_000); got != 0x3b {
		t.Errorf("QPSK 3/4 without pilots at 22 MSym/s = %#02x, want 0x3b", got)
	}
	// Anything at or above the last modcod is pinned to the last row.
	if got := optimalCarrierLoop(31, true, 22_000_000); got != 0x09 {
		t.Errorf("a modcod past the end of the table = %#02x, want 0x09", got)
	}
}

// TestStatusReportsLockAndCarrierToNoise drives the whole of read_status over a
// chip reporting a locked DVB-S2 QPSK 3/4 transponder with pilots. The noise
// estimate is seeded at 0x1149 = 4425, exactly the DVB-S2 table's 10.0 dB
// entry, so the interpolation has to land on it.
func TestStatusReportsLockAndCarrierToNoise(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, 1)
	demod.symbolRate = 22_000_000
	// The search has finished, and bit 5 being clear says it found DVB-S2. The
	// 0x80 in DSTATUS is the carrier lock, which Status deliberately ignores.
	bus.registers[regDMDSTATE] = demodSearchDone
	bus.registers[regDSTATUS] = statusLock | 0x80
	// Modcod 7 in bits 2..6, pilots set in bit 0, bit 1 clear for long frames.
	bus.registers[regDMDMODCOD] = 7<<2 | 0x01
	bus.registers[regPDELSTATUS] = packetDelineatorLock
	bus.registers[regNNOSPLHT1] = 0x11
	bus.registers[regNNOSPLHT1+1] = 0x49

	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked || !status.HasCNR {
		t.Fatalf("Status reported %+v, want a lock with a reading", status)
	}
	if status.CNRdB != 10 {
		t.Errorf("CNR = %v dB, want 10", status.CNRdB)
	}
	if got := bus.registers[regACLC2S2Q]; got != 0x1b {
		t.Errorf("the QPSK carrier loop is %#02x, want 0x1b", got)
	}
	// The demodulator must be pinned to DVB-S2, not left searching for both.
	if got := bus.registers[regDMDCFGMD]; got&0xc0 != 0x80 {
		t.Errorf("DMDCFGMD = %#02x, want the DVB-S2 mode bits 0x80", got)
	}
	// And the transport stream output must have been let out and resynced.
	if got := bus.registers[regTSCFGH]; got != demod.tscfgh {
		t.Errorf("TSCFGH = %#02x, want %#02x", got, demod.tscfgh)
	}
}

// TestStatusStaysUnlockedWithoutForwardErrorCorrection: a standard was
// recognized but the packet delineator has not locked, so there is no usable
// stream and no meaningful noise estimate.
func TestStatusStaysUnlockedWithoutForwardErrorCorrection(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, 1)
	bus.registers[regDMDSTATE] = demodSearchDone
	bus.registers[regDSTATUS] = statusLock
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Locked || status.HasCNR {
		t.Errorf("Status reported %+v, want no lock", status)
	}
}

// TestSymbolRateAppliesTheTimingOffset pins GetCurSymbolRate: a coarse rate as
// a fraction of the master clock, plus a signed 24-bit correction scaled by
// 2^29. A raw reading of 0x80000000 gives 67500000; an offset of 0x400000
// corrects by 527343 after truncation. The last case is the same offset with
// the sign bit set, which takes the rate down by 1054688 rather than 1054687.5,
// an arithmetic shift of a negative number flooring.
func TestSymbolRateAppliesTheTimingOffset(t *testing.T) {
	for _, test := range []struct {
		offset [3]byte
		want   uint32
	}{
		{[3]byte{0x00, 0x00, 0x00}, 67_500_000},
		{[3]byte{0x40, 0x00, 0x00}, 68_027_343},
		{[3]byte{0x80, 0x00, 0x00}, 66_445_312},
	} {
		bus := newFakeBus()
		demod := newTestDemod(bus, 1)
		demod.started = true
		bus.registers[regSFR3] = 0x80
		for offset, value := range test.offset {
			bus.registers[regTMGREG2+uint16(offset)] = value
		}
		got, err := demod.SymbolRate()
		if err != nil {
			t.Fatalf("SymbolRate: %v", err)
		}
		if got != test.want {
			t.Errorf("offset %v gave %d symbols per second, want %d", test.offset, got, test.want)
		}
	}
}

// fakeTuner is a Tuner that also implements gainCorrector, reporting a fixed
// gain in hundredths of a dB.
type fakeTuner struct{ gain int32 }

func (tuner *fakeTuner) Tune(frequencyHz, symbolRateSPS uint32) error { return nil }

func (tuner *fakeTuner) RFGainCentiDB(agc uint16, frequencyKHz uint32) int32 { return tuner.gain }

// TestSignalStrengthSubtractsTheTunersGain works read_status's strength portion
// by hand. Each of the five samples reads an I of 100 and a Q of zero, so the
// power is 10000. That falls between padcLookup's -9 dBm point at 14850 and its
// -11 dBm point at 9380 and interpolates to -1077 hundredths of a dBm; the
// fixed 352 correction brings it to -725, and the tuner's 6629 hundredths of
// gain comes off, leaving -73.54 dBm.
func TestSignalStrengthSubtractsTheTunersGain(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, 1)
	demod.AttachTuner(&fakeTuner{gain: 6629})
	bus.registers[regPOWERI] = 100
	bus.registers[regPOWERI+1] = 0

	strength, err := demod.SignalStrength(1_550_000)
	if err != nil {
		t.Fatalf("SignalStrength: %v", err)
	}
	if strength != -73.54 {
		t.Errorf("SignalStrength = %v dBm, want -73.54", strength)
	}
}

// TestTuneRefusesWhatTheChipCannotSearchFor guards the two things Tune checks
// before it touches the chip.
func TestTuneRefusesWhatTheChipCannotSearchFor(t *testing.T) {
	demod := newTestDemod(newFakeBus(), 1)
	if err := demod.Tune(Params{DeliverySystem: "DVBT", SymbolRate: 22_000_000}); err == nil {
		t.Error("a terrestrial channel was accepted")
	}
	if err := demod.Tune(Params{DeliverySystem: "DVBS2", SymbolRate: 99_000_000}); err == nil {
		t.Error("a symbol rate above the chip's range was accepted")
	}
}

// writtenRegisters is the set of addresses the write log reached.
func (bus *fakeBus) writtenRegisters() map[uint16]bool {
	touched := map[uint16]bool{}
	for _, write := range bus.writes {
		touched[uint16(write[0])<<8|uint16(write[1])] = true
	}
	return touched
}

// bootedBus is a chip another driver has already brought up: the identity, the
// PLL dividers a 30 MHz crystal gives, this half's transport stream
// configuration, and the repeater the board's tuner hangs off.
func bootedBus() *fakeBus {
	bus := newFakeBus()
	bus.registers[regMID] = identity
	bus.registers[regNCOARSE] = 0x39
	bus.registers[regNCOARSE1] = 18
	bus.registers[regNCOARSE2] = 0x04
	bus.registers[regTSCFGH] = 0x20
	bus.registers[regP1I2CRPT] = 0x3a
	return bus
}

func newWarmDemod(bus *fakeBus, index int) *Demod {
	return New(bus, Config{
		DemodAddress:  testAddress,
		Index:         index,
		TSMode:        TSModeParallel,
		RepeaterLevel: 3,
		Repeater:      RepeaterPath1,
		AlreadyBooted: true,
	})
}

// TestResumeTouchesNothingOutsideThisHalf: a chip another driver may be
// streaming from on its other demodulator must come out of Initialize with only
// this demodulator's registers written. The allowed set is probe()'s per-path
// share for the second path plus that path's share of init_diseqc; everything
// chip-wide must be absent.
func TestResumeTouchesNothingOutsideThisHalf(t *testing.T) {
	bus := bootedBus()
	demod := newWarmDemod(bus, 1)
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	allowed := map[uint16]bool{}
	for _, step := range demod.perPathInit(demod.tscfgh) {
		allowed[step.register] = true
	}
	for _, register := range []uint16{
		regDISRXCFG + diseqcOffset,
		regDISTXCFG + diseqcOffset,
		regDISTXF22 + diseqcOffset,
	} {
		allowed[register] = true
	}
	for register := range bus.writtenRegisters() {
		if !allowed[register] {
			t.Errorf("the warm path wrote %#04x, which is not this half's", register)
		}
	}

	// Named explicitly as well, in case a register stops being written at all.
	for _, register := range []uint16{
		regMID, regI2CCFG, regOUTCFG, regPADCFG, regSYNTCTRL, regTSGENERAL,
		regCFGEXT, regGENCFG, regNCOARSE, regNCOARSE1, regNCOARSE2, regTSTRES0,
		regP1I2CRPT, regP2I2CRPT,
		regTNRCFG2 + pathOffset, regTSCFGH + pathOffset, regTSCFGL + pathOffset,
		regDISTXCFG, regDISTXF22, regDISRXCFG,
	} {
		if bus.writtenRegisters()[register] {
			t.Errorf("the warm path wrote the shared or sibling register %#04x", register)
		}
	}
	// An empty write log would pass both loops above and prove nothing.
	if !bus.writtenRegisters()[demod.path(regTSCFGH)] {
		t.Error("the warm path wrote nothing at all")
	}
}

// TestResumeReadsTheMasterClockOffTheChip pins the arithmetic the warm path
// runs backwards, because Start's symbol rate and carrier window are scaled
// against a clock a warm start never sets. The dividers a cold bring-up leaves
// are 1, 18 and 4, giving a VCO of 1080 MHz and a master clock of 135 MHz --
// the same number the cold test pins.
func TestResumeReadsTheMasterClockOffTheChip(t *testing.T) {
	bus := bootedBus()
	demod := newWarmDemod(bus, 1)
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := demod.MasterClockHz(); got != 135_000_000 {
		t.Errorf("the read-back master clock is %d Hz, want 135000000", got)
	}
	// And the tone divider computed from it must match the cold path's 192.
	if got := bus.registers[regDISTXF22+diseqcOffset]; got != 192 {
		t.Errorf("the tone divider is %d, want 192", got)
	}

	// Halving the feedback divider to 9 gives a VCO of 540 MHz and a master clock
	// of 67.5 MHz -- but the answer is 67 MHz, because the arithmetic is carried
	// in whole megahertz. Pinning the truncation is the point: floating point
	// would disagree with the scaling.
	half := bootedBus()
	half.registers[regNCOARSE1] = 9
	demod = newWarmDemod(half, 1)
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := demod.MasterClockHz(); got != 67_000_000 {
		t.Errorf("a feedback divider of 9 gave %d Hz, want 67000000", got)
	}
}

// TestResumeRefusesAChipWhosePLLIsNotRunning: a zero divider is a PLL nobody
// configured, and configuring it is a chip-wide write that would take the other
// demodulator down, so the warm path errors rather than guess, writing nothing.
func TestResumeRefusesAChipWhosePLLIsNotRunning(t *testing.T) {
	for _, register := range []uint16{regNCOARSE, regNCOARSE1, regNCOARSE2} {
		bus := bootedBus()
		bus.registers[register] = 0
		if err := newWarmDemod(bus, 1).Initialize(); err == nil {
			t.Errorf("a chip with %#04x reading zero was resumed", register)
		}
		if len(bus.writes) != 0 {
			t.Errorf("a chip with %#04x reading zero was written to %d times",
				register, len(bus.writes))
		}
	}
}

// TestResumeTakesTheTransportStreamAndRepeaterFromTheChip pins the two settings
// the warm path reads back rather than imposes. The merger's reset bit is
// pulsed, not cached, so a chip found with it set must not carry it into the
// cache.
func TestResumeTakesTheTransportStreamAndRepeaterFromTheChip(t *testing.T) {
	bus := bootedBus()
	// Serial output, with the merger's reset bit happening to be set.
	bus.registers[regTSCFGH] = 0x61
	bus.registers[regP1I2CRPT] = 0x5a
	demod := newWarmDemod(bus, 1)
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if demod.tscfgh != 0x60 {
		t.Errorf("the cached transport stream configuration is %#02x, want 0x60", demod.tscfgh)
	}
	if got := bus.registers[regTSCFGH]; got != 0x60 {
		t.Errorf("TSCFGH was left at %#02x, want 0x60 after the reset pulse", got)
	}
	if demod.repeater != 0x5a {
		t.Errorf("the cached repeater is %#02x, want the chip's 0x5a", demod.repeater)
	}
	// Config said parallel, and the chip said serial. The chip wins.
	if demod.config.TSMode != TSModeParallel {
		t.Fatal("the test no longer sets up the disagreement it is checking")
	}
}

// TestColdAndWarmAgreeOnThePerPathRegisters guards the warm path's one
// duplication: perPathInit lists probe()'s per-demodulator writes, while the
// cold path keeps them interleaved with the chip-wide ones.
func TestColdAndWarmAgreeOnThePerPathRegisters(t *testing.T) {
	for _, index := range []int{0, 1} {
		bus := newFakeBus()
		bus.registers[regMID] = identity
		demod := New(bus, Config{
			DemodAddress: testAddress, Index: index,
			TSMode: TSModeParallel, RepeaterLevel: 3, Repeater: RepeaterPath1,
		})
		if err := demod.Initialize(); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		for _, step := range demod.perPathInit(demod.tscfgh) {
			// The merger's reset is pulsed, so only its settled value compares.
			if step.register == demod.path(regTSCFGH) && step.value&0x01 != 0 {
				continue
			}
			if got := bus.registers[step.register]; got != step.value {
				t.Errorf("demodulator %d: a cold start left %#04x at %#02x, but the warm path"+
					" would write %#02x", index, step.register, got, step.value)
			}
		}
	}
}
