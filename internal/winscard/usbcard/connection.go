// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"encoding/binary"
	"sync"

	"github.com/Till0196/coax/internal/winscard"
)

// MaxInput is the longest command APDU one of these readers accepts. The T=1
// layer sends an APDU as a single information block, so the limit is what fits
// one: the frame the byte pipes carry, less the prologue and the error check,
// and no more than the information field size the card asked for. The figure
// here is the smallest that combination can produce, since a
// SCARD_ATTR_MAXINPUT that holds only for some cards is not worth promising.
const MaxInput = 250

// connection is one client's connection to a slot, a winscard.Card.
type connection struct {
	context *providerContext
	slot    *slot
	share   winscard.ShareMode

	mutex       sync.Mutex
	closed      bool
	transaction bool
}

var _ winscard.Card = (*connection)(nil)

// Connect opens a card in one of this provider's readers. ShareDirect is
// refused: it connects to a reader without a card, for control codes addressed
// to the reader itself, and these interfaces have none.
func (c *providerContext) Connect(reader string, share winscard.ShareMode, protocols winscard.Protocol) (winscard.Card, winscard.Protocol, error) {
	c.mutex.Lock()
	released := c.released
	c.mutex.Unlock()
	if released {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidHandle
	}
	switch share {
	case winscard.ShareShared, winscard.ShareExclusive:
	default:
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidValue
	}
	if !protocols.Has(winscard.ProtocolT1) {
		return nil, winscard.ProtocolUndefined, winscard.ErrProtoMismatch
	}

	one, err := c.provider.find(reader)
	if err != nil {
		return nil, winscard.ProtocolUndefined, err
	}
	target := c.provider.slotFor(one)
	if err := target.admit(share); err != nil {
		return nil, winscard.ProtocolUndefined, err
	}
	opened := &connection{context: c, slot: target, share: share}
	target.claim(opened)
	_, err = target.session()
	target.drop()
	if err != nil {
		target.discharge(share)
		return nil, winscard.ProtocolUndefined, err
	}
	c.track(opened)
	return opened, winscard.ProtocolT1, nil
}

// Reconnect changes the share mode of an open connection, applying
// initialization to the card first.
func (c *connection) Reconnect(share winscard.ShareMode, protocols winscard.Protocol, initialization winscard.Disposition) (winscard.Protocol, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return winscard.ProtocolUndefined, winscard.ErrInvalidHandle
	}
	if !protocols.Has(winscard.ProtocolT1) {
		return winscard.ProtocolUndefined, winscard.ErrProtoMismatch
	}
	switch share {
	case winscard.ShareShared, winscard.ShareExclusive:
	default:
		return winscard.ProtocolUndefined, winscard.ErrInvalidValue
	}
	if err := c.dispose(initialization); err != nil {
		return winscard.ProtocolUndefined, err
	}
	if share != c.share {
		// Changing mode is a fresh admission: an exclusive claim has to be
		// granted against the other connections as if it were new.
		c.slot.discharge(c.share)
		if err := c.slot.admit(share); err != nil {
			// The old mode is gone and the new one was refused. Take the old
			// one back so that the connection is still what it was, which is
			// what a failed reconnect promises.
			if again := c.slot.admit(c.share); again != nil {
				c.closed = true
				c.context.forget(c)
				return winscard.ProtocolUndefined, winscard.ErrReaderUnavailable
			}
			return winscard.ProtocolUndefined, err
		}
		c.share = share
	}
	c.slot.claim(c)
	_, err := c.slot.session()
	c.slot.drop()
	if err != nil {
		return winscard.ProtocolUndefined, err
	}
	return winscard.ProtocolT1, nil
}

// Disconnect closes the connection, applying disposition to the card.
func (c *connection) Disconnect(disposition winscard.Disposition) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return winscard.ErrInvalidHandle
	}
	err := c.dispose(disposition)
	if c.transaction {
		c.transaction = false
		c.slot.drop()
	}
	c.closed = true
	c.slot.discharge(c.share)
	c.context.forget(c)
	return err
}

// BeginTransaction claims the card until EndTransaction.
func (c *connection) BeginTransaction() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return winscard.ErrInvalidHandle
	}
	if c.transaction {
		// PC/SC transactions do not nest for one connection; a second begin is
		// a caller that lost track of the first.
		return winscard.ErrNotTransacted
	}
	c.slot.claim(c)
	c.transaction = true
	return nil
}

// EndTransaction releases the claim, applying disposition to the card. The
// claim goes whether or not the disposition succeeds: holding a card because
// the requested reset failed would leave every other connection waiting on a
// card nobody is talking to.
func (c *connection) EndTransaction(disposition winscard.Disposition) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return winscard.ErrInvalidHandle
	}
	if !c.transaction {
		return winscard.ErrNotTransacted
	}
	err := c.dispose(disposition)
	c.transaction = false
	c.slot.drop()
	return err
}

