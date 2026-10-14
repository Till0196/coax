// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemoncard

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"sync"

	"github.com/Till0196/coax/pkg/host"
	"github.com/Till0196/coax/pkg/winscard"
)

// card is one open card at the daemon, with the HTTP connection that owns it.
type card struct {
	context *providerContext
	reader  string

	mutex sync.Mutex
	conn  *host.CardConn
	// transaction records a claim taken and not yet given back, so a card
	// disconnected mid-transaction does not leave the daemon's side claimed.
	transaction bool
}

// open returns the connection, or the error a caller of a card that has been
// disconnected should see.
func (c *card) open() (*host.CardConn, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.conn == nil {
		return nil, winscard.ErrInvalidHandle
	}
	return c.conn, nil
}

func (c *card) Reconnect(share winscard.ShareMode, protocols winscard.Protocol, initialization winscard.Disposition) (winscard.Protocol, error) {
	switch share {
	case winscard.ShareShared, winscard.ShareExclusive:
	default:
		return winscard.ProtocolUndefined, winscard.ErrInvalidValue
	}
	if !protocols.Has(winscard.ProtocolT1) {
		return winscard.ProtocolUndefined, winscard.ErrProtoMismatch
	}
	conn, err := c.open()
	if err != nil {
		return winscard.ProtocolUndefined, err
	}

	// A reconnect is a disconnect with the initialization applied and a
	// connect after it. Done here rather than at the daemon because the
	// connection is the claim: a new card means a new connection either way,
	// and an endpoint for it would only move these two calls across the wire.
	c.mutex.Lock()
	c.conn, c.transaction = nil, false
	c.mutex.Unlock()
	_ = conn.Close(context.Background(), dispositionName(initialization))

	opened, err := c.context.provider.client.OpenCard(context.Background(), c.reader)
	if err != nil {
		c.context.forget(c)
		return winscard.ProtocolUndefined, translate(err)
	}
	c.mutex.Lock()
	c.conn = opened
	c.mutex.Unlock()
	return winscard.ProtocolT1, nil
}

func (c *card) Disconnect(disposition winscard.Disposition) error {
	return c.close(disposition)
}

// close gives the card back and drops the connection holding it. The
// connection goes either way: it exists to hold this card, and the daemon
// releases what a closed connection held, so a failed request is not a card
// left open.
func (c *card) close(disposition winscard.Disposition) error {
	c.mutex.Lock()
	conn, transaction := c.conn, c.transaction
	c.conn, c.transaction = nil, false
	c.mutex.Unlock()
	if conn == nil {
		return winscard.ErrInvalidHandle
	}
	c.context.forget(c)
	if transaction {
		_ = conn.EndTransaction(context.Background(), dispositionName(winscard.LeaveCard))
	}
	return translate(conn.Close(context.Background(), dispositionName(disposition)))
}

func (c *card) BeginTransaction() error {
	conn, err := c.open()
	if err != nil {
		return err
	}
	if err := conn.BeginTransaction(context.Background()); err != nil {
		return translate(err)
	}
	c.mutex.Lock()
	c.transaction = true
	c.mutex.Unlock()
	return nil
}

func (c *card) EndTransaction(disposition winscard.Disposition) error {
	conn, err := c.open()
	if err != nil {
		return err
	}
	// Recorded as ended before the request, because the claim must be released
	// on this side even when the disposition fails: leaving it set would have
	// the disconnect path end a transaction the daemon no longer has.
	c.mutex.Lock()
	c.transaction = false
	c.mutex.Unlock()
	return translate(conn.EndTransaction(context.Background(), dispositionName(disposition)))
}

// Status reports what the daemon said when the card was opened, together with
// what the slot looks like now. The ATR does not change under an open
// connection -- a reset that would change it goes through Reconnect -- so it
// is the one from the connect rather than a request of its own.
func (c *card) Status() (winscard.Status, error) {
	conn, err := c.open()
	if err != nil {
		return winscard.Status{}, err
	}
	state := winscard.CardSpecific
	if seen, err := c.context.provider.client.CardEvents(context.Background(), c.reader, 0, 0); err == nil && !seen.Present {
		state = winscard.CardAbsent
	}
	return winscard.Status{
		Readers:  []string{c.reader},
		State:    state,
		Protocol: winscard.ProtocolT1,
		ATR:      conn.ATR,
	}, nil
}

