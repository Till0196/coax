// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package iso7816

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// fakeLine is a card on the end of a byte pipe, built from a script of
// answers. It records what the host sent so a test can check the framing.
type fakeLine struct {
	present bool
	// answers are handed out one per completed host block.
	answers [][]byte
	pending []byte
	sent    [][]byte
	resets  int
	baud    int
	// readLimit caps one Read, so a test can make an answer arrive in pieces.
	readLimit int
	// delay holds the answer to the delayAt'th write back, so a test can make
	// the card slow at one chosen point in an exchange.
	delay   time.Duration
	delayAt int
	writes  int
	dueAt   time.Time
}

func (line *fakeLine) Present() (bool, error) { return line.present, nil }

func (line *fakeLine) Reset() error {
	line.resets++
	line.pending = nil
	if len(line.answers) != 0 {
		line.pending = line.answers[0]
		line.answers = line.answers[1:]
	}
	return nil
}

func (line *fakeLine) SetBaudrate(rate int) error { line.baud = rate; return nil }

func (line *fakeLine) Ready() (bool, error) {
	if len(line.pending) == 0 {
		return false, nil
	}
	if !line.dueAt.IsZero() && time.Now().Before(line.dueAt) {
		return false, nil
	}
	return true, nil
}

func (line *fakeLine) Read(buffer []byte) (int, error) {
	limit := len(buffer)
	if line.readLimit > 0 && line.readLimit < limit {
		limit = line.readLimit
	}
	count := copy(buffer[:limit], line.pending)
	line.pending = line.pending[count:]
	return count, nil
}

func (line *fakeLine) Write(data []byte) error {
	line.sent = append(line.sent, append([]byte(nil), data...))
	line.writes++
	if len(line.answers) != 0 {
		line.pending = append(line.pending, line.answers[0]...)
		line.answers = line.answers[1:]
		if line.delay != 0 && line.writes == line.delayAt {
			line.dueAt = time.Now().Add(line.delay)
		} else {
			line.dueAt = time.Time{}
		}
	}
	return nil
}

// block frames a T=1 block the way a card would, with the one-byte check.
func block(pcb byte, data ...byte) []byte {
	frame := append([]byte{0x00, pcb, byte(len(data))}, data...)
	return append(frame, lrc(frame))
}

// plainATR is the simplest answer a T=1 card can give: the direct convention,
// T=1 named once, no interface bytes beyond the protocol indicator, no
// historical bytes, and the check byte that a non-T=0 protocol requires.
func plainATR() []byte {
	// T0 promises a protocol indicator and no historical bytes; TD1 names T=1
	// and promises nothing further, so the check byte closes the answer.
	atr := []byte{0x3b, 0x80, 0x01}
	return append(atr, lrc(atr[1:]))
}

func TestParseATRIncompleteThenComplete(t *testing.T) {
	full := plainATR()
	for length := 1; length < len(full); length++ {
		if _, err := parseATR(full[:length]); !errors.Is(err, errIncomplete) {
			t.Errorf("a %d-byte prefix gave %v, want errIncomplete", length, err)
		}
	}
	if _, err := parseATR(full); err != nil {
		t.Errorf("the complete answer gave %v", err)
	}
	// One byte too many is not a longer answer, it is a broken one.
	if _, err := parseATR(append(full, 0x00)); !errors.Is(err, errMalformed) {
		t.Errorf("a trailing byte gave %v, want errMalformed", err)
	}
}

func TestParseATRRejectsBadStart(t *testing.T) {
	if _, err := parseATR([]byte{0x00, 0x00}); !errors.Is(err, errMalformed) {
		t.Errorf("a bad initial byte gave %v, want errMalformed", err)
	}
}

func TestParseATRRejectsBadCheckByte(t *testing.T) {
	atr := plainATR()
	atr[len(atr)-1] ^= 0xff
	if _, err := parseATR(atr); !errors.Is(err, errMalformed) {
		t.Errorf("a wrong check byte gave %v, want errMalformed", err)
	}
}

