// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package rt710

import "testing"

func TestReverseBits(t *testing.T) {
	t.Parallel()
	for value := 0; value < 256; value++ {
		if got := reverseBits(reverseBits(byte(value))); got != byte(value) {
			t.Fatalf("reverseBits is not its own inverse at %#02x", value)
		}
	}
	if got := reverseBits(0x01); got != 0x80 {
		t.Errorf("reverseBits(0x01) = %#02x; want 0x80", got)
	}
}

func TestBandwidthTableIsOrdered(t *testing.T) {
	t.Parallel()
	// The lookup takes the first entry at or above the required bandwidth, so
	// the table has to rise monotonically.
	for index := 1; index < len(bandwidthTable); index++ {
		if bandwidthTable[index].bandwidth <= bandwidthTable[index-1].bandwidth {
			t.Fatalf("bandwidth table is not ascending at index %d", index)
		}
	}
}

func TestBandwidthCodesRT710(t *testing.T) {
	t.Parallel()
	tuner := &Tuner{chip: ChipRT710}
	// The ISDB-S carrier: 28860 kBaud at roll-off index 4 gives 389.6 MHz,
	// above the table's range, so the wide-channel formula applies.
	const symbolRate, rolloff = 28860, 4
	bandwidth := uint32(symbolRate) * (115 + rolloff*5) / 10
	if bandwidth < 380000 {
		t.Fatalf("expected the ISDB-S bandwidth %d to exceed the table range", bandwidth)
	}
	params := tuner.bandwidthCodes(bandwidth, symbolRate, rolloff)
	if params.fine != 1 {
		t.Errorf("fine = %d; want 1 for a wide channel", params.fine)
	}
	if params.coarse < 0x10 {
		t.Errorf("coarse = %#02x; want at least 0x10 for a wide channel", params.coarse)
	}

	// A narrow channel comes straight out of the table.
	narrow := tuner.bandwidthCodes(50000, 5000, 0)
	if narrow != bandwidthTable[0].params {
		t.Errorf("narrow codes = %+v; want %+v", narrow, bandwidthTable[0].params)
	}
}

func TestGainStepRT710(t *testing.T) {
	t.Parallel()
	tuner := &Tuner{chip: ChipRT710}
	// The raw field splits the step across the top nibble and bit 0.
	for raw, want := range map[byte]byte{
		0x00: 0,  // step 0
		0x20: 0,  // step 2
		0x30: 1,  // step 3
		0x90: 7,  // step 9
		0xc0: 7,  // step 12 saturates to 7
		0xd0: 8,  // step 13
		0xf1: 18, // step 31 saturates to 18
	} {
		if got := tuner.gainStep(raw); got != want {
			t.Errorf("gainStep(%#02x) = %d; want %d", raw, got, want)
		}
	}
	// Every step the folding can produce must index the gain table.
	for raw := 0; raw < 256; raw++ {
		if int(tuner.gainStep(byte(raw))) >= len(rt710LNAGain) {
			t.Fatalf("gainStep(%#02x) is outside the RT710 gain table", raw)
		}
	}
}

func TestGainStepRT720(t *testing.T) {
	t.Parallel()
	tuner := &Tuner{chip: ChipRT720}
	// The RT720 uses the raw step directly, and it must stay in its table.
	for raw := 0; raw < 256; raw++ {
		if int(tuner.gainStep(byte(raw))) >= len(rt720LNAGain) {
			t.Fatalf("gainStep(%#02x) is outside the RT720 gain table", raw)
		}
	}
}
