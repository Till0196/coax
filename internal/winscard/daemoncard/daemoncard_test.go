// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemoncard_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/winscard/daemoncard"
	"github.com/Till0196/coax/pkg/host"
	"github.com/Till0196/coax/pkg/winscard"
)

// daemon answers the card endpoints the way coaxd does, holding a card against
// the connection it was opened on so the ownership rule is exercised rather
// than assumed.
type daemon struct {
	mutex sync.Mutex
	// held maps a connection to the card it has open.
	held map[string]bool
	// sent is every command the card was given, in order.
	sent []string
	// events is the slot's event count, which the wait watches.
	events int
}

func serve(t *testing.T) (*host.Client, *daemon) {
	t.Helper()
	state := &daemon{held: map[string]bool{}}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(host.Version{Version: "test"})
	})
	mux.HandleFunc("GET /cards", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]host.Card{{ID: "slot", Reader: "slot", Backend: "px4"}})
	})
	mux.HandleFunc("POST /cards/{id}/connect", func(w http.ResponseWriter, r *http.Request) {
		who := r.RemoteAddr
		state.mutex.Lock()
		defer state.mutex.Unlock()
		if state.held[who] {
			http.Error(w, "already held", http.StatusConflict)
			return
		}
		state.held[who] = true
		_ = json.NewEncoder(w).Encode(host.CardConnection{ATR: "3B F0 11", Protocol: "T=1", MaxInput: 260})
	})
	mux.HandleFunc("DELETE /cards/{id}/connect", func(w http.ResponseWriter, r *http.Request) {
		state.mutex.Lock()
		defer state.mutex.Unlock()
		released := state.held[r.RemoteAddr]
		delete(state.held, r.RemoteAddr)
		_ = json.NewEncoder(w).Encode(host.CardRelease{Released: released})
	})
	mux.HandleFunc("POST /cards/{id}/transmit", func(w http.ResponseWriter, r *http.Request) {
		state.mutex.Lock()
		defer state.mutex.Unlock()
		if !state.held[r.RemoteAddr] {
			http.Error(w, "no card", http.StatusConflict)
			return
		}
		var body host.CardTransmitRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		state.sent = append(state.sent, body.APDU)
		_ = json.NewEncoder(w).Encode(host.CardTransmitResult{
			Response: "12 90 00", Status: "90 00", Protocol: "T=1",
		})
	})
	mux.HandleFunc("POST /cards/{id}/transaction", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(host.CardRelease{Released: true})
	})
	mux.HandleFunc("DELETE /cards/{id}/transaction", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(host.CardRelease{Released: true})
	})
	mux.HandleFunc("GET /cards/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		state.mutex.Lock()
		events := state.events
		state.mutex.Unlock()
		since := r.URL.Query().Get("since")
		answer := host.CardState{Events: events, Present: true, ATR: "3B F0 11", Sharing: 1}
		answer.Changed = since != "" && since != "0" || (since == "0" && events != 0)
		_ = json.NewEncoder(w).Encode(answer)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := host.Dial(t.Context(), host.Options{Address: server.URL, NoStart: true})
	if err != nil {
		t.Fatalf("reach the fake daemon: %v", err)
	}
	return client, state
}

// The point of the provider: a run of commands with no reset between them, on a
// card in another process, reached through the same API a local one is.
func TestACardInAnotherProcessLooksLikeAnyOther(t *testing.T) {
	client, state := serve(t)
	service := winscard.NewService(daemoncard.New(client))

	context, err := service.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	defer service.ReleaseContext(context)

	readers, err := service.ListReaders(context, nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 1 || readers[0] != "slot" {
		t.Fatalf("listed %v", readers)
	}

	card, protocol, err := service.Connect(context, "slot", winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if protocol != winscard.ProtocolT1 {
		t.Errorf("agreed on %v", protocol)
	}

	for _, apdu := range [][]byte{{0x90, 0x30, 0x00, 0x00, 0x00}, {0x90, 0x32, 0x00, 0x00, 0x00}} {
		response, _, err := service.Transmit(card, winscard.ProtocolT1, apdu)
		if err != nil {
			t.Fatalf("transmit: %v", err)
		}
		if !bytes.Equal(response, []byte{0x12, 0x90, 0x00}) {
			t.Errorf("the card answered %X", response)
		}
	}

	state.mutex.Lock()
	sent := append([]string(nil), state.sent...)
	state.mutex.Unlock()
	if len(sent) != 2 || sent[0] != "90 30 00 00 00" || sent[1] != "90 32 00 00 00" {
		t.Errorf("the daemon was given %q", sent)
	}

	status, err := service.Status(card)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !bytes.Equal(status.ATR, []byte{0x3B, 0xF0, 0x11}) {
		t.Errorf("the ATR came back as %X", status.ATR)
	}

	if err := service.Disconnect(card, winscard.LeaveCard); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if len(state.held) != 0 {
		t.Errorf("%d cards are still held at the daemon", len(state.held))
	}
}

// Two cards at once means two connections, since a connection is what holds
// one. A provider that reused a single connection would have the second open
// refused by the daemon.
func TestTwoCardsAtOnceTakeTwoConnections(t *testing.T) {
	client, state := serve(t)
	service := winscard.NewService(daemoncard.New(client))

	context, err := service.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	defer service.ReleaseContext(context)

	first, _, err := service.Connect(context, "slot", winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatalf("the first connect: %v", err)
	}
	second, _, err := service.Connect(context, "slot", winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatalf("the second connect: %v", err)
	}

	state.mutex.Lock()
	held := len(state.held)
	state.mutex.Unlock()
	if held != 2 {
		t.Errorf("the daemon is holding %d cards for two opens", held)
	}

	_ = service.Disconnect(first, winscard.LeaveCard)
	_ = service.Disconnect(second, winscard.LeaveCard)
}

// Releasing the context lets go of what was opened under it, so an application
// that forgot a card does not leave one held at the daemon.
func TestReleasingTheContextLetsGoOfItsCards(t *testing.T) {
	client, state := serve(t)
	service := winscard.NewService(daemoncard.New(client))

	context, err := service.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	if _, _, err := service.Connect(context, "slot", winscard.ShareShared, winscard.ProtocolT1); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := service.ReleaseContext(context); err != nil {
		t.Fatalf("release: %v", err)
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()
	if len(state.held) != 0 {
		t.Errorf("%d cards are still held after the context went", len(state.held))
	}
}

// The wait happens at the daemon: what this side does is ask, and what comes
// back is the state with the count in it.
func TestWaitingForTheSlotAsksTheDaemon(t *testing.T) {
	client, state := serve(t)
	service := winscard.NewService(daemoncard.New(client))

	context, err := service.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	defer service.ReleaseContext(context)

	state.mutex.Lock()
	state.events = 3
	state.mutex.Unlock()

	answered, err := service.GetStatusChange(context, time.Second,
		[]winscard.ReaderState{{Reader: "slot", Current: winscard.StateUnaware}})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(answered) != 1 {
		t.Fatalf("answered %d states", len(answered))
	}
	if !answered[0].Event.Has(winscard.StatePresent) {
		t.Errorf("the slot came back as %v", answered[0].Event)
	}
	if !answered[0].Event.Has(winscard.StateChanged) {
		t.Error("the slot moved and the answer does not say so")
	}
	if got := answered[0].Event.Count(); got != 3 {
		t.Errorf("the event count came back as %d, want 3", got)
	}
	if !bytes.Equal(answered[0].ATR, []byte{0x3B, 0xF0, 0x11}) {
		t.Errorf("the ATR came back as %X", answered[0].ATR)
	}
}
