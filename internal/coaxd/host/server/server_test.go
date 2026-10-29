// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
	"github.com/Till0196/coax/internal/coaxd/host/pool"
	mpegts "github.com/Till0196/coax/pkg/mpegts/analyse"
	"github.com/Till0196/coax/pkg/usbio"
)

// The fakes below stand in for a board, so the server can be exercised without
// hardware.

type fakeRegistry struct {
	mutex    sync.Mutex
	detected []pool.Detected
	tuners   []device.TunerInfo
	board    *fakeBoard
}

func (r *fakeRegistry) Detect() ([]pool.Detected, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]pool.Detected(nil), r.detected...), nil
}

func (r *fakeRegistry) Open(pool.Detected, device.Options) (device.Board, error) {
	return r.board, nil
}

type fakeBoard struct {
	tuners []device.TunerInfo

	mutex sync.Mutex
	taken map[int]bool
	locks bool
	noise bool
	// alwaysTLV produces TLV whatever the channel called itself, as a real
	// ISDB-S3 tuner does.
	alwaysTLV bool
	tuneErr   error
	devices   []*fakeDevice
}

// lastDevice is the tuner most recently handed out.
func (b *fakeBoard) lastDevice() *fakeDevice {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if len(b.devices) == 0 {
		return nil
	}
	return b.devices[len(b.devices)-1]
}

func (b *fakeBoard) Name() string               { return "fake" }
func (b *fakeBoard) Tuners() []device.TunerInfo { return b.tuners }
func (b *fakeBoard) Close() error               { return nil }

func (b *fakeBoard) OpenTuner(index int) (device.Tuner, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.taken == nil {
		b.taken = map[int]bool{}
	}
	if b.taken[index] {
		return nil, device.ErrTunerBusy
	}
	b.taken[index] = true
	opened := &fakeDevice{board: b, index: index}
	b.devices = append(b.devices, opened)
	return opened, nil
}

type fakeDevice struct {
	board *fakeBoard
	index int

	mutex sync.Mutex
	tuned map[string]string
	tunes int
}

// tuneCount is how many times this tuner has actually been retuned.
func (d *fakeDevice) tuneCount() int {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	return d.tunes
}

func (d *fakeDevice) Name() string         { return "fake tuner" }
func (d *fakeDevice) Supports(string) bool { return true }

func (d *fakeDevice) Tune(properties map[string]string) error {
	d.board.mutex.Lock()
	err := d.board.tuneErr
	d.board.mutex.Unlock()
	if err != nil {
		return err
	}
	d.mutex.Lock()
	defer d.mutex.Unlock()
	d.tuned = properties
	d.tunes++
	return nil
}

func (d *fakeDevice) Status() (device.Status, error) {
	d.board.mutex.Lock()
	defer d.board.mutex.Unlock()
	if !d.board.locks {
		// An empty frequency still measures, which is what tells "nothing here"
		// from "something that will not decode".
		return device.Status{Names: []string{"SIGNAL"}, Strength: device.Measured(-78.5)}, nil
	}
	return device.Status{
		Locked:   true,
		Names:    device.LockNames,
		CNR:      device.Measured(28.5),
		Strength: device.Measured(-42),
	}, nil
}

func (d *fakeDevice) OpenStream() (device.Stream, error) {
	d.board.mutex.Lock()
	noise, always := d.board.noise, d.board.alwaysTLV
	d.board.mutex.Unlock()
	d.mutex.Lock()
	defer d.mutex.Unlock()
	// A real ISDB-S3 tuner produces MMT over TLV, so the fake does too.
	return &fakeStream{noise: noise, tlv: always || d.tuned["DELIVERY_SYSTEM"] == "ISDBS3"}, nil
}

func (d *fakeDevice) Close() error {
	d.board.mutex.Lock()
	defer d.board.mutex.Unlock()
	delete(d.board.taken, d.index)
	return nil
}

type fakeStream struct {
	mutex  sync.Mutex
	closed bool
	noise  bool
	tlv    bool
	sent   int
	source *rand.Rand
}

// tlvPacketSize is the fake TLV packet: header, compressed-IP context and an
// MMTP packet inside, the shape of almost every packet of a Japanese TLV
// multiplex.
const tlvPacketSize = 4 + 3 + 32

// putTLV writes one such packet with the MMTP sequence number given.
func putTLV(packet []byte, number uint32) {
	packet[0], packet[1] = 0x7f, 0x03 // sync, compressed IP
	packet[2], packet[3] = 0x00, byte(tlvPacketSize-4)
	packet[4], packet[5], packet[6] = 0x00, 0x10, 0x21 // context 1, no header
	mmtp := packet[7:]
	mmtp[2], mmtp[3] = 0x10, 0x00 // packet identifier
	mmtp[8] = byte(number >> 24)
	mmtp[9] = byte(number >> 16)
	mmtp[10] = byte(number >> 8)
	mmtp[11] = byte(number)
}

