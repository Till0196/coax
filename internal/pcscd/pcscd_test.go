// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package pcscd

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/winscard"
)

// TestStructureSizes checks the constants against the C structures in
// pcsc-lite's winscard_msg.h and readers.h. They are the whole compatibility
// contract -- every field is at a fixed offset in a fixed-length message -- so
// a structure that changed size shows up here first.
func TestStructureSizes(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"struct version_struct", sizeVersion, 12},
		{"struct establish_struct", sizeEstablish, 12},
		{"struct release_struct", sizeRelease, 8},
		{"struct connect_struct", sizeConnect, 4 + maxReaderName + 4*5},
		{"struct reconnect_struct", sizeReconnect, 24},
		{"struct disconnect_struct", sizeDisconnect, 12},
		{"struct begin_struct", sizeBegin, 8},
		{"struct end_struct", sizeEnd, 12},
		{"struct cancel_struct", sizeCancel, 8},
		{"struct status_struct", sizeStatus, 8},
		{"struct transmit_struct", sizeTransmit, 32},
		{"struct control_struct", sizeControl, 24},
		{"struct getset_struct", sizeGetSet, 4 + 4 + maxAttribute + 4 + 4},
		{"struct wait_reader_state_change", sizeWait, 8},
		{"struct get_reader_events", sizeEvents, 8},
		{"READER_STATE", readerStateLen, 184},
	}
	for _, one := range cases {
		if one.got != one.want {
			t.Errorf("%s is %d bytes, want %d", one.name, one.got, one.want)
		}
	}
}

func TestReaderStateLayout(t *testing.T) {
	encoded := readerState{
		name:     "PLEX PX-W3U4 series Smart Card Reader (ID: 0123456789ABCDEF)",
		events:   7,
		state:    statePresent,
		sharing:  sharingExclusive,
		atr:      []byte{0x3B, 0xF0, 0x12},
		protocol: protocolT1,
	}.encode()

	if len(encoded) != readerStateLen {
		t.Fatalf("encoded %d bytes, want %d", len(encoded), readerStateLen)
	}
	if name := cString(encoded[0:maxReaderName]); name != "PLEX PX-W3U4 series Smart Card Reader (ID: 0123456789ABCDEF)" {
		t.Errorf("the name came back as %q", name)
	}
	if got := order.Uint32(encoded[128:132]); got != 7 {
		t.Errorf("eventCounter is at the wrong offset: %d", got)
	}
	if got := order.Uint32(encoded[132:136]); got != statePresent {
		t.Errorf("readerState is at the wrong offset: %#x", got)
	}
	if got := int32(order.Uint32(encoded[136:140])); got != sharingExclusive {
		t.Errorf("readerSharing is at the wrong offset: %d", got)
	}
	if got := encoded[140:143]; string(got) != string([]byte{0x3B, 0xF0, 0x12}) {
		t.Errorf("cardAtr is at the wrong offset: %X", got)
	}
	if got := order.Uint32(encoded[176:180]); got != 3 {
		t.Errorf("cardAtrLength is at the wrong offset: %d", got)
	}
	if got := order.Uint32(encoded[180:184]); got != protocolT1 {
		t.Errorf("cardProtocol is at the wrong offset: %d", got)
	}
}

// fakeSlots is one slot into which a test can insert a card from another
// goroutine while the table is measuring.
type fakeSlots struct {
	mutex   sync.Mutex
	present bool
}

func (s *fakeSlots) seated() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.present
}

func (s *fakeSlots) insert() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.present = true
}

func (s *fakeSlots) Readers() ([]string, error) { return []string{"Fake Smart Card Reader 0"}, nil }

func (s *fakeSlots) Open(reader string) (Slot, error) { return fakeSlot{slots: s}, nil }

type fakeSlot struct{ slots *fakeSlots }

func (s fakeSlot) Present() (bool, error) { return s.slots.seated(), nil }
func (s fakeSlot) ATR() []byte            { return []byte{0x3B, 0xF0} }
func (s fakeSlot) Sharing() int           { return 0 }
func (s fakeSlot) Close() error           { return nil }

