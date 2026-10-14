// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/usb"
	"github.com/Till0196/coax/pkg/usbio"
	"github.com/Till0196/coax/pkg/winscard"
)

// fakeBoard is a board with a card interface on it.
type fakeBoard struct {
	mutex sync.Mutex
	// present is whether a card is seated, which a test moves under the
	// provider to stand in for someone pulling the card out.
	present bool
	// noReader makes the board one of the halves that carries no slot.
	noReader bool
	atr      []byte
	sent     [][]byte
	resets   int
	opens    int
	closes   int
	// transmitErr fails the next exchange.
	transmitErr error
}

func (b *fakeBoard) Name() string               { return "fake" }
func (b *fakeBoard) Tuners() []device.TunerInfo { return nil }

func (b *fakeBoard) OpenTuner(index int) (device.Tuner, error) {
	return nil, errors.New("the fake board has no tuners")
}

func (b *fakeBoard) Close() error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.closes++
	return nil
}

func (b *fakeBoard) CardStatus() (device.CardStatus, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return device.CardStatus{Present: b.present, Reader: "FAKE rev 1"}, nil
}

func (b *fakeBoard) ResetCard() ([]byte, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if !b.present {
		return nil, errors.New("no card")
	}
	b.resets++
	return append([]byte(nil), b.atr...), nil
}

func (b *fakeBoard) TransmitAPDU(apdu []byte) ([]byte, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.transmitErr != nil {
		err := b.transmitErr
		b.transmitErr = nil
		return nil, err
	}
	b.sent = append(b.sent, append([]byte(nil), apdu...))
	return []byte{0x12, 0x90, 0x00}, nil
}

// plainBoard is a board with no card interface at all, standing in for the
// half of an enclosure whose slot is not wired.
type plainBoard struct{ board *fakeBoard }

func (b plainBoard) Name() string               { return "fake" }
func (b plainBoard) Tuners() []device.TunerInfo { return nil }
func (b plainBoard) Close() error               { return b.board.Close() }

func (b plainBoard) OpenTuner(index int) (device.Tuner, error) {
	return nil, errors.New("the fake board has no tuners")
}

// stub replaces the registry calls for the duration of a test.
func stub(t *testing.T, boards map[string]*fakeBoard, detected []usb.Detected) {
	t.Helper()
	savedDetect, savedOpen := detect, openBoard
	detect = func() ([]usb.Detected, error) { return detected, nil }
	openBoard = func(name string, options device.Options) (device.Board, error) {
		board, ok := boards[options.Selector]
		if !ok {
			return nil, errors.New("no such board")
		}
		board.mutex.Lock()
		board.opens++
		noReader := board.noReader
		board.mutex.Unlock()
		if noReader {
			return plainBoard{board: board}, nil
		}
		return board, nil
	}
	t.Cleanup(func() { detect, openBoard = savedDetect, savedOpen })
}

