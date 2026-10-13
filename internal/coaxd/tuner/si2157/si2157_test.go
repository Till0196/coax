// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package si2157

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// scriptedBus answers reads from a list of status bytes, one per read, the last
// repeating once the list runs out. Writes are recorded, or all fail with
// writeErr.
type scriptedBus struct {
	statuses []byte
	reads    int
	writes   [][]byte
	writeErr error
}

func (bus *scriptedBus) Write(address byte, data []byte) error {
	if bus.writeErr != nil {
		return bus.writeErr
	}
	bus.writes = append(bus.writes, append([]byte(nil), data...))
	return nil
}

func (bus *scriptedBus) Read(address byte, length int) ([]byte, error) {
	response := make([]byte, length)
	if bus.reads < len(bus.statuses) {
		response[0] = bus.statuses[bus.reads]
	} else if len(bus.statuses) != 0 {
		response[0] = bus.statuses[len(bus.statuses)-1]
	}
	bus.reads++
	return response, nil
}

// TestCommandWaitsForReadyBeforeReadingTheErrorBit: until the ready bit is set
// the error bit still describes whatever ran before.
func TestCommandWaitsForReadyBeforeReadingTheErrorBit(t *testing.T) {
	bus := &scriptedBus{statuses: []byte{
		0x40, // error bit set, not ready yet: belongs to the previous command
		0x00, // still working
		0x80, // ready, no error
	}}
	tuner := New(bus, "", Config{Variant: Si2141})
	response, err := tuner.command([]byte{0x11}, 1)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if response[0] != 0x80 {
		t.Fatalf("status = %#02x, want 0x80", response[0])
	}
	if bus.reads != 3 {
		t.Fatalf("the status was read %d times, want 3", bus.reads)
	}
}

// TestCommandReportsAnErrorOnceReady: an error bit arriving together with the
// ready bit is this command's, and is reported.
func TestCommandReportsAnErrorOnceReady(t *testing.T) {
	bus := &scriptedBus{statuses: []byte{0xc0}}
	tuner := New(bus, "", Config{Variant: Si2141})
	_, err := tuner.command([]byte{0xc0, 8, 1, 2, 0, 0, 1}, 1)
	if err == nil {
		t.Fatal("a status of 0xc0 should be reported as a refusal")
	}
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("error = %v, want it to wrap ErrRejected", err)
	}
	// The message must name the command, or a caller cannot tell which step of a
	// long bring-up failed.
	if want := "0xc0"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name the command opcode %s", err, want)
	}
}

// TestCommandTimesOutWithoutReady: a chip that never finishes is reported as a
// timeout rather than hanging.
func TestCommandTimesOutWithoutReady(t *testing.T) {
	bus := &scriptedBus{statuses: []byte{0x00}}
	tuner := New(bus, "", Config{Variant: Si2141})
	_, err := tuner.command([]byte{0x11}, 1)
	if err == nil {
		t.Fatal("a chip that never becomes ready should time out")
	}
	if errors.Is(err, ErrRejected) {
		t.Fatalf("a timeout should not be reported as a refusal: %v", err)
	}
}

// TestInitializeToleratesTheRefusedPowerUp: the Si2141 raises its error bit on
// the power-up command even when it accepts it.
func TestInitializeToleratesTheRefusedPowerUp(t *testing.T) {
	// Every command is answered ready-with-error, so both the power-up and the
	// wake-up after it are refused; failing at the wake-up is what shows the
	// power-up's refusal was tolerated.
	bus := &scriptedBus{statuses: []byte{0xc0}}
	tuner := New(bus, "", Config{Variant: Si2141})
	err := tuner.Initialize()
	if err == nil {
		t.Fatal("initialisation should fail when every command is refused")
	}
	if !strings.Contains(err.Error(), "wake the Si2141") {
		t.Fatalf("initialisation failed at %q, want it to have got as far as the wake-up", err)
	}
}

