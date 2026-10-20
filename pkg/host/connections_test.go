// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestStatusDoesNotWaitBehindAStream keeps the lease connection occupied and
// checks that a status request uses the separate control connection.
func TestStatusDoesNotWaitBehindAStream(t *testing.T) {
	streamDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/version":
			_ = json.NewEncoder(writer).Encode(Version{Version: "test"})
		case "/stream":
			writer.Header().Set("X-Coax-Tuner", "test:0")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			<-streamDone
		case "/tuners/test:0":
			locked := true
			_ = json.NewEncoder(writer).Encode(Tuner{ID: "test:0", Locked: &locked})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := Dial(context.Background(), Options{Address: server.URL, NoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := client.Stream(context.Background(), map[string]string{
		"DELIVERY_SYSTEM": "ISDBT",
		"FREQUENCY":       "557142857",
	}, TuneOptions{}, Bounds{})
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	defer close(streamDone)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := client.Tuner(ctx, "test:0")
	if err != nil {
		t.Fatalf("status waited behind the stream: %v", err)
	}
	if status.Locked == nil || !*status.Locked {
		t.Fatalf("unexpected status: %+v", status)
	}
}

// TestRequestsCarryTheAgent checks that every request names the program, and
// that closing idle connections still reaches the connection underneath: a
// card is given back by closing the connection it was opened on.
func TestRequestsCarryTheAgent(t *testing.T) {
	agents := make(chan string, 8)
	closed := make(chan struct{}, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		agents <- request.UserAgent()
		switch request.URL.Path {
		case "/version":
			_ = json.NewEncoder(writer).Encode(Version{Version: "test"})
		case "/tuners/test:0":
			_ = json.NewEncoder(writer).Encode(Tuner{ID: "test:0"})
		default:
			http.NotFound(writer, request)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.Start()
	defer server.Close()

	client, err := Dial(context.Background(), Options{Address: server.URL, NoStart: true, Agent: "coax/test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Tuner(context.Background(), "test:0"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if agent := <-agents; agent != "coax/test" {
			t.Fatalf("a request said it came from %q", agent)
		}
	}
	client.http.CloseIdleConnections()
	client.control.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("closing idle connections did not close one")
	}
}
