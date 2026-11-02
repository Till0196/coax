// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemoncard

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Till0196/coax/pkg/host"
	"github.com/Till0196/coax/pkg/winscard"
)

func TestCardStatusCodeSurvivesHTTP(t *testing.T) {
	for _, code := range []winscard.Code{winscard.ErrResetCard, winscard.ErrRemovedCard, winscard.ErrReaderUnavailable} {
		err := &host.FailureError{Problem: host.Problem{Code: "card_error", Reason: fmt.Sprintf("scard:%08X", uint32(code)), Message: "operation failed"}}
		if got := translate(err); !errors.Is(got, code) {
			t.Fatalf("got %v, want %v", got, code)
		}
	}
}

func TestReconnectCanRetryAndStatusReportsOutage(t *testing.T) {
	var opens atomic.Int32
	var offline atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"version":"test"}`) })
	mux.HandleFunc("POST /cards/slot/connect", func(w http.ResponseWriter, r *http.Request) {
		if opens.Add(1) == 2 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"code":"card_error","message":"try again","reason":"scard:80100016"}}`)
			return
		}
		fmt.Fprint(w, `{"atr":"3B 00","protocol":"T=1","max_input":250}`)
	})
	mux.HandleFunc("DELETE /cards/slot/connect", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"released":true}`) })
	mux.HandleFunc("GET /cards/slot/events", func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"code":"card_error","reason":"scard:80100017","message":"reader unavailable"}}`)
			return
		}
		fmt.Fprint(w, `{"present":true,"events":1}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := host.Dial(t.Context(), host.Options{Address: server.URL, NoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := New(client).EstablishContext(winscard.ScopeSystem)
	defer ctx.Release()
	card, _, err := ctx.Connect("slot", winscard.ShareShared, winscard.ProtocolT1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := card.Reconnect(winscard.ShareShared, winscard.ProtocolT1, winscard.ResetCard); err == nil {
		t.Fatal("expected a temporary failure")
	}
	if _, err := card.Reconnect(winscard.ShareShared, winscard.ProtocolT1, winscard.ResetCard); err != nil {
		t.Fatal(err)
	}
	offline.Store(true)
	if _, err := card.Status(); !errors.Is(err, winscard.ErrReaderUnavailable) {
		t.Fatalf("status hid the outage: %v", err)
	}
}
