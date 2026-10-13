// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CardConn is one open card on the daemon.
//
// It carries a connection of its own, because that is what owns the card: the
// daemon gives a card up when the connection it was opened on closes, exactly
// as it does with a tuner.
type CardConn struct {
	client *http.Client
	base   string
	reader string
	// Protocol and ATR are what the card answered when it was opened.
	Protocol string
	ATR      []byte
	// MaxInput is the longest command this reader carries, or zero when the
	// daemon did not say.
	MaxInput int
}

// OpenCard opens a card and takes it on a connection of its own.
func (c *Client) OpenCard(ctx context.Context, reader string) (*CardConn, error) {
	card := &CardConn{
		client: c.pinned(),
		base:   c.base,
		reader: reader,
	}
	var opened CardConnection
	if err := card.post(ctx, "/connect", nil, nil, &opened); err != nil {
		card.client.CloseIdleConnections()
		return nil, err
	}
	atr, err := ParseHex(opened.ATR)
	if err != nil {
		_ = card.Close(context.WithoutCancel(ctx), "leave")
		return nil, fmt.Errorf("the daemon sent an answer to reset this program could not read: %w", err)
	}
	card.Protocol, card.ATR, card.MaxInput = opened.Protocol, atr, opened.MaxInput
	return card, nil
}

// pinned builds a client whose requests all travel down one connection, which
// is what ties what it takes to it.
func (c *Client) pinned() *http.Client {
	transport := &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}
	if socket, ok := c.socketPath(); ok {
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		}
	}
	return &http.Client{Transport: transport}
}

// socketPath is the socket this client dials, when it dials one at all.
func (c *Client) socketPath() (string, bool) {
	address := c.options.Address
	if address == "" {
		address = DefaultAddress()
	}
	if !isSocket(address) {
		return "", false
	}
	return address, true
}

// Transmit sends one command APDU and returns what the card said, status bytes
// included.
func (c *CardConn) Transmit(ctx context.Context, apdu []byte) ([]byte, error) {
	var result CardTransmitResult
	if err := c.post(ctx, "/transmit", nil, CardTransmitRequest{APDU: FormatHex(apdu)}, &result); err != nil {
		return nil, err
	}
	return ParseHex(result.Response)
}

// BeginTransaction claims the card against everything else sharing it.
func (c *CardConn) BeginTransaction(ctx context.Context) error {
	var released CardRelease
	return c.post(ctx, "/transaction", nil, nil, &released)
}

// EndTransaction gives the claim back.
func (c *CardConn) EndTransaction(ctx context.Context, disposition string) error {
	var released CardRelease
	return c.do(ctx, http.MethodDelete, "/transaction", dispositionQuery(disposition), nil, &released)
}

// Close gives the card back and drops the connection that was holding it.
//
// The connection is dropped either way: it exists to hold this card, and the
// daemon releases the card when it goes, so a failed request here is not a card
// left held.
func (c *CardConn) Close(ctx context.Context, disposition string) error {
	var released CardRelease
	err := c.do(ctx, http.MethodDelete, "/connect", dispositionQuery(disposition), nil, &released)
	c.client.CloseIdleConnections()
	return err
}

// dispositionQuery names what should happen to the card, leaving the parameter
// off when the caller did not say.
func dispositionQuery(disposition string) url.Values {
	if disposition == "" {
		return nil
	}
	return url.Values{"disposition": []string{disposition}}
}

func (c *CardConn) post(ctx context.Context, path string, query url.Values, body, into any) error {
	return c.do(ctx, http.MethodPost, path, query, body, into)
}

func (c *CardConn) do(ctx context.Context, method, path string, query url.Values, body, into any) error {
	target := c.base + "/cards/" + url.PathEscape(c.reader) + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	var payload *strings.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = strings.NewReader(string(encoded))
	}
	var request *http.Request
	var err error
	if payload != nil {
		request, err = http.NewRequestWithContext(ctx, method, target, payload)
	} else {
		request, err = http.NewRequestWithContext(ctx, method, target, nil)
	}
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decode(response, into)
}

// CardEvents waits for a slot to change. A caller passes the event count it
// last saw and is answered when the daemon's count leaves it, or when the wait
// runs out -- the daemon has a ceiling of its own, so a longer wait simply
// comes back sooner.
//
// It goes on the ordinary control client rather than a pinned one: waiting for
// a card to be seated takes nothing from the daemon and holds no card.
func (c *Client) CardEvents(ctx context.Context, reader string, since uint32, wait time.Duration) (CardState, error) {
	query := url.Values{}
	query.Set("since", strconv.FormatUint(uint64(since), 10))
	query.Set("wait", strconv.FormatFloat(wait.Seconds(), 'f', -1, 64))
	var state CardState
	err := c.get(ctx, "/cards/"+url.PathEscape(reader)+"/events", query, &state)
	return state, err
}
