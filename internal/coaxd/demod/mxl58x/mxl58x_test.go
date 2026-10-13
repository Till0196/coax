// Copyright (c) Ralph Metzler; Marcus Metzler; Digital Devices; MaxLinear; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only

package mxl58x

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

const testAddress = 0x60

// fakeBus decodes the chip's three frames -- a register or block write, a
// register read request, a firmware command -- into maps and a command log.
type fakeBus struct {
	registers map[uint32]uint32
	blocks    map[uint32][]byte
	commands  []fakeCommand
	writes    [][]byte

	// A heartbeat that advances on every read is how the chip says its
	// firmware is running.
	alive     bool
	heartbeat uint32
}

type fakeCommand struct {
	id      byte
	payload []byte
}

func newFakeBus() *fakeBus {
	return &fakeBus{registers: map[uint32]uint32{}, blocks: map[uint32][]byte{}}
}

func (bus *fakeBus) Write(address byte, data []byte) error {
	if address != testAddress {
		return errors.New("wrong address")
	}
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	switch data[0] {
	case plidRegisterWrite:
		register := binary.LittleEndian.Uint32(data[2:6])
		body := data[6:]
		// A four-byte payload is both a register write and a one-word block
		// write; the chip cannot tell them apart either, so record both.
		if len(body) == 4 {
			bus.registers[register] = binary.LittleEndian.Uint32(body)
		}
		bus.blocks[register] = append([]byte(nil), body...)
	case plidCommandWrite:
		bus.commands = append(bus.commands, fakeCommand{
			id:      data[3],
			payload: append([]byte(nil), data[6:]...),
		})
	}
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return nil, errors.New("this chip is never read without a request first")
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if address != testAddress || write[0] != plidRegisterRead {
		return nil, errors.New("unexpected transfer")
	}
	register := binary.LittleEndian.Uint32(write[2:6])
	if register == regHeartbeat && bus.alive {
		bus.heartbeat++
		bus.registers[register] = bus.heartbeat
	}
	if readLength != 4 {
		return nil, errors.New("unexpected read length")
	}
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, bus.registers[register])
	return out, nil
}

// newTestDemod returns a frontend that believes the chip is already running.
func newTestDemod(bus *fakeBus, config Config) *Demod {
	config.DemodAddress = testAddress
	demod := New(bus, "", config)
	demod.warm = true
	return demod
}

func TestRegisterWriteFrame(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	if err := demod.regWrite(regFirmwareDownloadSign, 0xdeadbeef); err != nil {
		t.Fatalf("regWrite: %v", err)
	}
	// Hand-checked against write_register's initializer: the write PLID, a
	// length of eight, then address and value each low byte first.
	want := []byte{plidRegisterWrite, 0x08, 0xe0, 0xea, 0xff, 0x3f, 0xef, 0xbe, 0xad, 0xde}
	if !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("the frame was %#v, want %#v", bus.writes[0], want)
	}
}

// The frame is checked by the fake, which refuses anything but a read PLID at
// this address; what is asserted here is the little-endian decode.
func TestRegisterReadReturnsTheWord(t *testing.T) {
	bus := newFakeBus()
	bus.registers[regFirmwareVersion] = 0x01020304
	demod := newTestDemod(bus, Config{})
	value, err := demod.FirmwareVersion()
	if err != nil {
		t.Fatalf("FirmwareVersion: %v", err)
	}
	if value != 0x01020304 {
		t.Fatalf("the version read back as %#08x", value)
	}
}

func TestFieldMask(t *testing.T) {
	// MXL_GET_REG_MASK_32 by hand, on the mnemonics this driver uses.
	for _, test := range []struct {
		name  string
		field field
		want  uint32
	}{
		{"the crystal clock output enable", fieldXtalEnableClockOut, 0x00800000},
		{"the chip version", fieldChipVersion, 0x0000f000},
		{"the CPU reset", fieldCPUSoftResetN, 0x00000001},
		{"the analogue front end reset", fieldAFESoftResetN, 0x00001000},
	} {
		if got := test.field.mask(); got != test.want {
			t.Errorf("%s masked %#08x, want %#08x", test.name, got, test.want)
		}
	}
}

