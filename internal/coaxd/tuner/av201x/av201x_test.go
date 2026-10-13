// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package av201x

import "testing"

// The piecewise-linear interpolation, which uses truncating (round-toward-zero)
// integer division; a floored division gets the negative segments wrong.
func TestSignalStrengthFromAGCMatchesTheReferenceCurve(t *testing.T) {
	for _, test := range []struct {
		agc          byte
		tenthsOfADBm int32
	}{
		{0, 90}, {1, 89}, {50, 6}, {81, -47}, {82, -50}, {83, -61},
		{99, -250}, {100, -263}, {110, -323}, {140, -463}, {172, -651},
		{187, -761}, {209, -856}, {223, -891}, {254, -904},
	} {
		want := float64(test.tenthsOfADBm) / 10
		got := SignalStrengthFromAGC(test.agc)
		if diff := got - want; diff < -0.05 || diff > 0.05 {
			t.Errorf("SignalStrengthFromAGC(%d) = %v, want %v", test.agc, got, want)
		}
	}
}

// The top of the curve. The search advances until a table entry exceeds the
// reading, and at 255 -- the table's own last entry -- nothing does, so the
// search must clamp to the last segment rather than run off the end.
func TestSignalStrengthFromAGCHandlesTheTopOfTheRange(t *testing.T) {
	got := SignalStrengthFromAGC(255)
	if got < -95 || got > -85 {
		t.Errorf("SignalStrengthFromAGC(255) = %v, want a value near the -91 dBm the curve is heading toward", got)
	}
}

// The two variants differ only at 0x0e/0x0f.
func TestInitializePicksTheModelTable(t *testing.T) {
	for model, want := range map[Model][2]byte{
		AV2011: {0x94, 0x9a},
		AV2012: {0x5b, 0x6a},
	} {
		bus := &recordingBus{}
		tuner := New(bus, Config{Address: 0x62, Model: model})
		if err := tuner.Initialize(); err != nil {
			t.Fatalf("Initialize(%v): %v", model, err)
		}
		got := [2]byte{bus.registers[0x0e], bus.registers[0x0f]}
		if got != want {
			t.Errorf("model %v wrote %#v at 0x0e/0x0f, want %#v", model, got, want)
		}
	}
}

// recordingBus is an i2c.Bus that remembers the last value written to each
// register.
type recordingBus struct {
	registers map[byte]byte
}

func (bus *recordingBus) Write(_ byte, data []byte) error {
	if bus.registers == nil {
		bus.registers = map[byte]byte{}
	}
	for offset := 0; offset+1 < len(data); offset += 2 {
		bus.registers[data[offset]] = data[offset+1]
	}
	return nil
}

func (bus *recordingBus) Read(_ byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}

// The frequency-to-register conversion.
func TestTunePLLMatchesTheReferenceFormula(t *testing.T) {
	for _, test := range []struct {
		frequencyHz uint32
		want        [5]byte
	}{
		{950_000_000, [5]byte{0x00, 0x23, 0x2f, 0x68, 0xd0}},
		{1_049_000_000, [5]byte{0x00, 0x27, 0xda, 0x13, 0x50}},
		{1_200_000_000, [5]byte{0x00, 0x2c, 0x71, 0xc7, 0x50}},
		{1_500_000_000, [5]byte{0x00, 0x38, 0x8e, 0x39, 0x50}},
	} {
		bus := &sequenceBus{}
		tuner := New(bus, Config{Address: 0x62, XtalKHz: 27000})
		if err := tuner.Tune(test.frequencyHz, 22_000_000); err != nil {
			t.Fatalf("Tune(%d): %v", test.frequencyHz, err)
		}
		if len(bus.writes) == 0 || len(bus.writes[0]) != 5 {
			t.Fatalf("Tune(%d) did not write a 5-byte PLL command", test.frequencyHz)
		}
		var got [5]byte
		copy(got[:], bus.writes[0])
		if got != test.want {
			t.Errorf("Tune(%d) PLL = %#v, want %#v", test.frequencyHz, got, test.want)
		}
	}
}

// The symbol-rate-derived channel filter setting, including the 40 MHz ceiling.
func TestTuneBandwidthMatchesTheReferenceFormula(t *testing.T) {
	for _, test := range []struct {
		symbolRateSPS uint32
		want          byte
	}{
		{1_000_000, 0x38},
		{5_000_000, 0x4a},
		{22_000_000, 0x6e},
		{45_000_000, 0xd2}, // clamps to the 40 MHz ceiling
		{3_000_000, 0x41},
	} {
		bus := &sequenceBus{}
		tuner := New(bus, Config{Address: 0x62, XtalKHz: 27000})
		if err := tuner.Tune(1_049_000_000, test.symbolRateSPS); err != nil {
			t.Fatalf("Tune at %d sym/s: %v", test.symbolRateSPS, err)
		}
		var got byte
		found := false
		for _, write := range bus.writes {
			if len(write) == 2 && write[0] == registerBandwidth {
				got = write[1]
				found = true
			}
		}
		if !found {
			t.Fatalf("Tune at %d sym/s never wrote the bandwidth register", test.symbolRateSPS)
		}
		if got != test.want {
			t.Errorf("Tune at %d sym/s: bandwidth = %#02x, want %#02x", test.symbolRateSPS, got, test.want)
		}
	}
}

// sequenceBus records every write in order, for tests that need to find one
// particular command among several.
type sequenceBus struct {
	writes [][]byte
}

func (bus *sequenceBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *sequenceBus) Read(_ byte, length int) ([]byte, error) {
	return make([]byte, length), nil
}
