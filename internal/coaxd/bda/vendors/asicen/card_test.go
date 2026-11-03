// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/Till0196/coax/pkg/win32"
)

// cardDriver answers the card items as the driver does: it decrypts a command,
// hands it to the card, and holds the card's answer encrypted.
type cardDriver struct {
	t       *testing.T
	random  [16]byte
	block   cipher.Block
	state   byte
	answer  []byte
	sent    [][]byte
	respond func([]byte) []byte
	// write takes the I2C writes; without it one fails the test.
	write func([]byte) error
}

func newCardDriver(t *testing.T) *cardDriver {
	driver := &cardDriver{t: t, state: cardReady}
	copy(driver.random[:], "0123456789abcdef")
	key := cardKeyBase
	for i := range key {
		key[i] ^= driver.random[i]
	}
	driver.block, _ = aes.NewCipher(key[:])
	driver.respond = func(command []byte) []byte { return append(append([]byte{}, command...), 0x90, 0x00) }
	return driver
}

func (d *cardDriver) Get(_ win32.GUID, id uint32, b []byte) (uint32, error) {
	switch id {
	case propRandom:
		return uint32(copy(b, d.random[:])), nil
	case propCardReceive:
		if len(b) != cardBuffer {
			d.t.Fatalf("receive buffer of %d bytes", len(b))
		}
		clear(b)
		b[0], b[1] = d.state, byte(len(d.answer))
		padded := make([]byte, (len(d.answer)+15)/16*16)
		copy(padded, d.answer)
		for at := 0; at < len(padded); at += 16 {
			d.block.Encrypt(b[2+at:], padded[at:at+16])
		}
		return cardBuffer, nil
	}
	d.t.Fatalf("get of item %d", id)
	return 0, nil
}

func (d *cardDriver) Set(_ win32.GUID, id uint32, b []byte) error {
	if id == propWrite && d.write != nil {
		return d.write(b)
	}
	if id != propCardSend || len(b) != cardBuffer {
		d.t.Fatalf("set of item %d with %d bytes", id, len(b))
	}
	length := int(b[1])
	padded := make([]byte, (length+15)/16*16)
	for at := 0; at < len(padded); at += 16 {
		d.block.Decrypt(padded[at:], b[2+at:2+at+16])
	}
	command := padded[:length]
	repeated := len(d.sent) > 0 && bytes.Equal(command, d.sent[len(d.sent)-1])
	d.sent = append(d.sent, command)
	if repeated {
		// Answered from the copy, without the card.
		return nil
	}
	d.answer = d.respond(command)
	return nil
}

func TestTransmitCarriesTheCommandAndItsAnswerInTheClear(t *testing.T) {
	driver := newCardDriver(t)
	control := &Control{properties: driver}
	command := []byte{0x90, 0x32, 0x00, 0x00, 0x00}
	answer, err := control.Transmit(command, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(driver.sent) != 1 || !bytes.Equal(driver.sent[0], command) {
		t.Fatalf("the driver was sent %x", driver.sent)
	}
	if want := append(append([]byte{}, command...), 0x90, 0x00); !bytes.Equal(answer, want) {
		t.Fatalf("answer %x, want %x", answer, want)
	}

	// A command longer than one block goes over in several.
	long := bytes.Repeat([]byte{0xa5}, 40)
	answer, err = control.Transmit(long, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(driver.sent[1], long) || !bytes.Equal(answer[:40], long) {
		t.Fatalf("sent %x, answered %x", driver.sent[1], answer)
	}
}

func TestTransmitWaitsWhileTheCommandIsPending(t *testing.T) {
	driver := newCardDriver(t)
	pending := 3
	inner := driver.respond
	driver.respond = func(command []byte) []byte {
		driver.state = 2
		return inner(command)
	}
	control := &Control{properties: &countingDriver{cardDriver: driver, until: &pending}}
	answer, err := control.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 || len(answer) != 7 {
		t.Fatalf("answered %x with %d pending reads left", answer, pending)
	}
}

// countingDriver reports the command pending for a number of reads.
type countingDriver struct {
	*cardDriver
	until *int
}

func (d *countingDriver) Get(set win32.GUID, id uint32, b []byte) (uint32, error) {
	if id == propCardReceive && d.state != cardReady {
		if *d.until == 0 {
			d.state = cardReady
		} else {
			*d.until--
		}
	}
	return d.cardDriver.Get(set, id, b)
}

func TestTransmitRefusesWithoutACardSession(t *testing.T) {
	driver := newCardDriver(t)
	driver.state = 0
	control := &Control{properties: driver}
	if _, err := control.Transmit([]byte{0x90, 0x32, 0x00, 0x00, 0x00}, 30*time.Millisecond); !errors.Is(err, ErrCardNotReady) {
		t.Fatalf("error %v, want ErrCardNotReady", err)
	}
	if len(driver.sent) != 0 {
		t.Fatalf("a command went to the driver: %x", driver.sent)
	}
	if present, err := control.CardPresent(); present || err != nil {
		t.Fatalf("present %v, %v", present, err)
	}
}

func TestTransmitRefusesWhatTheDriverCannotCarry(t *testing.T) {
	control := &Control{properties: newCardDriver(t)}
	for _, command := range [][]byte{nil, make([]byte, cardLimit+1)} {
		if _, err := control.Transmit(command, time.Second); err == nil {
			t.Errorf("a %d-byte command was taken", len(command))
		}
	}
}

func TestTransmitReportsACardResetInsteadOfTheDriversAnswer(t *testing.T) {
	driver := newCardDriver(t)
	driver.respond = func([]byte) []byte {
		// The card stops answering; the driver resets it and keeps the answer to
		// its own command.
		driver.state = cardResetting
		return []byte{0x00, 0x0f, 0x90, 0x00}
	}
	control := &Control{properties: driver}
	if _, err := control.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00}, time.Second); !errors.Is(err, ErrCardReset) {
		t.Fatalf("error %v, want ErrCardReset", err)
	}
}