// fakeProvider is the winscard provider behind the service, backed by
// fakeSlots so that connecting fails until a card is seated.
type fakeProvider struct{ slots *fakeSlots }

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) EstablishContext(winscard.Scope) (winscard.ProviderContext, error) {
	return fakeContext{provider: p}, nil
}

type fakeContext struct{ provider *fakeProvider }

func (c fakeContext) Readers([]string) ([]string, error) {
	return []string{"Fake Smart Card Reader 0"}, nil
}
func (c fakeContext) ReaderGroups() ([]string, error) {
	return []string{winscard.DefaultReaderGroup}, nil
}
func (c fakeContext) Release() error { return nil }

func (c fakeContext) Connect(reader string, share winscard.ShareMode, protocols winscard.Protocol) (winscard.Card, winscard.Protocol, error) {
	if !c.provider.slots.seated() {
		return nil, winscard.ProtocolUndefined, winscard.ErrNoSmartcard
	}
	return fakeCard{}, winscard.ProtocolT1, nil
}

func (c fakeContext) GetStatusChange(ctx context.Context, timeout time.Duration, states []winscard.ReaderState) ([]winscard.ReaderState, error) {
	<-ctx.Done()
	return nil, winscard.ErrCancelled
}

type fakeCard struct{}

func (fakeCard) Reconnect(winscard.ShareMode, winscard.Protocol, winscard.Disposition) (winscard.Protocol, error) {
	return winscard.ProtocolT1, nil
}
func (fakeCard) Disconnect(winscard.Disposition) error     { return nil }
func (fakeCard) BeginTransaction() error                   { return nil }
func (fakeCard) EndTransaction(winscard.Disposition) error { return nil }
func (fakeCard) Control(uint32, []byte) ([]byte, error)    { return nil, winscard.ErrUnsupportedFeature }
func (fakeCard) SetAttrib(winscard.Attribute, []byte) error {
	return winscard.ErrUnsupportedFeature
}

func (fakeCard) Status() (winscard.Status, error) {
	return winscard.Status{
		Readers:  []string{"Fake Smart Card Reader 0"},
		State:    winscard.CardSpecific,
		Protocol: winscard.ProtocolT1,
		ATR:      []byte{0x3B, 0xF0},
	}, nil
}

func (fakeCard) Transmit(send []byte, protocol winscard.Protocol) ([]byte, winscard.Protocol, error) {
	return append(append([]byte(nil), send[0]), 0x90, 0x00), winscard.ProtocolT1, nil
}

func (fakeCard) GetAttrib(id winscard.Attribute) ([]byte, error) {
	if id == winscard.AttrATRString {
		return []byte{0x3B, 0xF0}, nil
	}
	return nil, winscard.ErrUnsupportedFeature
}

// running starts a server on a socket of its own and returns its path.
func running(t *testing.T) string {
	t.Helper()
	slots := &fakeSlots{present: true}
	table := NewTable(slots, 5*time.Millisecond, false)
	service := winscard.NewService(&fakeProvider{slots: slots})
	server := New(service, table, func(string) {})
	table.OnEvent(server.Notify)

	path := filepath.Join(t.TempDir(), "pcscd.comm")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go server.Serve(listener)
	t.Cleanup(func() {
		_ = listener.Close()
		_ = table.Close()
	})
	return path
}

// client is a test client speaking the protocol the way libpcsclite does.
type client struct {
	t      *testing.T
	socket net.Conn
}

func dial(t *testing.T, path string) *client {
	t.Helper()
	socket, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })
	return &client{t: t, socket: socket}
}

// send writes a request the way MessageSendWithHeader does: header, then body.
func (c *client) send(command uint32, payload []byte) {
	c.t.Helper()
	raw := make([]byte, 8)
	put(raw, 0, uint32(len(payload)))
	put(raw, 1, command)
	if _, err := c.socket.Write(raw); err != nil {
		c.t.Fatalf("send the header: %v", err)
	}
	if len(payload) > 0 {
		if _, err := c.socket.Write(payload); err != nil {
			c.t.Fatalf("send the body: %v", err)
		}
	}
}

