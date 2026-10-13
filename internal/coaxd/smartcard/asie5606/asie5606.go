// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package asie5606 drives the ASICEN ASIE5606 card interface IC, the on-board
// smart card reader of the PLEX ASV52xx enclosures.
//
// The IC is not a CCID reader: it is a byte pipe onto the card's serial line,
// exposed as a small register file on the bridge's I2C bus. A transmit buffer,
// a receive buffer and a 64-byte sliding window are all it offers, so the T=1
// block protocol lives here.
//
// Bytes travel over the card line bit-reversed with respect to how the host
// holds them, so every block is reversed both ways, and the LRC is computed
// over the reversed form.
package asie5606

import (
	"errors"
	"fmt"
	"math/bits"
	"time"
)

// Bus is the register access the IC needs from its host bridge: a register
// read and a register write, both of whatever length the revision accepts.
type Bus interface {
	Read(register byte, count int) ([]byte, error)
	Write(register byte, data []byte) error
}

// The IC's register file.
const (
	registerCommand      = 0x00
	registerProtocol     = 0x01
	registerTransmitLow  = 0x02
	registerReceiveLow   = 0x03
	registerStatus       = 0x04
	registerVersion      = 0x09
	registerTransmitHigh = 0x38
	registerReceiveHigh  = 0x39
	registerWindowPage   = 0x3a
	registerWindow       = 0x40
	windowSize           = 0x40
	burstTransferSize    = 8
)

// Commands written to registerCommand.
const (
	commandIdle        = 0x00
	commandPrepare     = 0x80
	commandPower       = 0x08
	commandReset       = 0x09
	commandTransmit    = 0x0a
	commandAcknowledge = 0x0c
)

// Bits of registerStatus. The card-present bit is read; the power bit is
// written to bring the card's supply up.
const (
	statusCardPresent = 0x01
	statusPowerOn     = 0x80
)

// How long the card is given to answer: a poll every 50ms, up to 42 polls for
// the head of a block and 8 more for its tail. A card's slowest answers need
// the whole of that.
const (
	pollInterval  = 50 * time.Millisecond
	headPolls     = 42
	tailPolls     = 8
	resetSettle   = 200 * time.Millisecond
	prepareSettle = 100 * time.Millisecond
	parkSettle    = 10 * time.Millisecond
	// transmitSettle is the line time the IC needs to clock a block out.
	// Polling its registers before then reports the previous answer's length.
	transmitSettle = 100 * time.Millisecond
)

// timings are the delays above, held on the reader rather than read from the
// constants directly so that a test can shorten them. New fills them in.
type timings struct {
	poll, park, prepare, reset, transmit time.Duration
	headPolls, tailPolls                 int
}

// defaultTimings is what the hardware needs.
func defaultTimings() timings {
	return timings{
		poll:      pollInterval,
		park:      parkSettle,
		prepare:   prepareSettle,
		reset:     resetSettle,
		transmit:  transmitSettle,
		headPolls: headPolls,
		tailPolls: tailPolls,
	}
}

// ErrNoCard reports that no card is seated in the reader.
var ErrNoCard = errors.New("no card in the reader")

// Reader is one ASIE5606 and the T=1 session running over it. It is not safe
// for concurrent use -- a T=1 session is a single conversation with sequence
// numbers on both sides -- so callers sharing a board must serialise access.
type Reader struct {
	bus Bus
	// version is the IC revision, as reported by registerVersion.
	version byte
	// extended marks the revisions that carry the paging and high-order length
	// registers, and so can buffer more than one 64-byte window.
	extended bool
	// burst marks the revisions whose window accepts multi-byte I2C transfers.
	burst bool
	// sequence is the T=1 send sequence number N(S) of the next I-block.
	sequence byte
	// timing is how long the card is given at each step.
	timing timings
}

