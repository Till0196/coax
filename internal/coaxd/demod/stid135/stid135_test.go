// Copyright (c) STMicroelectronics (Mathias Hilaire, Thierry Delahaye); CrazyCat; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package stid135

import (
	"errors"
	"testing"
)

const testAddress = 0x68

// fakeBus is the chip's register file behind the I2C-to-STBus bridge, with a
// log of the frames written to it. It decodes the transaction byte the way the
// bridge does, so a wrongly framed transfer fails here rather than writing to
// the wrong place.
type fakeBus struct {
	registers map[uint16]byte
	writes    [][]byte
	// pending is the address a read frame set up, kept because a bus that
	// cannot do a repeated start splits WriteRead into two calls.
	pending  uint16
	pendRun  bool
	failRead bool
}

func newFakeBus() *fakeBus { return &fakeBus{registers: map[uint16]byte{}} }

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testAddress {
		return errors.New("wrong I2C address")
	}
	if len(data) < 3 {
		return errors.New("an STBus frame is at least a transaction byte and two address bytes")
	}
	transaction := data[0]
	target := uint16(data[1])<<8 | uint16(data[2])
	payload := data[3:]
	run := transaction&stbusStreamIncr != 0
	if transaction&stbusAddr2Byte == 0 {
		return errors.New("the frame did not ask for a two-byte address")
	}
	if transaction&stbusRead != 0 {
		if len(payload) != 0 {
			return errors.New("a read frame carries no data")
		}
		bus.pending, bus.pendRun = target, run
		return nil
	}
	if len(payload) > 1 && !run {
		return errors.New("a multi-byte write must set the increment bit")
	}
	if len(payload) == 1 && run {
		return errors.New("a single-byte write must not set the increment bit")
	}
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	for offset, value := range payload {
		bus.registers[target+uint16(offset)] = value
	}
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	if address != testAddress {
		return nil, errors.New("wrong I2C address")
	}
	if bus.failRead {
		return nil, errors.New("bus failure")
	}
	if length > 1 && !bus.pendRun {
		return nil, errors.New("a multi-byte read must set the increment bit")
	}
	out := make([]byte, length)
	for offset := range out {
		out[offset] = bus.registers[bus.pending+uint16(offset)]
	}
	return out, nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if err := bus.Write(address, write); err != nil {
		return nil, err
	}
	return bus.Read(address, readLength)
}

// lastWriteTo returns the value last written to one register as a single-byte
// write.
func (bus *fakeBus) lastWriteTo(address uint16) (byte, bool) {
	for index := len(bus.writes) - 1; index >= 0; index-- {
		frame := bus.writes[index]
		target := uint16(frame[1])<<8 | uint16(frame[2])
		if target == address && len(frame) == 4 {
			return frame[3], true
		}
	}
	return 0, false
}

// runTo returns the payload of the last multi-byte write that started at one
// address.
func (bus *fakeBus) runTo(address uint16) []byte {
	for index := len(bus.writes) - 1; index >= 0; index-- {
		frame := bus.writes[index]
		target := uint16(frame[1])<<8 | uint16(frame[2])
		if target == address && len(frame) > 4 {
			return frame[3:]
		}
	}
	return nil
}

// bootedBus is a cut 2 part with the front-end PLL as a running chip leaves
// it: the ratios that give the design's 6200 MHz oscillator from a 27 MHz
// crystal -- integer part 114, fraction 26700 with the sigma-delta modulator
// running, and no reference division.
func bootedBus() *fakeBus {
	bus := newFakeBus()
	bus.registers[afeBase+regAFEID] = cutIDCut2
	bus.registers[afeBase+regAFEPLLREFDIV] = 0x02
	bus.registers[afeBase+regAFEPLLINT] = 114
	bus.registers[afeBase+regAFEPLLFRACH] = fldPLLSDON | byte(26700>>8)
	bus.registers[afeBase+regAFEPLLFRACL] = byte(26700 & 0xff)
	return bus
}

// newTestDemod is a Direct-mode demodulator on input 0, which is the mode in
// which SetTone touches the chip. Quattro-mode behaviour has tests of its own.
func newTestDemod(bus *fakeBus) *Demod {
	return newTestDemodIn(bus, Config{DemodAddress: testAddress, AlreadyBooted: true, Multiswitch: Direct})
}

// newTestDemodIn is newTestDemod with the wiring spelled out, already woken
// and holding the clocks Initialize would have recovered.
func newTestDemodIn(bus *fakeBus, config Config) *Demod {
	demod := New(bus, config)
	demod.warm = true
	demod.rfInput = 0
	if config.Multiswitch == Direct {
		demod.rfInput = config.RFInput
	}
	demod.loFrequencyHz = 1_550_000_000
	demod.masterClockHz = 1_550_000_000 / 12
	return demod
}

