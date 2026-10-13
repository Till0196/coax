// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// The protocol is ISO/IEC 7816-3's T=1: the block structure of clause 11.3, the
// sequence numbering and error handling of 11.6, and the answer to reset of
// clause 8. Where a particular reader's byte-level behaviour differs, it is
// marked.

// Package iso7816 speaks the T=1 protocol of ISO/IEC 7816-3 to a smart card
// over a plain byte pipe. T=1 is the block-oriented one of the standard's two
// transmission protocols, and the only one these readers use.
//
// Boards whose reader is a bare UART -- the IT930x ones -- hand the host
// individual bytes and nothing else, so the framing, block numbering,
// retransmissions and answer-to-reset are all built here. Boards whose hardware
// frames blocks (EM287x, IT9175) need none of it.
//
// A Session is one conversation with one card, serialised because a T=1
// exchange spans several transfers whose block numbers must stay in step.
package iso7816

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Line is the byte pipe a card hangs off, plus the two things only the board
// can do: notice the card and power-cycle it.
//
// Read returns what has already arrived and does not wait; Ready says whether
// that is worth doing. On these boards the length register climbs while a block
// is still arriving, so only the ready flag may gate a read.
type Line interface {
	// Present reports whether a card is seated.
	Present() (bool, error)
	// Reset power-cycles the card, leaving it about to send its ATR.
	Reset() error
	// SetBaudrate switches the line speed to 9600, 19200 or 38400.
	SetBaudrate(rate int) error
	// Ready reports whether any received bytes are waiting.
	Ready() (bool, error)
	// Read takes up to len(buffer) bytes that have already arrived.
	Read(buffer []byte) (int, error)
	// Write sends bytes to the card.
	Write(data []byte) error
}

// Block types. The high bits of the block type byte say which of the three
// kinds a block is, and the rest carries its sequence numbers and flags.
const (
	blockInformation = 0x00
	blockSendBit     = 0x40
	blockMoreData    = 0x20

	blockReceiveReady = 0x80
	blockReceiveBit   = 0x10
	blockEDCError     = 0x01

	blockSupervisory = 0xc0
	blockResponse    = 0x20
	blockResynch     = 0x00
	blockIFS         = 0x01
	blockWTX         = 0x03
)

// nodeAddress is the only address these readers use: one card, one reader.
const nodeAddress = 0x00

// Frame and field limits. The two directions differ: what this host sends must
// fit the 255 bytes the boards' byte pipes carry in one message, while what the
// card sends is bounded only by its own information field size, whose length
// byte reaches 254.
const (
	maxFrame       = 255
	maxInfoField   = 254
	prologueLength = 3
	maxBlock       = prologueLength + maxInfoField + 2
	maxATR         = 33
	defaultIFSC    = 32
)

// Timings. The retry limit is the standard's: ISO/IEC 7816-3 gives up after three
// attempts at the same block. The half second a block is allowed matches what the
// vendor's own driver waits. The rest bound this implementation rather than the
// protocol. Deadlines are absolute for a whole operation, so a card cannot extend
// the total by answering slowly again.
const (
	pollInterval     = 5 * time.Millisecond
	atrTimeout       = time.Second
	defaultBlockWait = 500 * time.Millisecond
	maxBlockWait     = 3 * time.Second
	minBlockWait     = 500 * time.Millisecond
	operationTimeout = 3 * time.Second
	maxRetries       = 3
	maxDiscardRounds = 4
)

// ErrNoCard reports that the slot is empty.
var ErrNoCard = errors.New("no smart card is seated in the slot")

// errIncomplete reports a buffer that is a prefix of a valid structure, so far. A
// caller sees it only as a timeout.
var errIncomplete = errors.New("more bytes are still to come")

// errMalformed reports bytes that cannot become valid however many follow.
var errMalformed = errors.New("the card sent a malformed answer")

// errNoSession reports an APDU sent before the card was reset.
var errNoSession = errors.New("the card reader has no session; reset the card first")

// Session is one conversation with one card.
type Session struct {
	line Line

	mutex sync.Mutex
	// open is set once a reset has produced a usable ATR and the block size has
	// been agreed.
	open bool
	atr  []byte
	// send and receive are the block numbers each end is up to.
	send    byte
	receive byte
	// useCRC selects the two-byte check the ATR may ask for instead of the default.
	useCRC bool
	// cardIFSC is the largest information field the card will accept.
	cardIFSC int
	// blockWait is how long one block may take, from the ATR's timeout hint.
	blockWait time.Duration
	baudrate  int
}

// New wraps a byte pipe. Nothing is sent until Reset runs.
func New(line Line) *Session {
	return &Session{line: line, cardIFSC: defaultIFSC, blockWait: defaultBlockWait, baudrate: 9600}
}