// TestInitializeDoesNotSwallowATransportFailure: a chip refusing a command and
// a dead bus are not the same thing, and only the first may be ignored.
func TestInitializeDoesNotSwallowATransportFailure(t *testing.T) {
	failure := errors.New("the USB device is gone")
	bus := &scriptedBus{writeErr: failure}
	tuner := New(bus, "", Config{Variant: Si2141})
	err := tuner.Initialize()
	if err == nil {
		t.Fatal("initialisation should fail when the bus does not work")
	}
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap the transport failure", err)
	}
	if !strings.Contains(err.Error(), "power up the Si2141") {
		t.Fatalf("error %q does not say which step could not reach the chip", err)
	}
}

// TestInitializeExplainsAMissingFirmwareOnAFailedBoot: the tuner refuses to
// start because there was no firmware to start. Firmware is optional, so the
// error message is the only place this can be said.
func TestInitializeExplainsAMissingFirmwareOnAFailedBoot(t *testing.T) {
	bus := &revisionBus{scriptedBus: scriptedBus{statuses: []byte{0x80}}, failOpcode: 0x01}
	tuner := New(bus, t.TempDir(), Config{Variant: Si2141})
	err := tuner.Initialize()
	if err == nil {
		t.Fatal("initialisation should fail when the boot command is refused")
	}
	for _, want := range []string{
		"no firmware was found",
		"dvb_driver_si2141_rom60.fw",
		"dvb-tuner-si2141-a10-01.fw",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// The underlying refusal must still be visible and still be classifiable.
	if !errors.Is(err, ErrRejected) {
		t.Errorf("error = %v, want it to still wrap ErrRejected", err)
	}
}

// revisionBus answers the revision query with a plausible identity, keeps the
// warm-tuner short circuit from firing, and refuses one chosen opcode.
type revisionBus struct {
	scriptedBus
	partID     byte
	failOpcode byte
	lastOpcode byte
}

func (bus *revisionBus) Write(address byte, data []byte) error {
	if len(data) != 0 {
		bus.lastOpcode = data[0]
	}
	return bus.scriptedBus.Write(address, data)
}

func (bus *revisionBus) Read(address byte, length int) ([]byte, error) {
	response := make([]byte, length)
	response[0] = 0x80
	if bus.lastOpcode == bus.failOpcode {
		response[0] = 0xc0
	}
	if bus.lastOpcode == 0x02 && length >= 13 {
		partID := bus.partID
		if partID == 0 {
			partID = 41 // Si2141
		}
		response[2] = partID
		response[12] = 0x60 // ROM revision
	}
	if bus.lastOpcode == 0x15 && length >= 4 {
		// A trim of 0xffff keeps the warm path from short-circuiting.
		response[2], response[3] = 0xff, 0xff
	}
	return response, nil
}

// TestSi2157PowerUpIsItsOwnSequence: the Si2157 takes a longer power-up command
// and, unlike the Si2141, must not be sent the wake-up that follows it.
func TestSi2157PowerUpIsItsOwnSequence(t *testing.T) {
	bus := &revisionBus{scriptedBus: scriptedBus{statuses: []byte{0x80}}, partID: 57}
	tuner := New(bus, t.TempDir(), Config{Variant: Si2157})
	if err := tuner.powerUp(); err != nil {
		t.Fatalf("powerUp: %v", err)
	}
	if len(bus.writes) != 1 {
		t.Fatalf("the Si2157 sent %d commands, want only the power-up", len(bus.writes))
	}
	want := []byte{0xc0, 0, 0x0c, 0, 0, 1, 1, 1, 1, 1, 1, 2, 0, 0, 1}
	if !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("power-up = %#v, want %#v", bus.writes[0], want)
	}
}

// TestSi2157PowerUpRefusalIsFatal: the tolerated refusal is an Si2141 quirk, so
// an Si2157 that refuses its power-up has really failed.
func TestSi2157PowerUpRefusalIsFatal(t *testing.T) {
	bus := &scriptedBus{statuses: []byte{0xc0}}
	tuner := New(bus, "", Config{Variant: Si2157})
	err := tuner.powerUp()
	if err == nil {
		t.Fatal("a refused Si2157 power-up should fail")
	}
	if !strings.Contains(err.Error(), "power up the Si2157") {
		t.Fatalf("error %q should name the power-up as the step that failed", err)
	}
}

