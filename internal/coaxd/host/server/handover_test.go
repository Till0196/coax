// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	"github.com/Till0196/coax/pkg/usbio"
)

// slowBoard is one tuner whose handle takes a moment to close, as real hardware
// does: stopping the endpoint and sleeping the demodulator are not instant.
type slowBoard struct {
	tuners []device.TunerInfo
	delay  time.Duration
	opens  atomic.Int64
	mutex  sync.Mutex
	taken  map[int]bool
}

func (b *slowBoard) Name() string               { return "slow" }
func (b *slowBoard) Tuners() []device.TunerInfo { return b.tuners }
func (b *slowBoard) Close() error               { return nil }

func (b *slowBoard) OpenTuner(index int) (device.Tuner, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.taken == nil {
		b.taken = map[int]bool{}
	}
	if b.taken[index] {
		return nil, device.ErrTunerBusy
	}
	b.taken[index] = true
	b.opens.Add(1)
	return &slowDevice{board: b, index: index}, nil
}

type slowDevice struct {
	board *slowBoard
	index int
}

func (d *slowDevice) Name() string                       { return "slow tuner" }
func (d *slowDevice) Supports(string) bool               { return true }
func (d *slowDevice) Tune(map[string]string) error       { return nil }
func (d *slowDevice) OpenStream() (device.Stream, error) { return &fakeStream{}, nil }
func (d *slowDevice) Status() (device.Status, error) {
	return device.Status{Locked: true, Names: device.LockNames,
		CNR: device.Measured(28.5), Strength: device.Measured(-42)}, nil
}

func (d *slowDevice) Close() error {
	time.Sleep(d.board.delay)
	d.board.mutex.Lock()
	defer d.board.mutex.Unlock()
	delete(d.board.taken, d.index)
	return nil
}

type slowRegistry struct {
	detected []pool.Detected
	board    *slowBoard
}

func (r *slowRegistry) Detect() ([]pool.Detected, error) { return r.detected, nil }
func (r *slowRegistry) Open(pool.Detected, device.Options) (device.Board, error) {
	return r.board, nil
}

func newSlowServer(t *testing.T, delay time.Duration) (*httptest.Server, *slowBoard) {
	t.Helper()
	tuners := []device.TunerInfo{{Index: 0, Name: "R850", Deliveries: []string{"ISDB-T"}}}
	board := &slowBoard{tuners: tuners, delay: delay}
	registry := &slowRegistry{
		board: board,
		detected: []pool.Detected{{
			Backend: "px4", Description: "one tuner", Selector: "001:007",
			Location: "001:007", Serial: "ABC123", Port: "1-2",
			USB:        usbio.ID{Vendor: 0x0511, Product: 0x083f},
			Deliveries: []string{"ISDB-T"},
			Tuners:     tuners,
		}},
	}
	supervisor := pool.New(registry, pool.Options{Linger: pool.LingerForever})
	t.Cleanup(func() { _ = supervisor.Close() })
	server := New(supervisor, Options{Version: "test"})
	listener := httptest.NewUnstartedServer(server.Handler())
	server.Attach(listener.Config)
	listener.Start()
	t.Cleanup(listener.Close)
	return listener, board
}

// closeDelay is how long the hardware takes to let a tuner go: stopping the
// endpoint and putting the demodulator to sleep are not instant.
const closeDelay = 50 * time.Millisecond

