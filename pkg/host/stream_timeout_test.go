// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Till0196/coax/pkg/tuner"
)

func streamDevice(t *testing.T, handler http.HandlerFunc) *Device {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := &Client{http: server.Client(), control: server.Client(), base: server.URL}
	d := client.Open(TuneOptions{})
	d.id = "test:0"
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestFirstByteDeadlineIncludesHeadersAndAnEmptyBody(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[headers], func(t *testing.T) {
			d := streamDevice(t, func(w http.ResponseWriter, r *http.Request) {
				if headers {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			})
			started := time.Now()
			stream, err := d.OpenStream(tuner.Bounds{Wait: 20 * time.Millisecond})
			if err == nil {
				defer stream.Close()
				_, err = stream.Read(t.Context(), make([]byte, 1))
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("waiting for first byte: %v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("startup deadline did not interrupt the HTTP wait")
			}
		})
	}
}

func TestFirstByteDeadlineDoesNotLimitAWorkingStream(t *testing.T) {
	second := make(chan struct{})
	d := streamDevice(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{'a'})
		w.(http.Flusher).Flush()
		select {
		case <-second:
			_, _ = w.Write([]byte{'b'})
		case <-r.Context().Done():
		}
	})
	stream, err := d.OpenStream(tuner.Bounds{Wait: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	buffer := make([]byte, 1)
	if n, err := stream.Read(t.Context(), buffer); n != 1 || err != nil || buffer[0] != 'a' {
		t.Fatalf("first read: %d, %v, %q", n, err, buffer)
	}
	time.Sleep(60 * time.Millisecond)
	close(second)
	if n, err := stream.Read(t.Context(), buffer); n != 1 || (err != nil && !errors.Is(err, io.EOF)) || buffer[0] != 'b' {
		t.Fatalf("read beyond startup deadline: %d, %v, %q", n, err, buffer)
	}
}

func TestReadContextInterruptsAStalledHTTPBody(t *testing.T) {
	d := streamDevice(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{'a'})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	stream, err := d.OpenStream(tuner.Bounds{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	buffer := make([]byte, 1)
	if _, err := stream.Read(t.Context(), buffer); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := stream.Read(ctx, buffer); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled read: %v", err)
	}
}