func (s *fakeStream) Read(_ context.Context, buffer []byte) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.closed {
		return 0, io.EOF
	}
	if s.noise {
		// What a demodulator locked to a dead lead hands over: bytes at the
		// tuner's full rate, none of them a packet.
		if s.source == nil {
			s.source = rand.New(rand.NewSource(1))
		}
		s.source.Read(buffer)
		s.sent += len(buffer)
		return len(buffer), nil
	}
	if s.tlv {
		sent := 0
		for sent+tlvPacketSize <= len(buffer) {
			putTLV(buffer[sent:sent+tlvPacketSize], uint32((s.sent+sent)/tlvPacketSize))
			sent += tlvPacketSize
		}
		s.sent += sent
		return sent, nil
	}
	// Transport packets, endlessly, with the counter going round.
	for offset := 0; offset+mpegts.PacketSize <= len(buffer); offset += mpegts.PacketSize {
		packet := buffer[offset : offset+mpegts.PacketSize]
		packet[0] = mpegts.SyncByte
		packet[1], packet[2] = 0x01, 0x00
		packet[3] = 0x10 | byte((s.sent/mpegts.PacketSize+offset/mpegts.PacketSize)&0x0f)
		for index := 4; index < mpegts.PacketSize; index++ {
			packet[index] = 0xff
		}
	}
	whole := len(buffer) / mpegts.PacketSize * mpegts.PacketSize
	s.sent += whole
	return whole, nil
}

func (s *fakeStream) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.closed = true
	return nil
}

func (s *fakeStream) Dropped() uint64 { return 7 }

// newTestServer wires a server over one four-tuner board.
func newTestServer(t *testing.T, cards Cards) (*httptest.Server, *pool.Supervisor, *fakeRegistry) {
	t.Helper()
	tuners := []device.TunerInfo{
		{Index: 0, Name: "RT710", Deliveries: []string{"ISDB-S", "ISDB-S3"}},
		{Index: 1, Name: "R850", Deliveries: []string{"ISDB-T"}},
	}
	registry := &fakeRegistry{
		detected: []pool.Detected{{
			Backend:     "px4",
			Description: "PLEX PX-W3U4",
			Selector:    "001:007",
			Location:    "001:007",
			Serial:      "ABC123",
			Port:        "1-2",
			USB:         usbio.ID{Vendor: 0x0511, Product: 0x083f},
			Deliveries:  []string{"ISDB-T", "ISDB-S", "ISDB-S3"},
			Tuners:      tuners,
		}},
		tuners: tuners,
		board:  &fakeBoard{tuners: tuners, locks: true},
	}
	supervisor := pool.New(registry, pool.Options{Linger: pool.LingerForever})
	t.Cleanup(func() { _ = supervisor.Close() })

	server := New(supervisor, Options{Version: "test", Cards: cards})
	listener := httptest.NewUnstartedServer(server.Handler())
	server.Attach(listener.Config)
	listener.Start()
	t.Cleanup(listener.Close)
	return listener, supervisor, registry
}

func get(t *testing.T, client *http.Client, url string) (*http.Response, []byte) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return response, body
}

// GET /version says what this build is, which is all it is for while there is
// no number to check.
func TestVersionReportsTheBuild(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)
	response, body := get(t, listener.Client(), listener.URL+"/version")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /version answered %d: %s", response.StatusCode, body)
	}
	var version hostapi.Version
	if err := json.Unmarshal(body, &version); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if version.Version == "" {
		t.Error("the daemon did not say which build it is")
	}
}

// TestTunersOpensNothing: describing what is attached must not power hardware up.
func TestTunersOpensNothing(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	response, body := get(t, listener.Client(), listener.URL+"/tuners")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /tuners answered %d: %s", response.StatusCode, body)
	}
	var boards []hostapi.Board
	if err := json.Unmarshal(body, &boards); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(boards) != 1 {
		t.Fatalf("listed %d boards, want 1", len(boards))
	}
	if boards[0].Open {
		t.Error("listing opened the board")
	}
	if boards[0].Probed {
		t.Error("listing claimed to have read the tuners from the hardware")
	}
	if boards[0].Name != "px4:ABC123@1-2" {
		t.Errorf("named the board %q", boards[0].Name)
	}
	if len(boards[0].Tuners) != 2 {
		t.Errorf("described %d tuners, want 2", len(boards[0].Tuners))
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
}