func TestWriteFieldLeavesTheRestOfTheRegisterAlone(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	// The CPU reset and the analogue front end reset share one register.
	bus.registers[fieldCPUSoftResetN.register] = 0x00001001
	if err := demod.regWriteField(fieldCPUSoftResetN, 0); err != nil {
		t.Fatalf("regWriteField: %v", err)
	}
	if got := bus.registers[fieldCPUSoftResetN.register]; got != 0x00001000 {
		t.Fatalf("the register became %#08x, want 0x00001000", got)
	}
}

func TestCommandFrameMatchesTheHandBuiltOne(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{Multiswitch: Direct})
	if err := demod.setInputTone(2, true); err != nil {
		t.Fatalf("setInputTone: %v", err)
	}
	// Spelt out literally, to pin sendCommand's framing against a hand-built
	// frame rather than against itself.
	want := []byte{
		plidCommandWrite,
		12, 8,
		commandDiSEqCContinuous, 0, 0,
		2, 0, 0, 0,
		1, 0, 0, 0,
	}
	if !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("the frame was %#v, want %#v", bus.writes[0], want)
	}
}

func TestStatusOffsetPerFrontend(t *testing.T) {
	// HYDRA_DMD_STATUS_OFFSET is the demodulator index times 0x100, and is the
	// whole of what separates one frontend's readings from another's.
	for index, want := range map[uint32]uint32{0: 0x000, 1: 0x100, 5: 0x500, 7: 0x700} {
		demod := newTestDemod(newFakeBus(), Config{DemodIndex: index})
		if got := demod.statusOffset(); got != want {
			t.Errorf("frontend %d read at offset %#x, want %#x", index, got, want)
		}
	}
}

func TestHundredthsOfADecibel(t *testing.T) {
	// A signed sixteen-bit count of hundredths of a dB: 1000 is 10 dB, and a
	// word whose low sixteen bits are -2000 is -20.
	for _, test := range []struct {
		raw  uint32
		want float64
	}{
		{0, 0},
		{1000, 10},
		{0xf830, -20},
		{0xfffff830, -20},
		{1234, 12.34},
	} {
		if got := hundredthsOfADecibel(test.raw); got != test.want {
			t.Errorf("a reading of %#x came out as %v dB, want %v", test.raw, got, test.want)
		}
	}
}

func TestStatusHoldsTheFirmwareOffTheSlice(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{DemodIndex: 3})
	const offset = 3 * demodStatusStride
	bus.registers[regDemodLockStatus+offset] = 1
	bus.registers[regDemodSNR+offset] = 1150 // 11.5 dB

	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked || !status.HasCNR || status.CNRdB != 11.5 {
		t.Fatalf("the frontend reported %+v", status)
	}
	// Every reading is bracketed by a one and a zero on this frontend's lock.
	var taken, released int
	for _, write := range bus.writes {
		if write[0] != plidRegisterWrite {
			continue
		}
		if binary.LittleEndian.Uint32(write[2:6]) != regDemodStatusLock+offset {
			continue
		}
		if binary.LittleEndian.Uint32(write[6:]) == 1 {
			taken++
		} else {
			released++
		}
	}
	if taken != 2 || released != 2 {
		t.Fatalf("the status lock was taken %d times and released %d, want 2 and 2", taken, released)
	}
	if bus.registers[regDemodStatusLock+offset] != 0 {
		t.Fatal("the status lock was left held")
	}
}

func TestStatusReportsNothingWithoutALock(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	bus.registers[regDemodLockStatus] = 0
	bus.registers[regDemodSNR] = 1150
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Locked || status.HasCNR {
		t.Fatalf("an unlocked frontend reported %+v", status)
	}
}

func TestSignalStrengthReadsTheInputPower(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{DemodIndex: 1})
	// -45.50 dBm, as a sixteen-bit count of hundredths.
	bus.registers[regDemodInputPower+demodStatusStride] = uint32(uint16(0x10000 - 4550))
	strength, err := demod.SignalStrength()
	if err != nil {
		t.Fatalf("SignalStrength: %v", err)
	}
	if strength != -45.5 {
		t.Fatalf("the level was %v dBm, want -45.5", strength)
	}
}