// New probes the IC at the address the bus is bound to. It fails when nothing
// answers, or when the revision is one this driver does not recognise.
func New(bus Bus) (*Reader, error) {
	// The revision lives in the top bits of this register, and decides the
	// rest of the bring-up, so it is the first thing asked for.
	identity, err := bus.Read(registerVersion, 1)
	if err != nil {
		return nil, fmt.Errorf("read ASIE5606 revision: %w", err)
	}
	version := (identity[0] & 0x3e) >> 1
	// 0x11 is not a known revision; 0x12 to 0x14 are the PX-W3U3 V2's, which
	// behave as 0x0f and 0x10 do.
	if version == 0x11 || version > 0x14 {
		return nil, fmt.Errorf("unsupported ASIE5606 revision %d", version)
	}
	return &Reader{
		bus:      bus,
		version:  version,
		extended: version != 0,
		burst:    version == 0x0f || version == 0x10 || version >= 0x12,
		timing:   defaultTimings(),
	}, nil
}

// Version reports the IC revision.
func (reader *Reader) Version() byte { return reader.version }

// bufferSize is how many bytes the IC can hold in one direction. Revisions
// without the paging registers are limited to the bare window.
func (reader *Reader) bufferSize() int {
	if reader.extended {
		return 0x140
	}
	return windowSize
}

// Present reports whether a card is seated. It is the one query that works
// before Reset, so callers can tell an empty slot from a broken card.
func (reader *Reader) Present() (bool, error) {
	status, err := reader.bus.Read(registerStatus, 1)
	if err != nil {
		return false, err
	}
	return status[0]&statusCardPresent != 0, nil
}

// Reset powers the card up and returns its ATR, starting a fresh T=1 session.
// It must be called before Transmit, and again after the card has been taken
// out and put back: a session's sequence numbers mean nothing to a card that
// was power-cycled underneath it.
func (reader *Reader) Reset() ([]byte, error) {
	// Park the IC first: without it a half-finished session from an earlier
	// owner leaves stale bytes in the receive buffer.
	if err := reader.command(commandPrepare); err != nil {
		return nil, err
	}
	time.Sleep(reader.timing.park)
	if err := reader.command(commandIdle); err != nil {
		return nil, err
	}
	if err := reader.bus.Write(registerStatus, []byte{0}); err != nil {
		return nil, err
	}
	if err := reader.command(commandIdle); err != nil {
		return nil, err
	}
	time.Sleep(reader.timing.prepare)

	present, err := reader.Present()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, ErrNoCard
	}

	if err := reader.command(commandPower); err != nil {
		return nil, err
	}
	if err := reader.bus.Write(registerStatus, []byte{statusPowerOn}); err != nil {
		return nil, err
	}
	time.Sleep(reader.timing.reset)
	if err := reader.command(commandReset); err != nil {
		return nil, err
	}
	atr, err := reader.receive(true)
	if err != nil {
		return nil, fmt.Errorf("read ATR: %w", err)
	}
	if err := reader.bus.Write(registerProtocol, []byte{0x01}); err != nil {
		return nil, err
	}
	reader.sequence = 0

	// Offer the card the largest information field it may send us. The answer is
	// deliberately not read: the card may propose a smaller size in its own
	// S-block, and the reply is dropped so the session starts with an empty
	// receive buffer either way.
	if reader.extended {
		if err := reader.send(frame([]byte{0x00, 0xc1, 0x01, 0xfe})); err != nil {
			return nil, fmt.Errorf("request information field size: %w", err)
		}
		if err := reader.command(commandAcknowledge); err != nil {
			return nil, err
		}
	}
	return atr, nil
}

// maximumInformation is the largest APDU one T=1 block can carry: the length
// field is one byte, with the three-byte header and the LRC alongside it.
const maximumInformation = 254

