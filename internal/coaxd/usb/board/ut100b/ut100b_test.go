// Copyright 2026 KASHIWAYA Rikuto
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package ut100b

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/demod/tc90522"
	"github.com/Till0196/coax/internal/coaxd/smartcard/asie5606"
)

// board records what the board does to the hardware, in the same words the
// recfriio trace is turned into: "gpio MASK VALUE", "gpioex MASK VALUE" and
// "write ADDRESS BYTES...". Reads are left out: the two drivers poll different
// registers to learn the same things, and a read changes nothing.
type recorder struct {
	t        *testing.T
	ops      []string
	filter   []string
	register map[[2]byte]byte
}

func newRecorder(t *testing.T) *recorder {
	return &recorder{t: t, register: map[[2]byte]byte{}}
}

func (rec *recorder) add(format string, args ...any) {
	rec.ops = append(rec.ops, fmt.Sprintf(format, args...))
}

func writeOp(address byte, data []byte) string {
	return fmt.Sprintf("write %02x % x", address, data)
}

// control is the bridge.
type fakeControl struct{ rec *recorder }

func (fake fakeControl) Name() string                  { return "test" }
func (fake fakeControl) Close() error                  { fake.rec.add("close"); return nil }
func (fake fakeControl) CustomerInfo() ([]byte, error) { return make([]byte, 0x39), nil }
func (fake fakeControl) SetGPIO(value, mask byte) error {
	fake.rec.add("gpio %02x %02x", mask, value)
	return nil
}
func (fake fakeControl) SetGPIOEx(value, mask byte) error {
	fake.rec.add("gpioex %02x %02x", mask, value)
	return nil
}
func (fake fakeControl) ResetFilter(channel int, mode byte) error {
	fake.rec.filter = append(fake.rec.filter, fmt.Sprintf("reset %d %d", channel, mode))
	return nil
}
func (fake fakeControl) SetFilterWord(channel int, register byte, value uint16) error {
	fake.rec.filter = append(fake.rec.filter, fmt.Sprintf("word %d %02x %04x", channel, register, value))
	return nil
}
func (fake fakeControl) SetFilterInput(channel int, on bool) error {
	fake.rec.filter = append(fake.rec.filter, fmt.Sprintf("input %d %t", channel, on))
	return nil
}
func (fake fakeControl) SetFilterRun(channel int, on bool) error {
	fake.rec.filter = append(fake.rec.filter, fmt.Sprintf("run %d %t", channel, on))
	return nil
}
func (fake fakeControl) StartStream(channel int) error {
	fake.rec.filter = append(fake.rec.filter, fmt.Sprintf("start %d", channel))
	return nil
}
func (fake fakeControl) StopStream(channel int) error {
	fake.rec.filter = append(fake.rec.filter, fmt.Sprintf("stop %d", channel))
	return nil
}
func (fake fakeControl) ReadStream(int, []byte, uint32) (int, error) { return 0, nil }

// fakeBus is the bridge's I2C bus as the demodulator sees it, with register
// memory so that the read-modify-writes have something to read.
type fakeBus struct{ rec *recorder }

func (bus fakeBus) Write(address byte, data []byte) error {
	if address == demodAddress && len(data) >= 2 && data[0] == 0xfe && data[1]&1 != 0 {
		bus.rec.t.Errorf("the tuner was read through the repeater: % x", data)
	}
	bus.rec.add("%s", writeOp(address, data))
	if len(data) == 2 && data[0] != 0xfe {
		bus.rec.register[[2]byte{address, data[0]}] = data[1]
	}
	return nil
}

func (bus fakeBus) Read(address byte, length int) ([]byte, error) {
	bus.rec.t.Errorf("a bare read of %d from %#02x", length, address)
	return make([]byte, length), nil
}

func (bus fakeBus) ReadRegisters(address, register byte, length int) ([]byte, error) {
	data := make([]byte, length)
	for index := range data {
		data[index] = bus.rec.register[[2]byte{address, register + byte(index)}]
	}
	return data, nil
}

// fakeCard is the card interface IC, answering only at 0x4a as on this board.
type fakeCard struct {
	rec     *recorder
	address byte
}

func (card fakeCard) Read(register byte, count int) ([]byte, error) {
	if card.address != 0x4a {
		return nil, errors.New("status 0")
	}
	if register == cardIdentity {
		return []byte{0x06}, nil
	}
	return make([]byte, count), nil
}

func (card fakeCard) Write(register byte, data []byte) error {
	card.rec.add("%s", writeOp(card.address, append([]byte{register}, data...)))
	return nil
}

func newTestHardware(rec *recorder) *hardware {
	bus := fakeBus{rec: rec}
	hardware := &hardware{
		control: fakeControl{rec: rec},
		cardBus: func(address byte) asie5606.Bus { return fakeCard{rec: rec, address: address} },
		demod:   tc90522.New(bus, demodAddress, false),
		sleep:   func(time.Duration) {},
	}
	hardware.tuner = &tuner{bus: hardware.demod.TunerBus()}
	return hardware
}

