// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winscard

import (
	"errors"
	"fmt"
)

// Code is a WinSCard status code, the value every WinSCard entry point
// returns. The numbering follows winscard.h, which pcsc-lite copies, so one
// set of constants serves both platforms. Code implements error and compares
// by value, so errors.Is works against the constants below directly and
// through fmt.Errorf's %w.
type Code uint32

// Success is the absence of an error. This package returns a nil error
// instead; CodeOf turns nil back into Success for a caller that has to produce
// a status code.
const Success Code = 0x00000000

// Failure codes, SCARD_F_* and SCARD_E_* in winscard.h.
const (
	ErrInternalError      Code = 0x80100001
	ErrCancelled          Code = 0x80100002
	ErrInvalidHandle      Code = 0x80100003
	ErrInvalidParameter   Code = 0x80100004
	ErrInvalidTarget      Code = 0x80100005
	ErrNoMemory           Code = 0x80100006
	ErrWaitedTooLong      Code = 0x80100007
	ErrInsufficientBuffer Code = 0x80100008
	ErrUnknownReader      Code = 0x80100009
	ErrTimeout            Code = 0x8010000A
	ErrSharingViolation   Code = 0x8010000B
	ErrNoSmartcard        Code = 0x8010000C
	ErrUnknownCard        Code = 0x8010000D
	ErrCantDispose        Code = 0x8010000E
	ErrProtoMismatch      Code = 0x8010000F
	ErrNotReady           Code = 0x80100010
	ErrInvalidValue       Code = 0x80100011
	ErrSystemCancelled    Code = 0x80100012
	ErrCommError          Code = 0x80100013
	ErrUnknownError       Code = 0x80100014
	ErrInvalidATR         Code = 0x80100015
	ErrNotTransacted      Code = 0x80100016
	ErrReaderUnavailable  Code = 0x80100017
	ErrShutdown           Code = 0x80100018
	ErrPCITooSmall        Code = 0x80100019
	ErrReaderUnsupported  Code = 0x8010001A
	ErrDuplicateReader    Code = 0x8010001B
	ErrCardUnsupported    Code = 0x8010001C
	ErrNoService          Code = 0x8010001D
	ErrServiceStopped     Code = 0x8010001E
	ErrUnexpected         Code = 0x8010001F
	ErrUnsupportedFeature Code = 0x80100022
	ErrNoReadersAvailable Code = 0x8010002E
	ErrCommDataLost       Code = 0x8010002F
	ErrServerTooBusy      Code = 0x80100031
)

// Warning codes, SCARD_W_* in winscard.h: the card is no longer in the state
// the caller assumed rather than the call being malformed, so a caller is
// expected to reconnect rather than give up.
const (
	ErrUnsupportedCard      Code = 0x80100065
	ErrUnresponsiveCard     Code = 0x80100066
	ErrUnpoweredCard        Code = 0x80100067
	ErrResetCard            Code = 0x80100068
	ErrRemovedCard          Code = 0x80100069
	ErrSecurityViolation    Code = 0x8010006A
	ErrCancelledByUser      Code = 0x8010006E
	ErrCardNotAuthenticated Code = 0x8010006F
)