// receive reads a reply of a known length, which is what a client does.
func (c *client) receive(length int) []byte {
	c.t.Helper()
	raw := make([]byte, length)
	_ = c.socket.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c.socket, raw); err != nil {
		c.t.Fatalf("receive %d bytes: %v", length, err)
	}
	return raw
}

// handshake agrees a protocol version and returns the daemon's result code.
func (c *client) handshake(major, minor uint32) uint32 {
	c.t.Helper()
	raw := make([]byte, sizeVersion)
	put(raw, 0, major)
	put(raw, 1, minor)
	c.send(cmdVersion, raw)
	return field(c.receive(sizeVersion), 2)
}

func (c *client) establish() (uint32, uint32) {
	c.t.Helper()
	raw := make([]byte, sizeEstablish)
	put(raw, 0, uint32(winscard.ScopeSystem))
	c.send(cmdEstablishContext, raw)
	answer := c.receive(sizeEstablish)
	return field(answer, 1), field(answer, 2)
}

func TestVersionHandshake(t *testing.T) {
	path := running(t)
	if result := dial(t, path).handshake(versionMajor, versionMinor); result != uint32(winscard.Success) {
		t.Errorf("the current version was refused: %v", winscard.Code(result))
	}
	if result := dial(t, path).handshake(versionMajor, backwardMinor); result != uint32(winscard.Success) {
		t.Errorf("a client one protocol behind was refused: %v", winscard.Code(result))
	}
	if result := dial(t, path).handshake(versionMajor+1, 0); result != uint32(winscard.ErrServiceStopped) {
		t.Errorf("a client of another protocol was accepted: %v", winscard.Code(result))
	}
}

func TestReaderStateArray(t *testing.T) {
	client := dial(t, running(t))
	client.handshake(versionMajor, versionMinor)

	client.send(cmdGetStateSize, nil)
	if size := int32(field(client.receive(4), 0)); size != maxReaders {
		t.Fatalf("the array is %d entries, want %d", size, maxReaders)
	}
	client.send(cmdGetStateArray, nil)
	raw := client.receive(maxReaders * readerStateLen)
	if name := cString(raw[0:maxReaderName]); name != "Fake Smart Card Reader 0" {
		t.Errorf("the first reader is %q", name)
	}
	if name := cString(raw[readerStateLen : readerStateLen+maxReaderName]); name != "" {
		t.Errorf("the second entry should be empty, got %q", name)
	}
}

func TestConnectTransmitDisconnect(t *testing.T) {
	client := dial(t, running(t))
	client.handshake(versionMajor, versionMinor)
	context, result := client.establish()
	if result != uint32(winscard.Success) {
		t.Fatalf("establish a context: %v", winscard.Code(result))
	}

	raw := make([]byte, sizeConnect)
	put(raw, 0, context)
	putCString(raw[4:4+maxReaderName], "Fake Smart Card Reader 0")
	order.PutUint32(raw[132:136], uint32(winscard.ShareShared))
	order.PutUint32(raw[136:140], uint32(winscard.ProtocolAny))
	client.send(cmdConnect, raw)
	answer := client.receive(sizeConnect)
	card := int32(order.Uint32(answer[140:144]))
	if result := order.Uint32(answer[148:152]); result != uint32(winscard.Success) {
		t.Fatalf("connect: %v", winscard.Code(result))
	}
	if protocol := order.Uint32(answer[144:148]); protocol != uint32(winscard.ProtocolT1) {
		t.Errorf("agreed protocol %d, want T=1", protocol)
	}

	command := []byte{0x90, 0x30, 0x00, 0x00, 0x00}
	raw = make([]byte, sizeTransmit)
	put(raw, 0, uint32(card))
	put(raw, 1, uint32(winscard.ProtocolT1))
	put(raw, 2, 8)
	put(raw, 3, uint32(len(command)))
	put(raw, 6, 264)
	client.send(cmdTransmit, raw)
	if _, err := client.socket.Write(command); err != nil {
		t.Fatalf("send the APDU: %v", err)
	}
	answer = client.receive(sizeTransmit)
	if result := field(answer, 7); result != uint32(winscard.Success) {
		t.Fatalf("transmit: %v", winscard.Code(result))
	}
	length := field(answer, 6)
	response := client.receive(int(length))
	if len(response) != 3 || response[1] != 0x90 || response[2] != 0x00 {
		t.Errorf("the card answered %X", response)
	}

	raw = make([]byte, sizeDisconnect)
	put(raw, 0, uint32(card))
	put(raw, 1, uint32(winscard.LeaveCard))
	client.send(cmdDisconnect, raw)
	if result := field(client.receive(sizeDisconnect), 2); result != uint32(winscard.Success) {
		t.Errorf("disconnect: %v", winscard.Code(result))
	}
}

