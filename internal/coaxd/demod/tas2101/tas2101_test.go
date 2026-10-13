// Copyright (c) Luis Alves; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later

package tas2101

import (
	"errors"
	"testing"
)

const testAddress = 0x60

// fakeBus is a register map with a log of what was written to it: this
// driver is register sequences and arithmetic, and nothing else.
type fakeBus struct {
	registers map[byte]byte
	writes    [][]byte
}

func newFakeBus() *fakeBus { return &fakeBus{registers: map[byte]byte{}} }

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testAddress {
		return errors.New("wrong address")
	}
	copied := append([]byte(nil), data...)
	bus.writes = append(bus.writes, copied)
	for offset, value := range data[1:] {
		bus.registers[data[0]+byte(offset)] = value
	}
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if address != testAddress || len(write) != 1 {
		return nil, errors.New("unexpected transfer")
	}
	out := make([]byte, readLength)
	for offset := range out {
		out[offset] = bus.registers[write[0]+byte(offset)]
	}
	return out, nil
}

func (bus *fakeBus) lastWriteTo(register byte) (byte, bool) {
	for index := len(bus.writes) - 1; index >= 0; index-- {
		if bus.writes[index][0] == register && len(bus.writes[index]) == 2 {
			return bus.writes[index][1], true
		}
	}
	return 0, false
}

func newTestDemod(bus *fakeBus) *Demod {
	demod := New(bus, Config{DemodAddress: testAddress})
	demod.warm = true
	return demod
}

func TestInitializeRefusesAChipThatIsNotOne(t *testing.T) {
	bus := newFakeBus()
	bus.registers[registerID0], bus.registers[registerID1] = 0x44, 0x00
	if err := New(bus, Config{DemodAddress: testAddress}).Initialize(); err == nil {
		t.Fatal("a chip answering the wrong identity was accepted")
	}
}

func TestInitializeWritesTheBoardBytes(t *testing.T) {
	bus := newFakeBus()
	bus.registers[registerID0], bus.registers[registerID1] = identity0, identity1
	config := Config{
		DemodAddress: testAddress,
		Init:         [7]byte{0xb0, 0x32, 0x81, 0x57, 0x64, 0x9a, 0x33},
	}
	demod := New(bus, config)
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// The first six go out as one run at 0xe6.
	var block []byte
	for _, write := range bus.writes {
		if write[0] == 0xe6 {
			block = write
		}
	}
	want := []byte{0xe6, 0xb0, 0x32, 0x81, 0x57, 0x64, 0x9a}
	if len(block) != len(want) {
		t.Fatalf("the block at 0xe6 was %v, want %v", block, want)
	}
	for index := range want {
		if block[index] != want[index] {
			t.Fatalf("the block at 0xe6 was %v, want %v", block, want)
		}
	}
	// The seventh replaces the whole of 0xe0, without a read first.
	if value, ok := bus.lastWriteTo(0xe0); !ok || value != 0x33 {
		t.Fatalf("0xe0 was written %#02x (present %v), want 0x33", value, ok)
	}
	if !demod.warm {
		t.Fatal("the chip was not marked initialized")
	}
}

func TestSetToneHoldsTheCommandField(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	// Other bits of the register are already in use, the supply among them.
	bus.registers[registerLNBControl] = voltageSelect | 0x80
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if got := bus.registers[registerLNBControl]; got != voltageSelect|0x80|toneOn {
		t.Fatalf("0x10 became %#02x, want %#02x", got, voltageSelect|0x80|toneOn)
	}
	if err := demod.SetTone(false); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if got := bus.registers[registerLNBControl]; got != voltageSelect|0x80 {
		t.Fatalf("0x10 became %#02x, want %#02x", got, voltageSelect|0x80)
	}
}