func TestParseATRReadsLineSpeed(t *testing.T) {
	cases := map[byte]int{0x11: 9600, 0x12: 19200, 0x13: 38400}
	for ta1, want := range cases {
		// TA1 present and T=1 named: interface byte map 0x90 in the second byte.
		atr := []byte{0x3b, 0x90, ta1, 0x01}
		atr = append(atr, lrc(atr[1:]))
		parameters, err := parseATR(atr)
		if err != nil {
			t.Fatalf("TA1 %#02x: %v", ta1, err)
		}
		if parameters.baudrate != want {
			t.Errorf("TA1 %#02x gave %d baud, want %d", ta1, parameters.baudrate, want)
		}
	}
	// A speed this reader has no divisor for is refused outright rather than
	// retried, because a second reset would produce the same answer.
	atr := []byte{0x3b, 0x90, 0x94, 0x01}
	atr = append(atr, lrc(atr[1:]))
	if _, err := parseATR(atr); !errors.Is(err, errUnsupported) {
		t.Errorf("an unsupported speed gave %v, want errUnsupported", err)
	}
}

func TestParseATRReadsT1Parameters(t *testing.T) {
	// TD1 names T=1 and promises TA2/TB2/TC2; TD2 names T=1 again and promises
	// the third group, which is where the T=1 parameters actually live.
	atr := []byte{
		0x3b, 0x80,
		0xf1,             // TD1: groups TA2..TD2 follow, protocol T=1
		0x00, 0x00, 0x00, // TA2, TB2, TC2 -- stepped over, not used
		0x71,             // TD2: TA3..TC3 follow, protocol T=1
		0xfe, 0x45, 0x01, // TA3 = IFSC, TB3 = timing, TC3 = CRC
	}
	atr = append(atr, lrc(atr[1:]))
	parameters, err := parseATR(atr)
	if err != nil {
		t.Fatalf("parseATR: %v", err)
	}
	if parameters.ifsc != 0xfe {
		t.Errorf("IFSC = %d, want 254", parameters.ifsc)
	}
	if !parameters.useCRC {
		t.Error("TC3 bit 0 was set but the two-byte check was not selected")
	}
	// BWI 4 is 100 << 4 = 1600 ms, inside the range this reader will wait.
	if parameters.blockWait != 1600*time.Millisecond {
		t.Errorf("block wait = %v, want 1.6s", parameters.blockWait)
	}
}

func TestBlockWaitIsClamped(t *testing.T) {
	if got := blockWaitFor(0); got != minBlockWait {
		t.Errorf("the shortest hint gave %v, want the %v floor", got, minBlockWait)
	}
	if got := blockWaitFor(9); got != maxBlockWait {
		t.Errorf("an out-of-range hint gave %v, want the %v ceiling", got, maxBlockWait)
	}
}

func TestCRC16(t *testing.T) {
	// The check value every CRC-16/CCITT-FALSE implementation is measured by.
	if got := crc16([]byte("123456789")); got != 0x29b1 {
		t.Errorf("crc16 = %#04x, want 0x29b1", got)
	}
}

func TestLRCOverAFrameComesToZero(t *testing.T) {
	frame := block(0x00, 0x01, 0x02, 0x03)
	if lrc(frame) != 0 {
		t.Error("a framed block did not check out to zero")
	}
}

// newTestSession builds a session already past its reset, so a test can drive
// Transmit without scripting the whole bring-up.
func newTestSession(line *fakeLine) *Session {
	session := New(line)
	session.open = true
	session.cardIFSC = 254
	return session
}

func TestTransmitFramesAndStrips(t *testing.T) {
	line := &fakeLine{present: true}
	// The card answers with an information block carrying two status bytes.
	line.answers = [][]byte{block(0x00, 0x90, 0x00)}
	session := newTestSession(line)

	answer, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00})
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if !bytes.Equal(answer, []byte{0x90, 0x00}) {
		t.Errorf("answer = %x, want 9000", answer)
	}
	if len(line.sent) != 1 {
		t.Fatalf("sent %d blocks, want 1", len(line.sent))
	}
	want := block(0x00, 0x00, 0xa4, 0x04, 0x00)
	if !bytes.Equal(line.sent[0], want) {
		t.Errorf("sent %x, want %x", line.sent[0], want)
	}
	// The host's block number advances only once the card has taken the block.
	if session.send != 1 {
		t.Errorf("send sequence = %d, want 1", session.send)
	}
}

