// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

// cardServer is a server with one fake slot, attached to its listener so a
// connection owns what it takes.
func cardServer(t *testing.T) (*httptest.Server, *fakeCards) {
	t.Helper()
	supervisor := pool.New(&fakeRegistry{board: &fakeBoard{}}, pool.Options{})
	t.Cleanup(func() { _ = supervisor.Close() })
	cards := &fakeCards{}
	server := New(supervisor, Options{Cards: cards})
	listener := httptest.NewUnstartedServer(server.Handler())
	server.Attach(listener.Config)
	listener.Start()
	t.Cleanup(listener.Close)
	return listener, cards
}

// pinned is a client whose requests all travel down one connection, which is
// what holds a card.
func pinned() *http.Client {
	return &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}}
}

func ask(t *testing.T, client *http.Client, method, target string, body string, into any) int {
	t.Helper()
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, target, payload)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, target, err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the answer to %s %s: %v", method, target, err)
	}
	if into != nil && response.StatusCode < 400 {
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatalf("the answer to %s %s is not what it should be: %v (%s)", method, target, err, raw)
		}
	}
	return response.StatusCode
}

const slot = "/cards/PLEX%20Smart%20Card%20Reader"

// A conversation is a run of commands with no reset between them, which is what
// a session cannot give and what a card whose next command depends on the last
// answer needs.
func TestACardIsHeldAcrossCommands(t *testing.T) {
	listener, cards := cardServer(t)
	client := pinned()
	defer client.CloseIdleConnections()

	var opened hostapi.CardConnection
	if status := ask(t, client, http.MethodPost, listener.URL+slot+"/connect", "", &opened); status != http.StatusOK {
		t.Fatalf("connect answered %d", status)
	}
	if opened.ATR != "3B F0" {
		t.Errorf("the card answered reset with %q", opened.ATR)
	}
	if opened.Protocol != "T=1" || opened.MaxInput != 260 {
		t.Errorf("opened as %q with room for %d", opened.Protocol, opened.MaxInput)
	}

	// Sent in order and answered in order: the fake counts what it has been
	// given, so the second answer says it saw the first.
	for index, command := range []string{`{"apdu":"90 30 00 00 00"}`, `{"apdu":"9032000000"}`} {
		var result hostapi.CardTransmitResult
		if status := ask(t, client, http.MethodPost, listener.URL+slot+"/transmit", command, &result); status != http.StatusOK {
			t.Fatalf("transmit answered %d", status)
		}
		want := fmt.Sprintf("%02X 90 00", index+1)
		if result.Response != want {
			t.Errorf("the card answered %q, want %q", result.Response, want)
		}
		if result.Status != "90 00" {
			t.Errorf("the status bytes came out as %q", result.Status)
		}
	}

	cards.mutex.Lock()
	open := cards.open
	cards.mutex.Unlock()
	if open != 1 {
		t.Errorf("%d cards are open; the run of commands should have taken one", open)
	}
	if cards.sessions != 0 {
		t.Error("the card was reset between commands, which is what holding it is meant to avoid")
	}
}

// The connection is the owner: a client that goes away without saying so leaves
// nothing behind, which is what makes the daemon free of sessions to expire.
func TestClosingTheConnectionGivesTheCardBack(t *testing.T) {
	listener, cards := cardServer(t)
	client := pinned()

	if status := ask(t, client, http.MethodPost, listener.URL+slot+"/connect", "", nil); status != http.StatusOK {
		t.Fatalf("connect answered %d", status)
	}
	if status := ask(t, client, http.MethodPost, listener.URL+slot+"/transaction", "", nil); status != http.StatusOK {
		t.Fatalf("begin answered %d", status)
	}

	client.CloseIdleConnections()

	deadline := time.Now().Add(2 * time.Second)
	for {
		cards.mutex.Lock()
		open := cards.open
		cards.mutex.Unlock()
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d cards are still open after the connection closed", open)
		}
		time.Sleep(time.Millisecond)
	}
}

// A command sent by a connection that never opened the card is refused rather
// than served off somebody else's conversation.
func TestACommandNeedsThisConnectionsOwnCard(t *testing.T) {
	listener, _ := cardServer(t)

	holder := pinned()
	defer holder.CloseIdleConnections()
	if status := ask(t, holder, http.MethodPost, listener.URL+slot+"/connect", "", nil); status != http.StatusOK {
		t.Fatalf("connect answered %d", status)
	}

	other := pinned()
	defer other.CloseIdleConnections()
	status := ask(t, other, http.MethodPost, listener.URL+slot+"/transmit", `{"apdu":"9030000000"}`, nil)
	if status != http.StatusConflict {
		t.Errorf("another connection's command answered %d, want 409", status)
	}
}

