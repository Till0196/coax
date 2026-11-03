// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"context"
	"errors"
	"slices"
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

func TestReaderNameOfABoardWithoutASerialCarriesAnIdentifier(t *testing.T) {
	_, _, name := oneBoard(t)
	if !strings.HasPrefix(name, "PLEX PX-W3U4 series Smart Card Reader (ID: ") {
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

// A board without a serial keeps its name when another of its model is
// plugged in beside it, whichever socket sorts first: the name an application
// remembers has to go on meaning the slot the card is in.
func TestABoardWithoutASerialKeepsItsNameBesideAnotherOfItsModel(t *testing.T) {
	alone := []usb.Detected{{Backend: "cech-zd1j", Device: "1:9", Port: "1-2"}}
	stub(t, nil, alone)
	before, err := New(Config{}).devices()
	if err != nil || len(before) != 1 {
		t.Fatalf("devices: %v, %v", before, err)
	}

	stub(t, nil, append([]usb.Detected{{Backend: "cech-zd1j", Device: "1:3", Port: "1-13.3"}}, alone...))
	after, err := New(Config{}).devices()
	if err != nil || len(after) != 2 {
		t.Fatalf("devices: %v, %v", after, err)
	}
	if after[0].name == after[1].name {
		t.Fatalf("both boards are %q", after[0].name)
	}
	for _, one := range after {
		if one.port == "1-2" && one.name != before[0].name {
			t.Errorf("the board in 1-2 was %q alone and %q beside another", before[0].name, one.name)
		}
	}
}

// Each dual-bridge PX3 enclosure has one physical card slot. Independent RF
// control bridges do not imply independent readers.
func TestDualBridgeEnclosuresAreOneReader(t *testing.T) {
	v2 := usbio.ID{Vendor: 0x0b06, Product: 0x0006}
	w3u3 := usbio.ID{Vendor: 0x0b06, Product: 0x0005}
	stub(t, nil, []usb.Detected{
		{Backend: "px3", Device: "1:9", ID: v2, Port: "1-1.2"},
		{Backend: "px3", Device: "1:8", ID: v2, Port: "1-1.1"},
		{Backend: "px3", Device: "1:20", ID: v2, Port: "1-4.1"},
		{Backend: "px3", Device: "1:30", ID: w3u3, Port: "1-3.1"},
		{Backend: "px3", Device: "1:31", ID: w3u3, Port: "1-3.2"},
		{Backend: "px3", Device: "1:40", ID: usbio.ID{Vendor: 0x0b06, Product: 4}, Port: "1-5.2"},
		{Backend: "px3", Device: "1:41", ID: usbio.ID{Vendor: 0x0b06, Product: 4}, Port: "1-5.1"},
		{Backend: "px3", Device: "1:50", ID: usbio.ID{Vendor: 0x0b06, Product: 3}, Port: "1-6.1"},
		{Backend: "px3", Device: "1:51", ID: usbio.ID{Vendor: 0x0b06, Product: 3}, Port: "1-6.2"},
	})
	found, err := New(Config{}).devices()
	if err != nil {
		t.Fatal(err)
	}
	var ports []string
	for _, one := range found {
		ports = append(ports, one.port)
	}
	if want := []string{"1-1.1", "1-4.1", "1-3.1", "1-5.1", "1-6.1", "1-6.2"}; !slices.Equal(ports, want) {
		t.Errorf("readers on %v, want %v", ports, want)
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

// listedSource lists boards and opens none.
type listedSource []Attached

func (l listedSource) Boards() ([]Attached, error) { return l, nil }
func (l listedSource) Open(string, string) (Borrow, error) {
	return Borrow{}, errors.New("not opened in this test")
}

func TestABDATunerIsAReaderOnlyWhenMarked(t *testing.T) {
	provider := New(Config{Boards: listedSource{
		{Backend: "bda", Selector: `@device:pnp:\\?\usb#vid_0b06&pid_0006&mi_00#a&1#{c}\{t0}`, CardSlot: true},
		{Backend: "bda", Selector: `@device:pnp:\\?\usb#vid_0b06&pid_0006&mi_00#a&1#{c}\{t1}`},
		{Backend: "bda", Selector: `@device:pnp:\\?\pci#ven_0000&dev_0000#1#{c}\{x}`},
	}})
	found, err := provider.devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.HasSuffix(found[0].selector, `\{t0}`) || !strings.HasPrefix(found[0].name, "PLEX ") {
		t.Fatalf("listed %+v, want the marked tuner alone", found)
	}
}

func TestBDACardAliasesShareOneReaderPerEnclosure(t *testing.T) {
	previous := bdaReaderGroup
	t.Cleanup(func() { bdaReaderGroup = previous })
	groups := map[string]string{
		"usb-w3u2-box-a-bridge-2":      "w3u2:box-a",
		"usb-w3u2-box-a-bridge-1":      "w3u2:box-a",
		"usb-w3u2-box-b-bridge-1":      "w3u2:box-b",
		"usb-w3u3-box-a-bridge-2":      "w3u3:box-a",
		"usb-w3u3-box-a-bridge-1":      "w3u3:box-a",
		"usb-w3u3-box-a-0-terrestrial": "w3u3:box-a",
	}
	bdaReaderGroup = func(selector string) string { return groups[selector] }
	source := listedSource{
		{Backend: "bda", Selector: "usb-w3u2-box-a-bridge-2", CardSlot: true},
		{Backend: "bda", Selector: "usb-w3u2-box-a-bridge-1", CardSlot: true},
		{Backend: "bda", Selector: "usb-w3u2-box-b-bridge-1", CardSlot: true},
		{Backend: "bda", Selector: "usb-w3u3-box-a-bridge-2", CardSlot: true},
		{Backend: "bda", Selector: "usb-w3u3-box-a-bridge-1", CardSlot: true},
		// An ineligible filter cannot hide a marked card owner, even when its
		// selector sorts before every eligible alias.
		{Backend: "bda", Selector: "usb-w3u3-box-a-0-terrestrial"},
		{Backend: "bda", Selector: "pci-card-a-tuner-0", CardSlot: true},
		{Backend: "bda", Selector: "pci-card-b-tuner-0", CardSlot: true},
	}
	before, err := New(Config{Boards: source}).devices()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"usb-w3u2-box-a-bridge-1", "usb-w3u2-box-b-bridge-1", "usb-w3u3-box-a-bridge-1", "pci-card-a-tuner-0", "pci-card-b-tuner-0"}
	if len(before) != len(want) {
		t.Fatalf("listed %+v", before)
	}
	names := map[string]string{}
	for i, one := range before {
		if one.selector != want[i] {
			t.Fatalf("reader %d: %s, want %s", i, one.selector, want[i])
		}
		names[one.selector] = one.name
	}
	slices.Reverse(source)
	after, err := New(Config{Boards: source}).devices()
	if err != nil || len(after) != len(before) {
		t.Fatalf("after reorder: %+v, %v", after, err)
	}
	for _, one := range after {
		if names[one.selector] != one.name {
			t.Fatalf("reader changed with enumeration order: %+v", one)
		}
	}
}

func TestUnknownBDAReaderTopologyDoesNotMergeDevices(t *testing.T) {
	previous := bdaReaderGroup
	t.Cleanup(func() { bdaReaderGroup = previous })
	bdaReaderGroup = func(string) string { return "" }
	found, err := New(Config{Boards: listedSource{
		{Backend: "bda", Selector: "usb-card-a", CardSlot: true},
		{Backend: "bda", Selector: "usb-card-b", CardSlot: true},
	}}).devices()
	if err != nil || len(found) != 2 {
		t.Fatalf("unresolved devices merged: %+v, %v", found, err)
	}
}