func TestTransmitAlternatesTheBlockNumber(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{block(0x00, 0x90, 0x00), block(0x40, 0x90, 0x00)}
	session := newTestSession(line)

	for round := 0; round < 2; round++ {
		if _, err := session.Transmit([]byte{0x80, 0x00, 0x00, 0x00}); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	if got := line.sent[0][1]; got != 0x00 {
		t.Errorf("first block type = %#02x, want 0x00", got)
	}
	if got := line.sent[1][1]; got != 0x40 {
		t.Errorf("second block type = %#02x, want 0x40", got)
	}
}

func TestTransmitReassemblesAChainedAnswer(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		// First part, with the more-data bit set.
		block(blockMoreData, 0x01, 0x02),
		// Second part, sent in answer to the receive-ready block.
		block(0x40, 0x03, 0x90, 0x00),
	}
	session := newTestSession(line)

	answer, err := session.Transmit([]byte{0x80, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if want := []byte{0x01, 0x02, 0x03, 0x90, 0x00}; !bytes.Equal(answer, want) {
		t.Errorf("answer = %x, want %x", answer, want)
	}
	if len(line.sent) != 2 {
		t.Fatalf("sent %d blocks, want 2", len(line.sent))
	}
	// The acknowledgement is a receive-ready block asking for block one.
	if got := line.sent[1][1]; got != blockReceiveReady|0x10 {
		t.Errorf("acknowledgement block type = %#02x, want %#02x", got, blockReceiveReady|0x10)
	}
}

func TestTransmitAnswersARequestForMoreTime(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		block(blockSupervisory|blockWTX, 0x03),
		block(0x00, 0x90, 0x00),
	}
	session := newTestSession(line)

	answer, err := session.Transmit([]byte{0x80, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if !bytes.Equal(answer, []byte{0x90, 0x00}) {
		t.Errorf("answer = %x, want 9000", answer)
	}
	if len(line.sent) != 2 {
		t.Fatalf("sent %d blocks, want 2", len(line.sent))
	}
	// The grant echoes the multiplier the card asked for.
	want := block(blockSupervisory|blockResponse|blockWTX, 0x03)
	if !bytes.Equal(line.sent[1], want) {
		t.Errorf("grant = %x, want %x", line.sent[1], want)
	}
}

func TestTransmitDropsTheSessionOnFailure(t *testing.T) {
	line := &fakeLine{present: true}
	// A block whose check byte is wrong, repeated: every retry sees the same.
	bad := block(0x00, 0x90, 0x00)
	bad[len(bad)-1] ^= 0xff
	line.answers = [][]byte{bad, bad, bad, bad}
	session := newTestSession(line)
	session.blockWait = 20 * time.Millisecond

	if _, err := session.Transmit([]byte{0x80, 0x00, 0x00, 0x00}); err == nil {
		t.Fatal("a corrupt answer was accepted")
	}
	// The block numbers are no longer in step, so the whole session goes.
	if session.open {
		t.Error("the session survived a failed exchange")
	}
	if session.atr != nil || session.send != 0 || session.receive != 0 {
		t.Error("the session was not fully discarded")
	}
}

func TestTransmitRefusesWithoutASession(t *testing.T) {
	session := New(&fakeLine{present: true})
	if _, err := session.Transmit([]byte{0x80}); !errors.Is(err, errNoSession) {
		t.Errorf("error = %v, want errNoSession", err)
	}
}

func TestTransmitRefusesAnOversizedAPDU(t *testing.T) {
	session := newTestSession(&fakeLine{present: true})
	session.cardIFSC = 32
	if _, err := session.Transmit(make([]byte, 33)); err == nil {
		t.Error("an APDU over the card's block size was accepted")
	}
}

func TestResetReadsTheATRAndAgreesABlockSize(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		plainATR(),
		// A card on the default line speed is resynchronised first, then asked
		// for a block size.
		block(blockSupervisory | blockResponse | blockResynch),
		block(blockSupervisory|blockResponse|blockIFS, maxFrame-prologueLength-1),
	}
	session := New(line)

	atr, err := session.Reset()
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !bytes.Equal(atr, plainATR()) {
		t.Errorf("ATR = %x, want %x", atr, plainATR())
	}
	if !session.open {
		t.Error("the session did not open")
	}
	if len(line.sent) != 2 {
		t.Fatalf("sent %d blocks during bring-up, want 2", len(line.sent))
	}
	if got := line.sent[0][1]; got != blockSupervisory|blockResynch {
		t.Errorf("first block type = %#02x, want a resynchronisation", got)
	}
	if got := line.sent[1][1]; got != blockSupervisory|blockIFS {
		t.Errorf("second block type = %#02x, want a block size request", got)
	}
	// A 9600-baud card is asked for the largest field a frame leaves room for.
	if got := line.sent[1][3]; got != maxFrame-prologueLength-1 {
		t.Errorf("asked for a block size of %d, want %d", got, maxFrame-prologueLength-1)
	}
}

func TestResetRefusesAnEmptySlot(t *testing.T) {
	session := New(&fakeLine{present: false})
	if _, err := session.Reset(); !errors.Is(err, ErrNoCard) {
		t.Errorf("error = %v, want ErrNoCard", err)
	}
}

func TestResetRetriesOnceOnAMalformedAnswer(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		// Leftovers in front of the answer, which a second reset clears.
		append([]byte{0xff, 0xff}, plainATR()...),
		plainATR(),
		block(blockSupervisory | blockResponse | blockResynch),
		block(blockSupervisory|blockResponse|blockIFS, maxFrame-prologueLength-1),
	}
	session := New(line)

	if _, err := session.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if line.resets != 2 {
		t.Errorf("the card was reset %d times, want 2", line.resets)
	}
}

func TestResetReadsAnATRThatArrivesInPieces(t *testing.T) {
	line := &fakeLine{present: true, readLimit: 1}
	line.answers = [][]byte{
		plainATR(),
		block(blockSupervisory | blockResponse | blockResynch),
		block(blockSupervisory|blockResponse|blockIFS, maxFrame-prologueLength-1),
	}
	session := New(line)

	atr, err := session.Reset()
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !bytes.Equal(atr, plainATR()) {
		t.Errorf("ATR = %x, want %x", atr, plainATR())
	}
	if line.resets != 1 {
		t.Errorf("the card was reset %d times, want 1", line.resets)
	}
}

func TestResetOrdersTheBringUpByLineSpeed(t *testing.T) {
	// A card asking for the fastest line speed has its block size agreed
	// first, and is resynchronised only if that fails.
	fastATR := []byte{0x3b, 0x90, 0x13, 0x01}
	fastATR = append(fastATR, lrc(fastATR[1:]))
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		fastATR,
		block(blockSupervisory|blockResponse|blockIFS, 254),
	}
	session := New(line)

	if _, err := session.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if line.baud != 38400 {
		t.Errorf("the line was left at %d baud, want 38400", line.baud)
	}
	if len(line.sent) != 1 {
		t.Fatalf("sent %d blocks during bring-up, want 1", len(line.sent))
	}
	if got := line.sent[0][1]; got != blockSupervisory|blockIFS {
		t.Errorf("first block type = %#02x, want a block size request", got)
	}
}