// TestTuneReportsANoLockAsAnAnswer: a scan needs an empty frequency to be a
// result, signal strength included.
func TestTuneReportsANoLockAsAnAnswer(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.locks = false
	registry.board.mutex.Unlock()

	response, body := get(t, listener.Client(),
		listener.URL+"/tune?system=ISDBT&freq=557142857&lock_timeout=50ms&settle=0")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("a channel that did not lock answered %d: %s", response.StatusCode, body)
	}
	var result hostapi.TuneResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Locked {
		t.Error("reported a lock that did not happen")
	}
	if result.Strength == nil {
		t.Error("reported no signal strength; an empty frequency and a strong one that will not decode are different problems")
	}
	if result.Tuner == "" {
		t.Error("did not say which tuner was used")
	}
}

func TestTuneReportsMeasurementsOnALock(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)
	response, body := get(t, listener.Client(),
		listener.URL+"/tune?system=ISDBT&freq=557142857&settle=0")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("answered %d: %s", response.StatusCode, body)
	}
	var result hostapi.TuneResult
	_ = json.Unmarshal(body, &result)
	if !result.Locked {
		t.Fatal("did not report the lock")
	}
	if result.CNR == nil || *result.CNR != 28.5 {
		t.Errorf("reported a carrier-to-noise figure of %v", result.CNR)
	}
}

// TestTuneKeepsTheTunerForTheConnection lets a scan work down a list without
// losing its tuner between channels.
func TestTuneKeepsTheTunerForTheConnection(t *testing.T) {
	listener, supervisor, _ := newTestServer(t, nil)

	// One client, one connection, two channels in a row.
	client := listener.Client()
	client.Transport = &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}

	response, body := get(t, client, listener.URL+"/tune?system=ISDBT&freq=557142857&settle=0")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first tune answered %d: %s", response.StatusCode, body)
	}
	var first hostapi.TuneResult
	_ = json.Unmarshal(body, &first)

	// A second client cannot take the tuner the first is holding.
	other := &http.Client{Transport: &http.Transport{}}
	response, _ = get(t, other, listener.URL+"/tune?tuner="+first.Tuner+"&system=ISDBT&freq=521142857&settle=0")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("another connection took a held tuner: %d", response.StatusCode)
	}

	// The same connection tunes again and keeps it.
	response, body = get(t, client, listener.URL+"/tune?tuner="+first.Tuner+"&system=ISDBT&freq=521142857&settle=0")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the same connection could not retune its own tuner: %d %s", response.StatusCode, body)
	}

	// Closing the connection gives it back.
	client.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for {
		boards, err := supervisor.Boards()
		if err != nil {
			t.Fatalf("list boards: %v", err)
		}
		free := true
		for _, tuner := range boards[0].Tuners {
			if tuner.Leased {
				free = false
			}
		}
		if free {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tuner was not given back when the connection closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTuneRefusesWhatItCannotDo(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	client := listener.Client()

	cases := []struct {
		name string
		url  string
		want int
		code string
	}{
		{"a parameter that does not exist", "/tune?system=ISDBT&freq=557142857&nonsense=1", http.StatusBadRequest, hostapi.CodeInvalidParameter},
		{"a tuner that does not exist", "/tune?tuner=px4:NOPE:0&system=ISDBT&freq=557142857", http.StatusNotFound, hostapi.CodeUnknownTuner},
		{"a delivery system nothing reaches", "/tune?system=DVBT2&freq=500000000", http.StatusConflict, hostapi.CodeUnsupported},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			response, body := get(t, client, listener.URL+one.url)
			if response.StatusCode != one.want {
				t.Fatalf("answered %d, want %d: %s", response.StatusCode, one.want, body)
			}
			var failure hostapi.Failure
			if err := json.Unmarshal(body, &failure); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if failure.Error.Code != one.code {
				t.Errorf("answered with code %q, want %q", failure.Error.Code, one.code)
			}
		})
	}

	// A tuning the hardware refuses is a different answer again.
	registry.board.mutex.Lock()
	registry.board.tuneErr = errors.New("the demodulator would not take it")
	registry.board.mutex.Unlock()
	response, body := get(t, client, listener.URL+"/tune?system=ISDBT&freq=557142857")
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("a refused tuning answered %d: %s", response.StatusCode, body)
	}
}

func TestStreamSendsTransportStream(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBT&freq=557142857")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("answered %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "video/mp2t" {
		t.Errorf("sent it as %q", got)
	}
	if response.Header.Get("X-Coax-Tuner") == "" {
		t.Error("did not say which tuner the stream came from")
	}

	buffer := make([]byte, 4096)
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	if buffer[0] != 0x47 {
		t.Errorf("the stream does not begin with a transport packet")
	}
}