func TestTunePayloadLayout(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{DemodIndex: 5})
	err := demod.Tune(Params{
		DeliverySystem: "DVB-S2",
		FrequencyKHz:   1_318_000,
		SymbolRate:     22_000_000,
		StreamID:       -1,
	})
	if err != nil {
		t.Fatalf("Tune: %v", err)
	}
	if len(bus.commands) != 1 || bus.commands[0].id != commandDemodSetParam {
		t.Fatalf("the chip was sent %+v", bus.commands)
	}
	// MXL_HYDRA_DEMOD_PARAM_T, field for field: tuner, demodulator, frequency
	// in hertz, standard, spectrum inversion, roll-off, symbol rate, pilots,
	// modulation, code rate, carrier offset.
	want := []uint32{0, 5, 1_318_000_000, standardDVBS2, spectrumAuto, rolloffAuto,
		22_000_000, pilotsAuto, modulationAuto, fecAuto, maxCarrierOffsetMHz}
	payload := bus.commands[0].payload
	if len(payload) != 4*len(want) {
		t.Fatalf("the payload was %d bytes, want %d", len(payload), 4*len(want))
	}
	for index, value := range want {
		if got := binary.LittleEndian.Uint32(payload[4*index:]); got != value {
			t.Errorf("field %d was %d, want %d", index, got, value)
		}
	}
}

func TestRFInputFollowsTheDemodIndexInDirectMode(t *testing.T) {
	bus := newFakeBus()
	// One lead per pair of demodulators, counting down: 0 and 1 take input 3,
	// 6 and 7 take input 0.
	for index, want := range map[uint32]uint32{0: 3, 1: 3, 2: 2, 4: 1, 7: 0} {
		demod := newTestDemod(bus, Config{DemodIndex: index, Multiswitch: Direct})
		if got := demod.RFInput(); got != want {
			t.Errorf("frontend %d listens to input %d, want %d", index, got, want)
		}
	}
	// The rfsource override pins every frontend to one input.
	demod := newTestDemod(bus, Config{DemodIndex: 7, Multiswitch: Direct, RFSource: 1})
	if got := demod.RFInput(); got != 3 {
		t.Fatalf("the override put the frontend on input %d, want 3", got)
	}
}

func TestTuneRefusesWhatTheChipCannotReceive(t *testing.T) {
	demod := newTestDemod(newFakeBus(), Config{})
	base := Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_318_000, SymbolRate: 22_000_000, StreamID: -1}

	refuse := func(name string, params Params) {
		t.Helper()
		if err := demod.Tune(params); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	below := base
	below.FrequencyKHz = minFrequencyKHz - 1
	refuse("a frequency below the L band", below)
	above := base
	above.FrequencyKHz = maxFrequencyKHz + 1
	refuse("a frequency above the L band", above)
	slow := base
	slow.SymbolRate = minSymbolRate - 1
	refuse("a symbol rate below the minimum", slow)
	fast := base
	fast.SymbolRate = maxSymbolRate + 1
	refuse("a symbol rate above the maximum", fast)
	terrestrial := base
	terrestrial.DeliverySystem = "ISDBT"
	refuse("ISDB-T", terrestrial)
	stream := base
	stream.StreamID = 4
	refuse("a stream identifier", stream)
}

func TestDemodulationPerDeliverySystem(t *testing.T) {
	for _, test := range []struct {
		system     string
		standard   uint32
		modulation uint32
		pilots     uint32
	}{
		{"DSS", standardDSS, modulationAuto, pilotsOff},
		{"DVB-S", standardDVBS, modulationQPSK, pilotsOff},
		{"dvbs2", standardDVBS2, modulationAuto, pilotsAuto},
	} {
		standard, modulation, pilots, err := demodulation(test.system)
		if err != nil {
			t.Fatalf("%s: %v", test.system, err)
		}
		if standard != test.standard || modulation != test.modulation || pilots != test.pilots {
			t.Errorf("%s gave %d/%d/%d, want %d/%d/%d", test.system,
				standard, modulation, pilots, test.standard, test.modulation, test.pilots)
		}
	}
	if _, _, _, err := demodulation("DVBC"); err == nil {
		t.Fatal("DVB-C was accepted")
	}
}