func TestInvalidateDiscardsEverything(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		plainATR(),
		block(blockSupervisory | blockResponse | blockResynch),
		block(blockSupervisory|blockResponse|blockIFS, maxFrame-prologueLength-1),
	}
	session := New(line)
	if _, err := session.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	session.send = 1
	session.receive = 1
	session.useCRC = true
	session.cardIFSC = 200

	session.Invalidate()

	if session.open || session.atr != nil || session.send != 0 || session.receive != 0 ||
		session.useCRC || session.cardIFSC != defaultIFSC || session.blockWait != defaultBlockWait ||
		session.baudrate != 9600 {
		t.Errorf("state survived the invalidation: %+v", session)
	}
	if session.ATR() != nil {
		t.Error("the ATR is still readable after the session went")
	}
}

// TestReceiveTakesOnlyItsOwnBlock checks the reader stops at the end of one
// block. The line carries whatever the card sends next, so reading past the
// block would leave the following exchange looking at a truncated one.
func TestReceiveTakesOnlyItsOwnBlock(t *testing.T) {
	line := &fakeLine{present: true}
	// Two answers arrive back to back, before the first is read.
	line.answers = [][]byte{
		append(block(0x00, 0x90, 0x00), block(0x40, 0x6a, 0x82)...),
		nil,
	}
	session := newTestSession(line)

	first, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00})
	if err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if !bytes.Equal(first, []byte{0x90, 0x00}) {
		t.Fatalf("first answer = %x, want 9000", first)
	}
	second, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00})
	if err != nil {
		t.Fatalf("second exchange: %v", err)
	}
	if !bytes.Equal(second, []byte{0x6a, 0x82}) {
		t.Errorf("second answer = %x, want 6a82", second)
	}
}