// recfriioOps reads the recorded control transfers and turns the GPIO steps and
// the I2C writes into recorder words. The single-register write (request 0x03)
// and the buffered write (0x0d fills, 0x0e sends) both become writes.
func recfriioOps(t *testing.T) []string {
	t.Helper()
	file, err := os.Open("testdata/recfriio-ch19.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var ops []string
	var buffer []byte
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var fields [4]uint64
		for index, field := range strings.Fields(line) {
			if fields[index], err = strconv.ParseUint(field, 16, 16); err != nil {
				t.Fatalf("%q: %v", line, err)
			}
		}
		request, value, index, length := byte(fields[0]), uint16(fields[1]), uint16(fields[2]), int(fields[3])
		switch request {
		case 0x08:
			ops = append(ops, fmt.Sprintf("gpio %02x %02x", value>>8, value&0xff))
		case 0x10:
			ops = append(ops, fmt.Sprintf("gpioex %02x %02x", value>>8, value&0xff))
		case 0x03:
			ops = append(ops, writeOp(byte(value), []byte{byte(value >> 8), byte(index)}))
		case 0x0d:
			chunk := []byte{byte(value >> 8), byte(index), byte(index >> 8)}[:length-1]
			buffer = append(buffer, chunk...)
		case 0x0e:
			ops = append(ops, writeOp(byte(value), buffer))
			buffer = nil
		}
	}
	return ops
}

// TestSameAsRecfriio opens the board, tunes channel 19 and closes it, and
// checks every GPIO step and I2C write against what recfriio sent to the same
// board for the same channel.
func TestSameAsRecfriio(t *testing.T) {
	rec := newRecorder(t)
	hardware := newTestHardware(rec)
	if err := hardware.powerUp(); err != nil {
		t.Fatal(err)
	}
	if hardware.cardAddress != 0x4a {
		t.Fatalf("card interface found at %#02x", hardware.cardAddress)
	}
	front := &frontend{hardware: hardware, packets: newPacketizer()}
	if err := front.Tune(map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "509142857"}); err != nil {
		t.Fatal(err)
	}
	if err := front.StartStream(); err != nil {
		t.Fatal(err)
	}
	if err := front.StopStream(); err != nil {
		t.Fatal(err)
	}
	if err := hardware.powerDown(); err != nil {
		t.Fatal(err)
	}
	got := slices.DeleteFunc(rec.ops, func(op string) bool { return op == "close" })
	want := recfriioOps(t)
	if len(want) < 150 {
		t.Fatalf("only %d steps read from the recording", len(want))
	}
	for index := range max(len(got), len(want)) {
		var have, expect string
		if index < len(got) {
			have = got[index]
		}
		if index < len(want) {
			expect = want[index]
		}
		if have != expect {
			t.Fatalf("step %d: got %q, recfriio sent %q", index, have, expect)
		}
	}
	wantFilter := []string{
		"reset 0 1", "word 0 41 1fff", "word 0 43 1fff", "input 0 true", "run 0 true", "start 0",
		"run 0 false", "stop 0",
	}
	if !slices.Equal(rec.filter, wantFilter) {
		t.Fatalf("channel filter: got %q, want %q", rec.filter, wantFilter)
	}
}

func TestTuneFrequency(t *testing.T) {
	for _, test := range []struct {
		frequency string
		mhz       uint32
	}{
		{"473142857", 473}, // UHF 13, as channel files write it
		{"509000000", 509}, // the whole megahertz
		{"767142857", 767}, // UHF 62
		{"111142857", 111}, // C13
		{"557143", 557},    // kHz
	} {
		rec := newRecorder(t)
		hardware := newTestHardware(rec)
		front := &frontend{hardware: hardware, packets: newPacketizer()}
		if err := front.Tune(map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": test.frequency}); err != nil {
			t.Fatalf("%s: %v", test.frequency, err)
		}
		code := test.mhz * tunerStepsPerMHz
		low := writeOp(demodAddress, []byte{0xfe, tunerAddress, tunerFreqLow, byte(code)})
		high := writeOp(demodAddress, []byte{0xfe, tunerAddress, tunerFreqHigh, byte(code >> 8)})
		if !slices.Contains(rec.ops, low) || !slices.Contains(rec.ops, high) {
			t.Fatalf("%s: the tuner was not given %d MHz: %q", test.frequency, test.mhz, rec.ops)
		}
	}
}

func TestTuneRefuses(t *testing.T) {
	rec := newRecorder(t)
	front := &frontend{hardware: newTestHardware(rec), packets: newPacketizer()}
	if err := front.Tune(map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480"}); err == nil {
		t.Fatal("tuned a satellite channel")
	}
	if err := front.Tune(map[string]string{"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "900000000"}); err == nil {
		t.Fatal("tuned 900 MHz")
	}
}

// TestPowerUpRefusesAnotherVariant leaves the board alone when the card
// interface is not the DES variant: its stream could not be undone.
func TestPowerUpRefusesAnotherVariant(t *testing.T) {
	rec := newRecorder(t)
	hardware := newTestHardware(rec)
	hardware.cardBus = func(address byte) asie5606.Bus { return otherVariant{fakeCard{rec: rec, address: address}} }
	if err := hardware.powerUp(); err == nil {
		t.Fatal("powered up with a card interface of another variant")
	}
}

type otherVariant struct{ fakeCard }

func (card otherVariant) Read(register byte, count int) ([]byte, error) {
	if card.address == 0x4a && register == cardIdentity {
		return []byte{0x07}, nil
	}
	return card.fakeCard.Read(register, count)
}
