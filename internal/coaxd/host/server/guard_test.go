// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

func guarded(t *testing.T, anyHost bool) *httptest.Server {
	t.Helper()
	supervisor := pool.New(&fakeRegistry{board: &fakeBoard{}}, pool.Options{})
	t.Cleanup(func() { _ = supervisor.Close() })
	server := New(supervisor, Options{Cards: &fakeCards{}, Port: true, AnyHost: anyHost})
	listener := httptest.NewServer(server.Handler())
	t.Cleanup(listener.Close)
	return listener
}

func status(t *testing.T, listener *httptest.Server, method, path, host string, header http.Header, body string) int {
	t.Helper()
	request, err := http.NewRequest(method, listener.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range header {
		request.Header[name] = values
	}
	if host != "" {
		request.Host = host
	}
	response, err := listener.Client().Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	response.Body.Close()
	return response.StatusCode
}

// TestPortRefusesWebPages: a page in a browser on this machine reaches
// 127.0.0.1 as easily as a program does, so what a browser marks as coming
// from a page is turned away, and so is a name rebound to the loopback.
func TestPortRefusesWebPages(t *testing.T) {
	listener := guarded(t, false)

	if got := status(t, listener, http.MethodGet, "/version", "", nil, ""); got != http.StatusOK {
		t.Errorf("a plain request answered %d", got)
	}
	if got := status(t, listener, http.MethodGet, "/version", "localhost:40772", nil, ""); got != http.StatusOK {
		t.Errorf("localhost answered %d", got)
	}
	if got := status(t, listener, http.MethodGet, "/version", "[::1]:40772", nil, ""); got != http.StatusOK {
		t.Errorf("[::1] answered %d", got)
	}
	if got := status(t, listener, http.MethodGet, "/version", "", http.Header{"Sec-Fetch-Site": {"none"}}, ""); got != http.StatusOK {
		t.Errorf("an address typed into a browser answered %d", got)
	}

	refused := []struct {
		name   string
		host   string
		header http.Header
	}{
		{"a rebound name", "attacker.example:40772", nil},
		{"an origin", "", http.Header{"Origin": {"http://attacker.example"}}},
		{"a null origin", "", http.Header{"Origin": {"null"}}},
		{"a cross-site fetch", "", http.Header{"Sec-Fetch-Site": {"cross-site"}}},
		{"a same-origin fetch", "", http.Header{"Sec-Fetch-Site": {"same-origin"}}},
	}
	for _, c := range refused {
		if got := status(t, listener, http.MethodGet, "/tune", c.host, c.header, ""); got != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403", c.name, got)
		}
	}
}

// TestPortOpenedOnPurposeTakesAnyName: --allow-remote is reached by whatever
// name the network gives the machine, and still not from a page.
func TestPortOpenedOnPurposeTakesAnyName(t *testing.T) {
	listener := guarded(t, true)
	if got := status(t, listener, http.MethodGet, "/version", "tuners.lan:40772", nil, ""); got != http.StatusOK {
		t.Errorf("a network name answered %d", got)
	}
	if got := status(t, listener, http.MethodGet, "/version", "tuners.lan:40772", http.Header{"Origin": {"http://tuners.lan:40772"}}, ""); got != http.StatusForbidden {
		t.Errorf("a page answered %d, want 403", got)
	}
}

// TestCardBodyMustBeJSON: a page may send a plain-text body without asking,
// and not one marked as JSON.
func TestCardBodyMustBeJSON(t *testing.T) {
	listener, _, _ := newTestServer(t, &fakeCards{})
	body := `{"apdus":["9030000000"]}`
	path := "/cards/PLEX Smart Card Reader/session"
	if got := status(t, listener, http.MethodPost, path, "", http.Header{"Content-Type": {"text/plain"}}, body); got != http.StatusUnsupportedMediaType {
		t.Errorf("a plain-text body answered %d, want 415", got)
	}
	if got := status(t, listener, http.MethodPost, path, "", nil, body); got != http.StatusUnsupportedMediaType {
		t.Errorf("a body with no type answered %d, want 415", got)
	}
	if got := status(t, listener, http.MethodPost, path, "", http.Header{"Content-Type": {"application/json; charset=utf-8"}}, body); got != http.StatusOK {
		t.Errorf("a JSON body answered %d", got)
	}
}