// Present reports whether a card is seated, without disturbing a session.
func (session *Session) Present() (bool, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.line.Present()
}

// ATR returns the answer-to-reset of the current session, or nil when there is
// none.
func (session *Session) ATR() []byte {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if !session.open {
		return nil
	}
	return append([]byte(nil), session.atr...)
}

// invalidate discards everything about the conversation at once: clearing the
// flag alone would leave the block numbers, agreed sizes and ATR for the next
// card.
func (session *Session) invalidate() {
	session.open = false
	session.atr = nil
	session.send = 0
	session.receive = 0
	session.useCRC = false
	session.cardIFSC = defaultIFSC
	session.blockWait = defaultBlockWait
	session.baudrate = 9600
}

// Invalidate drops the session, for a board that has noticed its USB device go
// away.
func (session *Session) Invalidate() {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.invalidate()
}

// Reset power-cycles the card, reads its answer-to-reset and agrees a block size.
func (session *Session) Reset() ([]byte, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.invalidate()

	present, err := session.line.Present()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, ErrNoCard
	}

	// Two attempts, and only for one reason: bytes left over from a previous
	// conversation can precede the answer on a back-to-back reset. Any other
	// failure is real.
	var parameters atrParameters
	for attempt := 0; attempt < 2; attempt++ {
		parameters = atrParameters{}
		if err = session.line.Reset(); err != nil {
			return nil, err
		}
		session.atr, parameters, err = session.readATR()
		if err == nil {
			break
		}
		if attempt != 0 || !errors.Is(err, errMalformed) {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}

	if err := session.line.SetBaudrate(parameters.baudrate); err != nil {
		return nil, err
	}
	session.baudrate = parameters.baudrate
	session.useCRC = parameters.useCRC
	session.cardIFSC = parameters.ifsc
	session.blockWait = parameters.blockWait

	if err := session.initialize(); err != nil {
		session.invalidate()
		return nil, err
	}
	session.open = true
	return append([]byte(nil), session.atr...), nil
}

// ourIFSD is the largest information field this host will accept. A card on the
// fastest line speed is offered the full field; the rest are held to what a
// 255-byte frame leaves once the prologue and check bytes are taken out.
func (session *Session) ourIFSD() int {
	if session.baudrate == 38400 {
		return 254
	}
	if session.useCRC {
		return maxFrame - prologueLength - 2
	}
	return maxFrame - prologueLength - 1
}

// initialize agrees the block size with the card. The asymmetry is deliberate:
// a card on one of the two slower speeds is resynchronised first and then asked
// for a block size, while one on the fastest speed is asked straight away --
// resynchronisation is what upsets it -- and resynchronised only if that ask
// fails.
func (session *Session) initialize() error {
	deadline := time.Now().Add(operationTimeout)
	ifsd := session.ourIFSD()
	if session.baudrate != 38400 {
		if err := session.resynchronise(deadline); err != nil {
			return err
		}
		return session.agreeIFS(ifsd, maxRetries, deadline)
	}
	err := session.agreeIFS(ifsd, 1, deadline)
	if err == nil {
		return nil
	}
	if !errors.Is(err, errMalformed) && !isTimeout(err) {
		return err
	}
	if err := session.resynchronise(deadline); err != nil {
		return err
	}
	return session.agreeIFS(ifsd, 1, deadline)
}

// resynchronise puts both ends back to block number zero.
func (session *Session) resynchronise(deadline time.Time) error {
	pcb, data, err := session.exchange(blockSupervisory|blockResynch, nil, 1, deadline)
	if err != nil {
		return err
	}
	if pcb != blockSupervisory|blockResponse|blockResynch || len(data) != 0 {
		return fmt.Errorf("%w: the card answered a resynchronisation with %#02x", errMalformed, pcb)
	}
	session.send = 0
	session.receive = 0
	return nil
}

// agreeIFS tells the card how much this host will accept and checks it agrees.
func (session *Session) agreeIFS(ifsd, retries int, deadline time.Time) error {
	pcb, data, err := session.exchange(blockSupervisory|blockIFS, []byte{byte(ifsd)}, retries, deadline)
	if err != nil {
		return err
	}
	if pcb != blockSupervisory|blockResponse|blockIFS || len(data) != 1 || int(data[0]) != ifsd {
		return fmt.Errorf("%w: the card answered a block size of %d with %#02x %x", errMalformed, ifsd, pcb, data)
	}
	return nil
}