func TestCancelSignalsTheWaitingConnection(t *testing.T) {
	path := running(t)
	waiter := dial(t, path)
	waiter.handshake(versionMajor, versionMinor)
	context, _ := waiter.establish()

	// The first look at a slot is itself a change -- the state went from never
	// having been measured to measured -- so read that event before waiting
	// for the one this test is about.
	waiter.send(cmdWaitStateChange, nil)
	if result := field(waiter.receive(sizeWait), 1); result != uint32(winscard.Success) {
		t.Fatalf("the first event was %v", winscard.Code(result))
	}
	waiter.send(cmdWaitStateChange, nil)

	// The cancel arrives on a connection of its own, because the waiting one
	// is blocked reading. This is the case the server's context table exists
	// for.
	canceller := dial(t, path)
	canceller.handshake(versionMajor, versionMinor)
	raw := make([]byte, sizeCancel)
	put(raw, 0, context)
	canceller.send(cmdCancel, raw)
	if result := field(canceller.receive(sizeCancel), 1); result != uint32(winscard.Success) {
		t.Fatalf("cancel: %v", winscard.Code(result))
	}

	if result := field(waiter.receive(sizeWait), 1); result != uint32(winscard.ErrCancelled) {
		t.Fatalf("the waiter was told %v, want %v", winscard.Code(result), winscard.ErrCancelled)
	}
}

func TestCardArrivalWakesAWaitingClient(t *testing.T) {
	slots := &fakeSlots{present: false}
	table := NewTable(slots, 5*time.Millisecond, false)
	service := winscard.NewService(&fakeProvider{slots: slots})
	server := New(service, table, func(string) {})
	table.OnEvent(server.Notify)
	path := filepath.Join(t.TempDir(), "pcscd.comm")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	defer table.Close()
	go server.Serve(listener)

	client := dial(t, path)
	client.handshake(versionMajor, versionMinor)
	client.establish()
	client.send(cmdWaitStateChange, nil)
	// The first sweep measures the empty slot, which is itself a change from
	// never having looked; read that one before making the card arrive.
	if result := field(client.receive(sizeWait), 1); result != uint32(winscard.Success) {
		t.Fatalf("the first event was %v", winscard.Code(result))
	}

	client.send(cmdWaitStateChange, nil)
	slots.insert()
	if result := field(client.receive(sizeWait), 1); result != uint32(winscard.Success) {
		t.Fatalf("the arrival was reported as %v", winscard.Code(result))
	}
	client.send(cmdGetStateArray, nil)
	raw := client.receive(maxReaders * readerStateLen)
	if state := order.Uint32(raw[132:136]); state&statePresent == 0 {
		t.Errorf("the reader still reports %#x, want a card present", state)
	}
}

func TestUnknownCommandDropsTheClient(t *testing.T) {
	client := dial(t, running(t))
	client.handshake(versionMajor, versionMinor)
	client.send(0x7F, nil)
	_ = client.socket.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.socket.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("the server answered an unknown command with %v, want the connection closed", err)
	}
}
