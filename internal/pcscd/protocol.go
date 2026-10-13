// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package pcscd speaks the protocol pcsc-lite's client library uses to reach
// its daemon, so a PC/SC application can find the card interfaces built into
// tuner boards without loading a driver into anything.
//
// The usual way to add a reader is an IFD handler, a shared library the daemon
// loads, which would mean cgo and unsafe. Speaking the daemon's protocol needs
// neither -- it is bytes on a Unix socket -- and keeps this in its own process
// where a failure cannot take an application down with it.
//
// The cost is that the protocol is pcsc-lite's internal interface, not a
// published one. It is versioned and both ends check the version first, so a
// mismatch is a clean refusal rather than a misread structure; the structures
// below are pcsc-lite's own and their sizes are asserted in the tests.
//
// The wire format is the C structures as the host lays them out, so numbers are
// in host byte order -- that is what the client library writes.
package pcscd

import (
	"encoding/binary"
	"fmt"
	"io"
)

// The protocol version this speaks, PROTOCOL_VERSION_MAJOR and
// PROTOCOL_VERSION_MINOR in winscard_msg.h. A client offering an older minor
// version down to backwardMinor is accepted and told the version below, which
// is what pcscd itself answers; anything else is refused with
// SCARD_E_SERVICE_STOPPED.
const (
	versionMajor   = 4
	versionMinor   = 6
	backwardMinor  = 4
	oldReaderState = 5 // minor versions at or below this use the fixed-size array
)

// Commands, from enum pcsc_msg_commands.
const (
	cmdEstablishContext = 0x01
	cmdReleaseContext   = 0x02
	cmdListReaders      = 0x03
	cmdConnect          = 0x04
	cmdReconnect        = 0x05
	cmdDisconnect       = 0x06
	cmdBeginTransaction = 0x07
	cmdEndTransaction   = 0x08
	cmdTransmit         = 0x09
	cmdControl          = 0x0A
	cmdStatus           = 0x0B
	cmdGetStatusChange  = 0x0C
	cmdCancel           = 0x0D
	cmdCancelTransact   = 0x0E
	cmdGetAttrib        = 0x0F
	cmdSetAttrib        = 0x10
	cmdVersion          = 0x11
	cmdGetReadersState  = 0x12
	cmdWaitStateChange  = 0x13
	cmdStopWaiting      = 0x14
	cmdGetReaderEvents  = 0x15
	cmdGetStateSize     = 0x16
	cmdGetStateArray    = 0x17
)

// Sizes fixed by pcsc-lite. maxReaders is PCSCLITE_MAX_READERS_CONTEXTS, the
// length of the reader array a client of any supported version expects;
// maxReaderName is MAX_READERNAME; maxATR is MAX_ATR_SIZE; maxAttribute is
// MAX_BUFFER_SIZE.
const (
	maxReaders     = 16
	maxReaderName  = 128
	maxATR         = 33
	maxAttribute   = 264
	maxTransmit    = 4 + 3 + (1 << 16) + 3 + 2 // MAX_BUFFER_SIZE_EXTENDED
	readerStateLen = 184
)

// order is the byte order the structures are written in: the host's, because
// the client library writes its structures straight to the socket.
var order = binary.NativeEndian

// header is struct rxHeader, which precedes every message from a client. A
// reply carries no header: the client knows what it asked for and reads
// exactly the answer's length.
type header struct {
	size    uint32
	command uint32
}

func readHeader(source io.Reader) (header, error) {
	raw := make([]byte, 8)
	if _, err := io.ReadFull(source, raw); err != nil {
		return header{}, err
	}
	return header{size: order.Uint32(raw[0:4]), command: order.Uint32(raw[4:8])}, nil
}

// body reads a request body of the size the header announced, refusing one that
// is not the size of the command's structure. A wrong length is not recoverable
// -- the next bytes would be read as the next header -- so pcscd drops such a
// client, and so does this.
func body(source io.Reader, announced uint32, expected int) ([]byte, error) {
	if int(announced) != expected {
		return nil, fmt.Errorf("message body is %d bytes, want %d", announced, expected)
	}
	raw := make([]byte, expected)
	if _, err := io.ReadFull(source, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// The structure sizes, asserted in the tests against the C ones.
const (
	sizeVersion    = 12
	sizeEstablish  = 12
	sizeRelease    = 8
	sizeConnect    = 152
	sizeReconnect  = 24
	sizeDisconnect = 12
	sizeBegin      = 8
	sizeEnd        = 12
	sizeCancel     = 8
	sizeStatus     = 8
	sizeTransmit   = 32
	sizeControl    = 24
	sizeGetSet     = 280
	sizeWait       = 8
	sizeEvents     = 8
)

func field(raw []byte, index int) uint32 { return order.Uint32(raw[index*4 : index*4+4]) }

func put(raw []byte, index int, value uint32) { order.PutUint32(raw[index*4:index*4+4], value) }

func cString(raw []byte) string {
	for index, letter := range raw {
		if letter == 0 {
			return string(raw[:index])
		}
	}
	return string(raw)
}

// putCString writes a C string into a fixed-width field, truncating a name
// that does not fit and leaving room for the terminator.
func putCString(raw []byte, value string) {
	for index := range raw {
		raw[index] = 0
	}
	if len(value) > len(raw)-1 {
		value = value[:len(raw)-1]
	}
	copy(raw, value)
}

// readerState is READER_STATE from readers.h, the entry a client reads a
// reader's whole condition from. The client derives the SCARD_STATE_* flags of
// SCardGetStatusChange from these fields itself, so what is published here is
// the hardware's state.
type readerState struct {
	name string
	// events counts card insertions and removals.
	events uint32
	// state is the SCARD_* bit field: pcsc-lite's flags, not the ordinals
	// Windows uses for the same names.
	state uint32
	// sharing is -1 for an exclusive connection, 0 for none, or the number of
	// shared connections.
	sharing int32
	atr     []byte
	// protocol is the SCARD_PROTOCOL_* value, in pcsc-lite's numbering.
	protocol uint32
}

// The reader state bits, as pcsc-lite numbers them. They are flags here and
// ordinals on Windows; this is the daemon's side of that difference.
const (
	stateUnknown    = 0x0001
	stateAbsent     = 0x0002
	statePresent    = 0x0004
	stateSwallowed  = 0x0008
	statePowered    = 0x0010
	stateNegotiable = 0x0020
	stateSpecific   = 0x0040
)

// sharingNone and sharingExclusive are PCSCLITE_SHARING_NO_CONTEXT and
// PCSCLITE_SHARING_EXCLUSIVE_CONTEXT.
const (
	sharingNone      int32 = 0
	sharingExclusive int32 = -1
)

// protocolT1 is SCARD_PROTOCOL_T1 as pcsc-lite numbers it.
const protocolT1 = 0x0002

func (r readerState) encode() []byte {
	raw := make([]byte, readerStateLen)
	putCString(raw[0:maxReaderName], r.name)
	order.PutUint32(raw[128:132], r.events)
	order.PutUint32(raw[132:136], r.state)
	order.PutUint32(raw[136:140], uint32(r.sharing))
	atr := r.atr
	if len(atr) > maxATR {
		atr = atr[:maxATR]
	}
	copy(raw[140:140+maxATR], atr)
	// 173 to 176 is the padding before the next word, which the C structure
	// has and which must be left alone rather than packed out.
	order.PutUint32(raw[176:180], uint32(len(atr)))
	order.PutUint32(raw[180:184], r.protocol)
	return raw
}