// TestStreamRefusesAChannelThatDoesNotLock keeps a player from being handed a
// connection that stays silent for ever.
func TestStreamRefusesAChannelThatDoesNotLock(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.locks = false
	registry.board.mutex.Unlock()

	response, body := get(t, listener.Client(), listener.URL+"/stream?system=ISDBT&freq=557142857&lock_timeout=50ms")
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("answered %d, want 502: %s", response.StatusCode, body)
	}
	var failure hostapi.Failure
	_ = json.Unmarshal(body, &failure)
	if failure.Error.Code != hostapi.CodeNoLock {
		t.Errorf("answered with code %q, want %q", failure.Error.Code, hostapi.CodeNoLock)
	}
	if failure.Status == nil || failure.Status.Strength == nil {
		t.Error("did not report what it measured before giving up")
	}
}

// TestStreamStartsAnywayWhenAsked covers begin=now: the stream whatever the
// demodulator thinks.
func TestStreamStartsAnywayWhenAsked(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.locks = false
	registry.board.mutex.Unlock()

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBT&freq=557142857&lock_timeout=50ms&begin=now")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want 200", response.StatusCode)
	}
}

// TestTunerReportsDropsWhileStreaming: drops tell a recording that lost data
// from one fed a poor signal.
func TestTunerReportsDropsWhileStreaming(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBT&freq=557142857")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	buffer := make([]byte, 1024)
	_, _ = io.ReadFull(response.Body, buffer)
	id := response.Header.Get("X-Coax-Tuner")

	// Asking about a tuner works while it is streaming, which is the point.
	other := &http.Client{Transport: &http.Transport{}}
	status, body := get(t, other, listener.URL+"/tuners/"+id)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("GET /tuners/%s answered %d: %s", id, status.StatusCode, body)
	}
	var tuner hostapi.Tuner
	if err := json.Unmarshal(body, &tuner); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tuner.Locked == nil || !*tuner.Locked {
		t.Error("did not report the lock of a streaming tuner")
	}
	if tuner.Dropped == nil || *tuner.Dropped != 7 {
		t.Errorf("reported drops as %v, want 7", tuner.Dropped)
	}
}

func TestUnknownTunerIsNotFound(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)
	response, _ := get(t, listener.Client(), listener.URL+"/tuners/px4:NOPE:0")
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("answered %d, want 404", response.StatusCode)
	}
}

// fakeCards is a card slot that answers without hardware.
type fakeCards struct {
	mutex    sync.Mutex
	sessions int
	// open counts the cards handed out and not yet given back, which is what a
	// connection closing has to bring back to zero.
	open   int
	events uint32
}

func (c *fakeCards) Readers() ([]CardInfo, error) {
	return []CardInfo{{Reader: "PLEX Smart Card Reader", Backend: "px4", Board: "px4:ABC123@1-2"}}, nil
}

func (c *fakeCards) Present(string) (bool, error) { return true, nil }

func (c *fakeCards) Session(_ string, apdus [][]byte) ([]byte, [][]byte, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.sessions++
	responses := make([][]byte, 0, len(apdus))
	for range apdus {
		responses = append(responses, []byte{0x12, 0x90, 0x00})
	}
	return []byte{0x3B, 0xF0}, responses, nil
}

