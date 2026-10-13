// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package mb86a20s

import (
	"bytes"
	"testing"
)

// fakeBus records writes and answers reads from a register file, keeping the
// repeated-start form a register read has to take.
type fakeBus struct {
	writes    [][]byte
	registers map[byte]byte
	pointer   byte
}

func newFakeBus() *fakeBus {
	return &fakeBus{registers: map[byte]byte{}}
}

func (bus *fakeBus) Write(address byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	if len(data) == 2 {
		bus.registers[data[0]] = data[1]
	}
	if len(data) == 1 {
		bus.pointer = data[0]
	}
	return nil
}

func (bus *fakeBus) Read(address byte, length int) ([]byte, error) {
	return []byte{bus.registers[bus.pointer]}, nil
}

func (bus *fakeBus) WriteRead(address byte, write []byte, readLength int) ([]byte, error) {
	if len(write) == 1 {
		bus.pointer = write[0]
	}
	return []byte{bus.registers[bus.pointer]}, nil
}

func TestOpenTunerGateIsActiveLow(t *testing.T) {
	bus := newFakeBus()
	demod := New(bus, A20S)
	if err := demod.OpenTunerGate(true); err != nil {
		t.Fatal(err)
	}
	if err := demod.OpenTunerGate(false); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{{registerTunerGate, 0}, {registerTunerGate, 1}}
	if len(bus.writes) != 2 {
		t.Fatalf("writes = %#v", bus.writes)
	}
	for index := range want {
		if !bytes.Equal(bus.writes[index], want[index]) {
			t.Errorf("write %d = %x; want %x", index, bus.writes[index], want[index])
		}
	}
}

func TestLockedUsesSequenceState(t *testing.T) {
	bus := newFakeBus()
	demod := New(bus, A20S)
	for state, want := range map[byte]bool{0: false, 4: false, 8: false, 9: true, 15: true} {
		bus.registers[registerSequenceState] = state
		locked, err := demod.Locked()
		if err != nil {
			t.Fatal(err)
		}
		if locked != want {
			t.Errorf("sequence state %d: locked = %v; want %v", state, locked, want)
		}
	}
}

func TestCNRNeedsCarrierAndFlag(t *testing.T) {
	bus := newFakeBus()
	demod := New(bus, A20S)

	// Below the carrier state there is nothing to report.
	bus.registers[registerSequenceState] = 3
	if cnr, err := demod.CNR(); err != nil || cnr != 0 {
		t.Fatalf("CNR = %d, %v; want 0", cnr, err)
	}

	// Past it, but with the ready flag clear, still nothing.
	bus.registers[registerSequenceState] = 9
	bus.registers[registerCNControl] = 0x00
	if cnr, err := demod.CNR(); err != nil || cnr != 0 {
		t.Fatalf("CNR = %d, %v; want 0", cnr, err)
	}

	// With the flag set the counter is interpolated.
	bus.registers[registerCNControl] = 0x40
	bus.registers[registerCNHigh] = 0x07
	bus.registers[registerCNLow] = 0xd0 // counter 2000
	cnr, err := demod.CNR()
	if err != nil {
		t.Fatal(err)
	}
	if cnr != 20160 {
		t.Fatalf("CNR = %d; want 20160", cnr)
	}
	// Reading restarts the counter so the next one is fresh.
	last := bus.writes[len(bus.writes)-1]
	if !bytes.Equal(last, []byte{registerCNControl, 0x40 & 0x6f}) {
		t.Errorf("counter was not restarted: last write = %x", last)
	}
}

// The values below pin the interpolation, including its truncation toward
// zero, so the curve cannot drift.
func TestCNRInterpolation(t *testing.T) {
	for _, test := range []struct {
		counter uint32
		want    uint32
	}{
		{19648, 0}, {19000, 444}, {18187, 1000}, {16534, 2000}, {13161, 4000},
		{11622, 5000}, {11000, 5464}, {8000, 8047}, {5000, 12054}, {3000, 16531},
		{788, 30000}, {600, 30000}, {100, 30000},
	} {
		if got := interpolate(test.counter, cnrToDB); got != test.want {
			t.Errorf("interpolate(%d) = %d; want %d", test.counter, got, test.want)
		}
	}
}

func TestRegisterTablesAreWellFormed(t *testing.T) {
	tables := map[string][][2]byte{
		"initA20S": initA20S, "resetA20S": resetA20S,
		"initA21S": initA21S, "resetA21S": resetA21S,
	}
	for name, table := range tables {
		if len(table) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	// The register sets are a fixed size; a short table would show here.
	if len(initA20S) != 266 || len(initA21S) != 241 {
		t.Errorf("init table sizes = %d, %d; want 266, 241", len(initA20S), len(initA21S))
	}
	if len(resetA20S) != 5 || len(resetA21S) != 7 {
		t.Errorf("reset table sizes = %d, %d; want 5, 7", len(resetA20S), len(resetA21S))
	}
}

func TestInitializeRunsTheVariantTable(t *testing.T) {
	bus := newFakeBus()
	if err := New(bus, A21S).Initialize(); err != nil {
		t.Fatal(err)
	}
	if len(bus.writes) != len(initA21S) {
		t.Fatalf("wrote %d registers; want %d", len(bus.writes), len(initA21S))
	}
	first := initA21S[0]
	if !bytes.Equal(bus.writes[0], []byte{first[0], first[1]}) {
		t.Errorf("first write = %x; want %x", bus.writes[0], first)
	}
}