func (c *card) Transmit(send []byte, sendProtocol winscard.Protocol) ([]byte, winscard.Protocol, error) {
	if sendProtocol != winscard.ProtocolT1 {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidParameter
	}
	conn, err := c.open()
	if err != nil {
		return nil, winscard.ProtocolUndefined, err
	}
	if conn.MaxInput > 0 && len(send) > conn.MaxInput {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidParameter
	}
	response, err := conn.Transmit(context.Background(), send)
	if err != nil {
		return nil, winscard.ProtocolUndefined, translate(err)
	}
	return response, winscard.ProtocolT1, nil
}

// Control is a reader control code. The card interfaces this reaches have none
// of their own, and the daemon publishes none either.
func (c *card) Control(uint32, []byte) ([]byte, error) {
	return nil, winscard.ErrUnsupportedFeature
}

// GetAttrib answers from what the connect already reported. The attributes
// client software actually asks for are the ATR, the protocol and how long a
// command may be, and all three came back when the card was opened; a request
// for each would be a round trip for something already known.
func (c *card) GetAttrib(id winscard.Attribute) ([]byte, error) {
	conn, err := c.open()
	if err != nil {
		return nil, err
	}
	switch id {
	case winscard.AttrATRString:
		return append([]byte(nil), conn.ATR...), nil
	case winscard.AttrCurrentProtocol:
		return binary.LittleEndian.AppendUint32(nil, uint32(winscard.ProtocolT1)), nil
	case winscard.AttrMaxInput:
		if conn.MaxInput <= 0 {
			return nil, winscard.ErrUnsupportedFeature
		}
		return binary.LittleEndian.AppendUint32(nil, uint32(conn.MaxInput)), nil
	case winscard.AttrVendorName:
		return text("coax"), nil
	case winscard.AttrVendorIFDType:
		return text("coaxd"), nil
	case winscard.AttrDeviceFriendlyA, winscard.AttrDeviceSystemA:
		return text(c.reader), nil
	case winscard.AttrDeviceFriendlyW, winscard.AttrDeviceSystemW:
		return wide(c.reader), nil
	default:
		return nil, winscard.ErrUnsupportedFeature
	}
}

// text and wide encode a string attribute the two ways the A and W attributes
// are defined in.
func text(value string) []byte { return append([]byte(value), 0) }

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

// SetAttrib writes nothing: none of these attributes is this side's to change.
func (c *card) SetAttrib(winscard.Attribute, []byte) error {
	return winscard.ErrUnsupportedFeature
}

// dispositionName is what the daemon's query parameter calls a disposition.
func dispositionName(disposition winscard.Disposition) string {
	switch disposition {
	case winscard.ResetCard:
		return "reset"
	case winscard.UnpowerCard:
		return "unpower"
	case winscard.EjectCard:
		return "eject"
	default:
		return "leave"
	}
}

// translate turns what the daemon reported into the status code the API layer
// above expects, so a client library sees SCARD_E_* rather than an HTTP
// failure it has no way to read.
func translate(err error) error {
	if err == nil {
		return nil
	}
	var failure *host.FailureError
	if errors.As(err, &failure) {
		switch failure.Code() {
		case "unknown_tuner":
			return winscard.ErrUnknownReader
		case "invalid_parameter":
			return winscard.ErrInvalidParameter
		case "no_card_reader":
			return winscard.ErrNoSmartcard
		case "card_error":
			if strings.Contains(failure.Problem.Message, "not holding a card") {
				return winscard.ErrInvalidHandle
			}
			return winscard.ErrNotTransacted
		}
		return winscard.ErrNotTransacted
	}
	// Nothing answered, so from the caller's side the resource manager is what
	// is unreachable rather than the card.
	return winscard.ErrNoService
}
