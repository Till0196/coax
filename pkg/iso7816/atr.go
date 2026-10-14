// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later
//
// The answer to reset, as ISO/IEC 7816-3 clause 8 lays it out: TS, T0, the
// interface bytes TA/TB/TC/TD that each TD announces, the historical bytes and
// the check byte.

package iso7816

import (
	"errors"
	"fmt"
	"time"
)

// errUnsupported reports a card whose answer is well formed but asks for
// something this reader cannot do. Unlike a malformed answer it is not worth
// a second reset.
var errUnsupported = errors.New("the card asked for something this reader does not support")

// atrParameters is what the answer-to-reset settles about the conversation
// that follows.
type atrParameters struct {
	// baudrate is the line speed the card wants, which also tells the two
	// card families apart: the 38400 ones need the block size agreed before
	// they are resynchronised rather than after.
	baudrate int
	// ifsc is the largest information field the card will accept.
	ifsc int
	// blockWait is how long one block may take.
	blockWait time.Duration
	// useCRC selects the two-byte check instead of the one-byte default.
	useCRC bool
}

// readATR collects the answer-to-reset. Knowing when it has all arrived is the
// difficulty: the length the board reports climbs while the answer is still
// coming in, so the only workable test is to parse what has arrived and see
// whether it is a complete structure. Gating on a non-zero length instead
// returned a truncated answer under load.
func (session *Session) readATR() ([]byte, atrParameters, error) {
	deadline := time.Now().Add(atrTimeout)
	var atr []byte
	for {
		if len(atr) != 0 {
			parameters, err := parseATR(atr)
			if err == nil {
				return atr, parameters, nil
			}
			if !errors.Is(err, errIncomplete) {
				return nil, parameters, err
			}
		}
		if time.Now().After(deadline) {
			if len(atr) == 0 {
				return nil, atrParameters{}, fmt.Errorf("%w: the card sent no answer to reset", errTimeout)
			}
			return nil, atrParameters{}, fmt.Errorf("%w: the answer to reset stopped after %d bytes", errTimeout, len(atr))
		}
		ready, err := session.line.Ready()
		if err != nil {
			return nil, atrParameters{}, err
		}
		if !ready {
			time.Sleep(pollInterval)
			continue
		}
		chunk := make([]byte, maxATR)
		read, err := session.line.Read(chunk)
		if err != nil {
			return nil, atrParameters{}, err
		}
		if read == 0 {
			time.Sleep(pollInterval)
			continue
		}
		atr = append(atr, chunk[:read]...)
		if len(atr) > maxATR {
			return nil, atrParameters{}, fmt.Errorf("%w: the answer to reset ran past %d bytes", errMalformed, maxATR)
		}
	}
}

// parseATR reads an answer-to-reset, reporting errIncomplete while it is still
// a valid prefix and errMalformed once it cannot become one. Only the
// interface bytes that change how this reader talks are acted on -- line speed
// from the first group, block size, timeout and check kind from the T=1 groups
// -- and the rest are stepped over, since their positions fix the length of
// the whole answer.
func parseATR(atr []byte) (atrParameters, error) {
	parameters := atrParameters{baudrate: 9600, ifsc: defaultIFSC, blockWait: defaultBlockWait}
	if len(atr) < 2 {
		return parameters, errIncomplete
	}
	// The first byte says which way round the card clocks its bits. Only the
	// direct convention appears on these boards, but both are well formed.
	if atr[0] != 0x3b && atr[0] != 0x3f {
		return parameters, fmt.Errorf("%w: the answer to reset starts with %#02x", errMalformed, atr[0])
	}

	present := atr[1] >> 4
	historical := int(atr[1] & 0x0f)
	offset := 2
	group := 1
	// t1Group records that the last protocol indicator named T=1, which is
	// what makes the next group's bytes the T=1 parameters.
	t1Group := false
	hasProtocolByte := false

	take := func() (byte, bool) {
		if offset >= len(atr) {
			return 0, false
		}
		value := atr[offset]
		offset++
		return value, true
	}

	for {
		if present&0x01 != 0 {
			ta, ok := take()
			if !ok {
				return parameters, errIncomplete
			}
			switch {
			case group == 1:
				switch ta {
				case 0x11:
					parameters.baudrate = 9600
				case 0x12:
					parameters.baudrate = 19200
				case 0x13:
					parameters.baudrate = 38400
				default:
					return parameters, fmt.Errorf("%w: line speed %#02x", errUnsupported, ta)
				}
			case t1Group && group >= 3:
				// Zero and 255 are reserved, so a value outside the range is
				// left at the default rather than treated as an error.
				if ta >= 1 && ta <= 254 {
					parameters.ifsc = int(ta)
				}
			}
		}
		if present&0x02 != 0 {
			tb, ok := take()
			if !ok {
				return parameters, errIncomplete
			}
			if t1Group && group >= 3 {
				parameters.blockWait = blockWaitFor(tb >> 4)
			}
		}
		if present&0x04 != 0 {
			tc, ok := take()
			if !ok {
				return parameters, errIncomplete
			}
			if t1Group && group >= 3 {
				parameters.useCRC = tc&0x01 != 0
			}
		}
		if present&0x08 == 0 {
			break
		}
		td, ok := take()
		if !ok {
			return parameters, errIncomplete
		}
		protocol := td & 0x0f
		// A card offering anything but T=0 closes its answer with a check byte.
		if protocol != 0 {
			hasProtocolByte = true
		}
		t1Group = protocol == 1
		present = td >> 4
		group++
	}

	expected := offset + historical
	if hasProtocolByte {
		expected++
	}
	if expected > maxATR {
		return parameters, fmt.Errorf("%w: the answer to reset claims %d bytes, over the %d one holds", errMalformed, expected, maxATR)
	}
	if len(atr) < expected {
		return parameters, errIncomplete
	}
	if len(atr) > expected {
		return parameters, fmt.Errorf("%w: %d bytes arrived for a %d-byte answer to reset", errMalformed, len(atr), expected)
	}
	if hasProtocolByte && lrc(atr[1:]) != 0 {
		return parameters, fmt.Errorf("%w: the answer to reset failed its check byte", errMalformed)
	}
	return parameters, nil
}

// blockWaitFor turns the card's block waiting-time index into a duration,
// clamped to the range this reader is prepared to wait.
func blockWaitFor(index byte) time.Duration {
	wait := maxBlockWait
	if index < 5 {
		wait = time.Duration(100<<index) * time.Millisecond
	}
	return min(max(wait, minBlockWait), maxBlockWait)
}
