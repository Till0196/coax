// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winscard

import "time"

// Scope is the scope of a resource manager context, SCARD_SCOPE_* in
// winscard.h. Only ScopeSystem means anything to the providers here, but the
// value is passed to a forwarding provider unchanged.
type Scope uint32

const (
	ScopeUser     Scope = 0
	ScopeTerminal Scope = 1
	ScopeSystem   Scope = 2
	// ScopeGlobal is pcsc-lite's own SCARD_SCOPE_GLOBAL; Windows has no such
	// scope. Accepted because clients written against pcsc-lite pass it and
	// pcsc-lite ignores the scope entirely.
	ScopeGlobal Scope = 3
)

// Protocol is a bitmask of card transmission protocols, SCARD_PROTOCOL_* in
// winscard.h. Connect takes the set a caller will accept and reports the one
// that was agreed.
//
// ProtocolRaw takes the Windows value; pcsc-lite numbers it 0x0004, so a
// binding built against pcsc-lite translates at the boundary. Windows reserves
// the low 16 bits for the ISO/IEC 7816-3 protocol number, while pcsc-lite takes
// the next free bit.
type Protocol uint32

const (
	ProtocolUndefined Protocol = 0x00000000
	ProtocolT0        Protocol = 0x00000001
	ProtocolT1        Protocol = 0x00000002
	ProtocolRaw       Protocol = 0x00010000
	// ProtocolAny is the pair a caller offers when it does not care, spelled
	// SCARD_PROTOCOL_Tx in winscard.h.
	ProtocolAny Protocol = ProtocolT0 | ProtocolT1
)

// Has reports whether the set contains the given protocol.
func (p Protocol) Has(other Protocol) bool { return p&other != 0 }

// String names the protocol the way winscard.h spells it.
func (p Protocol) String() string {
	switch p {
	case ProtocolUndefined:
		return "SCARD_PROTOCOL_UNDEFINED"
	case ProtocolT0:
		return "SCARD_PROTOCOL_T0"
	case ProtocolT1:
		return "SCARD_PROTOCOL_T1"
	case ProtocolRaw:
		return "SCARD_PROTOCOL_RAW"
	case ProtocolAny:
		return "SCARD_PROTOCOL_Tx"
	}
	return "SCARD_PROTOCOL_?"
}

// ShareMode says how a connection tolerates other connections to the same card,
// SCARD_SHARE_* in winscard.h. It matters more here than for an external
// reader: there is one slot per enclosure however many tuners share it.
type ShareMode uint32

const (
	ShareExclusive ShareMode = 1
	ShareShared    ShareMode = 2
	// ShareDirect connects to the reader with no card in it, for SCardControl
	// against the reader itself.
	ShareDirect ShareMode = 3
)

// Disposition is what should happen to the card when a connection or a
// transaction ends, SCARD_*_CARD in winscard.h.
type Disposition uint32

const (
	// LeaveCard leaves the card powered and its T=1 session intact.
	LeaveCard Disposition = 0
	// ResetCard warm-resets the card, so the next exchange starts from a fresh
	// answer to reset.
	ResetCard Disposition = 1
	// UnpowerCard removes power from the card.
	UnpowerCard Disposition = 2
	// EjectCard ejects the card. No reader reached through this package has a
	// motorised slot, so providers answer SCARD_E_UNSUPPORTED_FEATURE.
	EjectCard Disposition = 3
)

// State is the bitmask GetStatusChange trades in, SCARD_STATE_* in winscard.h.
// A caller passes the state it believes a reader is in and gets back the state
// the reader is actually in, with StateChanged set when the two differ.
type State uint32

const (
	StateUnaware     State = 0x00000000
	StateIgnore      State = 0x00000001
	StateChanged     State = 0x00000002
	StateUnknown     State = 0x00000004
	StateUnavailable State = 0x00000008
	StateEmpty       State = 0x00000010
	StatePresent     State = 0x00000020
	StateATRMatch    State = 0x00000040
	StateExclusive   State = 0x00000080
	StateInUse       State = 0x00000100
	StateMute        State = 0x00000200
	StateUnpowered   State = 0x00000400
)

// Has reports whether the mask contains the given flag.
func (s State) Has(other State) bool { return s&other != 0 }

// Count is the number of card insertions and removals a reader has seen,
// carried in the high 16 bits of the state word in winscard.h. It has its own
// accessor because a caller comparing whole state words would otherwise see a
// count change as a state change.
func (s State) Count() uint16 { return uint16(s >> 16) }

// WithCount returns the state word with its event count replaced.
func (s State) WithCount(count uint16) State {
	return (s & 0x0000FFFF) | State(uint32(count)<<16)
}

// CardState is the card's power state as SCardStatus reports it, SCARD_ABSENT
// through SCARD_SPECIFIC in winscard.h. These are ordinal values on Windows and
// bit flags in pcsc-lite; this package follows Windows.
type CardState uint32

const (
	CardAbsent     CardState = 1
	CardPresent    CardState = 2
	CardSwallowed  CardState = 3
	CardPowered    CardState = 4
	CardNegotiable CardState = 5
	CardSpecific   CardState = 6
)

