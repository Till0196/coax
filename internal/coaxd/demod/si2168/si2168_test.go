// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package si2168

import (
	"bytes"
	"testing"
)

// fakeBus records writes and answers every read with a "command accepted"
// status byte and zeroes, enough for command sequences whose responses are not
// inspected.
type fakeBus struct {
	writes [][]byte
}

func (bus *fakeBus) Write(_ byte, data []byte) error {
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *fakeBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	response[0] = 0x80 // ready bit set, no error bit
	return response, nil
}

func TestFirmwareForRevision(t *testing.T) {
	tests := []struct {
		revision []byte
		want     string
	}{
		{[]byte{0x80, 'A', 68, '2', '0'}, "dvb-demod-si2168-a20-01.fw"},
		{[]byte{0x80, 'B', 68, '4', '0'}, "dvb-demod-si2168-b40-01.fw"},
		{[]byte{0x80, 'D', 68, '6', '0'}, "dvb-demod-si2168-d60-01.fw"},
	}
	for _, test := range tests {
		got, err := firmwareForRevision(test.revision)
		if err != nil || got != test.want {
			t.Fatalf("firmwareForRevision(%v) = %q, %v; want %q", test.revision, got, err, test.want)
		}
	}
}

func TestDeliveryAndBandwidth(t *testing.T) {
	if code, err := deliveryCode("DVB-C/ANNEX_A"); err != nil || code != 0x30 {
		t.Fatalf("deliveryCode = %#x, %v", code, err)
	}
	if code, err := bandwidthCode(8_000_000); err != nil || code != 8 {
		t.Fatalf("bandwidthCode = %#x, %v", code, err)
	}
}

// Annex A, Annex C and ISDB-C are one mode, which is what lets a Japanese CATV
// channel file work.
func TestEveryJ83AnnexACSpellingReachesTheOneDVBCMode(t *testing.T) {
	for _, delivery := range []string{"DVB-C", "DVB-C/ANNEX_A", "DVB-C/ANNEX_C", "ISDB-C"} {
		code, err := deliveryCode(delivery)
		if err != nil {
			t.Errorf("deliveryCode(%q): %v", delivery, err)
			continue
		}
		if code != 0x30 {
			t.Errorf("deliveryCode(%q) = %#x, want 0x30", delivery, code)
		}
	}
}

// A channel tuned as ISDB-C must also be readable, or its lock is never seen.
func TestStatusAcceptsEveryJ83AnnexACSpelling(t *testing.T) {
	for _, delivery := range []string{"DVB-C", "DVB-C/ANNEX_C", "ISDB-C"} {
		bus := &fakeBus{}
		demod := New(bus, "", false)
		demod.system = delivery
		if _, err := demod.Status(); err != nil {
			t.Errorf("Status (%q): %v", delivery, err)
		}
	}
}

// The chip returns the CNR in the same response as the lock state, in
// quarter-decibels, and only while locked.
func TestStatusReadsTheCNRFromTheLockResponse(t *testing.T) {
	locked := &statusBus{state: 3, cnrQuarterDB: 114} // 28.5 dB
	demod := New(locked, "", false)
	demod.system = "DVB-T"
	status, err := demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Locked {
		t.Fatal("a state of 3 should be reported as locked")
	}
	if !status.HasCNR || status.CNRdB != 28.5 {
		t.Fatalf("CNR = %v dB (measured=%v), want 28.5 dB measured", status.CNRdB, status.HasCNR)
	}

	// The chip stops measuring without a lock, so a stale value must not be
	// presented as a reading.
	unlocked := &statusBus{state: 1, cnrQuarterDB: 114}
	demod = New(unlocked, "", false)
	demod.system = "DVB-T"
	status, err = demod.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.HasCNR {
		t.Fatalf("an unlocked demodulator reported a CNR of %v dB", status.CNRdB)
	}
}

// statusBus answers a status query with a chosen lock state and CNR.
type statusBus struct {
	state        byte
	cnrQuarterDB byte
}

func (bus *statusBus) Write(byte, []byte) error { return nil }

func (bus *statusBus) Read(_ byte, length int) ([]byte, error) {
	response := make([]byte, length)
	response[0] = 0x80 // ready bit set, no error bit
	if length >= 4 {
		response[2] = bus.state << 1
		response[3] = bus.cnrQuarterDB
	}
	return response, nil
}

// Annex B is not the DVB-C mode the other annexes share; it is MCNS.
func TestAnnexBIsItsOwnMode(t *testing.T) {
	code, err := deliveryCode("DVB-C/ANNEX_B")
	if err != nil || code != 0x10 {
		t.Fatalf("deliveryCode(\"DVB-C/ANNEX_B\") = %#x, %v; want 0x10", code, err)
	}
}