// Transmit sends one command APDU and returns the card's response, including its
// status bytes, reassembling a chained answer.
func (session *Session) Transmit(apdu []byte) ([]byte, error) {
	if len(apdu) == 0 {
		return nil, errors.New("the APDU is empty")
	}
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if !session.open {
		return nil, errNoSession
	}
	limit := min(session.cardIFSC, maxInfoField, maxFrame-prologueLength-session.edcLength())
	if len(apdu) > limit {
		return nil, fmt.Errorf("an APDU of %d bytes is over the %d this card accepts in one block", len(apdu), limit)
	}
	present, err := session.line.Present()
	if err != nil {
		session.invalidate()
		return nil, err
	}
	if !present {
		session.invalidate()
		return nil, ErrNoCard
	}

	answer, err := session.transmit(apdu)
	if err != nil {
		// Anything that went wrong mid-conversation leaves the block numbers out of
		// step, so the whole session goes rather than just the error.
		session.invalidate()
		return nil, err
	}
	return answer, nil
}

// transmit is Transmit's body, run under the session's lock.
func (session *Session) transmit(apdu []byte) ([]byte, error) {
	deadline := time.Now().Add(operationTimeout)
	pcb := byte(blockInformation | session.send<<6)
	block := apdu
	var answer []byte

	// A card that never clears its more-data bit would chain for ever, so the run is
	// bounded by what a full-length answer could possibly need.
	for part := 0; part <= maxFrame; part++ {
		received, data, err := session.exchange(pcb, block, maxRetries, deadline)
		if err != nil {
			return nil, err
		}
		switch {
		// An information block is the one kind with the top bit clear: the bit below it
		// is the block number, not part of the kind.
		case received&0x80 == blockInformation:
			// The card only sends an information block once it has taken ours.
			if part == 0 {
				session.send ^= 1
			}
			if (received>>6)&1 != session.receive {
				return nil, fmt.Errorf("%w: the card sent block %d, expecting %d", errMalformed, (received>>6)&1, session.receive)
			}
			session.receive ^= 1
			answer = append(answer, data...)
			if received&blockMoreData == 0 {
				return answer, nil
			}
			// More to come: acknowledge with a receive-ready block asking for the next,
			// and send nothing of our own.
			pcb = blockReceiveReady | session.receive<<4
			block = nil
		default:
			return nil, fmt.Errorf("%w: the card answered with block type %#02x", errMalformed, received)
		}
	}
	return nil, errors.New("the card kept chaining its answer without ending it")
}

// exchange sends one block and returns the card's, retrying the pair on the
// failures a retransmission can fix and answering the card's requests for more
// time.
func (session *Session) exchange(pcb byte, data []byte, retries int, deadline time.Time) (byte, []byte, error) {
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		// Sending again after the deadline has gone cannot help.
		if time.Now().After(deadline) {
			break
		}
		if attempt != 0 {
			// Whatever the failed attempt left on the line would otherwise be read as
			// the front of the retransmission's answer.
			if err := session.discard(); err != nil {
				return 0, nil, err
			}
		}
		if err := session.send1(pcb, data); err != nil {
			return 0, nil, err
		}
		// wait grows when the card asks for more time, and is always cut short by the
		// operation's own deadline.
		wait := session.blockWait
		for {
			blockDeadline := time.Now().Add(wait)
			if blockDeadline.After(deadline) {
				blockDeadline = deadline
			}
			answered, answer, err := session.receive1(blockDeadline)
			if err != nil {
				lastErr = err
				break
			}
			if supervisoryRequest(answered, blockWTX) {
				// The card is asking for more time, as a multiple of the wait it is
				// otherwise allowed. Granting it means both echoing the request and
				// waiting longer -- but never past the operation's deadline.
				if len(answer) != 1 || answer[0] == 0 {
					return 0, nil, fmt.Errorf("%w: the card asked for more time with %x", errMalformed, answer)
				}
				wait = session.blockWait * time.Duration(answer[0])
				if err := session.send1(blockSupervisory|blockResponse|blockWTX, answer); err != nil {
					return 0, nil, err
				}
				continue
			}
			if supervisoryRequest(answered, blockIFS) {
				// A card may set the block size in its own direction too, at any point.
				// Refusing it would end a conversation that is going perfectly well.
				if len(answer) != 1 {
					return 0, nil, fmt.Errorf("%w: the card asked for a block size with %x", errMalformed, answer)
				}
				if err := session.send1(blockSupervisory|blockResponse|blockIFS, answer); err != nil {
					return 0, nil, err
				}
				continue
			}
			if answered&0xc0 == blockReceiveReady {
				// The card is asking for a block back. If it wants the one we just sent,
				// it did not take it; if it wants the next, it did.
				if pcb&0x80 == blockInformation && (answered>>4)&1 == (pcb>>6)&1 {
					lastErr = fmt.Errorf("%w: the card asked for the block again", errMalformed)
					break
				}
			}
			return answered, answer, nil
		}
		// Only a lost or corrupted block is worth sending again.
		if !isTimeout(lastErr) && !errors.Is(lastErr, errMalformed) {
			return 0, nil, lastErr
		}
	}
	if lastErr == nil {
		lastErr = errTimeout
	}
	return 0, nil, lastErr
}

