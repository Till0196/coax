// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package si2183

import "testing"

// scriptedBus answers reads from a list of status bytes, one per read, and
// repeats the last one once the list runs out.
type scriptedBus struct {
	statuses []byte
	reads    int
	writes   [][]byte
}

func (bus *scriptedBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *scriptedBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	if bus.reads < len(bus.statuses) {
		response[0] = bus.statuses[bus.reads]
	} else if len(bus.statuses) != 0 {
		response[0] = bus.statuses[len(bus.statuses)-1]
	}
	bus.reads++
	return response, nil
}

func TestCommandWaitsForReadyBeforeReadingTheErrorBit(t *testing.T) {
	bus := &scriptedBus{statuses: []byte{0x40, 0x00, 0x80}}
	demod := New(bus, "", Config{})
	response, err := demod.command([]byte{0x11}, 1)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if response[0] != 0x80 {
		t.Fatalf("status = %#02x, want 0x80", response[0])
	}
}

func TestCommandReportsAnErrorOnceReady(t *testing.T) {
	bus := &scriptedBus{statuses: []byte{0xc0}}
	demod := New(bus, "", Config{})
	_, err := demod.command([]byte{0x02}, 13)
	if err == nil {
		t.Fatal("a status of 0xc0 should be reported as a refusal")
	}
}

// The delivery-system codes for the chip's MODE property, in both spellings.
func TestModeValueCoversEverySystemTheBoardsUse(t *testing.T) {
	for delivery, want := range map[string]uint16{
		"DVBT": 0x20, "DVB-T": 0x20,
		"DVBT2": 0x70, "DVB-T2": 0x70,
		"ISDBT": 0x40, "ISDB-T": 0x40,
		"DVBC": 0x38, "DVB-C": 0x38, "DVBC/ANNEXA": 0x38,
		"DVBC/ANNEXB": 0x18,
		"DVBS":        0x88,
		"DVBS2":       0x98,
	} {
		got, err := modeValue(delivery)
		if err != nil {
			t.Errorf("modeValue(%q): %v", delivery, err)
			continue
		}
		if got != want {
			t.Errorf("modeValue(%q) = %#02x, want %#02x", delivery, got, want)
		}
	}
	if _, err := modeValue("ATSC"); err == nil {
		t.Error("modeValue(\"ATSC\") should fail: the Si2183 has no ATSC mode")
	}
}

// The terrestrial bandwidth-to-code mapping, including the "everything above
// 10 MHz" case that folds into one code.
func TestBandwidthCodeMatchesTheReferenceThresholds(t *testing.T) {
	for bandwidthHz, want := range map[uint32]byte{
		1_700_000: 0x02, 5_000_000: 0x05, 6_000_000: 0x06,
		7_000_000: 0x07, 8_000_000: 0x08, 9_000_000: 0x09,
		10_000_000: 0x0a, 12_000_000: 0x0f,
	} {
		got, err := bandwidthCode(bandwidthHz)
		if err != nil {
			t.Errorf("bandwidthCode(%d): %v", bandwidthHz, err)
			continue
		}
		if got != want {
			t.Errorf("bandwidthCode(%d) = %#02x, want %#02x", bandwidthHz, got, want)
		}
	}
	if _, err := bandwidthCode(0); err == nil {
		t.Error("bandwidthCode(0) should fail: BANDWIDTH_HZ is required")
	}
}

// Tune must open with the terrestrial AGC command (0x89) or the satellite one
// (0x8a) according to the delivery system: sending the wrong one is silent
// until the AGC simply never converges.
func TestTuneSetsTheAGCCommandPerPath(t *testing.T) {
	for delivery, wantOpcode := range map[string]byte{
		"DVBT": 0x89, "DVBC": 0x89, "ISDBT": 0x89,
		"DVBS": 0x8a, "DVBS2": 0x8a,
	} {
		bus := &scriptedBus{statuses: []byte{0x80}}
		demod := New(bus, "", Config{})
		err := demod.Tune(Params{
			DeliverySystem: delivery,
			BandwidthHz:    6_000_000,
			SymbolRate:     27_500_000,
			StreamID:       -1,
		})
		if err != nil {
			t.Fatalf("Tune(%s): %v", delivery, err)
		}
		if len(bus.writes) == 0 || bus.writes[0][0] != wantOpcode {
			t.Fatalf("Tune(%s): first command opcode = %#02x, want %#02x",
				delivery, bus.writes[0][0], wantOpcode)
		}
	}
}

// lockedBus answers every command as ready, and any read long enough to carry
// them as locked with a fixed CNR byte.
type lockedBus struct {
	writes [][]byte
	cnr    byte
}