// Transmit sends one APDU and returns the card's answer, reassembling a
// chained response into a single buffer.
func (reader *Reader) Transmit(apdu []byte) ([]byte, error) {
	if len(apdu) == 0 {
		return nil, errors.New("APDU is empty")
	}
	limit := maximumInformation
	if reader.bufferSize()-4 < limit {
		limit = reader.bufferSize() - 4
	}
	if len(apdu) > limit {
		return nil, fmt.Errorf("APDU of %d bytes exceeds the %d this reader can send in one block", len(apdu), limit)
	}

	block := information(reader.sequence, apdu)
	var answer []byte
	// A chain longer than the receive buffer can hold is a card that never
	// clears its more-data bit, so the run is bounded rather than endless.
	for part := 0; part <= reader.bufferSize()/4; part++ {
		if err := reader.send(block); err != nil {
			return nil, err
		}
		received, err := reader.receive(false)
		if err != nil {
			return nil, err
		}
		if err := verify(received); err != nil {
			return nil, err
		}
		for index := range received {
			received[index] = bits.Reverse8(received[index])
		}
		if len(received) < 4 {
			return nil, fmt.Errorf("card returned a %d-byte block, too short for T=1", len(received))
		}
		answer = append(answer, received[3:len(received)-1]...)
		control := received[1]
		if control&0x20 == 0 {
			// Last block of the answer, so our own sequence number advances.
			reader.sequence ^= 1
			return answer, nil
		}
		// More to come. ISO 7816-3 acknowledges a chained I-block with an
		// R-block carrying the sequence number the card should send next.
		next := (received[1]>>6)&1 ^ 1
		block = receiveReady(next)
	}
	return nil, errors.New("card kept chaining its answer past the reader's buffer")
}

// information builds a T=1 I-block carrying an APDU, in the bit order the card
// line uses.
func information(sequence byte, apdu []byte) []byte {
	block := make([]byte, 0, len(apdu)+4)
	block = append(block, 0x00, sequence<<6, byte(len(apdu)))
	block = append(block, apdu...)
	return frame(block)
}

// receiveReady builds a T=1 R-block acknowledging a chained I-block and asking
// for the block numbered next.
func receiveReady(next byte) []byte {
	return frame([]byte{0x00, 0x80 | next<<4, 0x00})
}

// frame reverses a block into card bit order and appends its LRC, which the
// card computes over the reversed bytes.
func frame(block []byte) []byte {
	for index := range block {
		block[index] = bits.Reverse8(block[index])
	}
	check := byte(0)
	for _, value := range block {
		check ^= value
	}
	return append(block, check)
}

// verify checks the LRC of a block still in card bit order.
func verify(block []byte) error {
	if len(block) < 4 {
		return fmt.Errorf("card returned a %d-byte block, too short for T=1", len(block))
	}
	check := byte(0)
	for _, value := range block {
		check ^= value
	}
	if check != 0 {
		return errors.New("card block failed its LRC check")
	}
	return nil
}

func (reader *Reader) command(opcode byte) error {
	return reader.bus.Write(registerCommand, []byte{opcode})
}

// send loads a framed block into the transmit buffer and starts it on its way.
func (reader *Reader) send(block []byte) error {
	if err := reader.bus.Write(registerTransmitLow, []byte{byte(len(block))}); err != nil {
		return err
	}
	if reader.extended {
		if err := reader.bus.Write(registerTransmitHigh, []byte{byte(len(block) >> 8)}); err != nil {
			return err
		}
	}
	if err := reader.writeWindow(block); err != nil {
		return err
	}
	if err := reader.command(commandTransmit); err != nil {
		return err
	}
	time.Sleep(reader.timing.transmit)
	return nil
}