func TestResetCardDeactivatesTheCardAndWaitsForTheDriver(t *testing.T) {
	driver := newCardDriver(t)
	var writes [][]byte
	deactivated := false
	driver.write = func(b []byte) error {
		writes = append(writes, append([]byte{}, b...))
		if b[0] != 0x4a {
			return errors.New("no answer")
		}
		deactivated = true
		return nil
	}
	resetting := 3
	inner := driver.respond
	driver.respond = func(command []byte) []byte {
		if deactivated {
			driver.state = cardResetting
			return nil
		}
		return inner(command)
	}
	control := &Control{properties: &resettingDriver{cardDriver: driver, reads: &resetting}}

	last := []byte{0x90, 0x32, 0x00, 0x00, 0x00}
	if _, err := control.Transmit(last, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := control.ResetCard(time.Second); err != nil {
		t.Fatal(err)
	}
	if want := [][]byte{{0x5a, 2, 0x00, 0x80}, {0x4a, 2, 0x00, 0x80}}; len(writes) != 2 ||
		!bytes.Equal(writes[0], want[0]) || !bytes.Equal(writes[1], want[1]) {
		t.Fatalf("wrote %x", writes)
	}
	// The last command would have been answered from the copy, so the reset
	// was set off by the other one.
	if len(driver.sent) != 3 || !bytes.Equal(driver.sent[1], last) || bytes.Equal(driver.sent[2], last) {
		t.Fatalf("the reset was set off with %x", driver.sent)
	}
	if resetting != 0 || driver.state != cardReady {
		t.Fatalf("returned with the card in state %d", driver.state)
	}
}

// resettingDriver brings the card back up after a number of reads.
type resettingDriver struct {
	*cardDriver
	reads *int
}

func (d *resettingDriver) Get(set win32.GUID, id uint32, b []byte) (uint32, error) {
	if id == propCardReceive && d.state == cardResetting {
		if *d.reads == 0 {
			d.state = cardReady
		} else {
			*d.reads--
		}
	}
	return d.cardDriver.Get(set, id, b)
}

type cardCapabilityDriver struct {
	*cardDriver
	flags uint32
}

func (d *cardCapabilityDriver) Get(g win32.GUID, id uint32, data []byte) (uint32, error) {
	switch id {
	case propFlags:
		binary.LittleEndian.PutUint32(data, d.flags)
		return 4, nil
	case propSeed:
		copy(data, transportSeed[:])
		return uint32(len(data)), nil
	}
	return d.cardDriver.Get(g, id, data)
}
func (d *cardCapabilityDriver) Set(g win32.GUID, id uint32, data []byte) error {
	if id == propInit {
		return nil
	}
	return d.cardDriver.Set(g, id, data)
}

func TestCardCommandSizeFollowsDriverCapability(t *testing.T) {
	for _, flags := range []uint32{0x40, 0x44} {
		driver := &cardCapabilityDriver{cardDriver: newCardDriver(t), flags: flags}
		control, err := Open(driver)
		if err != nil {
			t.Fatal(err)
		}
		command := bytes.Repeat([]byte{0xa5}, 61)
		answer, err := control.Transmit(command, time.Second)
		if flags&4 == 0 {
			if err == nil || len(driver.sent) != 0 {
				t.Fatal("sent an extended command to a legacy card interface")
			}
		} else if err != nil || !bytes.Equal(answer, append(command, 0x90, 0)) {
			t.Fatalf("extended interface: %x, %v", answer, err)
		}
		if _, err := control.Transmit([]byte{0x90, 0x32, 0, 0, 0}, time.Second); err != nil {
			t.Fatal(err)
		}
	}
}