// String names the state the way winscard.h spells it.
func (c CardState) String() string {
	switch c {
	case CardAbsent:
		return "SCARD_ABSENT"
	case CardPresent:
		return "SCARD_PRESENT"
	case CardSwallowed:
		return "SCARD_SWALLOWED"
	case CardPowered:
		return "SCARD_POWERED"
	case CardNegotiable:
		return "SCARD_NEGOTIABLE"
	case CardSpecific:
		return "SCARD_SPECIFIC"
	}
	return "SCARD_?"
}

// Attribute identifies a reader or card attribute, SCARD_ATTR_* in winscard.h
// and pcsc-lite's reader.h. Only the ones a card interface of this kind can
// answer are named; the numbering is open, so a provider may accept others.
// Both headers agree on these values, PC/SC defining the class and tag they are
// composed from.
type Attribute uint32

// Attribute classes, SCARD_CLASS_* in reader.h, so a caller can compose an
// attribute this package has not named.
const (
	ClassVendorInfo     uint16 = 1
	ClassCommunications uint16 = 2
	ClassProtocol       uint16 = 3
	ClassPowerMgmt      uint16 = 4
	ClassSecurity       uint16 = 5
	ClassMechanical     uint16 = 6
	ClassVendorDefined  uint16 = 7
	ClassIFDProtocol    uint16 = 8
	ClassICCState       uint16 = 9
	ClassSystem         uint16 = 0x7FFF
)

// AttributeValue composes an attribute identifier from its class and tag, the
// SCARD_ATTR_VALUE macro in winscard.h.
func AttributeValue(class, tag uint16) Attribute {
	return Attribute(uint32(class)<<16 | uint32(tag))
}

const (
	AttrVendorName         Attribute = 0x00010100
	AttrVendorIFDType      Attribute = 0x00010101
	AttrVendorIFDVersion   Attribute = 0x00010102
	AttrVendorIFDSerialNo  Attribute = 0x00010103
	AttrChannelID          Attribute = 0x00020110
	AttrAsyncProtocolTypes Attribute = 0x00030120
	AttrMaxIFSD            Attribute = 0x00030125
	AttrSyncProtocolTypes  Attribute = 0x00030126
	AttrPowerMgmtSupport   Attribute = 0x00040131
	AttrCharacteristics    Attribute = 0x00060150
	// AttrMaxInput is the longest command a reader accepts in one call. It is the
	// only attribute here from the vendor-defined class, and it is how a client
	// learns that a card interface framing its own T=1 blocks cannot take an APDU
	// of any length.
	AttrMaxInput          Attribute = 0x0007A007
	AttrCurrentProtocol   Attribute = 0x00080201
	AttrCurrentIFSC       Attribute = 0x00080207
	AttrCurrentIFSD       Attribute = 0x00080208
	AttrICCPresence       Attribute = 0x00090300
	AttrICCInterfaceState Attribute = 0x00090301
	AttrCurrentIOState    Attribute = 0x00090302
	AttrATRString         Attribute = 0x00090303
	AttrICCTypePerATR     Attribute = 0x00090304
	AttrDeviceUnit        Attribute = 0x7FFF0001
	AttrDeviceInUse       Attribute = 0x7FFF0002
	AttrDeviceFriendlyA   Attribute = 0x7FFF0003
	AttrDeviceSystemA     Attribute = 0x7FFF0004
	AttrDeviceFriendlyW   Attribute = 0x7FFF0005
	AttrDeviceSystemW     Attribute = 0x7FFF0006
)

// AllReaderGroups is the group every reader belongs to, SCARD_ALL_READERS in
// winscard.h. An empty group list means the same thing.
const AllReaderGroups = "SCard$AllReaders"

// DefaultReaderGroup is the group a reader joins when nothing says otherwise,
// SCARD_DEFAULT_READERS in winscard.h.
const DefaultReaderGroup = "SCard$DefaultReaders"

// PnPNotification is the pseudo-reader whose state changes when the set of
// readers changes rather than when a card moves; applications watch it to
// notice a tuner being plugged in. pcsc-lite has no macro for the name but
// honours it, so the Windows spelling works on both.
const PnPNotification = `\\?PnP?\Notification`

// Infinite is the GetStatusChange timeout that never expires, INFINITE in
// winscard.h. Negative because zero already means "poll and return at once".
const Infinite time.Duration = -1

// ReaderState is one entry of the array GetStatusChange watches,
// SCARD_READERSTATE in winscard.h. The caller fills Reader and Current, the
// call fills Event and ATR, and UserData is carried through untouched because a
// caller's array is often a sparse view.
type ReaderState struct {
	// Reader names the reader, or is PnPNotification.
	Reader string
	// UserData is opaque to this package.
	UserData any
	// Current is the state the caller believes the reader is in.
	Current State
	// Event is the state the reader is actually in, with StateChanged set when
	// that differs from Current.
	Event State
	// ATR is the answer to reset of the card now in the reader, empty when
	// there is none.
	ATR []byte
}

// Status is what SCardStatus reports about a connected card.
type Status struct {
	// Readers names the reader the card is in. A list because the WinSCard call
	// returns a multi-string; a card is in one reader.
	Readers []string
	// State is the card's power state.
	State CardState
	// Protocol is the protocol in use, ProtocolUndefined for a ShareDirect
	// connection that never negotiated one.
	Protocol Protocol
	// ATR is the card's answer to reset.
	ATR []byte
}