func (bus *lockedBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *lockedBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	response[0] = 0x80
	if length > 2 {
		response[2] = 0x06 // state bits 2:1 = 11: locked
	}
	if length > 3 {
		response[3] = bus.cnr
	}
	return response, nil
}

// Every entry of the statusCommands table must send its own opcode and yield a
// lock; a wrong entry would silently read another system's status.
func TestStatusUsesTheRightCommandAndBankPerSystem(t *testing.T) {
	for delivery, spec := range statusCommands {
		bus := &lockedBus{cnr: 114} // 28.5 dB
		demod := New(bus, "", Config{})
		demod.system = delivery
		status, err := demod.Status()
		if err != nil {
			t.Errorf("Status() for %s: %v", delivery, err)
			continue
		}
		if !status.Locked {
			t.Errorf("Status() for %s did not report locked", delivery)
		}
		if len(bus.writes) == 0 || bus.writes[0][0] != spec.command[0] {
			t.Errorf("Status() for %s wrote opcode %#02x, want %#02x",
				delivery, bus.writes[0][0], spec.command[0])
		}
	}
}

// unlockedBus answers ready with the signal bit only, as a channel that has not
// yet locked does.
type unlockedBus struct{}

func (bus *unlockedBus) Write(byte, []byte) error { return nil }

func (bus *unlockedBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	response[0] = 0x80
	if length > 2 {
		response[2] = 0x02 // state bits 2:1 = 01: signal only
	}
	return response, nil
}

// The chip stops measuring once it loses lock, so a stale reading must not be
// presented as current. The Si2168 driver is tested the same way.
func TestStatusReadsCNROnlyWhenLocked(t *testing.T) {
	demod := New(&lockedBus{cnr: 114}, "", Config{})
	demod.system = "DVBT"
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.HasCNR || status.CNRdB != 28.5 {
		t.Fatalf("CNR = %v dB (measured=%v), want 28.5 dB measured", status.CNRdB, status.HasCNR)
	}

	demod = New(&unlockedBus{}, "", Config{})
	demod.system = "DVBT"
	status, err = demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.HasCNR {
		t.Fatal("an unlocked Si2183 should not report a CNR reading")
	}
}

// The constellation/symbol-rate pairing the annex fixes, and what an unnamed
// modulation becomes: the chip has no auto-detecting mode, unlike the LG
// demodulators whose files often say QAM/AUTO, so AUTO resolves to the 256-QAM
// digital cable carries rather than to 64-QAM.
func TestAnnexBConstellation(t *testing.T) {
	for modulation, want := range map[string][2]uint16{
		"QAM/64":   {9, 5057},
		"QAM64":    {9, 5057},
		"QAM/256":  {11, 5361},
		"QAM/AUTO": {11, 5361},
		"":         {11, 5361},
	} {
		constellation, symbolRate := annexBConstellation(modulation, 0)
		if constellation != want[0] || symbolRate != want[1] {
			t.Errorf("annexBConstellation(%q, 0) = %d, %d; want %d, %d",
				modulation, constellation, symbolRate, want[0], want[1])
		}
	}
}

// A rate the channel gives wins over the one the annex pairs with the
// constellation: Japan's optical distribution runs 256-QAM at 5.6 Msym/s.
func TestAnnexBSymbolRateIsTakenAsMeant(t *testing.T) {
	constellation, symbolRate := annexBConstellation("QAM/256", 5_600_000)
	if constellation != 11 || symbolRate != 5600 {
		t.Errorf("annexBConstellation(QAM/256, 5600000) = %d, %d; want 11, 5600", constellation, symbolRate)
	}
}

// The chip takes the gold sequence's starting state rather than its index, and
// these are the states the reference sequence generator produces.
func TestGoldSequenceSeed(t *testing.T) {
	for _, one := range []struct {
		index uint32
		seed  uint32
	}{
		{0, 1},
		{1, 131072},
		{2, 65536},
		{3, 32768},
		{4, 16384},
		{5, 8192},
	} {
		if got := goldSequenceSeed(one.index); got != one.seed {
			t.Errorf("goldSequenceSeed(%d) = %d, want %d", one.index, got, one.seed)
		}
	}
	// The register is eighteen bits, so no index may overflow it.
	for index := uint32(0); index < 300; index++ {
		if seed := goldSequenceSeed(index); seed > 0x3ffff {
			t.Fatalf("goldSequenceSeed(%d) = %#x, which does not fit eighteen bits", index, seed)
		}
	}
}