// receive waits for a block to arrive and reads it out, still in card bit order
// and with its LRC attached. An ATR is taken in one pass, having no length
// field to read ahead from; every other block is taken in two, as its third
// byte says how much more is coming.
func (reader *Reader) receive(atr bool) ([]byte, error) {
	head := 3
	if atr {
		head = 13
	}
	count, err := reader.wait(head, reader.timing.headPolls)
	if err != nil {
		return nil, err
	}
	if count > reader.bufferSize() {
		return nil, fmt.Errorf("card announced %d bytes, more than the reader's %d-byte buffer", count, reader.bufferSize())
	}
	block, err := reader.readWindow(0, count)
	if err != nil {
		return nil, err
	}
	if !atr {
		// The length field is bit-reversed like the rest of the block, and
		// covers the information field alone: the header and LRC add four.
		total := int(bits.Reverse8(block[2])) + 4
		if total > count {
			second, waitErr := reader.wait(total, reader.timing.tailPolls)
			if waitErr != nil {
				return nil, waitErr
			}
			if second > reader.bufferSize() {
				return nil, fmt.Errorf("card announced %d bytes, more than the reader's %d-byte buffer", second, reader.bufferSize())
			}
			tail, readErr := reader.readWindow(count, second-count)
			if readErr != nil {
				return nil, readErr
			}
			block = append(block, tail...)
		}
	}
	if err := reader.command(commandAcknowledge); err != nil {
		return nil, err
	}
	return block, nil
}

// wait polls until the receive buffer holds at least minimum bytes.
func (reader *Reader) wait(minimum, polls int) (int, error) {
	for poll := 0; poll < polls; poll++ {
		// The card is given the interval before the first look as well as
		// between looks: it has only just been handed a block to work on.
		time.Sleep(reader.timing.poll)
		count, err := reader.available()
		if err != nil {
			return 0, err
		}
		if count >= minimum {
			return count, nil
		}
	}
	return 0, fmt.Errorf("card did not answer with %d bytes within %s", minimum, time.Duration(polls)*reader.timing.poll)
}

// available reports how many bytes the receive buffer holds, failing if the
// card has been taken out in the meantime.
func (reader *Reader) available() (int, error) {
	high := byte(0)
	if reader.extended {
		data, err := reader.bus.Read(registerReceiveHigh, 1)
		if err != nil {
			return 0, err
		}
		high = data[0] & 1
	}
	low, err := reader.bus.Read(registerReceiveLow, 1)
	if err != nil {
		return 0, err
	}
	status, err := reader.bus.Read(registerStatus, 1)
	if err != nil {
		return 0, err
	}
	if status[0]&statusCardPresent == 0 {
		return 0, ErrNoCard
	}
	return int(low[0]) | int(high)<<8, nil
}

// writeWindow copies data into the IC's sliding window, selecting each page as
// it is reached.
func (reader *Reader) writeWindow(data []byte) error {
	return reader.walkWindow(0, len(data), func(register byte, offset, size int) error {
		return reader.bus.Write(register, data[offset:offset+size])
	})
}

// readWindow copies size bytes out of the window, starting at an offset into
// the buffer rather than into the window itself.
func (reader *Reader) readWindow(start, size int) ([]byte, error) {
	data := make([]byte, size)
	err := reader.walkWindow(start, size, func(register byte, offset, chunk int) error {
		read, readErr := reader.bus.Read(register, chunk)
		if readErr != nil {
			return readErr
		}
		copy(data[offset-start:], read)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// walkWindow steps over a buffer range in window-sized pages, calling transfer
// with the window register, the absolute offset and the transfer size. Pages
// are selected only on revisions that have the paging register.
func (reader *Reader) walkWindow(start, size int, transfer func(register byte, offset, chunk int) error) error {
	for offset := start; offset < start+size; {
		page, within := offset/windowSize, offset%windowSize
		if reader.extended && within == 0 {
			if err := reader.bus.Write(registerWindowPage, []byte{byte(page)}); err != nil {
				return err
			}
		}
		chunk := 1
		if reader.burst {
			chunk = burstTransferSize
			if remaining := start + size - offset; chunk > remaining {
				chunk = remaining
			}
			if remaining := windowSize - within; chunk > remaining {
				chunk = remaining
			}
		}
		if err := transfer(registerWindow+byte(within), offset, chunk); err != nil {
			return err
		}
		offset += chunk
	}
	return nil
}