func (c *fakeCards) Connect(reader string) (CardConn, error) {
	if reader != "PLEX Smart Card Reader" {
		return nil, errors.New("no card reader is called " + reader)
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.open++
	return &fakeCardConn{cards: c}, nil
}

func (c *fakeCards) Watch(ctx context.Context, reader string, since uint32, wait time.Duration) (CardState, error) {
	if reader != "PLEX Smart Card Reader" {
		return CardState{}, errors.New("no card reader is called " + reader)
	}
	c.mutex.Lock()
	events := c.events
	c.mutex.Unlock()
	state := CardState{Events: events, Present: true, ATR: []byte{0x3B, 0xF0}, Sharing: 1}
	if events != since {
		state.Changed = true
		return state, nil
	}
	if wait <= 0 {
		return state, nil
	}
	select {
	case <-ctx.Done():
		return CardState{}, ctx.Err()
	case <-time.After(wait):
		return state, nil
	}
}

// fakeCardConn is one open card, remembering what was done to it so a test can
// check the card was actually let go of.
type fakeCardConn struct {
	cards       *fakeCards
	transmitted [][]byte
	transaction bool
	closed      bool
	disposition string
}

func (f *fakeCardConn) ATR() []byte      { return []byte{0x3B, 0xF0} }
func (f *fakeCardConn) Protocol() string { return "T=1" }
func (f *fakeCardConn) MaxInput() int    { return 260 }

func (f *fakeCardConn) Transmit(send []byte) ([]byte, error) {
	if f.closed {
		return nil, errors.New("the card was let go of")
	}
	f.transmitted = append(f.transmitted, send)
	return []byte{byte(len(f.transmitted)), 0x90, 0x00}, nil
}

func (f *fakeCardConn) BeginTransaction() error {
	f.transaction = true
	return nil
}

func (f *fakeCardConn) EndTransaction(disposition string) error {
	f.transaction = false
	f.disposition = disposition
	return nil
}

func (f *fakeCardConn) Close(disposition string) error {
	f.closed = true
	f.disposition = disposition
	f.cards.mutex.Lock()
	defer f.cards.mutex.Unlock()
	f.cards.open--
	return nil
}

func TestCardSessionIsOneConversation(t *testing.T) {
	cards := &fakeCards{}
	listener, _, _ := newTestServer(t, cards)

	body := strings.NewReader(`{"apdus":["90 30 00 00 00","9032000000"]}`)
	response, err := listener.Client().Post(
		listener.URL+"/cards/PLEX Smart Card Reader/session", "application/json", body)
	if err != nil {
		t.Fatalf("POST a card session: %v", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("answered %d: %s", response.StatusCode, raw)
	}
	var session hostapi.CardSession
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if session.ATR != "3B F0" {
		t.Errorf("returned the ATR %q", session.ATR)
	}
	if len(session.Exchanges) != 2 {
		t.Fatalf("returned %d exchanges, want 2", len(session.Exchanges))
	}
	if session.Exchanges[0].Status != "90 00" {
		t.Errorf("split out the status bytes as %q", session.Exchanges[0].Status)
	}
	if cards.sessions != 1 {
		t.Errorf("ran %d sessions for one request; a reset and its commands are one conversation", cards.sessions)
	}
}

// TestOneSetOfRoutesEverywhere: the card is served wherever the daemon listens,
// so a client that reached it over --http can hand the card to the program
// it passes the stream through.
// Whether the port is opened at all is settled by --http and --allow-remote.
func TestOneSetOfRoutesEverywhere(t *testing.T) {
	supervisor := pool.New(&fakeRegistry{board: &fakeBoard{}}, pool.Options{})
	defer supervisor.Close()
	server := New(supervisor, Options{Cards: &fakeCards{}})

	listener := httptest.NewServer(server.Handler())
	defer listener.Close()

	for _, path := range []string{"/version", "/cards", "/cards/PLEX Smart Card Reader"} {
		response, err := listener.Client().Get(listener.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			t.Errorf("%s is not served", path)
		}
	}
}

// A daemon with no card slots does not answer for them at all, rather than
// answering that it has none: the endpoints are not there to be asked.
func TestCardRoutesAreAbsentWithoutCards(t *testing.T) {
	supervisor := pool.New(&fakeRegistry{board: &fakeBoard{}}, pool.Options{})
	defer supervisor.Close()
	server := New(supervisor, Options{})

	listener := httptest.NewServer(server.Handler())
	defer listener.Close()

	for _, path := range []string{"/cards", "/cards/x", "/cards/x/session"} {
		response, err := listener.Client().Get(listener.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %d on a daemon with no cards", path, response.StatusCode)
		}
	}
}

// TestConnectionsCounted: the count is how the daemon decides whether anything
// still needs it.
func TestConnectionsCounted(t *testing.T) {
	tuners := []device.TunerInfo{{Index: 0, Name: "R850", Deliveries: []string{"ISDB-T"}}}
	registry := &fakeRegistry{
		detected: []pool.Detected{{Backend: "px4", Selector: "001:007", Location: "001:007", Serial: "A", Port: "1-1"}},
		tuners:   tuners,
		board:    &fakeBoard{tuners: tuners, locks: true},
	}
	supervisor := pool.New(registry, pool.Options{})
	defer supervisor.Close()
	server := New(supervisor, Options{})
	listener := httptest.NewUnstartedServer(server.Handler())
	server.Attach(listener.Config)
	listener.Start()
	defer listener.Close()

	if got := server.Connections(); got != 0 {
		t.Errorf("counted %d connections before anything connected", got)
	}
	client := &http.Client{Transport: &http.Transport{}}
	response, err := client.Get(listener.URL + "/version")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()

	deadline := time.Now().Add(time.Second)
	for server.Connections() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a live connection was never counted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	client.CloseIdleConnections()
	deadline = time.Now().Add(2 * time.Second)
	for server.Connections() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("a closed connection is still counted (%d)", server.Connections())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTwoStreamsGetTwoTuners covers double recording: the rule that lets a scan
// keep its tuner between channels must not hand two clients the same tuner.
func TestTwoStreamsGetTwoTuners(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)
	client := listener.Client()

	first, err := client.Get(listener.URL + "/stream?system=ISDBS&freq=1049480&STREAM_ID=16625")
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first stream answered %d", first.StatusCode)
	}

	second, err := client.Get(listener.URL + "/stream?system=ISDBT&freq=557142857")
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second stream answered %d", second.StatusCode)
	}

	one := first.Header.Get("X-Coax-Tuner")
	two := second.Header.Get("X-Coax-Tuner")
	if one == "" || two == "" {
		t.Fatalf("a stream did not say which tuner it came from: %q and %q", one, two)
	}
	if one == two {
		t.Errorf("both recordings were given tuner %q", one)
	}

	// Both are really running.
	buffer := make([]byte, 1024)
	if _, err := io.ReadFull(first.Body, buffer); err != nil {
		t.Errorf("read the first stream: %v", err)
	}
	if _, err := io.ReadFull(second.Body, buffer); err != nil {
		t.Errorf("read the second stream: %v", err)
	}
}