// One connection is one conversation. A client wanting two cards opens two
// connections, which is also what makes each of them independently releasable.
func TestOneConnectionHoldsOneCard(t *testing.T) {
	listener, _ := cardServer(t)
	client := pinned()
	defer client.CloseIdleConnections()

	if status := ask(t, client, http.MethodPost, listener.URL+slot+"/connect", "", nil); status != http.StatusOK {
		t.Fatalf("the first connect answered %d", status)
	}
	if status := ask(t, client, http.MethodPost, listener.URL+slot+"/connect", "", nil); status != http.StatusConflict {
		t.Errorf("a second connect on the same connection answered %d, want 409", status)
	}
}

// Giving the card back on purpose leaves the connection usable, which is the
// only reason to do it rather than just dropping the connection.
func TestGivingACardBackLeavesTheConnectionUsable(t *testing.T) {
	listener, cards := cardServer(t)
	client := pinned()
	defer client.CloseIdleConnections()

	ask(t, client, http.MethodPost, listener.URL+slot+"/connect", "", nil)
	var released hostapi.CardRelease
	if status := ask(t, client, http.MethodDelete, listener.URL+slot+"/connect", "", &released); status != http.StatusOK {
		t.Fatalf("disconnect answered %d", status)
	}
	if !released.Released {
		t.Error("the disconnect did not say it had let the card go")
	}

	// Asking again is what a client tidying up after itself does, and it is not
	// a failure: what it wanted is already true.
	if status := ask(t, client, http.MethodDelete, listener.URL+slot+"/connect", "", &released); status != http.StatusOK {
		t.Errorf("a second disconnect answered %d", status)
	}
	if released.Released {
		t.Error("the second disconnect claimed to have let a card go")
	}

	if status := ask(t, client, http.MethodPost, listener.URL+slot+"/connect", "", nil); status != http.StatusOK {
		t.Errorf("the connection could not open another card (%d)", status)
	}
	cards.mutex.Lock()
	defer cards.mutex.Unlock()
	if cards.open != 1 {
		t.Errorf("%d cards are open", cards.open)
	}
}

// The wait is what stands in for SCardGetStatusChange: it answers when the
// count moves, and otherwise when the caller's patience runs out.
func TestTheWaitAnswersWhenTheCountMoves(t *testing.T) {
	listener, cards := cardServer(t)
	client := listener.Client()

	var state hostapi.CardState
	if status := ask(t, client, http.MethodGet, listener.URL+slot+"/events?wait=0", "", &state); status != http.StatusOK {
		t.Fatalf("the wait answered %d", status)
	}
	if !state.Present || state.ATR != "3B F0" {
		t.Errorf("read the slot as present=%v atr=%q", state.Present, state.ATR)
	}
	if state.Changed {
		t.Error("nothing had moved, so nothing should have changed")
	}

	cards.mutex.Lock()
	cards.events = 7
	cards.mutex.Unlock()

	if status := ask(t, client, http.MethodGet, listener.URL+slot+"/events?since=0&wait=5", "", &state); status != http.StatusOK {
		t.Fatalf("the wait answered %d", status)
	}
	if !state.Changed || state.Events != 7 {
		t.Errorf("the wait reported changed=%v events=%d", state.Changed, state.Events)
	}
}

// A wait that runs out says so rather than failing: the caller asks again from
// a count it has actually seen.
func TestAWaitThatRunsOutIsNotAFailure(t *testing.T) {
	listener, _ := cardServer(t)
	started := time.Now()
	var state hostapi.CardState
	if status := ask(t, listener.Client(), http.MethodGet, listener.URL+slot+"/events?since=0&wait=0.2", "", &state); status != http.StatusOK {
		t.Fatalf("the wait answered %d", status)
	}
	if state.Changed {
		t.Error("nothing moved, so the answer should not say something did")
	}
	if waited := time.Since(started); waited < 150*time.Millisecond {
		t.Errorf("the wait came back after %s, which is not waiting", waited)
	}
}

// A slot nothing is called is the caller's mistake, and is told apart from a
// card that would not answer.
func TestAnUnknownSlotIsNotFound(t *testing.T) {
	listener, _ := cardServer(t)
	client := pinned()
	defer client.CloseIdleConnections()
	if status := ask(t, client, http.MethodPost, listener.URL+"/cards/nothing/connect", "", nil); status != http.StatusNotFound {
		t.Errorf("connecting to a slot that is not there answered %d, want 404", status)
	}
}