// TestInitializeChecksThePartAgainstTheVariant: the two parts answer the same
// commands, so only the revision query tells them apart.
func TestInitializeChecksThePartAgainstTheVariant(t *testing.T) {
	bus := &revisionBus{scriptedBus: scriptedBus{statuses: []byte{0x80}}, partID: 41}
	tuner := New(bus, t.TempDir(), Config{Variant: Si2157})
	err := tuner.Initialize()
	if err == nil {
		t.Fatal("an Si2141 answering an Si2157 driver should be rejected")
	}
	if !strings.Contains(err.Error(), "expected Si2157, found Si2141") {
		t.Fatalf("error %q does not say which part was found", err)
	}
}

// TestTuneUsesTheConfiguredIFPort: the T230C family feeds the demodulator from
// port 0 and the plain T230 from port 1; the wrong one produces silence rather
// than an error.
func TestTuneUsesTheConfiguredIFPort(t *testing.T) {
	for _, test := range []struct {
		name   string
		config Config
		want   byte
	}{
		{"T230C family", Config{Variant: Si2141, IFPort: 0}, 0},
		{"plain T230", Config{Variant: Si2157, IFPort: 1}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			bus := &scriptedBus{statuses: []byte{0x81}}
			tuner := New(bus, "", test.config)
			if err := tuner.Tune(473_000_000, 6_000_000, "DVBT"); err != nil {
				t.Fatalf("Tune: %v", err)
			}
			for _, write := range bus.writes {
				if len(write) == 6 && write[0] == 0x14 && write[2] == 2 && write[3] == 7 {
					if write[4] != test.want {
						t.Fatalf("IF port = %d, want %d", write[4], test.want)
					}
					return
				}
			}
			t.Fatal("Tune never set the IF port")
		})
	}
}

// TestFirmwareNamesFollowTheVariant pins the file names each part boots from;
// the fallback names are not derivable from one another.
func TestFirmwareNamesFollowTheVariant(t *testing.T) {
	for _, test := range []struct {
		variant Variant
		romID   byte
		want    []string
	}{
		{Si2141, 0x60, []string{"dvb_driver_si2141_rom60.fw", "dvb-tuner-si2141-a10-01.fw"}},
		{Si2141, 0x61, []string{"dvb_driver_si2141_rom61.fw", "dvb-tuner-si2141-a10-01.fw"}},
		{Si2157, 0x50, []string{"dvb_driver_si2157_rom50.fw", "dvb-tuner-si2157-a30-01.fw"}},
	} {
		names := firmwareNames(test.variant, test.romID)
		if len(names) != len(test.want) {
			t.Fatalf("%s ROM %#02x: got %v, want %v", test.variant, test.romID, names, test.want)
		}
		for index, want := range test.want {
			if names[index] != want {
				t.Errorf("%s ROM %#02x: name %d = %q, want %q", test.variant, test.romID, index, names[index], want)
			}
		}
	}
}

// TestTuneSelectsTheQAMModeForEveryJ83AnnexACSpelling: Annex C and ISDB-C have
// to reach the same 0x30 mode as Annex A, or the tuner filters the channel as if
// it were terrestrial. Terrestrial systems are the negative case, staying 0x20.
func TestTuneSelectsTheQAMModeForEveryJ83AnnexACSpelling(t *testing.T) {
	for delivery, want := range map[string]byte{
		"DVB-C":         0x30,
		"DVB-C/ANNEX_A": 0x30,
		"DVB-C/ANNEX_C": 0x30,
		"ISDB-C":        0x30,
		"DVB-T":         0x20,
		"DVB-T2":        0x20,
	} {
		bus := &scriptedBus{statuses: []byte{0x81}}
		tuner := New(bus, "", Config{Variant: Si2157, IFPort: 1})
		if err := tuner.Tune(473_000_000, 6_000_000, delivery); err != nil {
			t.Errorf("Tune(%q): %v", delivery, err)
			continue
		}
		found := false
		for _, write := range bus.writes {
			if len(write) == 6 && write[0] == 0x14 && write[2] == 3 && write[3] == 7 {
				if got := write[4] & 0xf0; got != want {
					t.Errorf("Tune(%q) mode = %#02x, want %#02x", delivery, got, want)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Tune(%q) never set the delivery system", delivery)
		}
	}
}