// TestTheSameChannelIsNotTunedTwice: a scan measures a channel with /tune and
// then opens its stream naming the same channel. Retuning costs the acquisition
// twice and on a marginal signal gives the demodulator a second chance to miss
// -- observed on a MyGica T230C2 on cable, where a channel measured as locked
// failed to stream.
func TestTheSameChannelIsNotTunedTwice(t *testing.T) {
	server, _, registry := newTestServer(t, nil)
	client := server.Client()

	// The first request names no tuner and the answer says which was chosen;
	// later requests name it, as a client does. Answers are drained so the
	// connection returns to the pool -- the tuner belongs to the connection.
	chosen := tuneAndName(t, client, server.URL+"/tune?system=ISDBT&freq=557142857")
	opened := registry.board.lastDevice()
	if opened == nil {
		t.Fatal("nothing was tuned")
	}
	if got := opened.tuneCount(); got != 1 {
		t.Fatalf("the first tune touched the hardware %d times, want 1", got)
	}
	named := "&tuner=" + url.QueryEscape(chosen)

	// Asking for the channel it is already locked to changes nothing.
	if status := drain(t, client, server.URL+"/tune?system=ISDBT&freq=557142857"+named); status != http.StatusOK {
		t.Fatalf("re-asking for the same channel answered %d", status)
	}
	if got := opened.tuneCount(); got != 1 {
		t.Errorf("asking for the channel it was already on retuned the hardware (%d tunes)", got)
	}

	// A different channel is of course tuned.
	if status := drain(t, client, server.URL+"/tune?system=ISDBT&freq=563142857"+named); status != http.StatusOK {
		t.Fatalf("tuning another channel answered %d", status)
	}
	if got := opened.tuneCount(); got != 2 {
		t.Fatalf("a different channel was not tuned (%d tunes)", got)
	}

	// Streaming the channel it is on does not tune it again either, the case
	// this is all for.
	response, err := client.Get(server.URL + "/stream?system=ISDBT&freq=563142857" + named)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("streaming the channel just measured answered %d", response.StatusCode)
	}
	if got := opened.tuneCount(); got != 2 {
		t.Errorf("streaming the channel it was already on retuned the hardware (%d tunes)", got)
	}
}

// TestALostLockIsTunedAgain: skipping the retune is only safe while the tuner
// is still locked, since asking again is a caller's only way to say "retry".
func TestALostLockIsTunedAgain(t *testing.T) {
	server, _, registry := newTestServer(t, nil)
	client := server.Client()

	channel := "system=ISDBT&freq=557142857"
	chosen := tuneAndName(t, client, server.URL+"/tune?"+channel)
	channel += "&tuner=" + url.QueryEscape(chosen)
	opened := registry.board.lastDevice()

	registry.board.mutex.Lock()
	registry.board.locks = false
	registry.board.mutex.Unlock()

	drain(t, client, server.URL+"/tune?"+channel+"&lock_timeout=10ms")
	if got := opened.tuneCount(); got != 2 {
		t.Errorf("a tuner that had lost its lock was not tuned again (%d tunes)", got)
	}
}

// drain reads the answer to the end, returning the connection to the pool. A
// test that leaks one has two clients, and a tuner belongs to its connection.
func drain(t *testing.T, client *http.Client, target string) int {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("request %s: %v", target, err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("read the answer to %s: %v", target, err)
	}
	return response.StatusCode
}

