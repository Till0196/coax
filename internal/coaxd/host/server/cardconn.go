// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"net/http"
	"sync"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// held is what one HTTP connection has taken of the cards.
//
// A card belongs to the connection that opened it, which is the rule a tuner
// already follows: there is no session to create, nothing to expire, and a
// client that vanished is cleaned up by the socket closing under it.
type held struct {
	mutex sync.Mutex
	// card is the connection to the card, or nil when this HTTP connection is
	// holding none.
	card CardConn
	// reader names the slot card was opened in, for the error a request naming
	// a different one gets.
	reader string
	// transaction records that BeginTransaction was called and not yet ended,
	// so the release path can end it rather than leaving the card claimed.
	transaction bool
}

// take records a card this connection has opened, refusing a second one: one
// connection is one conversation, and a client that wants two opens two.
func (h *held) take(reader string, card CardConn) bool {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.card != nil {
		return false
	}
	h.card = card
	h.reader = reader
	return true
}

// on returns the card this connection holds in the named slot.
func (h *held) on(reader string) (CardConn, bool) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.card == nil || h.reader != reader {
		return nil, false
	}
	return h.card, true
}

func (h *held) beginning() (CardConn, bool) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.card == nil || h.transaction {
		return nil, false
	}
	h.transaction = true
	return h.card, true
}

func (h *held) ending() (CardConn, bool) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.card == nil || !h.transaction {
		return nil, false
	}
	h.transaction = false
	return h.card, true
}

// release gives up whatever is still held, ending a transaction first so the
// card is not left claimed against a connection that has gone.
func (h *held) release(disposition string) bool {
	h.mutex.Lock()
	card, transaction := h.card, h.transaction
	h.card, h.reader, h.transaction = nil, "", false
	h.mutex.Unlock()
	if card == nil {
		return false
	}
	if transaction {
		_ = card.EndTransaction(dispositionLeave)
	}
	_ = card.Close(disposition)
	return true
}

// cardsHeld is what the connection a request arrived on is holding.
func cardsHeld(request *http.Request) *held {
	if value, ok := request.Context().Value(cardKey{}).(*held); ok {
		return value
	}
	return nil
}

// The dispositions a request may name, which are winscard's own written out.
// An unnamed one leaves the card as it is, which is what a client that only
// wants its handle back means.
const (
	dispositionLeave   = "leave"
	dispositionReset   = "reset"
	dispositionUnpower = "unpower"
	dispositionEject   = "eject"
)

// disposition reads the query parameter, defaulting to leaving the card alone.
func disposition(request *http.Request) (string, bool) {
	switch named := request.URL.Query().Get("disposition"); named {
	case "":
		return dispositionLeave, true
	case dispositionLeave, dispositionReset, dispositionUnpower, dispositionEject:
		return named, true
	default:
		return "", false
	}
}

// noCardHeld is the failure a request that needs an open card gets when this
// connection has none. It is a 409 rather than a 404: the slot is there, the
// caller just has not opened it on this connection.
func noCardHeld(writer http.ResponseWriter, id string) {
	fail(writer, http.StatusConflict, hostapi.CodeCardError,
		"this connection is not holding a card in "+id+"; POST /cards/"+id+"/connect first")
}