func TestSetVoltageKeepsTheToneAlone(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[registerLNBControl] = toneOn
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if got := bus.registers[registerLNBControl]; got != toneOn|voltageSelect {
		t.Fatalf("0x10 became %#02x, want %#02x", got, toneOn|voltageSelect)
	}
	if err := demod.SetVoltage(false, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if got := bus.registers[registerLNBControl]; got != toneOn {
		t.Fatalf("0x10 became %#02x, want %#02x", got, toneOn)
	}
	// Asking for no supply leaves the register alone; see SetVoltage for why.
	if err := demod.SetVoltage(false, true); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if got := bus.registers[registerLNBControl]; got != toneOn {
		t.Fatalf("0x10 became %#02x, want %#02x", got, toneOn)
	}
}

func TestSendDiSEqCPutsTheLeadBack(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[registerLNBControl] = voltageSelect | toneOn
	message := []byte{0xe0, 0x10, 0x38, 0xf0}
	if err := demod.SendDiSEqC(message); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	// The message went into the buffer at 0x20 in one transfer.
	var buffer []byte
	for _, write := range bus.writes {
		if write[0] == registerDiSEqC {
			buffer = write
		}
	}
	if len(buffer) != len(message)+1 {
		t.Fatalf("the buffer write was %v", buffer)
	}
	// The command carries the length less one, above the command bits.
	var sent bool
	for _, write := range bus.writes {
		if len(write) == 2 && write[0] == registerLNBControl &&
			write[1]&diseqcCommandMask == diseqcSendMsg {
			sent = true
			if want := byte(len(message)-1) << 3; write[1]&diseqcLengthMask != want {
				t.Fatalf("the length field was %#02x, want %#02x", write[1]&diseqcLengthMask, want)
			}
			if write[1]&voltageSelect == 0 {
				t.Fatal("the supply voltage was dropped while sending")
			}
		}
	}
	if !sent {
		t.Fatal("no send command was written")
	}
	if got := bus.registers[registerLNBControl]; got != voltageSelect|toneOn {
		t.Fatalf("the lead was left at %#02x, want %#02x", got, voltageSelect|toneOn)
	}
}

func TestSendDiSEqCRefusesAMessageThatDoesNotFit(t *testing.T) {
	demod := newTestDemod(newFakeBus())
	if err := demod.SendDiSEqC(nil); err == nil {
		t.Fatal("an empty message was accepted")
	}
	if err := demod.SendDiSEqC(make([]byte, 9)); err == nil {
		t.Fatal("a nine-byte message was accepted")
	}
}

func TestStatusReadsLockAndCarrierToNoise(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	bus.registers[registerStatus] = 0x00
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Locked || status.HasCNR {
		t.Fatalf("an unlocked chip reported %+v", status)
	}

	bus.registers[registerStatus] = statusLocked | 0x88 // bits outside the mask
	bus.registers[register04] = 0x08
	// 0x100 is a calibrated point: 9.0 dB.
	bus.registers[0x92], bus.registers[0x93] = 0x00, 0x01
	status, err = demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked || !status.HasCNR {
		t.Fatalf("a locked chip reported %+v", status)
	}
	if status.CNRdB != 9 {
		t.Fatalf("the carrier-to-noise ratio was %v dB, want 9", status.CNRdB)
	}
	// The bit the pre-tune table set to hold the output off is dropped again.
	if bus.registers[register04]&0x08 != 0 {
		t.Fatal("the transport stream output was left held off")
	}
}

func TestSignalStrengthReadsTheTwelveBitLevel(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus)
	// 0x231 is a calibrated point: -30.0 dBm. The upper four bits arrive in
	// the high nibble of the second byte.
	bus.registers[registerStrength0] = 0x31
	bus.registers[registerStrength0+1] = 0x20
	strength, err := demod.SignalStrength()
	if err != nil {
		t.Fatalf("SignalStrength: %v", err)
	}
	if strength != -30 {
		t.Fatalf("the level was %v dBm, want -30", strength)
	}
}

func TestTuneWritesTheSymbolRateAndMovesTheTuner(t *testing.T) {
	bus := newFakeBus()
	var tuned [2]uint32
	demod := New(bus, Config{DemodAddress: testAddress})
	demod.AttachTuner(tunerFunc(func(frequencyHz, symbolRateSPS uint32) error {
		tuned = [2]uint32{frequencyHz, symbolRateSPS}
		return nil
	}))
	demod.warm = true
	// Lock on the first look, so the tune returns rather than timing out.
	bus.registers[registerStatus] = statusLocked

	if err := demod.Tune(Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_310_000, SymbolRate: 22_000_000}); err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if tuned != [2]uint32{1_310_000_000, 22_000_000} {
		t.Fatalf("the tuner was moved to %v", tuned)
	}
	// 22000 kSps, low byte first.
	if got := [2]byte{bus.registers[registerSetSRate0], bus.registers[registerSetSRate0+1]}; got != [2]byte{0xf0, 0x55} {
		t.Fatalf("the symbol rate registers were %#02x, want {0xf0, 0x55}", got)
	}
	if bus.registers[registerFreqOffset] != 0 || bus.registers[registerFreqOffset+1] != 0 {
		t.Fatal("the frequency offset was not cleared")
	}
	if bus.registers[register30]&tunerStartTune == 0 {
		t.Fatal("the search was never started")
	}
}

func TestTuneRefusesWhatTheChipCannotReceive(t *testing.T) {
	demod := newTestDemod(newFakeBus())
	if err := demod.Tune(Params{DeliverySystem: "ISDBS", SymbolRate: 28_860_000}); err == nil {
		t.Fatal("ISDB-S was accepted")
	}
	if err := demod.Tune(Params{DeliverySystem: "DVBS"}); err == nil {
		t.Fatal("a channel with no symbol rate was accepted")
	}
}

func TestCallsBeforeInitializeAreRefused(t *testing.T) {
	demod := New(newFakeBus(), Config{DemodAddress: testAddress})
	if err := demod.SetTone(true); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("SetTone answered %v", err)
	}
	if _, err := demod.Status(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Status answered %v", err)
	}
	if _, err := demod.SignalStrength(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("SignalStrength answered %v", err)
	}
}

func TestInterpolateBetweenCalibratedPoints(t *testing.T) {
	// A reading above the top of the table takes the top point's value.
	if got := interpolate(snrTable, 0x1000); got != 10 {
		t.Fatalf("a reading off the top gave %d, want 10", got)
	}
	// Halfway between 0x100 (9.0 dB) and 0xcc (10.0 dB).
	if got := interpolate(snrTable, 0xe6); got != 95 {
		t.Fatalf("the midpoint gave %d, want 95", got)
	}
}

type tunerFunc func(frequencyHz, symbolRateSPS uint32) error

func (tune tunerFunc) Tune(frequencyHz, symbolRateSPS uint32) error {
	return tune(frequencyHz, symbolRateSPS)
}