// oneBoard arranges a single px4 board with a card in it.
func oneBoard(t *testing.T) (*Provider, *fakeBoard, string) {
	t.Helper()
	board := &fakeBoard{present: true, atr: []byte{0x3B, 0xF0, 0x12}}
	stub(t, map[string]*fakeBoard{"1:4": board}, []usb.Detected{{
		Backend:     "px4",
		Description: "PLEX PX-W3U4",
		Device:      "1:4",
		ID:          usbio.ID{Vendor: 0x0511, Product: 0x083F},
	}})
	provider := New(Config{PollInterval: 5 * time.Millisecond})
	context, err := provider.EstablishContext(winscard.ScopeSystem)
	if err != nil {
		t.Fatalf("establish a context: %v", err)
	}
	readers, err := context.Readers(nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 1 {
		t.Fatalf("listed %d readers, want 1: %v", len(readers), readers)
	}
	t.Cleanup(func() { _ = context.Release() })
	return provider, board, readers[0]
}

func TestReaderNameOfABoardWithoutASerialIsNumbered(t *testing.T) {
	_, _, name := oneBoard(t)
	if name != "PLEX PX-W3U4 series Smart Card Reader 0" {
		t.Errorf("named the reader %q", name)
	}
}

func TestReaderNameOfABoardWithASerialCarriesItsIdentifier(t *testing.T) {
	stub(t, nil, []usb.Detected{{Backend: "px4", Device: "1:4", Serial: "ABC123", Port: "1-2"}})
	context, _ := New(Config{}).EstablishContext(winscard.ScopeSystem)
	defer context.Release()
	readers, err := context.Readers(nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 1 || !strings.HasPrefix(readers[0], "PLEX PX-W3U4 series Smart Card Reader (ID: ") ||
		len(readers[0]) < len("PLEX PX-W3U4 series Smart Card Reader (ID: 0123456789ABCDEF)") {
		t.Errorf("listed %v", readers)
	}
}

func TestBoardsWithoutASerialAreNumberedBySocketWithinTheirModel(t *testing.T) {
	stub(t, nil, []usb.Detected{
		{Backend: "us3pout", Device: "1:9", Port: "1-14"},
		{Backend: "us3pout", Device: "1:3", Port: "1-13.3"},
		{Backend: "fsusb2", Device: "1:5", Port: "1-2"},
	})
	context, _ := New(Config{}).EstablishContext(winscard.ScopeSystem)
	defer context.Release()
	readers, err := context.Readers(nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	want := map[string]bool{
		"DigiBest US-3POUT Smart Card Reader 0": true,
		"DigiBest US-3POUT Smart Card Reader 1": true,
		"KEIAN KTV-FSUSB2 Smart Card Reader 0":  true,
	}
	if len(readers) != len(want) {
		t.Fatalf("listed %v", readers)
	}
	for _, name := range readers {
		if !want[name] {
			t.Errorf("listed %q, want %v", name, want)
		}
	}
	// The one in the socket that sorts first is number 0.
	devices, err := New(Config{}).devices()
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	for _, one := range devices {
		if one.port == "1-13.3" && one.name != "DigiBest US-3POUT Smart Card Reader 0" {
			t.Errorf("the board in 1-13.3 is %q", one.name)
		}
	}
}

func TestOnlyCardCarryingBackendsAreListed(t *testing.T) {
	stub(t, nil, []usb.Detected{
		{Backend: "t230", Device: "1:2"},
		{Backend: "px4", Device: "1:4"},
	})
	provider := New(Config{})
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	readers, err := context.Readers(nil)
	if err != nil {
		t.Fatalf("list readers: %v", err)
	}
	if len(readers) != 1 || !strings.HasPrefix(readers[0], "PLEX PX-W3U4 series") {
		t.Fatalf("listed %v, want only the px4 board", readers)
	}
}

func TestConnectResetsTheCardAndTransmits(t *testing.T) {
	provider, board, name := oneBoard(t)
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	card, protocol, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if protocol != winscard.ProtocolT1 {
		t.Errorf("agreed %v, want T=1", protocol)
	}
	if board.resets != 1 {
		t.Errorf("reset the card %d times, want 1", board.resets)
	}
	response, answered, err := card.Transmit([]byte{0x90, 0x30, 0x00, 0x00, 0x00}, winscard.ProtocolT1)
	if err != nil {
		t.Fatalf("transmit: %v", err)
	}
	if answered != winscard.ProtocolT1 || len(response) != 3 {
		t.Errorf("answered %v with %d bytes", answered, len(response))
	}
	status, err := card.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != winscard.CardSpecific {
		t.Errorf("reported %v, want %v", status.State, winscard.CardSpecific)
	}
	if string(status.ATR) != string(board.atr) {
		t.Errorf("reported the ATR %X, want %X", status.ATR, board.atr)
	}
	if err := card.Disconnect(winscard.LeaveCard); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if board.closes != 1 {
		t.Errorf("closed the board %d times, want 1 once the last connection went", board.closes)
	}
}

func TestConnectRefusesWhatTheseReadersCannotDo(t *testing.T) {
	provider, _, name := oneBoard(t)
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	if _, _, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolT0); !errors.Is(err, winscard.ErrProtoMismatch) {
		t.Errorf("connected with T=0: %v, want %v", err, winscard.ErrProtoMismatch)
	}
	if _, _, err := context.Connect(name, winscard.ShareDirect, winscard.ProtocolAny); !errors.Is(err, winscard.ErrInvalidValue) {
		t.Errorf("connected directly: %v, want %v", err, winscard.ErrInvalidValue)
	}
}

func TestConnectReportsAnEmptySlot(t *testing.T) {
	provider, board, name := oneBoard(t)
	board.present = false
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	if _, _, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny); !errors.Is(err, winscard.ErrNoSmartcard) {
		t.Fatalf("connected to an empty slot: %v, want %v", err, winscard.ErrNoSmartcard)
	}
	if board.closes != 1 {
		t.Errorf("left the board open after a refused connection (%d closes)", board.closes)
	}
}

func TestBoardWithoutASlotIsReportedRatherThanUsed(t *testing.T) {
	board := &fakeBoard{present: true, noReader: true}
	stub(t, map[string]*fakeBoard{"1:9": board}, []usb.Detected{
		{Backend: "px4", Device: "1:9"},
	})
	provider := New(Config{})
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()
	readers, _ := context.Readers(nil)
	if _, _, err := context.Connect(readers[0], winscard.ShareShared, winscard.ProtocolAny); !errors.Is(err, winscard.ErrReaderUnsupported) {
		t.Fatalf("connected to a board with no slot: %v, want %v", err, winscard.ErrReaderUnsupported)
	}
}

func TestSharingRules(t *testing.T) {
	provider, _, name := oneBoard(t)
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	first, _, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	if _, _, err := context.Connect(name, winscard.ShareExclusive, winscard.ProtocolAny); !errors.Is(err, winscard.ErrSharingViolation) {
		t.Errorf("took the card exclusively while it was shared: %v", err)
	}
	second, _, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("second shared connection: %v", err)
	}
	_ = first.Disconnect(winscard.LeaveCard)
	_ = second.Disconnect(winscard.LeaveCard)

	only, _, err := context.Connect(name, winscard.ShareExclusive, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("exclusive connection once the card was free: %v", err)
	}
	if _, _, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny); !errors.Is(err, winscard.ErrSharingViolation) {
		t.Errorf("shared the card while it was held exclusively: %v", err)
	}
	_ = only.Disconnect(winscard.LeaveCard)
}