// supervisoryRequest reports whether a block is the card asking for something,
// rather than answering something this host asked for.
func supervisoryRequest(pcb, kind byte) bool {
	return pcb&0xc0 == blockSupervisory && pcb&blockResponse == 0 && pcb&0x1f == kind
}

// errTimeout reports that the card did not answer within the deadline.
var errTimeout = errors.New("the card did not answer in time")

func isTimeout(err error) bool { return errors.Is(err, errTimeout) }

// edcLength is how many bytes the check at the end of a block takes.
func (session *Session) edcLength() int {
	if session.useCRC {
		return 2
	}
	return 1
}

func (session *Session) send1(pcb byte, data []byte) error {
	length := prologueLength + len(data) + session.edcLength()
	if length > maxFrame {
		return fmt.Errorf("a %d-byte block is over the %d a frame holds", length, maxFrame)
	}
	frame := make([]byte, 0, length)
	frame = append(frame, nodeAddress, pcb, byte(len(data)))
	frame = append(frame, data...)
	if session.useCRC {
		check := crc16(frame)
		frame = append(frame, byte(check>>8), byte(check))
	} else {
		frame = append(frame, lrc(frame))
	}
	return session.line.Write(frame)
}

// receive1 reads one block, waiting for it until the deadline. Only as much as
// the block still needs is read: over-reading would take the front of the
// following block out of the pipe and leave the next exchange looking at a
// truncated one.
func (session *Session) receive1(deadline time.Time) (byte, []byte, error) {
	frame := make([]byte, 0, maxBlock)
	for {
		// Until the length byte has arrived, the prologue is all that is known to be
		// coming.
		want := prologueLength - len(frame)
		if len(frame) >= prologueLength {
			expected := prologueLength + int(frame[2]) + session.edcLength()
			if len(frame) >= expected {
				break
			}
			want = expected - len(frame)
		}
		if time.Now().After(deadline) {
			return 0, nil, errTimeout
		}
		ready, err := session.line.Ready()
		if err != nil {
			return 0, nil, err
		}
		if !ready {
			time.Sleep(pollInterval)
			continue
		}
		chunk := make([]byte, want)
		read, err := session.line.Read(chunk)
		if err != nil {
			return 0, nil, err
		}
		if read == 0 {
			time.Sleep(pollInterval)
			continue
		}
		frame = append(frame, chunk[:read]...)
	}

	if frame[0] != nodeAddress {
		return 0, nil, fmt.Errorf("%w: the block is addressed to %#02x", errMalformed, frame[0])
	}
	if err := session.verify(frame); err != nil {
		return 0, nil, err
	}
	return frame[1], frame[prologueLength : prologueLength+int(frame[2])], nil
}

func (session *Session) verify(frame []byte) error {
	if session.useCRC {
		body := frame[:len(frame)-2]
		want := crc16(body)
		got := uint16(frame[len(frame)-2])<<8 | uint16(frame[len(frame)-1])
		if want != got {
			return fmt.Errorf("%w: block check %#04x, want %#04x", errMalformed, got, want)
		}
		return nil
	}
	if lrc(frame) != 0 {
		return fmt.Errorf("%w: the block failed its check byte", errMalformed)
	}
	return nil
}

// discard clears whatever is left on the line, so that a broken exchange does not
// leave a prologue for the next one to trip over.
func (session *Session) discard() error {
	for round := 0; round < maxDiscardRounds; round++ {
		ready, err := session.line.Ready()
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
		chunk := make([]byte, maxFrame)
		if _, err := session.line.Read(chunk); err != nil {
			return err
		}
	}
	return fmt.Errorf("%w: the card would not stop sending", errMalformed)
}

// lrc is the one-byte check: the exclusive-or of the bytes it covers. Run over
// a whole received frame, including its own check byte, it comes to zero.
func lrc(data []byte) byte {
	var result byte
	for _, value := range data {
		result ^= value
	}
	return result
}

// crc16 is the two-byte check some cards ask for, CRC-16/CCITT-FALSE.
func crc16(data []byte) uint16 {
	crc := uint16(0xffff)
	for _, value := range data {
		crc ^= uint16(value) << 8
		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