// The three things an Annex B lock needs: the MCNS mode code, the 6MHz width
// the annex fixes whatever the file says, and the symbol rate going to the MCNS
// property (0x1602) rather than DVB-C's (0x1102).
func TestTuneAnnexB(t *testing.T) {
	tests := []struct {
		name       string
		parameters Params
		wantRateKS uint16
	}{
		{
			name:       "256-QAM fixes the rate at 5.361 Msym/s",
			parameters: Params{DeliverySystem: "DVB-C/ANNEX_B", Modulation: "QAM/256", BandwidthHz: 8_000_000},
			wantRateKS: 5361,
		},
		{
			name:       "64-QAM fixes it at 5.057",
			parameters: Params{DeliverySystem: "DVB-C/ANNEX_B", Modulation: "QAM/64"},
			wantRateKS: 5057,
		},
		{
			// An explicit rate is taken as meant: a 64-QAM file without
			// MODULATION still works.
			name:       "an unnamed modulation falls back to the file's rate",
			parameters: Params{DeliverySystem: "DVB-C/ANNEX_B", SymbolRate: 5_057_000},
			wantRateKS: 5057,
		},
		{
			// The optical distribution of the 124/128 degree east service is
			// 256-QAM at 5.6 Msym/s. Naming the constellation must not put it
			// back at 5.361: it never locked there (a MyGica T230 on such a
			// line, 112.75 MHz).
			name: "an explicit rate wins over the constellation",
			parameters: Params{
				DeliverySystem: "DVB-C/ANNEX_B",
				Modulation:     "QAM/256",
				SymbolRate:     5_600_000,
			},
			wantRateKS: 5600,
		},
		{
			// The LG demodulators these files were written for detect the
			// constellation themselves; this chip cannot, so AUTO becomes the
			// 256-QAM digital cable actually carries.
			name:       "QAM/AUTO is assumed to be 256-QAM",
			parameters: Params{DeliverySystem: "DVB-C/ANNEX_B", Modulation: "QAM/AUTO"},
			wantRateKS: 5361,
		},
		{
			name:       "so is a channel that names no modulation at all",
			parameters: Params{DeliverySystem: "DVB-C/ANNEX_B"},
			wantRateKS: 5361,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := &fakeBus{}
			demod := New(bus, "", false)
			test.parameters.StreamID = -1
			if err := demod.Tune(test.parameters); err != nil {
				t.Fatal(err)
			}
			// The mode property carries the delivery code and bandwidth in one
			// byte: MCNS at 6MHz, never the 8MHz the first case asks for.
			wantMode := []byte{0x14, 0, 0x0a, 0x10, 0x10 | 0x06, 0}
			wantRate := []byte{0x14, 0, 0x02, 0x16, byte(test.wantRateKS), byte(test.wantRateKS >> 8)}
			if !containsWrite(bus.writes, wantMode) {
				t.Errorf("no mode write %x in %x", wantMode, bus.writes)
			}
			if !containsWrite(bus.writes, wantRate) {
				t.Errorf("no MCNS symbol rate write %x in %x", wantRate, bus.writes)
			}
			for _, write := range bus.writes {
				if len(write) >= 4 && write[0] == 0x14 && write[2] == 0x02 && write[3] == 0x11 {
					t.Errorf("Annex B wrote the DVB-C symbol rate property: %x", write)
				}
			}
		})
	}
}

// The other half of the mode split: Annex B answers a different command from
// DVB-C's, and asking the wrong one leaves a locked channel looking dead.
func TestStatusUsesTheAnnexBQuery(t *testing.T) {
	bus := &fakeBus{}
	demod := New(bus, "", false)
	demod.system = "DVB-C/ANNEX_B"
	if _, err := demod.Status(); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x98, 1}
	if len(bus.writes) != 1 || !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("writes = %x; want [%x]", bus.writes, want)
	}
}

func containsWrite(writes [][]byte, want []byte) bool {
	for _, write := range writes {
		if bytes.Equal(write, want) {
			return true
		}
	}
	return false
}

func TestSetTSBusPlain(t *testing.T) {
	bus := &fakeBus{}
	demod := New(bus, "", false)
	if err := demod.setTSBus(true); err != nil {
		t.Fatal(err)
	}
	// 0x10 is the base of the field, plus 0x06 for a parallel bus.
	want := []byte{0x14, 0, 0x01, 0x10, 0x16, 0}
	if len(bus.writes) != 1 || !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("writes = %#v; want [%x]", bus.writes, want)
	}
	bus.writes = nil
	if err := demod.setTSBus(false); err != nil {
		t.Fatal(err)
	}
	// Parking the bus leaves the base alone and adds tristate, which is zero.
	want = []byte{0x14, 0, 0x01, 0x10, 0x10, 0}
	if len(bus.writes) != 1 || !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("writes = %#v; want [%x]", bus.writes, want)
	}
}

func TestSetTSBusClockManual(t *testing.T) {
	bus := &fakeBus{}
	demod := New(bus, "", true)
	if err := demod.setTSBus(true); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{
		{0x14, 0, 0x0d, 0x10, 0xe8, 0x03},
		{0x14, 0, 0x01, 0x10, 0x26, 0},
	}
	if len(bus.writes) != len(want) {
		t.Fatalf("writes = %#v; want %#v", bus.writes, want)
	}
	for index := range want {
		if !bytes.Equal(bus.writes[index], want[index]) {
			t.Fatalf("write %d = %x; want %x", index, bus.writes[index], want[index])
		}
	}
	bus.writes = nil
	if err := demod.setTSBus(false); err != nil {
		t.Fatal(err)
	}
	want = [][]byte{
		{0x14, 0, 0x0d, 0x10, 0xe8, 0x03},
		{0x14, 0, 0x01, 0x10, 0x20, 0},
	}
	for index := range want {
		if !bytes.Equal(bus.writes[index], want[index]) {
			t.Fatalf("write %d = %x; want %x", index, bus.writes[index], want[index])
		}
	}
}

func TestSupports(t *testing.T) {
	for delivery, want := range map[string]bool{
		"DVB-T": true, "DVB-T2": true, "DVB-C/ANNEX_A": true,
		// Japanese CATV is J.83 Annex C, which differs from Annex A only in
		// roll-off, so the Si2168's one DVB-C mode receives it. ISDB-C is that
		// same system under its Japanese name.
		"DVB-C/ANNEX_C": true, "ISDB-C": true,
		// Annex B's trellis coding is a different system, which the chip has
		// its own mode for: MCNS. ISDB-T is not J.83 at all.
		"DVB-C/ANNEX_B": true, "ISDB-T": false,
	} {
		if got := Supports(delivery); got != want {
			t.Errorf("Supports(%q) = %v; want %v", delivery, got, want)
		}
	}
}