func TestTransactionKeepsTheOtherConnectionOut(t *testing.T) {
	provider, _, name := oneBoard(t)
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	holder, _, _ := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny)
	other, _, _ := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny)
	if err := holder.BeginTransaction(); err != nil {
		t.Fatalf("begin: %v", err)
	}

	blocked := make(chan error, 1)
	go func() {
		_, _, err := other.Transmit([]byte{0x00}, winscard.ProtocolT1)
		blocked <- err
	}()
	select {
	case err := <-blocked:
		t.Fatalf("the other connection transmitted during a transaction: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if err := holder.EndTransaction(winscard.LeaveCard); err != nil {
		t.Fatalf("end: %v", err)
	}
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("the waiting transmit failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the waiting transmit never ran")
	}
	_ = holder.Disconnect(winscard.LeaveCard)
	_ = other.Disconnect(winscard.LeaveCard)
}

func TestRemovalIsReportedAsRemovalAndEndsTheSession(t *testing.T) {
	provider, board, name := oneBoard(t)
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	card, _, err := context.Connect(name, winscard.ShareShared, winscard.ProtocolAny)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	board.mutex.Lock()
	board.transmitErr = errors.New("the card stopped answering")
	board.present = false
	board.mutex.Unlock()

	if _, _, err := card.Transmit([]byte{0x90, 0x30}, winscard.ProtocolT1); !errors.Is(err, winscard.ErrRemovedCard) {
		t.Fatalf("reported %v, want %v", err, winscard.ErrRemovedCard)
	}
	status, err := card.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != winscard.CardAbsent {
		t.Errorf("reported %v, want %v", status.State, winscard.CardAbsent)
	}
	if _, _, err := card.Transmit([]byte{0x90, 0x30}, winscard.ProtocolT1); !errors.Is(err, winscard.ErrResetCard) {
		t.Errorf("the next exchange gave %v, want %v", err, winscard.ErrResetCard)
	}
	_ = card.Disconnect(winscard.LeaveCard)
}

func TestGetStatusChangeSeesACardArrive(t *testing.T) {
	provider, board, name := oneBoard(t)
	board.present = false
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	// Learn the state first, the way an application does before it waits.
	first, err := context.GetStatusChange(t.Context(), 0, []winscard.ReaderState{
		{Reader: name, Current: winscard.StateUnaware},
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if !first[0].Event.Has(winscard.StateEmpty) {
		t.Fatalf("polled %#x, want StateEmpty", uint32(first[0].Event))
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		board.mutex.Lock()
		board.present = true
		board.mutex.Unlock()
	}()
	answered, err := context.GetStatusChange(t.Context(), time.Second, []winscard.ReaderState{
		{Reader: name, Current: first[0].Event &^ winscard.StateChanged},
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !answered[0].Event.Has(winscard.StatePresent) || !answered[0].Event.Has(winscard.StateChanged) {
		t.Errorf("waited out %#x, want StatePresent and StateChanged", uint32(answered[0].Event))
	}
	if board.opens != 2 {
		t.Errorf("opened the board %d times, want one per wait rather than one per poll", board.opens)
	}
}

func TestGetStatusChangeIsCancellable(t *testing.T) {
	provider, _, name := oneBoard(t)
	context, _ := provider.EstablishContext(winscard.ScopeSystem)
	defer context.Release()

	// Learn the state, including the insertion count the reader carries in the
	// high half of its state word, so that the wait below has nothing to
	// report and really does wait.
	known, err := context.GetStatusChange(t.Context(), 0, []winscard.ReaderState{
		{Reader: name, Current: winscard.StateUnaware},
	})
	if err != nil {
		t.Fatalf("poll: %v", err)
	}

	waitCtx, stop := context2()
	failures := make(chan error, 1)
	go func() {
		_, err := context.GetStatusChange(waitCtx, winscard.Infinite, []winscard.ReaderState{
			{Reader: name, Current: known[0].Event &^ winscard.StateChanged},
		})
		failures <- err
	}()
	time.Sleep(20 * time.Millisecond)
	stop()
	if err := <-failures; !errors.Is(err, winscard.ErrCancelled) {
		t.Fatalf("the cancelled wait gave %v, want %v", err, winscard.ErrCancelled)
	}
}

// context2 is context.WithCancel, named so that the test's own "context"
// variables for provider contexts do not shadow the package.
func context2() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
