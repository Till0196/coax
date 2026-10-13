// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package winscard

import (
	"context"
	"time"
)

// Provider is one source of readers behind the API. Two kinds exist and a
// caller of Service cannot tell them apart: one drives a card interface built
// into a tuner board over device.CardReader, the other forwards to the platform
// resource manager so an application loading this instead of the system
// implementation does not lose the readers it had.
//
// Enumeration follows the providers' order in the Service, unsorted, with the
// built-in readers first: the common client behaviour is to take the first
// reader with a card in it.
type Provider interface {
	// Name identifies the provider in diagnostics, not to applications.
	Name() string
	// EstablishContext opens the provider's own resource manager context,
	// lazily on the first call that needs one so that an unavailable provider
	// (no boards attached, no pcscd running) costs nothing.
	EstablishContext(scope Scope) (ProviderContext, error)
}

// ProviderContext is one provider's half of an application context. A Service
// context holds one of these per provider and releases them all together.
type ProviderContext interface {
	// Readers lists the provider's readers by name. Names must be stable while the
	// hardware is attached and unique across all providers of one Service, so a
	// built-in reader's name must distinguish one board from another of the same
	// model. An empty group list, or one naming AllReaderGroups, asks for every
	// reader.
	Readers(groups []string) ([]string, error)
	// ReaderGroups lists the reader groups the provider knows.
	ReaderGroups() ([]string, error)
	// Connect opens a card in one of the provider's readers and reports the agreed
	// protocol. A built-in reader answers ErrProtoMismatch when the set omits
	// ProtocolT1, and ErrInvalidValue for ShareDirect: it has no reader-level
	// channel to connect to without a card. Only a forwarding provider supports
	// ShareDirect.
	Connect(reader string, share ShareMode, protocols Protocol) (Card, Protocol, error)
	// GetStatusChange waits for one of the given readers to leave the state the
	// caller believes it is in, and returns the array with Event filled. It must
	// return ErrCancelled when ctx is done. A provider forwarding to a blocking
	// platform call issues the platform's own cancel, but only while its wait is
	// in progress: on some implementations a cancel left pending on an idle
	// context is inherited by the next call.
	GetStatusChange(ctx context.Context, timeout time.Duration, states []ReaderState) ([]ReaderState, error)
	// Release closes the provider context and everything still open under it.
	Release() error
}

// Card is one connection to one card, not the card itself: several
// connections may share one card and the provider serialises their exchanges,
// because T=1 is a single conversation whose sequence numbers both ends keep.
type Card interface {
	// Reconnect changes the share mode or protocol of an existing connection,
	// applying initialization to the card first.
	Reconnect(share ShareMode, protocols Protocol, initialization Disposition) (Protocol, error)
	// Disconnect closes the connection, applying disposition to the card.
	Disconnect(disposition Disposition) error
	// BeginTransaction claims the card until EndTransaction.
	BeginTransaction() error
	// EndTransaction releases the claim, applying disposition to the card. The
	// claim must be released even when the disposition fails, or every other
	// connection stays blocked on a card nobody is talking to.
	EndTransaction(disposition Disposition) error
	// Status reports the card's power state, protocol and ATR. A built-in reader
	// reports only three states -- CardSpecific once the T=1 session is up,
	// CardPresent when a card is seated but not initialised, CardAbsent otherwise
	// -- because the rest describe power sequencing these readers do not report.
	// The protocol is always ProtocolT1.
	Status() (Status, error)
	// Transmit sends one command APDU and returns the response including its two
	// status bytes, plus the protocol it came back under. A built-in reader
	// answers ErrInvalidParameter for a sendProtocol other than ProtocolT1 and for
	// a command longer than the AttrMaxInput it reports; these paths carry one T=1
	// block per USB message.
	Transmit(send []byte, sendProtocol Protocol) ([]byte, Protocol, error)
	// Control sends a reader control code. Card interfaces of the kind this
	// package exists for have no control codes of their own, so a provider for
	// one answers ErrUnsupportedFeature.
	Control(code uint32, send []byte) ([]byte, error)
	// GetAttrib reads a reader or card attribute. A built-in reader is expected to
	// answer at least AttrATRString, AttrCurrentProtocol, AttrVendorName,
	// AttrVendorIFDType, AttrMaxInput and the four device-name attributes, which
	// is the set client software actually asks for. Anything else is
	// ErrUnsupportedFeature.
	GetAttrib(id Attribute) ([]byte, error)
	// SetAttrib writes a reader or card attribute.
	SetAttrib(id Attribute, value []byte) error
}