// TestAScanKeepsItsTunerFromChannelToChannel is what a scan does: per channel,
// tune, take a sample of the stream, move on. The sample is asked for with a
// bound so the response ends by itself -- a response that ends is read to the
// end, its connection is reused, and a lease belongs to its connection, so the
// tuner is taken once and stays taken.
func TestAScanKeepsItsTunerFromChannelToChannel(t *testing.T) {
	listener, board := newSlowServer(t, closeDelay)
	var dials atomic.Int64
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dials.Add(1)
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
		// One connection, as the coax client does, since a lease belongs to the
		// connection it was taken on.
		MaxConnsPerHost:     1,
		MaxIdleConnsPerHost: 1,
	}}

	failed, tuner := 0, ""
	for channel := 13; channel < 23; channel++ {
		// A frequency, not a channel number: resolving a channel of the Japanese
		// plan is the client's job.
		query := "system=ISDBT&freq=" + itoa(473_000_000+(channel-13)*6_000_000+142_857) + "&settle=0"
		if tuner != "" {
			query += "&tuner=" + tuner
		}
		response, err := client.Get(listener.URL + "/tune?" + query)
		if err != nil {
			t.Fatalf("channel %d: GET /tune: %v", channel, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			failed++
			t.Errorf("channel %d: /tune answered %d: %s", channel, response.StatusCode, compact(body))
			continue
		}
		var result struct {
			Tuner string `json:"tuner"`
		}
		_ = json.Unmarshal(body, &result)
		tuner = result.Tuner

		// The sample is bounded, so the daemon ends it.
		sample := "system=ISDBT&freq=" + itoa(473_000_000+(channel-13)*6_000_000+142_857) + "&tuner=" + tuner + "&bytes=65536"
		stream, err := client.Get(listener.URL + "/stream?" + sample)
		if err != nil {
			t.Fatalf("channel %d: GET /stream: %v", channel, err)
		}
		received, err := io.ReadAll(stream.Body)
		stream.Body.Close()
		if stream.StatusCode != http.StatusOK {
			failed++
			t.Errorf("channel %d: /stream answered %d: %s", channel, stream.StatusCode, compact(received))
			continue
		}
		if len(received) != 65536 {
			t.Errorf("channel %d: the stream carried %d bytes, want the 65536 asked for", channel, len(received))
		}
	}

	if failed != 0 {
		t.Fatalf("%d of 10 channels failed", failed)
	}
	// The point of the bound: one connection and one tuner for the whole walk.
	if dialled := dials.Load(); dialled != 1 {
		t.Errorf("the scan used %d connections, want 1: a stream that ends is one the connection survives", dialled)
	}
	if opens := board.opens.Load(); opens != 1 {
		t.Errorf("the tuner was opened %d times, want 1", opens)
	}
}

// TestABoundedStreamEndsOnTime is the other bound: a sample by the clock rather
// than by the byte.
func TestABoundedStreamEndsOnTime(t *testing.T) {
	listener, _ := newSlowServer(t, closeDelay)
	client := listener.Client()

	started := time.Now()
	response, err := client.Get(listener.URL + "/stream?system=ISDBT&freq=557142857&for=300ms")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	received, err := io.ReadAll(response.Body)
	response.Body.Close()
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("answered %d: %s", response.StatusCode, compact(received))
	}
	if len(received) == 0 {
		t.Error("the stream carried nothing")
	}
	if elapsed > 3*time.Second {
		t.Errorf("the stream ran for %v; it was asked for 300ms of it", elapsed)
	}
}

// TestAWalkThatTakesNoStreamKeepsOneConnection is the control: without the
// stream step there is nothing to race, which is what pins the loss on the
// streams.
func TestAWalkThatTakesNoStreamKeepsOneConnection(t *testing.T) {
	listener, board := newSlowServer(t, closeDelay)
	var dials atomic.Int64
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dials.Add(1)
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
		MaxConnsPerHost:     1,
		MaxIdleConnsPerHost: 1,
	}}
	failed, tuner := 0, ""
	for channel := 13; channel < 23; channel++ {
		// A frequency, not a channel number: resolving a channel of the Japanese
		// plan is the client's job.
		query := "system=ISDBT&freq=" + itoa(473_000_000+(channel-13)*6_000_000+142_857) + "&settle=0"
		if tuner != "" {
			query += "&tuner=" + tuner
		}
		response, err := client.Get(listener.URL + "/tune?" + query)
		if err != nil {
			t.Fatalf("channel %d: %v", channel, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			failed++
			t.Errorf("channel %d: %s", channel, compact(body))
			continue
		}
		var result struct {
			Tuner string `json:"tuner"`
		}
		_ = json.Unmarshal(body, &result)
		tuner = result.Tuner
	}
	if failed != 0 {
		t.Fatalf("%d of 10 channels failed", failed)
	}
	if dialled := dials.Load(); dialled != 1 {
		t.Errorf("the walk used %d connections, want 1", dialled)
	}
	if opens := board.opens.Load(); opens != 1 {
		t.Errorf("the tuner was opened %d times, want 1", opens)
	}
}

func itoa(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return itoa(value/10) + string(rune('0'+value%10))
}

func compact(body []byte) string {
	return strings.Join(strings.Fields(string(body)), " ")
}