// Status reports the card's state, protocol and ATR.
func (c *connection) Status() (winscard.Status, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return winscard.Status{}, winscard.ErrInvalidHandle
	}
	c.slot.claim(c)
	present, _, err := c.slot.look()
	c.slot.drop()
	if err != nil {
		return winscard.Status{}, err
	}
	c.slot.state.Lock()
	initialized := c.slot.initialized
	atr := append([]byte(nil), c.slot.atr...)
	c.slot.state.Unlock()

	status := winscard.Status{
		Readers:  []string{c.slot.cardDevice.name},
		Protocol: winscard.ProtocolT1,
		ATR:      atr,
	}
	switch {
	case initialized:
		status.State = winscard.CardSpecific
	case present:
		status.State = winscard.CardPresent
	default:
		status.State = winscard.CardAbsent
	}
	return status, nil
}

// Transmit sends one command APDU and returns the response.
func (c *connection) Transmit(send []byte, sendProtocol winscard.Protocol) ([]byte, winscard.Protocol, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidHandle
	}
	if sendProtocol != winscard.ProtocolT1 {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidParameter
	}
	if len(send) == 0 || len(send) > MaxInput {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidParameter
	}
	c.slot.claim(c)
	response, err := c.slot.exchange(send)
	c.slot.drop()
	if err != nil {
		return nil, winscard.ProtocolUndefined, err
	}
	return response, winscard.ProtocolT1, nil
}

// Control has nothing to address: these readers have no control codes of their
// own.
func (c *connection) Control(code uint32, send []byte) ([]byte, error) {
	return nil, winscard.ErrUnsupportedFeature
}

// GetAttrib answers the attributes client software actually asks for.
func (c *connection) GetAttrib(id winscard.Attribute) ([]byte, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return nil, winscard.ErrInvalidHandle
	}
	c.slot.state.Lock()
	atr := append([]byte(nil), c.slot.atr...)
	present := c.slot.present
	c.slot.state.Unlock()

	switch id {
	case winscard.AttrATRString:
		return atr, nil
	case winscard.AttrCurrentProtocol, winscard.AttrAsyncProtocolTypes:
		return word(uint32(winscard.ProtocolT1)), nil
	case winscard.AttrMaxInput:
		return word(MaxInput), nil
	case winscard.AttrVendorName:
		return text(c.slot.cardDevice.vendor), nil
	case winscard.AttrVendorIFDType:
		return text(c.slot.cardDevice.model), nil
	case winscard.AttrDeviceFriendlyA, winscard.AttrDeviceSystemA:
		return text(c.slot.cardDevice.name), nil
	case winscard.AttrDeviceFriendlyW, winscard.AttrDeviceSystemW:
		return wide(c.slot.cardDevice.name), nil
	case winscard.AttrICCPresence:
		if present {
			return []byte{2}, nil
		}
		return []byte{0}, nil
	}
	return nil, winscard.ErrUnsupportedFeature
}

// SetAttrib has nothing a caller may change: everything these readers report
// is fixed by the hardware or by the card.
func (c *connection) SetAttrib(id winscard.Attribute, value []byte) error {
	return winscard.ErrUnsupportedFeature
}

// dispose applies a disposition to the card, with the connection's own lock
// held. Reset and unpower both come down to giving up the session: nothing
// here can take power off one card of a board on its own, and the next use
// resets the card anyway, so the promise that the conversation is over holds
// either way.
func (c *connection) dispose(disposition winscard.Disposition) error {
	switch disposition {
	case winscard.LeaveCard:
		return nil
	case winscard.ResetCard, winscard.UnpowerCard:
		c.slot.invalidate()
		return nil
	case winscard.EjectCard:
		return winscard.ErrUnsupportedFeature
	}
	return winscard.ErrInvalidValue
}

// word encodes a DWORD attribute the way PC/SC carries one.
func word(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint32(encoded, value)
	return encoded
}

// text encodes a string attribute, terminated as the C string a caller copies
// it into expects.
func text(value string) []byte {
	return append([]byte(value), 0)
}

// wide encodes a string attribute in the two-byte units the W attributes are
// defined in. The names here are ASCII, so the encoding is the simple one.
func wide(value string) []byte {
	encoded := make([]byte, 0, (len(value)+1)*2)
	for _, letter := range value {
		if letter > 0xFFFF {
			letter = '?'
		}
		encoded = append(encoded, byte(letter), byte(letter>>8))
	}
	return append(encoded, 0, 0)
}
