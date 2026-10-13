// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asie5606

import (
	"bytes"
	"errors"
	"math/bits"
	"testing"
	"time"
)

// fakeIC is enough of an ASIE5606 with a card in it to drive a whole T=1
// exchange. One array stands in for the window and for both directions'
// buffers, which is all the driver's access pattern needs.
type fakeIC struct {
	version  byte
	present  bool
	page     byte
	transmit int
	buffered int
	window   [0x140]byte
	// sent records every block the host handed the card, already unreversed.
	sent [][]byte
	// answer plays the card: it is given one block and returns the blocks the
	// card sends back, one per call.
	answer func(block []byte) []byte
	// acknowledged counts the commandAcknowledge writes, which is how the
	// driver says it is done with a block.
	acknowledged int
}

func (ic *fakeIC) Read(register byte, count int) ([]byte, error) {
	data := make([]byte, count)
	for offset := 0; offset < count; offset++ {
		value, err := ic.readOne(register + byte(offset))
		if err != nil {
			return nil, err
		}
		data[offset] = value
	}
	return data, nil
}

func (ic *fakeIC) readOne(register byte) (byte, error) {
	switch {
	case register == registerVersion:
		return ic.version << 1, nil
	case register == registerReceiveLow:
		return byte(ic.buffered), nil
	case register == registerReceiveHigh:
		return byte(ic.buffered >> 8), nil
	case register == registerStatus:
		if ic.present {
			return statusCardPresent, nil
		}
		return 0, nil
	case register >= registerWindow:
		return ic.window[int(ic.page)*windowSize+int(register-registerWindow)], nil
	}
	return 0, errors.New("read of an unmodelled register")
}

func (ic *fakeIC) Write(register byte, data []byte) error {
	switch {
	case register == registerWindowPage:
		ic.page = data[0]
	case register == registerTransmitLow:
		ic.transmit = ic.transmit&^0xff | int(data[0])
	case register == registerTransmitHigh:
		ic.transmit = ic.transmit&0xff | int(data[0])<<8
	case register == registerCommand:
		return ic.command(data[0])
	case register == registerProtocol, register == registerStatus:
	case register >= registerWindow:
		copy(ic.window[int(ic.page)*windowSize+int(register-registerWindow):], data)
	default:
		return errors.New("write to an unmodelled register")
	}
	return nil
}

func (ic *fakeIC) command(opcode byte) error {
	switch opcode {
	case commandTransmit:
		block := make([]byte, ic.transmit)
		copy(block, ic.window[:ic.transmit])
		for index := range block {
			block[index] = bits.Reverse8(block[index])
		}
		ic.sent = append(ic.sent, block)
		ic.load(ic.answer(block))
	case commandAcknowledge:
		ic.acknowledged++
		ic.buffered = 0
	case commandReset:
		ic.load(frame([]byte{0x3b, 0xf0, 0x12, 0x00, 0xff, 0x91, 0x81, 0xb1, 0x7c, 0x45, 0x1f, 0x03}))
	case commandPrepare, commandPower, commandIdle:
	default:
		return errors.New("unmodelled command")
	}
	return nil
}

// load puts an already-framed answer where the host will read it.
func (ic *fakeIC) load(block []byte) {
	copy(ic.window[:], block)
	ic.buffered = len(block)
}

// respond builds the framed I-block a card would answer an I-block with.
func respond(sequence byte, information []byte, more bool) []byte {
	control := sequence << 6
	if more {
		control |= 0x20
	}
	block := append([]byte{0x00, control, byte(len(information))}, information...)
	return frame(block)
}

// newTestReader returns a reader over a fake IC that is silent until answer is
// set. The delays are shortened to the smallest thing that still exercises the
// same number of polls, so a test that lets the card stay silent does not wait
// out the hardware's timeout.
func newTestReader(t *testing.T, version byte) (*Reader, *fakeIC) {
	t.Helper()
	ic := &fakeIC{version: version, present: true, answer: func([]byte) []byte { return nil }}
	reader, err := New(ic)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	reader.timing = timings{
		poll:      time.Microsecond,
		park:      time.Microsecond,
		prepare:   time.Microsecond,
		reset:     time.Microsecond,
		transmit:  time.Microsecond,
		headPolls: headPolls,
		tailPolls: tailPolls,
	}
	return reader, ic
}

func TestNewReportsRevisionAndCapabilities(t *testing.T) {
	for _, want := range []struct {
		version  byte
		extended bool
		burst    bool
	}{
		{version: 0, extended: false, burst: false},
		{version: 0x0e, extended: true, burst: false},
		{version: 0x0f, extended: true, burst: true},
		{version: 0x10, extended: true, burst: true},
	} {
		reader, _ := newTestReader(t, want.version)
		if reader.Version() != want.version || reader.extended != want.extended || reader.burst != want.burst {
			t.Errorf("revision %d: got version=%d extended=%v burst=%v", want.version,
				reader.Version(), reader.extended, reader.burst)
		}
	}
}

func TestNewRejectsUnknownRevision(t *testing.T) {
	if _, err := New(&fakeIC{version: 0x11, present: true}); err == nil {
		t.Fatal("accepted an unsupported revision")
	}
}

func TestPresentReportsAnEmptySlot(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	ic.present = false
	present, err := reader.Present()
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("reported a card in an empty slot")
	}
}