// tuneAndName tunes and reports which tuner the daemon chose, as a client does
// before naming it in everything that follows.
func tuneAndName(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Fatalf("request %s: %v", target, err)
	}
	defer response.Body.Close()
	var result hostapi.TuneResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode the answer to %s: %v", target, err)
	}
	if result.Tuner == "" {
		t.Fatalf("the answer to %s names no tuner", target)
	}
	return result.Tuner
}

// TestStreamRefusesATunerThatLockedToNoise: a demodulator locks on the state of
// its own loops, so a dead lead locks and hands over noise indefinitely while
// every hardware measurement says the tuner is working. Only the bytes say
// otherwise.
func TestStreamRefusesATunerThatLockedToNoise(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.noise = true
	registry.board.mutex.Unlock()

	response, body := get(t, listener.Client(), listener.URL+"/stream?system=ISDBT&freq=557142857")
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("answered %d, want 502: %s", response.StatusCode, body)
	}
	var failure hostapi.Failure
	_ = json.Unmarshal(body, &failure)
	if failure.Error.Code != hostapi.CodeNoSync {
		t.Errorf("answered with code %q, want %q", failure.Error.Code, hostapi.CodeNoSync)
	}
	if !strings.Contains(failure.Error.Message, "lnb_power") {
		t.Errorf("did not say what to try: %q", failure.Error.Message)
	}
}

// A caller that asks to see what the tuner is actually producing gets it.
func TestStreamHandsOverNoiseWhenTheCheckIsTurnedOff(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.noise = true
	registry.board.mutex.Unlock()

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBT&freq=557142857&sync=off&bytes=4096")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("answered %d, want 200", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	if len(body) != 4096 {
		t.Errorf("sent %d bytes, want the 4096 asked for", len(body))
	}
}

// A multiplex that is not a transport stream is checked as what it is. ISDB-S3
// carries MMT over TLV, whose packets carry their own length, so the boundary
// is stated rather than assumed.
func TestStreamChecksSyncOnTLVAsWell(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBS3&freq=1049480&bytes=" + strconv.Itoa(tlvPacketSize*8))
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("a TLV stream answered %d, want 200", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	// Closed here rather than deferred: the connection holds the tuner and only
	// one of the two receives ISDB-S3, so the rest of the test would be told
	// the tuner was busy.
	response.Body.Close()
	if err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	for offset := 0; offset < len(body); offset += tlvPacketSize {
		if body[offset] != 0x7f {
			t.Fatalf("the TLV packet at %d does not begin the stream where it should", offset)
		}
	}

}

// Noise on ISDB-S3 is refused too, in the terms of the multiplex it should have
// been producing.
func TestStreamRefusesNoiseOnTLVTooAndSaysSo(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.noise = true
	registry.board.mutex.Unlock()

	status, failed := get(t, listener.Client(), listener.URL+"/stream?system=ISDBS3&freq=1049480&bytes=4096")
	if status.StatusCode != http.StatusBadGateway {
		t.Fatalf("noise on ISDB-S3 answered %d, want 502: %s", status.StatusCode, failed)
	}
	var failure hostapi.Failure
	_ = json.Unmarshal(failed, &failure)
	if failure.Error.Code != hostapi.CodeNoSync {
		t.Errorf("answered with code %q, want %q", failure.Error.Code, hostapi.CodeNoSync)
	}
	if !strings.Contains(failure.Error.Message, "TLV") {
		t.Errorf("did not say what it was looking for: %q", failure.Error.Message)
	}
}

// A channel written as ISDBS -- as real ISDB-S3 ones are, the Linux DVB API
// having no number of its own -- is still recognised as TLV from its bytes.
func TestTLVIsRecognisedWithoutBeingAnnounced(t *testing.T) {
	listener, _, registry := newTestServer(t, nil)
	registry.board.mutex.Lock()
	registry.board.alwaysTLV = true
	registry.board.mutex.Unlock()

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBS&freq=1049480&stream_id=16625")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("a TLV stream announced as ISDBS answered %d, want 200", response.StatusCode)
	}
	buffer := make([]byte, tlvPacketSize*64)
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatalf("read the stream: %v", err)
	}

	status, body := get(t, &http.Client{Transport: &http.Transport{}},
		listener.URL+"/tuners/"+response.Header.Get("X-Coax-Tuner"))
	if status.StatusCode != http.StatusOK {
		t.Fatalf("GET /tuners answered %d: %s", status.StatusCode, body)
	}
	var described hostapi.Tuner
	if err := json.Unmarshal(body, &described); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if described.Stream == nil || described.Stream.Format != "tlv" {
		t.Fatalf("read an unannounced TLV stream as %+v", described.Stream)
	}
}