func TestInitializeRefusesAChipNobodyHasBroughtUp(t *testing.T) {
	demod := New(bootedBus(), Config{DemodAddress: testAddress})
	if err := demod.Initialize(); err == nil {
		t.Fatal("a cold chip was accepted")
	}
}

func TestInitializeRefusesTheWrongCut(t *testing.T) {
	bus := bootedBus()
	bus.registers[afeBase+regAFEID] = 0x04
	demod := New(bus, Config{DemodAddress: testAddress, AlreadyBooted: true})
	if err := demod.Initialize(); err == nil {
		t.Fatal("a part reporting a cut the reference driver rejects was accepted")
	}
}

func TestInitializeRecoversTheClocksAndPointsTheCrossbar(t *testing.T) {
	bus := bootedBus()
	demod := New(bus, Config{DemodAddress: testAddress, AlreadyBooted: true,
		Multiswitch: Direct, Demod: 5, RFInput: 2})
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// The PLL gives 6200 MHz; the local oscillator is a quarter of that and
	// the master clock a twelfth of the local oscillator.
	if got := demod.LocalOscillatorHz(); got != 1_550_000_000 {
		t.Fatalf("local oscillator %d Hz, want 1550000000", got)
	}
	if got := demod.MasterClockHz(); got != 1_550_000_000/12 {
		t.Fatalf("master clock %d Hz, want %d", got, 1_550_000_000/12)
	}
	// RFMUX5 belongs to demodulator 6, and it should now select input 2.
	if got, ok := bus.lastWriteTo(rfmuxBase + 5); !ok || got&fldRFMUXDEMODSEL != 2 {
		t.Fatalf("RFMUX5 became %#02x (present %v), want the low two bits at 2", got, ok)
	}
}

func TestInitializeRejectsAnImpossibleWiring(t *testing.T) {
	for _, config := range []Config{
		{DemodAddress: testAddress, AlreadyBooted: true, Demod: 8},
		{DemodAddress: testAddress, AlreadyBooted: true, RFInput: 4},
	} {
		if err := New(bootedBus(), config).Initialize(); err == nil {
			t.Fatalf("config %+v was accepted", config)
		}
	}
}

func TestRegisterFramingUsesTwoAddressBytes(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	if err := demod.regWrite(0x1234, 0x5a); err != nil {
		t.Fatalf("regWrite: %v", err)
	}
	frame := bus.writes[len(bus.writes)-1]
	want := []byte{transactionWriteOne, 0x12, 0x34, 0x5a}
	if len(frame) != len(want) {
		t.Fatalf("frame %v, want %v", frame, want)
	}
	for index := range want {
		if frame[index] != want[index] {
			t.Fatalf("frame %v, want %v", frame, want)
		}
	}
	value, err := demod.regRead(0x1234)
	if err != nil {
		t.Fatalf("regRead: %v", err)
	}
	if value != 0x5a {
		t.Fatalf("read back %#02x, want 0x5a", value)
	}
}

func TestFieldWriteKeepsTheRestOfTheRegister(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[0x0900] = 0xc3
	// SPECINV_CONTROL is bits 4 and 5.
	if err := demod.fieldWrite(0x0900, fldSPECINVCONTROL, 2); err != nil {
		t.Fatalf("fieldWrite: %v", err)
	}
	if got := bus.registers[0x0900]; got != 0xe3 {
		t.Fatalf("register became %#02x, want 0xe3", got)
	}
	value, err := demod.fieldRead(0x0900, fldSPECINVCONTROL)
	if err != nil {
		t.Fatalf("fieldRead: %v", err)
	}
	if value != 2 {
		t.Fatalf("field read back %d, want 2", value)
	}
}

func TestBlockBasesFollowTheAddressMap(t *testing.T) {
	// Path 1 and path 8 of each block this driver reaches, against the
	// reference driver's address map.
	cases := []struct {
		name  string
		base  [8]uint16
		first uint16
		last  uint16
	}{
		{"DVBSX_DEMOD", demodBase, 0x0900, 0x4100},
		{"DVBSX_VITERBI", viterbiBase, 0x0D00, 0x4500},
		{"DVBSX_HWARE", hwareBase, 0x0E00, 0x4600},
		{"DVBSX_PKTDELIN", pktdelinBase, 0x0F00, 0x4700},
		{"DVBSX_FECSPY", fecspyBase, 0x0F80, 0x4780},
	}
	for _, test := range cases {
		if test.base[0] != test.first || test.base[7] != test.last {
			t.Errorf("%s bases are %#04x..%#04x, want %#04x..%#04x",
				test.name, test.base[0], test.base[7], test.first, test.last)
		}
	}
}