func TestResetReturnsTheATRAndRequestsAnInformationFieldSize(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	ic.answer = func([]byte) []byte { return nil }
	atr, err := reader.Reset()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x3b, 0xf0, 0x12, 0x00, 0xff, 0x91, 0x81, 0xb1, 0x7c, 0x45, 0x1f, 0x03}
	// The ATR comes back in card bit order with its checksum attached, which
	// is what a caller needs to see the card's own convention.
	if len(atr) != len(want)+1 {
		t.Fatalf("ATR is %d bytes; want %d", len(atr), len(want)+1)
	}
	for index, value := range want {
		if bits.Reverse8(atr[index]) != value {
			t.Fatalf("ATR byte %d = %#02x; want %#02x reversed", index, atr[index], value)
		}
	}
	if len(ic.sent) != 1 {
		t.Fatalf("sent %d blocks during reset; want the one information field request", len(ic.sent))
	}
	if got := ic.sent[0][:4]; !bytes.Equal(got, []byte{0x00, 0xc1, 0x01, 0xfe}) {
		t.Fatalf("information field request = % x; want 00 c1 01 fe", got)
	}
}

func TestResetSkipsTheInformationFieldRequestOnTheOldestRevision(t *testing.T) {
	reader, ic := newTestReader(t, 0)
	if _, err := reader.Reset(); err != nil {
		t.Fatal(err)
	}
	if len(ic.sent) != 0 {
		t.Fatalf("sent %d blocks on a revision that cannot buffer them", len(ic.sent))
	}
}

func TestResetReportsAnEmptySlot(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	ic.present = false
	if _, err := reader.Reset(); !errors.Is(err, ErrNoCard) {
		t.Fatalf("reset an empty slot: %v", err)
	}
}

func TestTransmitCarriesAnAPDUAndReturnsTheAnswer(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	answer := []byte{0xa1, 0xb2, 0xc3, 0x90, 0x00}
	ic.answer = func(block []byte) []byte {
		if block[1]&0xc0 == 0xc0 {
			return respond(0, nil, false)
		}
		return respond(0, answer, false)
	}
	if _, err := reader.Reset(); err != nil {
		t.Fatal(err)
	}
	apdu := []byte{0x00, 0xca, 0x00, 0x00, 0x00}
	response, err := reader.Transmit(apdu)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, answer) {
		t.Fatalf("response = % x; want % x", response, answer)
	}
	last := ic.sent[len(ic.sent)-1]
	if !bytes.Equal(last[3:3+len(apdu)], apdu) {
		t.Fatalf("card received % x; want the APDU % x", last[3:3+len(apdu)], apdu)
	}
	if last[0] != 0x00 || last[1] != 0x00 || last[2] != byte(len(apdu)) {
		t.Fatalf("block header = % x; want 00 00 %02x", last[:3], len(apdu))
	}
}

func TestTransmitAdvancesTheSequenceNumber(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	ic.answer = func(block []byte) []byte {
		if block[1]&0xc0 == 0xc0 {
			return respond(0, nil, false)
		}
		return respond(0, []byte{0x90, 0x00}, false)
	}
	if _, err := reader.Reset(); err != nil {
		t.Fatal(err)
	}
	var sequences []byte
	for exchange := 0; exchange < 3; exchange++ {
		if _, err := reader.Transmit([]byte{0x00, 0xa4, 0x04, 0x00, 0x00}); err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, ic.sent[len(ic.sent)-1][1]>>6&1)
	}
	if !bytes.Equal(sequences, []byte{0, 1, 0}) {
		t.Fatalf("sequence numbers = %v; want 0 1 0", sequences)
	}
}

func TestTransmitReassemblesAChainedAnswer(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	first := bytes.Repeat([]byte{0x11}, 40)
	second := []byte{0x22, 0x22, 0x90, 0x00}
	part := 0
	ic.answer = func(block []byte) []byte {
		if block[1]&0xc0 == 0xc0 {
			// The information field request sent during reset.
			return respond(0, nil, false)
		}
		if block[1]&0xc0 == 0x80 {
			// An R-block acknowledging the chain: the card sends the rest.
			if got, want := block[1], byte(0x90); got != want {
				t.Errorf("acknowledgement = %#02x; want %#02x", got, want)
			}
			return respond(1, second, false)
		}
		part++
		return respond(0, first, true)
	}
	if _, err := reader.Reset(); err != nil {
		t.Fatal(err)
	}
	response, err := reader.Transmit([]byte{0x00, 0xca, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, append(append([]byte{}, first...), second...)) {
		t.Fatalf("reassembled % x", response)
	}
}

func TestTransmitRejectsACorruptBlock(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	ic.answer = func(block []byte) []byte {
		if block[1]&0xc0 == 0xc0 {
			return respond(0, nil, false)
		}
		bad := respond(0, []byte{0x90, 0x00}, false)
		bad[len(bad)-1] ^= 0xff
		return bad
	}
	if _, err := reader.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Transmit([]byte{0x00, 0xa4, 0x04, 0x00, 0x00}); err == nil {
		t.Fatal("accepted a block with a broken LRC")
	}
}

func TestTransmitRejectsAnOversizedAPDU(t *testing.T) {
	reader, _ := newTestReader(t, 0)
	if _, err := reader.Transmit(bytes.Repeat([]byte{0}, windowSize)); err == nil {
		t.Fatal("accepted an APDU the reader cannot frame")
	}
}

func TestTransmitRejectsAnEmptyAPDU(t *testing.T) {
	reader, _ := newTestReader(t, 0x0f)
	if _, err := reader.Transmit(nil); err == nil {
		t.Fatal("accepted an empty APDU")
	}
}

func TestReceiveGivesUpOnASilentCard(t *testing.T) {
	reader, ic := newTestReader(t, 0x0f)
	ic.answer = func([]byte) []byte { return nil }
	if _, err := reader.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Transmit([]byte{0x00, 0xa4, 0x04, 0x00, 0x00}); err == nil {
		t.Fatal("accepted a silent card")
	}
}