// The counts a TLV stream reports are its own: TLV packet types and MMTP
// sequence numbers, not transport packets.
func TestTunerReportsTLVCountsForISDBS3(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBS3&freq=1049480")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	buffer := make([]byte, tlvPacketSize*64)
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	id := response.Header.Get("X-Coax-Tuner")

	status, body := get(t, &http.Client{Transport: &http.Transport{}}, listener.URL+"/tuners/"+id)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("GET /tuners/%s answered %d: %s", id, status.StatusCode, body)
	}
	var described hostapi.Tuner
	if err := json.Unmarshal(body, &described); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if described.Stream == nil {
		t.Fatal("a streaming tuner said nothing about its stream")
	}
	if described.Stream.Format != "tlv" {
		t.Errorf("reported the format as %q, want tlv", described.Stream.Format)
	}
	if described.Stream.CompressedIP == 0 {
		t.Error("counted no compressed-IP packets in a stream of them")
	}
	if described.Stream.SyncLosses != 0 || described.Stream.Discontinuities != 0 || described.Stream.Undecodable != 0 {
		t.Errorf("a clean TLV stream reported %+v", *described.Stream)
	}
}

// Nothing the sync check reads is lost: it is the start of the stream, not a
// sample taken out of it.
func TestTheBytesTheSyncCheckReadAreSentOn(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBT&freq=557142857&bytes=18800")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	if len(body) != 18800 {
		t.Fatalf("sent %d bytes, want the 18800 asked for", len(body))
	}
	for offset := 0; offset < len(body); offset += mpegts.PacketSize {
		if body[offset] != mpegts.SyncByte {
			t.Fatalf("the packet at %d does not begin the stream where it should", offset)
		}
	}
}

// TestTunerReportsWhatTheStreamLookedLike: the counts separate a recording that
// lost data, one fed a poor signal and one on a dead lead, which look identical
// in the file.
func TestTunerReportsWhatTheStreamLookedLike(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	response, err := listener.Client().Get(listener.URL + "/stream?system=ISDBT&freq=557142857")
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	defer response.Body.Close()
	buffer := make([]byte, 64*mpegts.PacketSize)
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	id := response.Header.Get("X-Coax-Tuner")

	other := &http.Client{Transport: &http.Transport{}}
	status, body := get(t, other, listener.URL+"/tuners/"+id)
	if status.StatusCode != http.StatusOK {
		t.Fatalf("GET /tuners/%s answered %d: %s", id, status.StatusCode, body)
	}
	var described hostapi.Tuner
	if err := json.Unmarshal(body, &described); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if described.Stream == nil {
		t.Fatal("a streaming tuner said nothing about its stream")
	}
	if described.Stream.Packets == 0 {
		t.Error("counted no packets in a stream of packets")
	}
	if described.Stream.SyncLosses != 0 || described.Stream.Errors != 0 {
		t.Errorf("a clean stream reported %+v", *described.Stream)
	}
}

// A tuner nobody is streaming reports nothing rather than zeroes: no packets
// seen and no packets wrong are opposite readings.
func TestATunerThatIsNotStreamingReportsNoStreamCounts(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	status, body := get(t, client, listener.URL+"/tune?system=ISDBT&freq=557142857&settle=0")
	if status.StatusCode != http.StatusOK {
		t.Fatalf("GET /tune answered %d: %s", status.StatusCode, body)
	}
	var result hostapi.TuneResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	other, body := get(t, &http.Client{Transport: &http.Transport{}}, listener.URL+"/tuners/"+result.Tuner)
	if other.StatusCode != http.StatusOK {
		t.Fatalf("GET /tuners answered %d: %s", other.StatusCode, body)
	}
	var described hostapi.Tuner
	if err := json.Unmarshal(body, &described); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if described.Stream != nil {
		t.Errorf("a tuner that is not streaming reported %+v", *described.Stream)
	}
}

func TestStreamPrefixAlignsBothFramings(t *testing.T) {
	ts := make([]byte, 188*4)
	for at := 0; at < len(ts); at += 188 {
		ts[at] = 0x47
	}
	tlv := []byte{0x7f, 0xff, 0, 1, 5, 0x7f, 0xff, 0, 0, 0x7f, 0xff, 0, 2, 1, 2}
	for format, data := range map[string][]byte{"ts": ts, "tlv": tlv} {
		prefix := append([]byte{0xfe, 0xff, 0, 1, 2}, data...)
		got := alignedPrefix(prefix, format)
		if string(got) != string(data) {
			t.Fatalf("%s prefix not aligned", format)
		}
	}
}