// codeNames is the winscard.h spelling of each code: a developer looking a
// failure up searches for SCARD_E_SHARING_VIOLATION, not a sentence about it.
var codeNames = map[Code]string{
	Success:                 "SCARD_S_SUCCESS",
	ErrInternalError:        "SCARD_F_INTERNAL_ERROR",
	ErrCancelled:            "SCARD_E_CANCELLED",
	ErrInvalidHandle:        "SCARD_E_INVALID_HANDLE",
	ErrInvalidParameter:     "SCARD_E_INVALID_PARAMETER",
	ErrInvalidTarget:        "SCARD_E_INVALID_TARGET",
	ErrNoMemory:             "SCARD_E_NO_MEMORY",
	ErrWaitedTooLong:        "SCARD_F_WAITED_TOO_LONG",
	ErrInsufficientBuffer:   "SCARD_E_INSUFFICIENT_BUFFER",
	ErrUnknownReader:        "SCARD_E_UNKNOWN_READER",
	ErrTimeout:              "SCARD_E_TIMEOUT",
	ErrSharingViolation:     "SCARD_E_SHARING_VIOLATION",
	ErrNoSmartcard:          "SCARD_E_NO_SMARTCARD",
	ErrUnknownCard:          "SCARD_E_UNKNOWN_CARD",
	ErrCantDispose:          "SCARD_E_CANT_DISPOSE",
	ErrProtoMismatch:        "SCARD_E_PROTO_MISMATCH",
	ErrNotReady:             "SCARD_E_NOT_READY",
	ErrInvalidValue:         "SCARD_E_INVALID_VALUE",
	ErrSystemCancelled:      "SCARD_E_SYSTEM_CANCELLED",
	ErrCommError:            "SCARD_F_COMM_ERROR",
	ErrUnknownError:         "SCARD_F_UNKNOWN_ERROR",
	ErrInvalidATR:           "SCARD_E_INVALID_ATR",
	ErrNotTransacted:        "SCARD_E_NOT_TRANSACTED",
	ErrReaderUnavailable:    "SCARD_E_READER_UNAVAILABLE",
	ErrShutdown:             "SCARD_P_SHUTDOWN",
	ErrPCITooSmall:          "SCARD_E_PCI_TOO_SMALL",
	ErrReaderUnsupported:    "SCARD_E_READER_UNSUPPORTED",
	ErrDuplicateReader:      "SCARD_E_DUPLICATE_READER",
	ErrCardUnsupported:      "SCARD_E_CARD_UNSUPPORTED",
	ErrNoService:            "SCARD_E_NO_SERVICE",
	ErrServiceStopped:       "SCARD_E_SERVICE_STOPPED",
	ErrUnexpected:           "SCARD_E_UNEXPECTED",
	ErrUnsupportedFeature:   "SCARD_E_UNSUPPORTED_FEATURE",
	ErrNoReadersAvailable:   "SCARD_E_NO_READERS_AVAILABLE",
	ErrCommDataLost:         "SCARD_E_COMM_DATA_LOST",
	ErrServerTooBusy:        "SCARD_E_SERVER_TOO_BUSY",
	ErrUnsupportedCard:      "SCARD_W_UNSUPPORTED_CARD",
	ErrUnresponsiveCard:     "SCARD_W_UNRESPONSIVE_CARD",
	ErrUnpoweredCard:        "SCARD_W_UNPOWERED_CARD",
	ErrResetCard:            "SCARD_W_RESET_CARD",
	ErrRemovedCard:          "SCARD_W_REMOVED_CARD",
	ErrSecurityViolation:    "SCARD_W_SECURITY_VIOLATION",
	ErrCancelledByUser:      "SCARD_W_CANCELLED_BY_USER",
	ErrCardNotAuthenticated: "SCARD_W_CARD_NOT_AUTHENTICATED",
}

// String is the winscard.h name of the code, or its hexadecimal value when
// unnamed here: a forwarding provider may return a code from outside this
// list, and a single "unknown" would lose the only clue there is.
func (c Code) String() string {
	if name, ok := codeNames[c]; ok {
		return name
	}
	return fmt.Sprintf("SCARD_0x%08X", uint32(c))
}

// Error makes every code usable as an error, Success included so a misplaced
// Success is visible in a log rather than an empty message.
func (c Code) Error() string { return c.String() }

// OrNil turns a status code into the error convention this package uses:
// Success is no error at all, everything else is itself.
func (c Code) OrNil() error {
	if c == Success {
		return nil
	}
	return c
}

// CodeOf recovers the status code an error carries, for a binding that must
// return a LONG. A nil error is Success, and a code anywhere in the chain is
// that code, so wrapping does not lose it. Anything else is
// SCARD_F_INTERNAL_ERROR: an error reaching the boundary without a code is a
// fault here, not a card condition, and dressing it as one would send the
// caller looking at the card.
func CodeOf(err error) Code {
	if err == nil {
		return Success
	}
	var code Code
	if errors.As(err, &code) {
		return code
	}
	return ErrInternalError
}