// TestTransmitAcceptsAFullSizeAnswer checks a card may send an information
// field right up to its own limit. The block that carries it is longer than
// anything this host sends, so the two bounds have to be separate.
func TestTransmitAcceptsAFullSizeAnswer(t *testing.T) {
	payload := make([]byte, maxInfoField)
	for index := range payload {
		payload[index] = byte(index)
	}
	line := &fakeLine{present: true}
	line.answers = [][]byte{block(0x00, payload...)}
	session := newTestSession(line)

	answer, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00})
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if !bytes.Equal(answer, payload) {
		t.Errorf("answer was %d bytes, want %d", len(answer), len(payload))
	}
}

// TestExchangeClearsTheLineBeforeRetrying checks a failed attempt does not
// poison the retry. Bytes the card already sent stay in the pipe, and would
// otherwise be read as the front of the retransmission's answer.
func TestExchangeClearsTheLineBeforeRetrying(t *testing.T) {
	line := &fakeLine{present: true}
	corrupt := block(0x00, 0x90, 0x00)
	corrupt[len(corrupt)-1] ^= 0xff
	// The card follows its bad block with bytes that belong to nothing. They
	// stay in the pipe, and the retransmission's answer lands behind them.
	corrupt = append(corrupt, 0xff, 0xff)
	line.answers = [][]byte{corrupt, block(0x00, 0x90, 0x00)}
	session := newTestSession(line)
	session.blockWait = 50 * time.Millisecond

	answer, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00})
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if !bytes.Equal(answer, []byte{0x90, 0x00}) {
		t.Errorf("answer = %x, want 9000", answer)
	}
	if len(line.sent) != 2 {
		t.Errorf("sent %d blocks, want the original and one retransmission", len(line.sent))
	}
}

// TestExchangeAnswersACardInitiatedBlockSizeRequest checks a card may set the
// size in its own direction mid-conversation without the session being torn
// down over it.
func TestExchangeAnswersACardInitiatedBlockSizeRequest(t *testing.T) {
	line := &fakeLine{present: true}
	line.answers = [][]byte{
		block(blockSupervisory|blockIFS, 0x20),
		block(0x00, 0x90, 0x00),
	}
	session := newTestSession(line)

	answer, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00})
	if err != nil {
		t.Fatalf("Transmit: %v", err)
	}
	if !bytes.Equal(answer, []byte{0x90, 0x00}) {
		t.Errorf("answer = %x, want 9000", answer)
	}
	want := block(blockSupervisory|blockResponse|blockIFS, 0x20)
	if len(line.sent) != 2 || !bytes.Equal(line.sent[1], want) {
		t.Errorf("the card's request was answered with %x, want %x", line.sent[1], want)
	}
}

// TestExchangeGrantsTheTimeItPromises checks a request for more time actually
// buys the card that time, rather than only being acknowledged.
func TestExchangeGrantsTheTimeItPromises(t *testing.T) {
	// The request arrives at once; the answer to the grant is what takes time.
	line := &fakeLine{present: true, delay: 300 * time.Millisecond, delayAt: 2}
	line.answers = [][]byte{
		block(blockSupervisory|blockWTX, 0x0a),
		block(0x00, 0x90, 0x00),
	}
	session := newTestSession(line)
	// Ten times this wait covers the delay. One times it does not, and neither
	// does retrying: the multiplier has to be applied for the answer to arrive.
	session.blockWait = 40 * time.Millisecond

	if _, err := session.Transmit([]byte{0x00, 0xa4, 0x04, 0x00}); err != nil {
		t.Fatalf("Transmit: %v", err)
	}
}