func TestQuattroModeChoosesAnInputRatherThanSwitchingTheLead(t *testing.T) {
	bus := newFakeBus()
	// Quattro is Multiswitch's zero value.
	demod := newTestDemod(bus, Config{})
	for _, test := range []struct {
		high, tone bool
		want       uint32
	}{
		{high: true, tone: true, want: 0},
		{high: true, tone: false, want: 1},
		{high: false, tone: true, want: 2},
		{high: false, tone: false, want: 3},
	} {
		if err := demod.SetVoltage(test.high, false); err != nil {
			t.Fatalf("SetVoltage: %v", err)
		}
		if err := demod.SetTone(test.tone); err != nil {
			t.Fatalf("SetTone: %v", err)
		}
		if got := demod.RFInput(); got != test.want {
			t.Errorf("high=%v tone=%v put the frontend on input %d, want %d",
				test.high, test.tone, got, test.want)
		}
	}
	// In this mode neither call touches the chip.
	if len(bus.writes) != 0 {
		t.Fatalf("the chip was written to %d times", len(bus.writes))
	}
	// No supply at all falls into the same branch as 13 V.
	if err := demod.SetVoltage(true, true); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if demod.RFInput()&rfInputLowVoltage == 0 {
		t.Fatal("asking for no supply moved the frontend onto an 18 V input")
	}
}

// TestMultiswitchAgreesWithTheInputEncoding pins the invariant Quattro mode
// rests on: the inputs InitializeMultiswitch sets up must be at the band and
// polarisation their own numbers encode, or choosing an input by moving those
// bits would land on the wrong one.
func TestMultiswitchAgreesWithTheInputEncoding(t *testing.T) {
	bus := newFakeBus()
	voltage := &recordingVoltage{}
	demod := newTestDemod(bus, Config{Voltage: voltage})
	if err := demod.InitializeMultiswitch(); err != nil {
		t.Fatalf("InitializeMultiswitch: %v", err)
	}
	if len(voltage.calls) != 4 {
		t.Fatalf("the supply was set on %d inputs, want 4", len(voltage.calls))
	}
	for _, call := range voltage.calls {
		if high := call.input&rfInputLowVoltage == 0; high != call.high {
			t.Errorf("input %d was set to high=%v, which its number does not encode", call.input, call.high)
		}
	}
	if len(bus.commands) != 4 {
		t.Fatalf("the tone was set on %d inputs, want 4", len(bus.commands))
	}
	for _, command := range bus.commands {
		if command.id != commandDiSEqCContinuous {
			t.Fatalf("command %d was sent, want %d", command.id, commandDiSEqCContinuous)
		}
		input := binary.LittleEndian.Uint32(command.payload[0:])
		on := binary.LittleEndian.Uint32(command.payload[4:]) == 1
		if want := input&rfInputToneOff == 0; want != on {
			t.Errorf("input %d was given tone=%v, which its number does not encode", input, on)
		}
	}
}

func TestDirectModeSwitchesTheLead(t *testing.T) {
	bus := newFakeBus()
	voltage := &recordingVoltage{}
	demod := newTestDemod(bus, Config{DemodIndex: 4, Multiswitch: Direct, Voltage: voltage})
	if err := demod.SetVoltage(true, false); err != nil {
		t.Fatalf("SetVoltage: %v", err)
	}
	if len(voltage.calls) != 1 || voltage.calls[0].input != 1 || !voltage.calls[0].high {
		t.Fatalf("the board was asked for %+v", voltage.calls)
	}
	if err := demod.SetTone(true); err != nil {
		t.Fatalf("SetTone: %v", err)
	}
	if len(bus.commands) != 1 || bus.commands[0].id != commandDiSEqCContinuous {
		t.Fatalf("the chip was sent %+v", bus.commands)
	}
	// With no way to reach the card's regulator the request is refused.
	bare := newTestDemod(newFakeBus(), Config{Multiswitch: Direct})
	if err := bare.SetVoltage(true, false); err == nil {
		t.Fatal("a frontend with no supply switch claimed to have set one")
	}
}

func TestDiSEqCPayloadLayout(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{DemodIndex: 2, Multiswitch: Direct})
	message := []byte{0xe0, 0x10, 0x38, 0xf0}
	if err := demod.SendDiSEqC(message); err != nil {
		t.Fatalf("SendDiSEqC: %v", err)
	}
	payload := bus.commands[0].payload
	// MXL_HYDRA_DISEQC_TX_MSG_T is a controller, a length, a fixed thirty-two
	// byte buffer and a burst: forty-four bytes whatever the message is.
	if len(payload) != 44 {
		t.Fatalf("the payload was %d bytes, want 44", len(payload))
	}
	if got := binary.LittleEndian.Uint32(payload[0:]); got != 2 {
		t.Fatalf("the message went to controller %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint32(payload[4:]); got != 4 {
		t.Fatalf("the length was %d, want 4", got)
	}
	if !bytes.Equal(payload[8:12], message) {
		t.Fatalf("the buffer held %#v", payload[8:12])
	}
	if !bytes.Equal(payload[12:40], make([]byte, 28)) {
		t.Fatal("the rest of the buffer was not clear")
	}
	if got := binary.LittleEndian.Uint32(payload[40:]); got != toneBurstNone {
		t.Fatalf("a tone burst of %d was sent with the message", got)
	}
}

func TestSendBurstSendsNoMessage(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	if err := demod.SendBurst(true); err != nil {
		t.Fatalf("SendBurst: %v", err)
	}
	payload := bus.commands[0].payload
	if got := binary.LittleEndian.Uint32(payload[4:]); got != 0 {
		t.Fatalf("the burst carried a %d byte message", got)
	}
	if got := binary.LittleEndian.Uint32(payload[40:]); got != toneBurstB {
		t.Fatalf("burst %d was sent, want %d", got, toneBurstB)
	}
}

func TestSendDiSEqCRefusesAMessageThatDoesNotFit(t *testing.T) {
	demod := newTestDemod(newFakeBus(), Config{})
	if err := demod.SendDiSEqC(nil); err == nil {
		t.Fatal("an empty message was accepted")
	}
	if err := demod.SendDiSEqC(make([]byte, diseqcMessageMax+1)); err == nil {
		t.Fatal("a message longer than the firmware's buffer was accepted")
	}
}

func TestFlipWords(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 0, 0}
	flipWords(data)
	want := []byte{4, 3, 2, 1, 0, 0, 6, 5}
	if !bytes.Equal(data, want) {
		t.Fatalf("the words came out %#v, want %#v", data, want)
	}
}

// mbin builds a one-segment firmware container.
func mbin(address uint32, body []byte) []byte {
	image := make([]byte, mbinFileHeaderLength)
	image[0], image[1], image[2], image[3] = mbinFileHeaderID, '1', mbinFileHeaderLength, 1
	header := make([]byte, mbinSegmentHeaderLength)
	header[0] = mbinSegmentHeaderID
	header[1] = byte(len(body) >> 16)
	header[2] = byte(len(body) >> 8)
	header[3] = byte(len(body))
	binary.BigEndian.PutUint32(header[4:], address)
	return append(append(image, header...), body...)
}

func TestWriteImageWritesEachSegmentWordReversed(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	if err := demod.writeImage(mbin(0x40000000, []byte{1, 2, 3, 4, 5, 6})); err != nil {
		t.Fatalf("writeImage: %v", err)
	}
	// The signature is invalidated first: a half written image must not pass.
	if _, ok := bus.blocks[regFirmwareDownloadSign]; !ok {
		t.Fatal("the download signature was not cleared")
	}
	got := bus.blocks[0x40000000]
	want := []byte{4, 3, 2, 1, 0, 0, 6, 5}
	if !bytes.Equal(got, want) {
		t.Fatalf("the segment went out as %#v, want %#v", got, want)
	}
}

func TestWriteImageSplitsLongSegments(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	body := make([]byte, maxBlockWrite+16)
	if err := demod.writeImage(mbin(0x40000000, body)); err != nil {
		t.Fatalf("writeImage: %v", err)
	}
	// The second block starts where the first ended.
	if got := len(bus.blocks[0x40000000]); got != maxBlockWrite {
		t.Fatalf("the first block was %d bytes, want %d", got, maxBlockWrite)
	}
	if got := len(bus.blocks[0x40000000+maxBlockWrite]); got != 16 {
		t.Fatalf("the second block was %d bytes, want 16", got)
	}
}

func TestWriteImageRefusesWhatItCannotWrite(t *testing.T) {
	demod := newTestDemod(newFakeBus(), Config{})
	if err := demod.writeImage([]byte{'X', '1', 16, 1}); err == nil {
		t.Fatal("a file with the wrong header was accepted")
	}
	if err := demod.writeImage(mbin(0x90760000, []byte{1, 2, 3, 4})); err == nil {
		t.Fatal("a segment in the second processor's memory was written")
	}
	if err := demod.writeImage(mbin(0x90740000, []byte{1, 2, 3, 4})); err == nil {
		t.Fatal("a segment in the second processor's memory was written")
	}
	truncated := mbin(0x40000000, []byte{1, 2, 3, 4})
	truncated = truncated[:len(truncated)-2]
	if err := demod.writeImage(truncated); err == nil {
		t.Fatal("a truncated segment was accepted")
	}
}

func TestInitializeLeavesARunningChipAlone(t *testing.T) {
	bus := newFakeBus()
	bus.alive = true
	demod := New(bus, "", Config{DemodAddress: testAddress, DemodIndex: 6})
	if err := demod.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !demod.warm {
		t.Fatal("the frontend was not marked ready")
	}
	// Nothing at all was written: this is the whole of the warm path, and is
	// what lets one chip's other seven frontends initialize after the first.
	if len(bus.writes) != 0 {
		t.Fatalf("a running chip was written to %d times", len(bus.writes))
	}
}

func TestInitializeRefusesToResetAChipItWasToldWasBooted(t *testing.T) {
	bus := newFakeBus()
	bus.alive = false
	demod := New(bus, "", Config{DemodAddress: testAddress, AlreadyBooted: true})
	if err := demod.Initialize(); err == nil {
		t.Fatal("a chip declared already booted was taken through the cold path")
	}
	if len(bus.writes) != 0 {
		t.Fatalf("the chip was written to %d times", len(bus.writes))
	}
}

func TestCallsBeforeInitializeAreRefused(t *testing.T) {
	demod := New(newFakeBus(), "", Config{DemodAddress: testAddress})
	if _, err := demod.Status(); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("Status answered %v", err)
	}
	if _, err := demod.SignalStrength(); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("SignalStrength answered %v", err)
	}
	if err := demod.Tune(Params{DeliverySystem: "DVBS2", FrequencyKHz: 1_318_000,
		SymbolRate: 22_000_000, StreamID: -1}); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("Tune answered %v", err)
	}
	if err := demod.SetTone(true); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("SetTone answered %v", err)
	}
	if err := demod.SendDiSEqC([]byte{0xe0}); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("SendDiSEqC answered %v", err)
	}
	if err := demod.InitializeMultiswitch(); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("InitializeMultiswitch answered %v", err)
	}
}

func TestCrystalSettingFollowsTheFittedCrystal(t *testing.T) {
	for clock, want := range map[uint32]uint32{24_000_000: 0, 27_000_000: 1} {
		bus := newFakeBus()
		demod := newTestDemod(bus, Config{ClockHz: clock, CrystalCap: 12})
		if err := demod.configureCrystal(); err != nil {
			t.Fatalf("configureCrystal: %v", err)
		}
		if got := bus.registers[regCrystalSetting]; got != want {
			t.Errorf("a %d Hz crystal was configured as %d, want %d", clock, got, want)
		}
		if got := bus.registers[regCrystalCap]; got != 12 {
			t.Errorf("the load capacitance was written as %d, want 12", got)
		}
	}
}

func TestTransportStreamSetupIsTheReferenceDriversTwelveWrites(t *testing.T) {
	bus := newFakeBus()
	demod := newTestDemod(bus, Config{})
	if err := demod.configureTransportStream(); err != nil {
		t.Fatalf("configureTransportStream: %v", err)
	}
	if len(bus.writes) != len(transportStreamSetup) {
		t.Fatalf("%d registers were written, want %d", len(bus.writes), len(transportStreamSetup))
	}
	// The minimum NCO count decides the transport clock: MXL_HYDRA_NCO_CLK of
	// 418 MHz over the 139 MHz maximum is 3, and it sits in bits 16 to 23.
	const ncoCountMin = 418 / 139
	if got := bus.registers[0x90700044] >> 16 & 0xff; got != ncoCountMin {
		t.Fatalf("the minimum NCO count was %d, want %d", got, ncoCountMin)
	}
	// All eight inputs and outputs enabled: one chip serves eight tuners.
	if got := bus.registers[0x90700000] & 0xff; got != 0xff {
		t.Fatalf("the enabled inputs were %#02x, want 0xff", got)
	}
	if got := bus.registers[0x9070000c] & 0xff; got != 0xff {
		t.Fatalf("the enabled outputs were %#02x, want 0xff", got)
	}
}

type recordingVoltage struct {
	calls []struct {
		input     uint32
		high, off bool
	}
}

func (voltage *recordingVoltage) SetInputVoltage(input uint32, high, off bool) error {
	voltage.calls = append(voltage.calls, struct {
		input     uint32
		high, off bool
	}{input, high, off})
	return nil
}
